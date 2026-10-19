package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type loadCfg struct {
	url            string
	clients        int
	roomSize       int
	ramp           int
	duration       time.Duration
	actionInterval time.Duration
	pidfile        string
	sshHost        string
	sshKey         string
	label          string
}

// ---- latency series ----

type series struct {
	mu sync.Mutex
	v  []float64
}

func (s *series) add(ms float64) {
	s.mu.Lock()
	s.v = append(s.v, ms)
	s.mu.Unlock()
}

type summary struct {
	N                  int
	P50, P95, P99, Max float64
}

func (s *series) summary() summary {
	s.mu.Lock()
	v := append([]float64(nil), s.v...)
	s.mu.Unlock()
	if len(v) == 0 {
		return summary{}
	}
	sort.Float64s(v)
	at := func(p float64) float64 { return v[min(len(v)-1, int(p*float64(len(v))))] }
	return summary{N: len(v), P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: v[len(v)-1]}
}

func (s summary) String() string {
	if s.N == 0 {
		return "n=0"
	}
	return fmt.Sprintf("n=%d p50=%.1fms p95=%.1fms p99=%.1fms max=%.1fms", s.N, s.P50, s.P95, s.P99, s.Max)
}

// ---- run state ----

type loadRun struct {
	cfg     loadCfg
	closing atomic.Bool
	// The steady window in Unix ns (to: MaxInt64 until it ends). Pings and actions count by their send time, so a
	// late answer to something sent near the end still counts.
	steadyFrom, steadyTo atomic.Int64

	join, ping, fanout series

	recv, sent, disconnects atomic.Int64
	errMu                   sync.Mutex
	errors                  map[string]int
}

func (r *loadRun) inSteady(ns int64) bool {
	from := r.steadyFrom.Load()
	return from != 0 && ns >= from && ns < r.steadyTo.Load()
}

func (r *loadRun) countErr(k string) {
	r.errMu.Lock()
	r.errors[k]++
	r.errMu.Unlock()
}

type lroom struct {
	mu        sync.Mutex
	code      string
	host      *lclient
	members   []*lclient
	pendingID int
	pending   struct {
		kind      string
		t0        time.Time
		revBefore int64
	}
}

type lclient struct {
	run     *loadRun
	room    *lroom
	conn    *websocket.Conn
	writeMu sync.Mutex
	joined  chan Msg
	done    chan struct{}

	peerID string
	maxRev atomic.Int64
	rttMs  atomic.Int64
	// joinedOK: joined arrived; abandoned: connect gave up on it. Only a joined, kept connection that closes before
	// the end is an unexpected disconnect; a failed join is counted as one already.
	joinedOK, abandoned atomic.Bool

	mu           sync.Mutex
	epoch        int
	player       string
	lastRecorded int
}

func (c *lclient) send(m Msg) {
	b, _ := json.Marshal(m)
	c.writeMu.Lock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := c.conn.WriteMessage(websocket.TextMessage, b)
	c.writeMu.Unlock()
	if err == nil {
		c.run.sent.Add(1)
	}
}

func (c *lclient) status(player string) {
	c.mu.Lock()
	c.player = player
	epoch := c.epoch
	c.mu.Unlock()
	var match any
	if epoch > 0 {
		match = Msg{"state": "matched", "deltaMs": 0}
	}
	c.send(Msg{"type": "status", "epoch": epoch, "player": player, "match": match, "offsetMs": 0, "rttMs": c.rttMs.Load()})
}

// simulateLoad: loading now, ready 100 ms later (a player opening a file or seeking).
func (c *lclient) simulateLoad() {
	c.status("loading")
	time.AfterFunc(100*time.Millisecond, func() {
		if !c.run.closing.Load() {
			c.status("ready")
		}
	})
}

