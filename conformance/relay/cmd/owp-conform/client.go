package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Msg is a decoded protocol message. Numbers decode as float64.
type Msg = map[string]any

// Client is one WebSocket connection to a relay. A reader goroutine decodes frames into [in]; the conformance
// scenarios consume them in order with [Client.waitFor].
type Client struct {
	name string
	conn *websocket.Conn
	in   chan Msg

	closed   chan struct{}
	closeErr error

	writeMu sync.Mutex

	pings atomic.Int64 // WebSocket ping frames received
	mute  atomic.Bool  // when set, ping frames get no pong
	pace  *pacer       // nil: no pacing

	revMu         sync.Mutex
	prevRev       int64 // rev of the last joined/room on this connection (0: none yet), under revMu
	revViolations []string

	latest Msg // latest room snapshot received (by the reader), under revMu
	// Set from joined.
	PeerID, Token, Code string
}

// WriteBufferSize above the message size limit: gorilla splits a message larger than its write buffer into several
// frames, and the oversize scenario needs one frame over the limit.
var dialer = websocket.Dialer{HandshakeTimeout: 5 * time.Second, WriteBufferSize: 16 << 10}

// useTransport applies --ca-file and --connect to every WebSocket and HTTP connection the tool makes. caFile adds
// its PEM certificates to the system roots; nothing skips verification. connect is the HOST:PORT dialed for relayURL's
// host and port, which stay the TLS server name and the Host header; other hosts are dialed as they are.
func useTransport(relayURL, caFile, connect string) error {
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return fmt.Errorf("--ca-file: %v", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("--ca-file %s: no PEM certificate", caFile)
		}
		cfg := &tls.Config{RootCAs: roots}
		dialer.TLSClientConfig = cfg
		http.DefaultTransport.(*http.Transport).TLSClientConfig = cfg
	}
	if connect != "" {
		u, err := url.Parse(relayURL)
		if err != nil {
			return err
		}
		port := u.Port()
		if port == "" {
			port = map[string]string{"ws": "80", "wss": "443"}[u.Scheme]
		}
		from := net.JoinHostPort(u.Hostname(), port)
		nd := &net.Dialer{Timeout: 5 * time.Second}
		dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr == from {
				addr = connect
			}
			return nd.DialContext(ctx, network, addr)
		}
		dialer.NetDialContext = dial
		http.DefaultTransport.(*http.Transport).DialContext = dial
	}
	return nil
}

// dial connects one client with the given request headers (X-Forwarded-For, Origin).
func dial(url, name string, h http.Header) (*Client, error) {
	conn, resp, err := dialer.Dial(url, h)
	if err != nil {
		if resp != nil {
			return nil, &dialError{name: name, status: resp.StatusCode}
		}
		return nil, fmt.Errorf("dial %s: %w", name, err)
	}
	c := &Client{name: name, conn: conn, in: make(chan Msg, 4096), closed: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

// dialError is a refused upgrade: the relay answered the WebSocket request with a plain HTTP status.
type dialError struct {
	name   string
	status int
}

func (e *dialError) Error() string {
	return fmt.Sprintf("dial %s: refused with HTTP %d", e.name, e.status)
}

func (c *Client) readLoop() {
	defer close(c.closed)
	c.conn.SetPingHandler(func(data string) error {
		c.pings.Add(1)
		if c.mute.Load() {
			return nil
		}
		err := c.conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		if errors.Is(err, websocket.ErrCloseSent) {
			return nil
		}
		return err
	})
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			c.closeErr = err
			return
		}
		if mt != websocket.TextMessage {
			c.addViolation("binary message from relay")
			continue
		}
		var m Msg
		if err := json.Unmarshal(data, &m); err != nil {
			c.addViolation(fmt.Sprintf("non-JSON message from relay: %q", truncate(string(data), 120)))
			continue
		}
		switch str(m, "type") {
		case "joined":
			// joined starts the connection's snapshot order whatever its rev (§8.1).
			room := withType(obj(m, "room"))
			c.revMu.Lock()
			for _, k := range []string{"action", "media"} {
				if _, ok := obj(m, "room")[k]; ok {
					c.revViolations = append(c.revViolations, fmt.Sprintf("joined.room carries %s", k))
				}
			}
			c.prevRev = int64(num(room, "rev"))
			c.latest = room
			c.revMu.Unlock()
		case "room":
			// Every published snapshot reaches every connected member with rev + 1 (§9.2), so a connection sees
			// consecutive revs from its joined on, and with them every snapshot that starts an epoch.
			rev := int64(num(m, "rev"))
			c.revMu.Lock()
			if c.prevRev > 0 && rev != c.prevRev+1 {
				c.revViolations = append(c.revViolations, fmt.Sprintf("room rev %d after %d", rev, c.prevRev))
			}
			c.checkRoom(m, c.latest)
			c.prevRev = rev
			c.latest = m
			c.revMu.Unlock()
		}
		c.in <- m
	}
}

