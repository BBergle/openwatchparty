package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/edde746/openwatchparty/relay/internal/relay"
	"github.com/gorilla/websocket"
)

// clearEnv unsets every OWP_ variable for the test, so the caller's
// environment doesn't leak into parseConfig.
func clearEnv(t *testing.T) {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "OWP_") {
			t.Setenv(name, "")
			os.Unsetenv(name)
		}
	}
}

func TestTLSFlags(t *testing.T) {
	clearEnv(t)
	_, l, err := parseConfig([]string{"--addr", ":443", "--state-dir", "/state",
		"--tls-domains", "NA.relay.example, eu.relay.example", "--acme-email", "ops@relay.example"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.tlsDomains, []string{"na.relay.example", "eu.relay.example"}) || l.addr != ":443" ||
		l.acmeDirectory != "https://acme-v02.api.letsencrypt.org/directory" || l.acmeEmail != "ops@relay.example" {
		t.Fatalf("flags: %+v", l)
	}

	t.Setenv("OWP_TLS_DOMAINS", "relay.example")
	t.Setenv("OWP_STATE_DIR", "/state")
	t.Setenv("OWP_ACME_DIRECTORY", "https://pebble:14000/dir")
	t.Setenv("OWP_ACME_EMAIL", "env@relay.example")
	_, l, err = parseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.tlsDomains, []string{"relay.example"}) || l.acmeDirectory != "https://pebble:14000/dir" ||
		l.acmeEmail != "env@relay.example" {
		t.Fatalf("environment: %+v", l)
	}
	_, l, err = parseConfig([]string{"--tls-domains", "flag.example", "--acme-directory",
		"https://acme-staging-v02.api.letsencrypt.org/directory"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.tlsDomains, []string{"flag.example"}) ||
		l.acmeDirectory != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Fatalf("a flag must win over its variable: %+v", l)
	}

	t.Setenv("OWP_TLS_DOMAINS", "")
	_, l, err = parseConfig(nil)
	if err != nil || l.tlsDomains != nil {
		t.Fatalf("without --tls-domains the listener is plain: %+v %v", l, err)
	}
}

func TestTLSFlagErrors(t *testing.T) {
	clearEnv(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--tls-domains", "relay.example"}, "--tls-domains needs --state-dir"},
		{[]string{"--state-dir", "/s", "--tls-domains", "a.example,,b.example"}, `"" is not a hostname`},
		{[]string{"--state-dir", "/s", "--tls-domains", "localhost"}, `"localhost" is not a hostname`},
		{[]string{"--state-dir", "/s", "--tls-domains", "192.0.2.1"}, `"192.0.2.1" is not a hostname`},
		{[]string{"--state-dir", "/s", "--tls-domains", "*.relay.example"}, `"*.relay.example" is not`},
		{[]string{"--state-dir", "/s", "--tls-domains", "relay.example."}, `"relay.example." is not`},
		{[]string{"--state-dir", "/s", "--tls-domains", "-a.example"}, `"-a.example" is not`},
		{[]string{"--state-dir", "/s", "--tls-domains", "bücher.example"}, `"bücher.example" is not`},
		{[]string{"--state-dir", "/s", "--tls-domains", "relay.example", "--acme-directory", "http://acme.example/dir"},
			"--acme-directory must be an https:// URL"},
	} {
		_, _, err := parseConfig(c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want an error with %q", c.args, err, c.want)
		}
	}
}

func TestRoomLifeFlags(t *testing.T) {
	clearEnv(t)
	cfg, _, err := parseConfig(nil)
	if err != nil || cfg.MaxRoomAge != 24*time.Hour || cfg.KeepEmptyRooms {
		t.Fatalf("defaults: %v %v %v, want 24h and false", cfg.MaxRoomAge, cfg.KeepEmptyRooms, err)
	}
	t.Setenv("OWP_KEEP_EMPTY_ROOMS", "true")
	if cfg, _, err = parseConfig(nil); err != nil || !cfg.KeepEmptyRooms {
		t.Fatalf("$OWP_KEEP_EMPTY_ROOMS: %v %v, want true", cfg.KeepEmptyRooms, err)
	}
	t.Setenv("OWP_MAX_ROOM_AGE", "168h")
	if cfg, _, err = parseConfig(nil); err != nil || cfg.MaxRoomAge != 168*time.Hour {
		t.Fatalf("environment: %v %v, want 168h", cfg.MaxRoomAge, err)
	}
	if cfg, _, err = parseConfig([]string{"--max-room-age", "48h"}); err != nil || cfg.MaxRoomAge != 48*time.Hour {
		t.Fatalf("a flag must win over its variable: %v %v", cfg.MaxRoomAge, err)
	}
	t.Setenv("OWP_MAX_ROOM_AGE", "0")
	if cfg, _, err = parseConfig(nil); err != nil || cfg.MaxRoomAge != 0 {
		t.Fatalf("0, no maximum: %v %v", cfg.MaxRoomAge, err)
	}
	t.Setenv("OWP_MAX_ROOM_AGE", "7d")
	if _, _, err = parseConfig(nil); err == nil || !strings.Contains(err.Error(), "is not a duration") {
		t.Fatalf("7d: %v, want a duration error", err)
	}
}

