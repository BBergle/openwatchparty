package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edde746/openwatchparty/relay/internal/relay"
	"github.com/gorilla/websocket"
)

// These tests serve the relay over TLS the way run does: parseConfig, the
// ACME manager with its cache in <state-dir>/acme, tlsConfig and listenTLS.
// The seam is the cache: it holds a test certificate for testDomain, so the
// manager never needs the ACME server, which counts every connection made to it.

const testDomain = "relay.test"

type tlsRelay struct {
	t     *testing.T
	addr  string // 127.0.0.1:port
	roots *x509.CertPool
	logs  *lockedBuffer // net/http's error log and the standard logger
	acme  *atomic.Int64 // connections to the ACME directory
}

// startTLS runs the relay's public endpoints on TLS for testDomain until the
// test ends, with at most maxPending sockets pending, read as the read and
// idle timeouts, and flags added to the relay's.
func startTLS(t *testing.T, maxPending int, read time.Duration, flags ...string) *tlsRelay {
	t.Helper()
	acmeConns := new(atomic.Int64)
	acmeSrv := httptest.NewUnstartedServer(http.NotFoundHandler())
	acmeSrv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			acmeConns.Add(1)
		}
	}
	acmeSrv.StartTLS()
	t.Cleanup(acmeSrv.Close)

	state := t.TempDir()
	cert := newCertificate(t, testDomain, time.Now().Add(-time.Hour), time.Now().Add(365*24*time.Hour))
	writeCached(t, filepath.Join(state, "acme", testDomain), cert)
	cfg, l, err := parseConfig(append([]string{"--id", "test", "--state-dir", state, "--tls-domains", testDomain,
		"--acme-directory", acmeSrv.URL + "/directory"}, flags...))
	if err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(t)
	srv, err := relay.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := httpServer(srv.Handler(), newErrorLog(logs), read, read)
	ln := listenTLS(hs, tcp, tlsConfig(certManager(l, cfg.StateDir).GetCertificate), maxPending, srv.AdmitPending)
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	return &tlsRelay{t: t, addr: tcp.Addr().String(), roots: roots(cert), logs: logs, acme: acmeConns}
}

func (r *tlsRelay) port() string {
	_, port, _ := net.SplitHostPort(r.addr)
	return port
}

// dialContext reaches the relay for any host name, as DNS would.
func (r *tlsRelay) dialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, r.addr)
}

func (r *tlsRelay) dialTLS(serverName string, protos ...string) (*tls.Conn, error) {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second},
		Config: &tls.Config{ServerName: serverName, RootCAs: r.roots, NextProtos: protos}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return nil, err
	}
	return c.(*tls.Conn), nil
}

func (r *tlsRelay) dialWS(timeout time.Duration) (*websocket.Conn, error) {
	d := websocket.Dialer{NetDialContext: r.dialContext, TLSClientConfig: &tls.Config{RootCAs: r.roots},
		HandshakeTimeout: timeout}
	ws, _, err := d.Dial("wss://"+testDomain+":"+r.port()+"/v1/ws", nil)
	return ws, err
}

func (r *tlsRelay) get(path string) (*http.Response, error) {
	hc := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: r.dialContext,
		TLSClientConfig: &tls.Config{RootCAs: r.roots}}}
	return hc.Get("https://" + testDomain + ":" + r.port() + path)
}

// noAddressLogged fails the test if a log line names the client address.
func (r *tlsRelay) noAddressLogged() {
	r.t.Helper()
	if s := r.logs.String(); strings.Contains(s, "127.0.0.1") || strings.Contains(s, "handshake") {
		r.t.Fatalf("the log names a client or a handshake:\n%s", s)
	}
}

func send(t *testing.T, ws *websocket.Conn, m map[string]any) {
	t.Helper()
	if err := ws.WriteJSON(m); err != nil {
		t.Fatal(err)
	}
}

// expect reads up to the next message of type typ.
func expect(t *testing.T, ws *websocket.Conn, typ string) map[string]any {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var m map[string]any
		if err := ws.ReadJSON(&m); err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		if m["type"] == typ {
			return m
		}
	}
}