// checkRoom checks what a room carries besides the snapshot: media exactly when its epoch is new (§8.3), and an
// action, when present, with a kind and a by (§9.1). Called under revMu.
func (c *Client) checkRoom(room, prev Msg) {
	if prev != nil {
		_, has := room["media"]
		if newEpoch := num(room, "epoch") != num(prev, "epoch"); has != newEpoch {
			c.revViolations = append(c.revViolations, fmt.Sprintf("rev %v: epoch %v after %v, media present %v",
				room["rev"], room["epoch"], prev["epoch"], has))
		}
	}
	a, has := room["action"]
	if !has {
		return
	}
	am, _ := a.(map[string]any)
	by, hasBy := am["by"]
	if _, isStr := by.(string); str(am, "kind") == "" || !hasBy || (by != nil && !isStr) {
		c.revViolations = append(c.revViolations, fmt.Sprintf("rev %v: action %v", room["rev"], a))
	}
}

func (c *Client) addViolation(s string) {
	c.revMu.Lock()
	c.revViolations = append(c.revViolations, s)
	c.revMu.Unlock()
}

func (c *Client) violations() []string {
	c.revMu.Lock()
	defer c.revMu.Unlock()
	return append([]string(nil), c.revViolations...)
}

func (c *Client) lastRoom() Msg {
	c.revMu.Lock()
	defer c.revMu.Unlock()
	return c.latest
}

// pacer keeps a client under the relay's per-connection message rate, so scenarios that send many messages aren't
// answered with rate_limited. It mirrors the relay's bucket with a margin: two tokens fewer, 10 % slower refill.
type pacer struct {
	mu          sync.Mutex
	tokens, cap float64
	rate        float64 // tokens per second
	last        time.Time
}

func newPacer(burst, rate float64) *pacer {
	p := &pacer{cap: max(burst-2, 1), rate: rate * 0.9, last: time.Now()}
	p.tokens = p.cap
	return p
}

// take spends n tokens, first waiting for them when wait is set.
func (p *pacer) take(n int, wait bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		now := time.Now()
		p.tokens = min(p.cap, p.tokens+now.Sub(p.last).Seconds()*p.rate)
		p.last = now
		if !wait || p.tokens >= float64(n) {
			p.tokens -= float64(n)
			return
		}
		time.Sleep(time.Duration((float64(n) - p.tokens) / p.rate * float64(time.Second)))
	}
}

func (c *Client) send(m Msg) error {
	c.pace.take(1, true)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.conn.WriteJSON(m)
}

func (c *Client) sendRaw(mt int, data []byte) error {
	c.pace.take(1, true)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.conn.WriteMessage(mt, data)
}

// frame is one raw client frame: an opcode (websocket.TextMessage, BinaryMessage, or 0 for a continuation) and
// whether it ends its message.
type frame struct {
	op      int
	fin     bool
	payload []byte
}

func textFrame(b []byte) frame { return frame{op: websocket.TextMessage, fin: true, payload: b} }

// sendBatch writes several messages, one text frame each, in a single write to the socket, so they reach the relay
// back to back (gorilla flushes every message on its own, and a lost segment would hold the later ones back).
func (c *Client) sendBatch(ms ...Msg) error {
	fs := make([]frame, 0, len(ms))
	for _, m := range ms {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		fs = append(fs, textFrame(b))
	}
	return c.sendFrames(fs...)
}

// sendFrames writes raw frames in a single write to the socket, at once: a batch spends tokens without waiting.
func (c *Client) sendFrames(fs ...frame) error {
	c.pace.take(len(fs), false)
	var buf []byte
	for _, f := range fs {
		buf = appendFrame(buf, f)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	nc := c.conn.UnderlyingConn()
	_ = nc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := nc.Write(buf)
	return err
}

// appendFrame appends one masked client frame (RFC 6455 §5.2).
func appendFrame(buf []byte, f frame) []byte {
	var key [4]byte
	_, _ = rand.Read(key[:])
	b0 := byte(f.op)
	if f.fin {
		b0 |= 0x80
	}
	buf = append(buf, b0)
	switch n := len(f.payload); {
	case n < 126:
		buf = append(buf, 0x80|byte(n))
	case n < 1<<16:
		buf = append(buf, 0x80|126, byte(n>>8), byte(n))
	default:
		buf = binary.BigEndian.AppendUint64(append(buf, 0x80|127), uint64(n))
	}
	buf = append(buf, key[:]...)
	for i, b := range f.payload {
		buf = append(buf, b^key[i&3])
	}
	return buf
}

func (c *Client) close() {
	c.writeMu.Lock()
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second))
	c.writeMu.Unlock()
	_ = c.conn.Close()
}