func (c *lclient) readLoop() {
	defer close(c.done)
	r := c.run
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if !r.closing.Load() && c.joinedOK.Load() && !c.abandoned.Load() {
				r.disconnects.Add(1)
			}
			return
		}
		r.recv.Add(1)
		var m Msg
		if json.Unmarshal(data, &m) != nil {
			r.countErr("bad-json")
			continue
		}
		switch str(m, "type") {
		case "joined":
			c.joinedOK.Store(true)
			select {
			case c.joined <- m:
			default:
			}
		case "pong":
			id := str(m, "id")
			if ns, err := strconv.ParseInt(strings.TrimPrefix(id, "p"), 10, 64); err == nil {
				rtt := float64(time.Now().UnixNano()-ns) / 1e6
				c.rttMs.Store(int64(rtt))
				if r.inSteady(ns) {
					r.ping.add(rtt)
				}
			}
		case "room":
			if hasMedia(m) {
				c.mu.Lock()
				c.epoch = int(num(m, "epoch"))
				c.mu.Unlock()
				c.simulateLoad()
			}
			c.onRoom(m)
		case "error":
			r.countErr("error:" + str(m, "code"))
		case "closed":
			r.countErr("closed:" + str(m, "reason"))
		}
	}
}

func (c *lclient) onRoom(m Msg) {
	rev := int64(num(m, "rev"))
	if rev > c.maxRev.Load() {
		c.maxRev.Store(rev)
	}
	kind, by := actionOf(m)
	// Every seek reloads the player, whatever the phase: a relay that releases at once (everyone ready, §12) gets the
	// same loading/ready traffic per seek as one that doesn't.
	if kind == "seek" {
		c.mu.Lock()
		ready := c.player == "ready"
		c.mu.Unlock()
		if ready {
			c.simulateLoad()
		}
	}
	room := c.room
	if room == nil || room.host == c {
		return
	}
	hostID, _ := by.(string)
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.pendingID == 0 || !c.run.inSteady(room.pending.t0.UnixNano()) || hostID != room.host.peerID ||
		kind != room.pending.kind || rev <= room.pending.revBefore {
		return
	}
	c.mu.Lock()
	if c.lastRecorded < room.pendingID {
		c.lastRecorded = room.pendingID
		c.run.fanout.add(float64(time.Since(room.pending.t0).Microseconds()) / 1000)
	}
	c.mu.Unlock()
}

