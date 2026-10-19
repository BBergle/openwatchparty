package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type runCfg struct {
	url, relayBin string
	scale         float64
	trustProxy    bool
	ports, only   string
	examples      string
}

// env is what every scenario shares: the relay under test and how it runs.
type env struct {
	url, httpBase string
	scale         float64    // the relay's --timer-scale, for values /v1/info doesn't report
	trustProxy    bool       // the relay takes the client address from X-Forwarded-For
	relay         *relayProc // the relay the suite started (--relay-bin), nil with --url
	bin           string
	ports         *ports
	examples      string // the spec/examples directory, "" when not found
	limits        Msg    // /v1/info limits and policy, read once at the start
	policy        Msg
}

// need is what a scenario requires beyond a running relay.
type need int

const (
	needNothing need = iota
	needProcess      // --relay-bin: the suite restarts the relay, rewrites its files or starts more relays
	needProxy        // the relay trusts X-Forwarded-For, so each client can have its own address
)

func (n need) reason() string {
	switch n {
	case needProcess:
		return "needs --relay-bin (starts, restarts or reconfigures the relay)"
	case needProxy:
		return "needs a relay started with --trust-proxy (pass --trust-proxy)"
	}
	return ""
}

type scenario struct {
	section string // SPEC.md section it checks
	name    string
	needs   need
	fn      func(x *sc) error
}

// skipError ends a scenario without a verdict, for a reason the runner prints.
type skipError struct{ reason string }

func (e *skipError) Error() string { return e.reason }

func skipf(format string, args ...any) error { return &skipError{fmt.Sprintf(format, args...)} }

// sc is one scenario's context: the shared env plus the clients and relays to close afterwards.
type sc struct {
	*env
	clients []*Client
	extra   []*relayProc
	subs    []*sc
}

// addrSeq numbers the client addresses: every client gets its own X-Forwarded-For address from 198.18.0.0/15 (the
// benchmarking range), so per-IP limits apply per client unless a scenario shares one on purpose. It starts at a
// random point, so a run right after another against the same relay doesn't meet the first run's per-IP state.
var addrSeq atomic.Int64

func init() { addrSeq.Store(time.Now().UnixNano() % (2 * 256 * 254)) }

func nextAddr() string {
	n := addrSeq.Add(1)
	return fmt.Sprintf("198.%d.%d.%d", 18+(n/(254*256))%2, (n/254)%256, n%254+1)
}

// v6Seq numbers IPv6 /48 networks in 2001:db8::/32 (the documentation range). Each part of a scenario that checks
// the IPv6 address groups (§3.1) takes a /48 of its own, so its /64, /56 and /48 groups hold nothing from other
// scenarios or from an earlier run against the same relay.
var v6Seq atomic.Int64

func init() { v6Seq.Store(time.Now().UnixNano() % 0xf000) }

// v6Net is one IPv6 /48.
type v6Net int64

func newV6Net() v6Net { return v6Net(v6Seq.Add(1) & 0xffff) }

// at is address i of /64 number h (0–255) of /56 number s (0–255) in the /48.
func (n v6Net) at(s, h, i int) string {
	return fmt.Sprintf("2001:db8:%x:%x::%x", int64(n), s<<8|h, i+1)
}

// mapped is the IPv4-mapped IPv6 form of an IPv4 address (§3.1: it is the IPv4 address).
func mapped(v4 string) string { return "::ffff:" + v4 }

func (x *sc) dial(name string) (*Client, error) { return x.dialAt(x.url, name, nextAddr()) }

// dialFrom connects with a given client address.
func (x *sc) dialFrom(name, addr string) (*Client, error) { return x.dialAt(x.url, name, addr) }

func (x *sc) dialAt(url, name, addr string) (*Client, error) {
	recordIssued(issued.addrs, addr)
	return x.dialHeader(url, name, http.Header{"X-Forwarded-For": {addr}})
}

func (x *sc) dialHeader(url, name string, h http.Header) (*Client, error) {
	c, err := dial(url, name, h)
	if err == nil {
		c.pace = newPacer(x.lim("messageBurst"), x.lim("messagesPerSecond"))
		x.clients = append(x.clients, c)
	}
	return c, err
}

// startRelay starts a relay for this scenario only; the runner stops it afterwards.
func (x *sc) startRelay(scale float64, extra ...string) (*relayProc, error) {
	p, err := x.newRelay(scale, extra...)
	if err != nil {
		return nil, err
	}
	return p, p.start()
}

// newRelay prepares a relay for this scenario only, for a scenario that adds flags naming files in the relay's
// directory before it starts it. The runner stops it afterwards.
func (x *sc) newRelay(scale float64, extra ...string) (*relayProc, error) {
	p, err := newExtraRelay(x.bin, x.ports, scale, extra...)
	if err != nil {
		return nil, err
	}
	x.extra = append(x.extra, p)
	track(p)
	return p, nil
}

// on is a context for a relay this scenario started with startRelay at the given scale: its URL, limits and policy,
// no proxy trust. The scenario's cleanup closes its clients.
func (x *sc) on(p *relayProc, scale float64) *sc {
	e := &env{url: p.url(), httpBase: p.httpBase(), scale: scale, bin: x.bin, ports: x.ports}
	e.loadInfo()
	y := &sc{env: e}
	x.subs = append(x.subs, y)
	return y
}