// drop closes the TCP connection without a close frame (a vanished client).
func (c *Client) drop() { _ = c.conn.Close() }

var errClosed = errors.New("connection closed by relay")

func (c *Client) next(timeout time.Duration) (Msg, error) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case m := <-c.in:
		return m, nil
	case <-t.C:
		return nil, errTimeout
	case <-c.closed:
		select {
		case m := <-c.in:
			return m, nil
		default:
			return nil, errClosed
		}
	}
}

var errTimeout = errors.New("timeout")

// waitFor consumes messages until one satisfies pred; others are discarded. The error names what was awaited and the
// last messages seen.
func (c *Client) waitFor(timeout time.Duration, what string, pred func(Msg) bool) (Msg, error) {
	deadline := time.Now().Add(timeout)
	var seen []string
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return nil, fmt.Errorf("%s: no %s within %v (saw: %s)", c.name, what, timeout, strings.Join(tail(seen, 4), " | "))
		}
		m, err := c.next(left)
		if err != nil {
			if errors.Is(err, errTimeout) {
				continue
			}
			return nil, fmt.Errorf("%s: waiting for %s: %v (saw: %s)", c.name, what, err, strings.Join(tail(seen, 4), " | "))
		}
		if pred(m) {
			return m, nil
		}
		seen = append(seen, brief(m))
	}
}

// expectNone fails if a message satisfying pred arrives within d. Other messages are consumed.
func (c *Client) expectNone(d time.Duration, what string, pred func(Msg) bool) error {
	deadline := time.Now().Add(d)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return nil
		}
		m, err := c.next(left)
		if err != nil {
			if errors.Is(err, errTimeout) || errors.Is(err, errClosed) {
				return nil
			}
			return err
		}
		if pred(m) {
			return fmt.Errorf("%s: unexpected %s: %s", c.name, what, brief(m))
		}
	}
}

func (c *Client) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// ---- predicates and accessors ----

func isType(t string) func(Msg) bool { return func(m Msg) bool { return str(m, "type") == t } }

func isError(code string) func(Msg) bool {
	return func(m Msg) bool { return str(m, "type") == "error" && str(m, "code") == code }
}

func isRoom(pred func(Msg) bool) func(Msg) bool {
	return func(m Msg) bool { return str(m, "type") == "room" && pred(m) }
}

func str(m Msg, k string) string {
	v, _ := m[k].(string)
	return v
}

func num(m Msg, k string) float64 {
	v, _ := m[k].(float64)
	return v
}

func obj(m Msg, k string) Msg {
	v, _ := m[k].(map[string]any)
	return v
}

// withType is a copy of the snapshot inside joined as a room message.
func withType(room Msg) Msg {
	if room == nil {
		return nil
	}
	out := Msg{"type": "room"}
	for k, v := range room {
		out[k] = v
	}
	return out
}

func list(m Msg, k string) []any {
	v, _ := m[k].([]any)
	return v
}

func participants(room Msg) []Msg {
	var out []Msg
	for _, p := range list(room, "participants") {
		if pm, ok := p.(map[string]any); ok {
			out = append(out, pm)
		}
	}
	return out
}

func participant(room Msg, peerID string) Msg {
	for _, p := range participants(room) {
		if str(p, "peerId") == peerID {
			return p
		}
	}
	return nil
}

func strList(m Msg, k string) []string {
	var out []string
	for _, v := range list(m, k) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hostOf(room Msg) string {
	for _, p := range participants(room) {
		if b, _ := p["host"].(bool); b {
			return str(p, "peerId")
		}
	}
	return ""
}

// actionOf is the action a room carries: kind "" when it carries none (§9.1).
func actionOf(room Msg) (kind string, by any) {
	a := obj(room, "action")
	if a == nil {
		return "", nil
	}
	return str(a, "kind"), a["by"]
}

func brief(m Msg) string {
	switch str(m, "type") {
	case "room":
		k, by := actionOf(m)
		return fmt.Sprintf("room{rev:%v epoch:%v phase:%s pos:%v at:%v rate:%v waitingOn:%v action:%s/%v n:%d}",
			m["rev"], m["epoch"], str(m, "phase"), m["positionMs"], m["atMs"], m["rate"], strList(m, "waitingOn"), k, by,
			len(participants(m)))
	case "error":
		return fmt.Sprintf("error{%s re:%v}", str(m, "code"), m["re"])
	default:
		b, _ := json.Marshal(m)
		return truncate(string(b), 140)
	}
}

func tail(s []string, n int) []string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// msNow is local monotonic time in ms (relay time is monotonic too, SPEC.md §2).
func msNow(t time.Time) float64 { return float64(t.Sub(processStart)) / 1e6 }

var processStart = time.Now()