func (c *lclient) pingLoop() {
	time.Sleep(time.Duration(rand.Int64N(int64(5 * time.Second))))
	ping := time.NewTicker(5 * time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	defer heartbeat.Stop()
	for {
		if c.run.closing.Load() {
			return
		}
		c.send(Msg{"type": "ping", "id": fmt.Sprintf("p%d", time.Now().UnixNano())})
		select {
		case <-ping.C:
		case <-heartbeat.C:
			c.mu.Lock()
			p := c.player
			c.mu.Unlock()
			c.status(p)
			<-ping.C
		case <-c.done:
			return
		}
	}
}

func (r *loadRun) connect(room *lroom, msg Msg) (*lclient, error) {
	// Each client its own X-Forwarded-For address, so a relay run with --trust-proxy applies its per-IP limits per
	// client, as for real users.
	conn, _, err := dialer.Dial(r.cfg.url, http.Header{"X-Forwarded-For": {nextAddr()}})
	if err != nil {
		return nil, err
	}
	c := &lclient{run: r, room: room, conn: conn, joined: make(chan Msg, 1), done: make(chan struct{}), player: "idle"}
	go c.readLoop()
	t0 := time.Now()
	c.send(msg)
	select {
	case j := <-c.joined:
		r.join.add(float64(time.Since(t0).Microseconds()) / 1000)
		c.peerID = str(obj(j, "you"), "peerId")
		if room.code == "" {
			room.code = str(j, "code")
		}
		return c, nil
	case <-time.After(15 * time.Second):
		c.abandoned.Store(true)
		_ = conn.Close()
		return nil, fmt.Errorf("no joined within 15 s")
	case <-c.done:
		return nil, fmt.Errorf("closed before joined")
	}
}

func (r *loadRun) hostLoop(room *lroom, stop <-chan struct{}) {
	kinds := []string{"seek", "pause", "seek", "play"}
	for i := 0; ; i++ {
		jitter := 0.8 + 0.4*rand.Float64()
		select {
		case <-stop:
			return
		case <-time.After(time.Duration(float64(r.cfg.actionInterval) * jitter)):
		}
		kind := kinds[i%len(kinds)]
		msg := Msg{"type": kind}
		if kind == "seek" {
			msg["positionMs"] = rand.IntN(7_000_000)
		}
		room.mu.Lock()
		room.pendingID++
		room.pending.kind = kind
		room.pending.t0 = time.Now()
		room.pending.revBefore = room.host.maxRev.Load()
		room.mu.Unlock()
		room.host.send(msg)
	}
}

// ---- relay process sampling over SSH ----

// sample is one line from the sampler. t is when it reached the Mac (windows are cut by the Mac's clock, the Pi has
// no RTC); upS is the Pi's /proc/uptime, read together with the CPU ticks, for CPU time differences.
type sample struct {
	t              time.Time
	upS            float64
	cpuTicks       float64
	rssKB, availKB float64
	hz             float64
}

type sampler struct {
	cmd     *exec.Cmd
	mu      sync.Mutex
	samples []sample
	err     error
}

// sampleAwk prints one line per run: Pi uptime (s), relay utime+stime (ticks), VmRSS (KiB), MemAvailable (KiB) and
// CLK_TCK. One process reads them all, so the uptime and the ticks are taken together. The stat fields are counted
// after the last ") " (comm may hold spaces): utime and stime are fields 14 and 15.
const sampleAwk = `FILENAME == "/proc/uptime" { up = $1 }
FILENAME ~ /\/stat$/ { s = $0; sub(/^.*\) /, "", s); split(s, f, " "); ticks = f[12] + f[13] }
FILENAME ~ /\/status$/ && $1 == "VmRSS:" { rss = $2 }
FILENAME == "/proc/meminfo" && $1 == "MemAvailable:" { avail = $2 }
END { if (ticks != "" && rss != "") print up, ticks, rss, avail, hz }`

func startSampler(host, key, pidfile string) (*sampler, error) {
	pidfile = strings.TrimPrefix(pidfile, "~/")
	script := fmt.Sprintf(`pid=$(cat %q) || exit 3; kill -0 "$pid" || exit 4; hz=$(getconf CLK_TCK); `+
		`while kill -0 "$pid" 2>/dev/null; do awk -v hz="$hz" '%s' /proc/uptime /proc/$pid/stat /proc/$pid/status `+
		`/proc/meminfo 2>/dev/null; sleep 0.5; done`, pidfile, sampleAwk)
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", host, script}
	if key != "" {
		args = append([]string{"-i", key}, args...)
	}
	cmd := exec.Command("ssh", args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &sampler{cmd: cmd}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			now := time.Now()
			f := strings.Fields(sc.Text())
			if len(f) != 5 {
				continue
			}
			var v [5]float64
			ok := true
			for i := range f {
				var err error
				if v[i], err = strconv.ParseFloat(f[i], 64); err != nil {
					ok = false
				}
			}
			if !ok {
				continue
			}
			s.mu.Lock()
			s.samples = append(s.samples, sample{t: now, upS: v[0], cpuTicks: v[1], rssKB: v[2], availKB: v[3], hz: v[4]})
			s.mu.Unlock()
		}
		if err := cmd.Wait(); err != nil {
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
		}
	}()
	return s, nil
}

func (s *sampler) stop() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func (s *sampler) snapshot() []sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sample(nil), s.samples...)
}

// window summarizes samples that reached the Mac in [from, to); CPU uses the Pi's uptime between samples.
type window struct {
	cpuAvg, cpuMax float64 // percent of one core, over cpuIntervals sample intervals
	cpuIntervals   int
	rssLast        float64 // KiB
	rssMax         float64
	availMin       float64
}

func summarize(ss []sample, from, to time.Time) window {
	var w window
	w.availMin = -1
	var first, last *sample
	for i := range ss {
		s := &ss[i]
		if s.t.Before(from) || !s.t.Before(to) {
			continue
		}
		if first == nil {
			first = s
		}
		if last != nil {
			dt := s.upS - last.upS
			if dt > 0 && s.hz > 0 {
				w.cpuMax = max(w.cpuMax, (s.cpuTicks-last.cpuTicks)/s.hz/dt*100)
				w.cpuIntervals++
			}
		}
		last = s
		w.rssMax = max(w.rssMax, s.rssKB)
		w.rssLast = s.rssKB
		if w.availMin < 0 || s.availKB < w.availMin {
			w.availMin = s.availKB
		}
	}
	if first != nil && last != nil && last.upS > first.upS && last.hz > 0 {
		w.cpuAvg = (last.cpuTicks - first.cpuTicks) / last.hz / (last.upS - first.upS) * 100
	}
	return w
}