func (x *sc) cleanup() error {
	for _, y := range x.subs {
		_ = y.cleanup()
	}
	for _, c := range x.clients {
		c.close()
	}
	var errs []string
	for _, p := range x.extra {
		if err := p.close(false); err != nil {
			errs = append(errs, err.Error())
		}
		untrack(p)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Every relay the suite started, so an interrupt can stop them.
var (
	procMu sync.Mutex
	procs  = map[*relayProc]bool{}
)

func track(p *relayProc) {
	procMu.Lock()
	procs[p] = true
	procMu.Unlock()
}

func untrack(p *relayProc) {
	procMu.Lock()
	delete(procs, p)
	procMu.Unlock()
}

func killAll() {
	procMu.Lock()
	defer procMu.Unlock()
	for p := range procs {
		p.kill()
	}
}

func listScenarios() {
	section := ""
	for _, s := range scenarios {
		if s.section != section {
			section = s.section
			fmt.Printf("%s\n", section)
		}
		if s.needs != needNothing {
			fmt.Printf("  %-26s %s\n", s.name, s.needs.reason())
		} else {
			fmt.Printf("  %s\n", s.name)
		}
	}
}

func runSuite(cfg runCfg) int {
	e := &env{url: cfg.url, scale: cfg.scale, trustProxy: cfg.trustProxy, bin: cfg.relayBin, examples: cfg.examples}
	if e.examples == "" {
		e.examples = findExamples()
	} else if fi, err := os.Stat(e.examples); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "--examples %s: not a directory\n", e.examples)
		return 2
	}
	if cfg.relayBin != "" {
		pp, err := parsePorts(cfg.ports)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		e.ports = pp
		p, err := newMainRelay(cfg.relayBin, pp, cfg.scale)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		track(p)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sig
			killAll()
			fmt.Fprintf(os.Stderr, "\ninterrupted; relay files kept in %s\n", p.dir)
			os.Exit(130)
		}()
		if err := p.start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		e.relay, e.url, e.trustProxy = p, p.url(), true
		fmt.Printf("relay %s on %s, files in %s\n\n", cfg.relayBin, p.url(), p.dir)
	}
	e.httpBase = httpBase(e.url)
	e.loadInfo()

	var passed, skipped int
	var failed []string
	section := ""
	for _, s := range scenarios {
		if cfg.only != "" && !strings.Contains(s.name, cfg.only) {
			continue
		}
		if s.section != section {
			section = s.section
			fmt.Printf("%s\n", section)
		}
		if (s.needs == needProcess && e.relay == nil) || (s.needs == needProxy && !e.trustProxy) {
			fmt.Printf("  SKIP %-26s %s\n", s.name, s.needs.reason())
			skipped++
			continue
		}
		x := &sc{env: e}
		start := time.Now()
		err := s.fn(x)
		if err == nil {
			for _, c := range x.clients {
				if v := c.violations(); len(v) > 0 {
					err = fmt.Errorf("%s: %s", c.name, strings.Join(v, "; "))
					break
				}
			}
		}
		if cerr := x.cleanup(); err == nil && cerr != nil {
			err = cerr
		}
		if e.relay != nil && !e.relay.running() {
			msg := fmt.Sprintf("relay exited (%v); restarted for the next scenario", e.relay.err)
			e.relay.cmd = nil
			if serr := e.relay.start(); serr != nil {
				msg += fmt.Sprintf("; restart failed: %v", serr)
			}
			if err == nil {
				err = errors.New(msg)
			} else {
				err = fmt.Errorf("%v; %s", err, msg)
			}
		}
		el := time.Since(start).Round(10 * time.Millisecond)
		var sk *skipError
		switch {
		case errors.As(err, &sk):
			fmt.Printf("  SKIP %-26s %s\n", s.name, sk.reason)
			skipped++
		case err != nil:
			fmt.Printf("  FAIL %-26s %6v  %v\n", s.name, el, err)
			failed = append(failed, s.name)
		default:
			fmt.Printf("  PASS %-26s %6v\n", s.name, el)
			passed++
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Printf("\nowp-conform: %d passed, %d failed, %d skipped", passed, len(failed), skipped)
	if len(failed) > 0 {
		fmt.Printf(" (failed: %s)", strings.Join(failed, ", "))
	}
	fmt.Println()
	if e.relay != nil {
		keep := len(failed) > 0
		if err := e.relay.close(keep); err != nil {
			fmt.Printf("relay shutdown: %v\n", err)
			failed = append(failed, "shutdown")
		}
		untrack(e.relay)
		if keep {
			fmt.Printf("relay files kept in %s\n", e.relay.dir)
		}
	}
	if len(failed) > 0 {
		return 1
	}
	return 0
}

func httpBase(wsURL string) string {
	u := strings.TrimSuffix(wsURL, "/v1/ws")
	if strings.HasPrefix(u, "wss://") {
		return "https://" + strings.TrimPrefix(u, "wss://")
	}
	return "http://" + strings.TrimPrefix(u, "ws://")
}
