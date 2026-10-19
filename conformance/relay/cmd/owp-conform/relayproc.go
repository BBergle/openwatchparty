package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The relay that --relay-bin mode starts: its ID and the first characters it creates codes with.
const (
	ownID       = "c1"
	ownPrefixes = "2345"
)

// ports hands out free ports from the range given with --ports; a port stays with the relay it was given to.
type ports struct {
	mu       sync.Mutex
	lo, hi   int
	assigned map[int]bool
}

func parsePorts(s string) (*ports, error) {
	a, b, ok := strings.Cut(s, "-")
	lo, err1 := strconv.Atoi(a)
	hi, err2 := strconv.Atoi(b)
	if !ok || err1 != nil || err2 != nil || lo < 1 || hi > 65535 || lo > hi {
		return nil, fmt.Errorf("--ports %q: want LO-HI", s)
	}
	return &ports{lo: lo, hi: hi, assigned: map[int]bool{}}, nil
}

func (p *ports) take() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for port := p.lo; port <= p.hi; port++ {
		if p.assigned[port] {
			continue
		}
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = l.Close()
		p.assigned[port] = true
		return port, nil
	}
	return 0, fmt.Errorf("no free port in %d-%d", p.lo, p.hi)
}

// relayProc is one relay process the suite started. Its files live in dir and survive restarts.
type relayProc struct {
	bin  string
	dir  string
	port int
	args []string

	cmd    *exec.Cmd
	exited chan struct{}
	err    error // the process's exit status, set before exited closes
	log    *os.File
}

func (p *relayProc) url() string      { return fmt.Sprintf("ws://127.0.0.1:%d/v1/ws", p.port) }
func (p *relayProc) httpBase() string { return fmt.Sprintf("http://127.0.0.1:%d", p.port) }
func (p *relayProc) stateDir() string { return filepath.Join(p.dir, "state") }
func (p *relayProc) denylist() string { return filepath.Join(p.dir, "denylist") }
func (p *relayProc) logPath() string  { return filepath.Join(p.dir, "relay.log") }

// newMainRelay prepares the relay most scenarios run against: persistence, deny list and proxy trust on, so the
// scenarios that restart it, rewrite its files or need distinct client addresses can run.
func newMainRelay(bin string, pp *ports, scale float64) (*relayProc, error) {
	port, err := pp.take()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "owp-conform-")
	if err != nil {
		return nil, err
	}
	p := &relayProc{bin: bin, dir: dir, port: port}
	if err := os.Mkdir(p.stateDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p.denylist(), []byte("# owp-conform\n"), 0o644); err != nil {
		return nil, err
	}
	p.args = []string{
		"--addr", fmt.Sprintf("127.0.0.1:%d", port), "--admin-addr=", "--id", ownID, "--prefixes", ownPrefixes,
		"--denylist", p.denylist(), "--state-dir", p.stateDir(), "--trust-proxy",
		"--timer-scale", strconv.FormatFloat(scale, 'g', -1, 64),
	}
	return p, nil
}

// newExtraRelay prepares a bare relay for one scenario: its own port, timer scale and extra flags; no files, no
// proxy trust.
func newExtraRelay(bin string, pp *ports, scale float64, extra ...string) (*relayProc, error) {
	port, err := pp.take()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "owp-conform-extra-")
	if err != nil {
		return nil, err
	}
	p := &relayProc{bin: bin, dir: dir, port: port}
	p.args = append([]string{
		"--addr", fmt.Sprintf("127.0.0.1:%d", port), "--admin-addr=",
		"--timer-scale", strconv.FormatFloat(scale, 'g', -1, 64),
	}, extra...)
	return p, nil
}

// setFlag sets a flag's value for the relay's next start, replacing the value it had.
func (p *relayProc) setFlag(name, value string) {
	for i := 0; i+1 < len(p.args); i++ {
		if p.args[i] == name {
			p.args[i+1] = value
			return
		}
	}
	p.args = append(p.args, name, value)
}

// writeFileAtomic replaces a file the relay watches in one step, so it never reads a half-written one.
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// start runs the relay and returns once it printed `listening <addr>`.
func (p *relayProc) start() error {
	if p.log == nil {
		f, err := os.OpenFile(p.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		p.log = f
	}
	fmt.Fprintf(p.log, "---- %s %s %s\n", time.Now().Format(time.RFC3339), p.bin, strings.Join(p.args, " "))
	cmd := exec.Command(p.bin, p.args...)
	cmd.Stderr = p.log
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", p.bin, err)
	}
	p.cmd, p.exited, p.err = cmd, make(chan struct{}), nil
	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		first := true
		for sc.Scan() {
			line := sc.Text()
			fmt.Fprintln(p.log, line)
			if first {
				ready <- line
				first = false
			}
		}
		if first {
			close(ready)
		}
		_, _ = io.Copy(io.Discard, out)
		p.err = cmd.Wait()
		close(p.exited)
	}()
	select {
	case line, ok := <-ready:
		if !ok {
			<-p.exited
			return fmt.Errorf("relay exited before listening (%v); log %s", p.err, p.logPath())
		}
		if !strings.HasPrefix(line, "listening ") {
			p.kill()
			return fmt.Errorf("relay's first stdout line %q, want \"listening <addr>\"; log %s", line, p.logPath())
		}
		return nil
	case <-time.After(10 * time.Second):
		p.kill()
		return fmt.Errorf("relay printed no line within 10 s; log %s", p.logPath())
	}
}

// stop sends SIGTERM and waits for the process to exit 0.
func (p *relayProc) stop() error {
	if p.cmd == nil {
		return nil
	}
	select {
	case <-p.exited:
		p.cmd = nil
		return fmt.Errorf("relay had already exited: %v; log %s", p.err, p.logPath())
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.exited:
	case <-time.After(10 * time.Second):
		p.kill()
		return fmt.Errorf("relay still running 10 s after SIGTERM; killed; log %s", p.logPath())
	}
	p.cmd = nil
	if p.err != nil {
		return fmt.Errorf("relay exit after SIGTERM: %v, want status 0; log %s", p.err, p.logPath())
	}
	return nil
}

// kill ends the process at once. Used only when it misbehaves or the suite is interrupted.
func (p *relayProc) kill() {
	if p.cmd == nil {
		return
	}
	_ = p.cmd.Process.Kill()
	<-p.exited
	p.cmd = nil
}

func (p *relayProc) running() bool {
	if p.cmd == nil {
		return false
	}
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// close stops the relay and removes its files unless keep is set.
func (p *relayProc) close(keep bool) error {
	err := p.stop()
	if p.log != nil {
		_ = p.log.Close()
		p.log = nil
	}
	if !keep && err == nil {
		_ = os.RemoveAll(p.dir)
	}
	return err
}