// cpuText and cpuJSON report n/a for a window that holds fewer than two samples (shorter than the sample interval).
func (w window) cpuText() string {
	if w.cpuIntervals == 0 {
		return "n/a (under two samples)"
	}
	return fmt.Sprintf("avg %.0f%% max %.0f%%", w.cpuAvg, w.cpuMax)
}

func (w window) cpuJSON(v float64) any {
	if w.cpuIntervals == 0 {
		return nil
	}
	return v
}

// ---- the run ----

func lockBench() (*os.File, error) {
	f, err := os.OpenFile("/tmp/owp-bench.lock", os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fmt.Println("waiting for /tmp/owp-bench.lock (another load run holds it)…")
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func runLoad(cfg loadCfg) int {
	lock, err := lockBench()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lock:", err)
		return 2
	}
	defer lock.Close()

	r := &loadRun{cfg: cfg, errors: map[string]int{}}
	var smp *sampler
	if cfg.pidfile != "" {
		smp, err = startSampler(cfg.sshHost, cfg.sshKey, cfg.pidfile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sampler:", err)
			return 2
		}
		defer smp.stop()
		time.Sleep(3 * time.Second)
		if len(smp.snapshot()) == 0 {
			fmt.Fprintln(os.Stderr, "sampler: no samples (is the relay running and the pidfile right?)", smp.err)
			return 2
		}
	}

	nRooms := cfg.clients / cfg.roomSize
	tokens := make(chan struct{})
	stopTokens := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second / time.Duration(cfg.ramp))
		defer t.Stop()
		for {
			select {
			case <-t.C:
				select {
				case tokens <- struct{}{}:
				case <-stopTokens:
					return
				}
			case <-stopTokens:
				return
			}
		}
	}()

	rampStart := time.Now()
	rooms := make([]*lroom, nRooms)
	var all []*lclient
	var allMu sync.Mutex
	var failedJoins atomic.Int64
	var wg sync.WaitGroup
	for i := range nRooms {
		room := &lroom{}
		rooms[i] = room
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-tokens
			host, err := r.connect(room, Msg{"type": "create", "name": "host", "client": "owpbench/1"})
			if err != nil {
				failedJoins.Add(int64(cfg.roomSize))
				r.countErr("connect: " + err.Error())
				return
			}
			room.host = host
			members := []*lclient{host}
			for j := 1; j < cfg.roomSize; j++ {
				<-tokens
				c, err := r.connect(room, Msg{"type": "join", "code": room.code, "name": fmt.Sprintf("m%d", j), "client": "owpbench/1"})
				if err != nil {
					failedJoins.Add(1)
					r.countErr("connect: " + err.Error())
					continue
				}
				members = append(members, c)
			}
			room.mu.Lock()
			room.members = members
			room.mu.Unlock()
			allMu.Lock()
			all = append(all, members...)
			allMu.Unlock()
		}()
	}
	wg.Wait()
	close(stopTokens)
	rampEnd := time.Now()
	rampDur := rampEnd.Sub(rampStart)
	fmt.Printf("ramp: %d connected, %d failed, in %v\n", len(all), failedJoins.Load(), rampDur.Round(time.Millisecond))

	// Everyone into the player, then media per room.
	for _, c := range all {
		go c.pingLoop()
		c.status("loading")
	}
	for _, room := range rooms {
		if room.host != nil {
			room.host.send(Msg{"type": "setMedia", "media": sampleMedia(7_200_000)})
		}
	}
	time.Sleep(3 * time.Second)

	stop := make(chan struct{})
	if cfg.actionInterval > 0 {
		for _, room := range rooms {
			if room.host != nil {
				go r.hostLoop(room, stop)
			}
		}
	}
	time.Sleep(2 * time.Second)
	steadyStart := time.Now()
	r.steadyTo.Store(math.MaxInt64)
	r.steadyFrom.Store(steadyStart.UnixNano())
	recv0, sent0 := r.recv.Load(), r.sent.Load()
	time.Sleep(cfg.duration)
	steadyEnd := time.Now()
	r.steadyTo.Store(steadyEnd.UnixNano())
	recv1, sent1 := r.recv.Load(), r.sent.Load()
	close(stop)
	// Answers to pings and actions sent near the end of the steady window still count; give them time to arrive.
	time.Sleep(3 * time.Second)
	r.closing.Store(true)

	for _, c := range all {
		c.writeMu.Lock()
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		c.writeMu.Unlock()
		_ = c.conn.Close()
	}
	closeEnd := time.Now()
	time.Sleep(4 * time.Second)
	end := time.Now()

	secs := steadyEnd.Sub(steadyStart).Seconds()
	res := map[string]any{
		"label": cfg.label, "url": cfg.url, "clients": cfg.clients, "roomSize": cfg.roomSize, "rooms": nRooms,
		"actionIntervalS": cfg.actionInterval.Seconds(), "durationS": cfg.duration.Seconds(),
		"connected": len(all), "failedJoins": failedJoins.Load(), "rampS": rampDur.Seconds(),
		"join": r.join.summary(), "pingRtt": r.ping.summary(), "fanout": r.fanout.summary(),
		"recvPerS": float64(recv1-recv0) / secs, "sentPerS": float64(sent1-sent0) / secs,
		"unexpectedDisconnects": r.disconnects.Load(), "errors": r.errors,
	}
	fmt.Printf("\n%-24s %s\n", "relay", cfg.url)
	fmt.Printf("%-24s %d clients in %d rooms of %d, ramp %d/s, steady %v, action every %v per room\n", "load",
		cfg.clients, nRooms, cfg.roomSize, cfg.ramp, cfg.duration, cfg.actionInterval)
	fmt.Printf("%-24s %d ok, %d failed, ramp %.1fs; %s\n", "join", len(all), failedJoins.Load(), rampDur.Seconds(), r.join.summary())
	fmt.Printf("%-24s %s\n", "ping rtt (steady)", r.ping.summary())
	fmt.Printf("%-24s %s\n", "fan-out (steady)", r.fanout.summary())
	fmt.Printf("%-24s recv %.0f/s, sent %.0f/s\n", "messages (steady)", float64(recv1-recv0)/secs, float64(sent1-sent0)/secs)
	fmt.Printf("%-24s %d unexpected disconnects, errors %v\n", "problems", r.disconnects.Load(), r.errors)

	if smp != nil {
		ss := smp.snapshot()
		base := summarize(ss, time.Time{}, rampStart)
		ramp := summarize(ss, rampStart, rampEnd)
		steady := summarize(ss, steadyStart, steadyEnd)
		after := summarize(ss, closeEnd.Add(2*time.Second), end)
		whole := summarize(ss, time.Time{}, end)
		perConn := 0.0
		if len(all) > 0 {
			perConn = (steady.rssLast - base.rssLast) / float64(len(all))
		}
		fmt.Printf("%-24s ramp %s; steady %s (100%% = one core of 4)\n", "relay cpu", ramp.cpuText(), steady.cpuText())
		fmt.Printf("%-24s idle %.1f MiB, steady %.1f MiB, peak %.1f MiB, after close %.1f MiB; %.1f KiB per connection\n",
			"relay rss", base.rssLast/1024, steady.rssLast/1024, whole.rssMax/1024, after.rssLast/1024, perConn)
		fmt.Printf("%-24s %.0f MiB\n", "pi MemAvailable min", whole.availMin/1024)
		res["cpuRampAvg"], res["cpuRampMax"] = ramp.cpuJSON(ramp.cpuAvg), ramp.cpuJSON(ramp.cpuMax)
		res["cpuSteadyAvg"], res["cpuSteadyMax"] = steady.cpuJSON(steady.cpuAvg), steady.cpuJSON(steady.cpuMax)
		res["rssIdleMiB"], res["rssSteadyMiB"], res["rssPeakMiB"], res["rssAfterMiB"] = base.rssLast/1024, steady.rssLast/1024, whole.rssMax/1024, after.rssLast/1024
		res["rssPerConnKiB"], res["memAvailMinMiB"] = perConn, whole.availMin/1024
	}
	b, _ := json.Marshal(res)
	fmt.Printf("\nRESULT %s\n", b)
	if r.disconnects.Load() > 0 || failedJoins.Load() > 0 {
		return 1
	}
	return 0
}