func TestTLSClientsJoinARoom(t *testing.T) {
	r := startTLS(t, maxPending, readTimeout)
	host, err := r.dialWS(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	send(t, host, map[string]any{"type": "create", "name": "Host", "client": "Test/1.0"})
	code := expect(t, host, "joined")["code"]

	member, err := r.dialWS(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer member.Close()
	send(t, member, map[string]any{"type": "join", "code": code, "name": "Member", "client": "Test/1.0"})
	if joined := expect(t, member, "joined"); joined["code"] != code {
		t.Fatalf("joined %v, want room %v", joined["code"], code)
	}
	room := expect(t, host, "room")
	if n := len(room["participants"].([]any)); n != 2 {
		t.Fatalf("the host sees %d participants, want 2: %v", n, room)
	}
	if r.acme.Load() != 0 {
		t.Fatal("a cached certificate must not reach the ACME server")
	}
}

func TestTLSPortGivesPlainHTTPNoAnswer(t *testing.T) {
	r := startTLS(t, maxPending, readTimeout)
	conn, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /health HTTP/1.1\r\nHost: "+testDomain+"\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := io.ReadAll(conn)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("the relay kept a plain HTTP request open")
	}
	if len(got) > 0 {
		t.Fatalf("a plain HTTP request got an answer: %q", got)
	}

	ws, resp, err := websocket.DefaultDialer.Dial("ws://"+r.addr+"/v1/ws", nil)
	if err == nil {
		ws.Close()
	}
	if err == nil || resp != nil {
		t.Fatalf("a ws:// dial got %v, %v; want no HTTP answer", resp, err)
	}
	r.noAddressLogged()
}

func TestTLSUnlistedNameGetsNoCertificate(t *testing.T) {
	r := startTLS(t, maxPending, readTimeout)
	if c, err := r.dialTLS(testDomain, "http/1.1"); err != nil {
		t.Fatalf("%s: %v", testDomain, err)
	} else {
		c.Close()
	}
	// The client skips verification, so only the relay can refuse: it sends
	// no certificate. An empty name is a client that sends no SNI.
	for _, name := range []string{"other.test", "sub." + testDomain, "localhost", ""} {
		c, err := tls.Dial("tcp", r.addr, &tls.Config{ServerName: name, InsecureSkipVerify: true})
		if err == nil {
			names := c.ConnectionState().PeerCertificates[0].DNSNames
			c.Close()
			t.Fatalf("server name %q got a certificate for %v", name, names)
		}
	}
	if n := r.acme.Load(); n != 0 {
		t.Fatalf("unlisted names made %d connections to the ACME server", n)
	}
	r.noAddressLogged()
}

func TestTLSApplicationProtocols(t *testing.T) {
	cert := newCertificate(t, testDomain, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := httpServer(http.NotFoundHandler(), log.New(io.Discard, "", 0), readTimeout, idleTimeout)
	admitAll := func(netip.Addr) (func(), bool) { return func() {}, true }
	ln := listenTLS(hs, tcp, tlsConfig(func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return cert, nil }), 8,
		admitAll)
	go hs.Serve(ln)
	defer hs.Close()
	r := &tlsRelay{t: t, addr: tcp.Addr().String(), roots: roots(cert)}

	var accepted []string
	for _, proto := range []string{"h2", "h2c", "h3", "http/1.0", "http/1.1", "acme-tls/1", "spdy/3.1", "webrtc"} {
		c, err := r.dialTLS(testDomain, proto)
		if err != nil {
			continue
		}
		if got := c.ConnectionState().NegotiatedProtocol; got != proto {
			t.Errorf("offered %q, negotiated %q", proto, got)
		}
		c.Close()
		accepted = append(accepted, proto)
	}
	if !slices.Equal(accepted, []string{"http/1.1", "acme-tls/1"}) {
		t.Fatalf("the relay accepts %q, want exactly http/1.1 and acme-tls/1", accepted)
	}
	c, err := r.dialTLS(testDomain, "h2", "http/1.1") // what browsers offer
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.ConnectionState().NegotiatedProtocol; got != "http/1.1" {
		t.Fatalf("offered h2 and http/1.1, negotiated %q", got)
	}
	tls12, err := tls.Dial("tcp", r.addr, &tls.Config{ServerName: testDomain, RootCAs: r.roots,
		MaxVersion: tls.VersionTLS11})
	if err == nil {
		tls12.Close()
		t.Fatal("the relay accepted TLS 1.1")
	}
}

func TestTLSPendingSocketsBounded(t *testing.T) {
	r := startTLS(t, 2, 5*time.Second)
	var idle []net.Conn
	for range 2 {
		c, err := net.Dial("tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		idle = append(idle, c)
	}
	time.Sleep(100 * time.Millisecond) // both accepted

	if c, err := r.dialTLS(testDomain, "http/1.1"); err == nil {
		c.Close()
		t.Fatal("a third socket got its handshake while two idle sockets held both slots")
	}
	idle[0].Close()
	c, err := r.dialTLS(testDomain, "http/1.1")
	if err != nil {
		t.Fatalf("a socket closed, so a new handshake must go through: %v", err)
	}
	c.Close()

	// One idle socket holds one slot; WebSockets give theirs back once upgraded.
	for i := range 3 {
		ws, err := r.dialWS(2 * time.Second)
		if err != nil {
			t.Fatalf("WebSocket %d: %v", i+1, err)
		}
		defer ws.Close()
	}
	resp, err := r.get("/health")
	if err != nil {
		t.Fatalf("/health beside three WebSockets and an idle socket: %v", err)
	}
	resp.Body.Close()
}

// One address holds at most its WebSocket allowance of pending sockets, so it
// can't take every slot: one more is closed at once although slots are free,
// and a socket that closes gives its share back. A request that doesn't
// upgrade ends its connection, so it doesn't keep a slot either.
func TestTLSOneAddressHoldsOnlyItsShareOfPendingSockets(t *testing.T) {
	r := startTLS(t, 8, 5*time.Second, "--max-connections-per-ip", "2")
	var held []net.Conn
	for range 2 {
		c, err := net.Dial("tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		held = append(held, c)
	}
	time.Sleep(100 * time.Millisecond) // both accepted

	extra, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	if !closedByServer(extra, time.Second) {
		t.Fatal("a third pending socket from one address must be closed at once")
	}
	held[0].Close()
	time.Sleep(100 * time.Millisecond)
	resp, err := r.get("/health")
	if err != nil {
		t.Fatalf("a held socket closed, so the address may connect again: %v", err)
	}
	resp.Body.Close()
	if !resp.Close {
		t.Fatal("a request that doesn't upgrade must end its connection")
	}
	time.Sleep(100 * time.Millisecond)
	ws, err := r.dialWS(2 * time.Second)
	if err != nil {
		t.Fatalf("the /health connection ended, so its share is free again: %v", err)
	}
	ws.Close()
	r.noAddressLogged()
}

func TestTLSHandshakeWithinReadTimeout(t *testing.T) {
	r := startTLS(t, maxPending, 300*time.Millisecond)
	conn, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	if !closedByServer(conn, 2*time.Second) || time.Since(start) < 250*time.Millisecond {
		t.Fatal("a socket without a handshake must be closed after the read timeout, not before")
	}
	r.noAddressLogged()
}

func TestTLSHandshakeErrorsNotLogged(t *testing.T) {
	r := startTLS(t, maxPending, 300*time.Millisecond)
	fail := []func(net.Conn){
		func(c net.Conn) { io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n") },
		func(c net.Conn) { c.Write(bytes.Repeat([]byte{0x16, 0x03, 0x01, 0x00, 0x05}, 4)) },
		func(c net.Conn) { tls.Client(c, &tls.Config{ServerName: "other.test", RootCAs: r.roots}).Handshake() },
		func(c net.Conn) {
			tls.Client(c, &tls.Config{ServerName: testDomain, RootCAs: r.roots, NextProtos: []string{"h2"}}).Handshake()
		},
		func(c net.Conn) { tls.Client(c, &tls.Config{ServerName: testDomain}).Handshake() }, // untrusted: client aborts
		func(net.Conn) {}, // nothing until the read timeout
	}
	for _, f := range fail {
		c, err := net.Dial("tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		f(c)
		closedByServer(c, 2*time.Second)
		c.Close()
	}
	resp, err := r.get("/health") // the relay still serves
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	time.Sleep(100 * time.Millisecond)
	r.noAddressLogged()
}

func TestCertificateCheckLogs(t *testing.T) {
	acmeSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer acmeSrv.Close()
	state := t.TempDir()
	const day = 24 * time.Hour
	now := time.Now()
	// 90-day certificates renew from 30 days before expiry; overdue a day later.
	certs := map[string][2]time.Time{
		"fresh.test":   {now.Add(-day), time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)},
		"due.test":     {now.Add(-90*day + 29*day + 12*time.Hour), now.Add(29*day + 12*time.Hour)},
		"overdue.test": {now.Add(-90*day + 28*day + 12*time.Hour), now.Add(28*day + 12*time.Hour)},
	}
	for name, validity := range certs {
		writeCached(t, filepath.Join(state, "acme", name), newCertificate(t, name, validity[0], validity[1]))
	}
	_, l, err := parseConfig([]string{"--state-dir", state, "--tls-domains", "fresh.test,due.test,overdue.test,new.test",
		"--acme-directory", acmeSrv.URL + "/directory"})
	if err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // one round
	checkCertificates(ctx, certManager(l, state), l.tlsDomains, time.Hour)
	got := logs.String()
	date := func(name string) string { return certs[name][1].UTC().Format(time.DateOnly) }
	for _, want := range []string{
		"relay: certificate for fresh.test valid until 2099-01-02\n",
		"relay: certificate for due.test valid until " + date("due.test") + "\n",
		"relay: certificate for overdue.test valid until " + date("overdue.test") + ", renewal overdue\n",
		"relay: certificate for new.test: ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in the log:\n%s", want, got)
		}
	}
}

// newCertificate is a self-signed ECDSA certificate for name, its own root.
func newCertificate(t *testing.T, name string, notBefore, notAfter time.Time) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{name},
		NotBefore: notBefore, NotAfter: notAfter, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// writeCached stores cert where the ACME manager's cache keeps it: the key,
// then the chain, PEM-encoded.
func writeCached(t *testing.T, path string, cert *tls.Certificate) {
	t.Helper()
	key, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	pem.Encode(&b, &pem.Block{Type: "EC PRIVATE KEY", Bytes: key})
	for _, der := range cert.Certificate {
		pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func roots(cert *tls.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	return pool
}

// captureLogs sends the standard logger to a buffer until the test ends.
func captureLogs(t *testing.T) *lockedBuffer {
	logs := &lockedBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return logs
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