// TestTLSWithoutStateDirExits2 runs main in a child process.
func TestTLSWithoutStateDirExits2(t *testing.T) {
	if os.Getenv("OWP_TEST_MAIN") == "1" {
		os.Args = []string{"owp-relay", "--addr", "127.0.0.1:0", "--admin-addr=", "--tls-domains", "relay.example"}
		main()
		return
	}
	clearEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTLSWithoutStateDirExits2$")
	cmd.Env = append(os.Environ(), "OWP_TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 ||
		!strings.Contains(string(out), "owp-relay: --tls-domains needs --state-dir") {
		t.Fatalf("exit %v, output %q; want exit 2 with the reason", err, out)
	}
}

// startPublic serves the relay's public endpoints with short timeouts.
func startPublic(t *testing.T, read, idle time.Duration) string {
	t.Helper()
	srv, err := relay.New(relay.Config{ID: "test", TimerScale: 1, Limits: relay.DefaultLimits,
		MaxRoomAge: relay.DefaultMaxRoomAge})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := httpServer(srv.Handler(), log.New(io.Discard, "", 0), read, idle)
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	return ln.Addr().String()
}

// closedByServer reports whether the server closes conn within wait.
func closedByServer(conn net.Conn, wait time.Duration) bool {
	conn.SetReadDeadline(time.Now().Add(wait))
	_, err := conn.Read(make([]byte, 1))
	return err == io.EOF
}

func TestIdleKeepAliveConnectionCloses(t *testing.T) {
	addr := startPublic(t, 300*time.Millisecond, 300*time.Millisecond)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /health HTTP/1.1\r\nHost: x\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v %v", resp, err)
	}
	resp.Body.Close()
	if !closedByServer(conn, 2*time.Second) {
		t.Fatal("an idle keep-alive connection stayed open")
	}
}

// A client that sends requests and stops reading the responses loses its
// connection once a response can't be written within the timeout.
func TestUnreadResponsesClose(t *testing.T) {
	addr := startPublic(t, 300*time.Millisecond, time.Minute)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.(*net.TCPConn).SetReadBuffer(1024)
	const requests = 20000
	go func() {
		for range requests {
			if _, err := io.WriteString(conn, "GET /v1/info HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
				return
			}
		}
	}()
	time.Sleep(time.Second)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	responses := 0
	br := bufio.NewReader(conn)
	for {
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			break
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		responses++
	}
	if responses > requests/2 {
		t.Fatalf("the relay wrote %d of %d responses to a client that wasn't reading", responses, requests)
	}
}

func TestSlowRequestCloses(t *testing.T) {
	addr := startPublic(t, 300*time.Millisecond, 300*time.Millisecond)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /health HTTP/1.1\r\nHost: x\r\n") // headers never end
	start := time.Now()
	if !closedByServer(conn, 2*time.Second) || time.Since(start) < 250*time.Millisecond {
		t.Fatal("an unfinished request must be closed after the read timeout, not before")
	}
}

func TestWebSocketOutlivesHTTPTimeouts(t *testing.T) {
	addr := startPublic(t, 200*time.Millisecond, 200*time.Millisecond)
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/v1/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	time.Sleep(600 * time.Millisecond) // past both timeouts, with nothing sent
	if err := ws.WriteJSON(map[string]any{"type": "ping", "id": 1}); err != nil {
		t.Fatal(err)
	}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var m map[string]any
	if err := ws.ReadJSON(&m); err != nil || m["type"] != "pong" {
		t.Fatalf("WebSocket after the HTTP timeouts: %v %v", m, err)
	}
}
