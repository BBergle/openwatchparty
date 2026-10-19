package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// SPEC §3: endpoints and transport.

// scOrigin: the relay accepts a WebSocket from any Origin.
func scOrigin(x *sc) error {
	c, err := x.dialHeader(x.url, "C", http.Header{
		"Origin": {"https://some-site.example"}, "X-Forwarded-For": {nextAddr()},
	})
	if err != nil {
		return fmt.Errorf("upgrade with a foreign Origin: %w", err)
	}
	_ = c.send(Msg{"type": "ping", "id": "o"})
	_, err = c.waitFor(reply, "pong", isType("pong"))
	return err
}

// scOversize: a message of exactly maxMessageBytes is accepted, in one frame or in fragments; one byte more, in one
// frame or in fragments, closes the connection with 1009 and no error.
func scOversize(x *sc) error {
	limit := int(x.lim("maxMessageBytes"))
	pre := `{"type":"ping","id":"edge","pad":"`
	edge := []byte(pre + strings.Repeat("x", limit-len(pre)-2) + `"}`)
	over := []byte(pre + strings.Repeat("x", limit-len(pre)-1) + `"}`)
	c, err := x.dial("C")
	if err != nil {
		return err
	}
	pong := func(what string) error {
		p, err := c.waitFor(reply, "pong for "+what, func(m Msg) bool { return isType("pong")(m) || isType("error")(m) })
		if err != nil {
			return err
		}
		if str(p, "type") != "pong" || str(p, "id") != "edge" {
			return fmt.Errorf("%s: got %s, want pong", what, brief(p))
		}
		return nil
	}
	// The dialer's write buffer is larger than the limit, so gorilla sends each of these as one frame.
	if err := c.sendRaw(websocket.TextMessage, edge); err != nil {
		return err
	}
	if err := pong(fmt.Sprintf("one %d-byte frame", limit)); err != nil {
		return err
	}
	if err := c.sendFrames(fragments(edge, 3)...); err != nil {
		return err
	}
	if err := pong(fmt.Sprintf("a %d-byte message in 3 fragments", limit)); err != nil {
		return err
	}
	if err := c.sendRaw(websocket.TextMessage, over); err != nil {
		return err
	}
	if err := closedWith(c, websocket.CloseMessageTooBig, false); err != nil {
		return fmt.Errorf("one frame of %d bytes: %w", len(over), err)
	}
	d, err := x.dial("D")
	if err != nil {
		return err
	}
	if err := d.sendFrames(fragments(over, 2)...); err != nil {
		return err
	}
	if err := closedWith(d, websocket.CloseMessageTooBig, false); err != nil {
		return fmt.Errorf("%d bytes in 2 fragments: %w", len(over), err)
	}
	return nil
}

// fragments splits one text message into n frames.
func fragments(b []byte, n int) []frame {
	var fs []frame
	size := (len(b) + n - 1) / n
	for i := 0; i < len(b); i += size {
		end := min(i+size, len(b))
		op := websocket.TextMessage
		if i > 0 {
			op = 0
		}
		fs = append(fs, frame{op: op, fin: end == len(b), payload: b[i:end]})
	}
	return fs
}

// scUTF8: a text message that isn't valid UTF-8 anywhere in it closes the connection with 1007 and no error, checked
// before the rate limit; a character split across fragments is valid; a binary message is invalid_message whatever
// its bytes; invalid UTF-8 in a setMedia never reaches the other members (§3.1).
func scUTF8(x *sc) error {
	c, err := x.dial("C")
	if err != nil {
		return err
	}
	msg := []byte(`{"type":"ping","id":"x😀é"}`)
	i, j := bytes.Index(msg, []byte("😀")), bytes.Index(msg, []byte("é"))
	if err := c.sendFrames(
		frame{op: websocket.TextMessage, payload: msg[:i+1]},
		frame{payload: msg[i+1 : i+3]},
		frame{payload: msg[i+3 : j+1]},
		frame{fin: true, payload: msg[j+1:]},
	); err != nil {
		return err
	}
	p, err := c.waitFor(reply, "pong", func(m Msg) bool { return isType("pong")(m) || isType("error")(m) })
	if err != nil {
		return fmt.Errorf("characters split across fragments: %w", err)
	}
	if str(p, "type") != "pong" || str(p, "id") != "x😀é" {
		return fmt.Errorf("characters split across fragments: got %s, want pong id %q", brief(p), "x😀é")
	}
	if err := c.sendRaw(websocket.BinaryMessage, []byte("{\"type\":\"ping\",\"id\":\"\xff\"}")); err != nil {
		return err
	}
	if err := expectErr(c, "invalid_message", ""); err != nil {
		return fmt.Errorf("binary message that isn't UTF-8: %w", err)
	}
	for _, s := range []struct {
		what   string
		frames []frame
	}{
		{"a lone continuation byte", []frame{textFrame([]byte("{\"type\":\"ping\",\"id\":\"a\x80\"}"))}},
		{"an overlong encoding", []frame{textFrame([]byte("{\"type\":\"ping\",\"id\":\"\xc0\xaf\"}"))}},
		{"an encoded surrogate", []frame{textFrame([]byte("{\"type\":\"ping\",\"id\":\"\xed\xa0\x80\"}"))}},
		{"a code point above U+10FFFF", []frame{textFrame([]byte("{\"type\":\"ping\",\"id\":\"\xf4\x90\x80\x80\"}"))}},
		{"invalid UTF-8 in a member name", []frame{textFrame([]byte("{\"type\":\"ping\",\"id\":1,\"\xff\":1}"))}},
		{"invalid UTF-8 between members", []frame{textFrame([]byte("{\"type\":\"ping\",\xff\"id\":1}"))}},
		{"a character cut by the end of a fragment", []frame{
			{op: websocket.TextMessage, payload: []byte("{\"type\":\"ping\",\"id\":\"x\xf0\x9f")},
			{fin: true, payload: []byte(`"}`)},
		}},
	} {
		d, err := x.dial("D")
		if err != nil {
			return err
		}
		if err := d.sendFrames(s.frames...); err != nil {
			return err
		}
		if err := closedWith(d, websocket.CloseInvalidFramePayloadData, true); err != nil {
			return fmt.Errorf("%s: %w", s.what, err)
		}
	}
	// More messages than the bucket holds, then invalid UTF-8: the relay closes with 1007, which it can only do
	// when it checks UTF-8 before the rate limit; otherwise the last message gets rate_limited with re null. Answers
	// to the pings before it may be cut off by the close.
	r, err := x.dial("R")
	if err != nil {
		return err
	}
	r.pace = nil
	n := int(x.lim("messageBurst")) + 3
	var fs []frame
	for k := range n {
		fs = append(fs, textFrame(fmt.Appendf(nil, `{"type":"ping","id":%d}`, k)))
	}
	fs = append(fs, textFrame([]byte("{\"type\":\"ping\",\"id\":\"\xff\"}")))
	if err := r.sendFrames(fs...); err != nil {
		return err
	}
	for {
		m, err := r.next(reply)
		if errors.Is(err, errClosed) {
			break
		}
		if err != nil {
			return fmt.Errorf("%d pings, then invalid UTF-8: connection still open (%v)", n, err)
		}
		if !isType("pong")(m) && !(isError("rate_limited")(m) && str(m, "re") == "ping") {
			return fmt.Errorf("%d pings, then invalid UTF-8: got %s; want only answers to the pings, then the close",
				n, brief(m))
		}
	}
	var ce *websocket.CloseError
	if !errors.As(r.closeErr, &ce) || ce.Code != websocket.CloseInvalidFramePayloadData {
		return fmt.Errorf("invalid UTF-8 with the message bucket empty: connection ended with %v, want close status 1007",
			r.closeErr)
	}
	// Invalid UTF-8 in setMedia's media: the sender is closed, the room never sees the media.
	cs, err := x.roomOf(2)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	if err := a.sendRaw(websocket.TextMessage,
		[]byte("{\"type\":\"setMedia\",\"media\":{\"kind\":\"movie\",\"title\":\"\xff\",\"durationMs\":60000}}")); err != nil {
		return err
	}
	if err := closedWith(a, websocket.CloseInvalidFramePayloadData, false); err != nil {
		return fmt.Errorf("setMedia with invalid UTF-8 in its title: %w", err)
	}
	d, err := b.waitFor(reply, "A disconnected", func(m Msg) bool { return hasMedia(m) || connectedIs(a.PeerID, false)(m) })
	if err != nil {
		return err
	}
	if hasMedia(d) {
		return fmt.Errorf("%s: got media %s after a setMedia with invalid UTF-8", b.name, brief(d))
	}
	if num(d, "epoch") != 0 || str(d, "phase") != "idle" {
		return fmt.Errorf("room after a setMedia with invalid UTF-8 closed its sender: %s; want epoch 0, idle", brief(d))
	}
	return b.expectNone(300*time.Millisecond, "media after a setMedia with invalid UTF-8", hasMedia)
}

// scMessageRate: the bucket starts full with messageBurst tokens and refills at messagesPerSecond; every data message
// takes one, text or binary, valid or not. A message without a token is dropped and answered with rate_limited, re
// its type or null, and the connection stays open.
func scMessageRate(x *sc) error {
	c, err := x.dial("C")
	if err != nil {
		return err
	}
	burst, rate := int(x.lim("messageBurst")), x.lim("messagesPerSecond")
	c.pace = nil // this scenario overruns the bucket on purpose
	var fs []frame
	var kinds []string // what each message is: "bad" (invalid, no string type) or "ping"
	for i := range burst {
		if i%2 == 0 {
			fs = append(fs, textFrame([]byte("nope")))
		} else {
			fs = append(fs, frame{op: websocket.BinaryMessage, fin: true, payload: []byte(`{"type":"ping","id":0}`)})
		}
		kinds = append(kinds, "bad")
	}
	for i := range burst {
		fs = append(fs, textFrame(fmt.Appendf(nil, `{"type":"ping","id":%d}`, i)))
		kinds = append(kinds, "ping")
	}
	for range 3 {
		fs = append(fs, textFrame([]byte("nope")))
		kinds = append(kinds, "bad")
	}
	t0 := time.Now()
	if err := c.sendFrames(fs...); err != nil {
		return err
	}
	// The relay answers every message once, in order (§6.2, §8).
	served, limited := 0, 0
	for i, kind := range kinds {
		m, err := c.next(reply)
		if err != nil {
			return fmt.Errorf("answer %d of %d: %v", i+1, len(kinds), err)
		}
		ok := (kind == "ping" && isType("pong")(m)) || (kind == "bad" && isError("invalid_message")(m) && m["re"] == nil)
		switch {
		case ok && i < burst:
		case ok:
			served++
		case i >= burst && isError("rate_limited")(m):
			want := any(nil)
			if kind == "ping" {
				want = "ping"
			}
			if m["re"] != want {
				return fmt.Errorf("message %d (%s): rate_limited re %v, want %v", i+1, kind, m["re"], want)
			}
			limited++
		default:
			return fmt.Errorf("message %d of %d (%s): got %s", i+1, len(kinds), kind, brief(m))
		}
	}
	refill := int(rate*time.Since(t0).Seconds()) + 1
	if limited == 0 || served > refill {
		return fmt.Errorf("%d messages at once: the first %d served, then %d more served and %d rate_limited; want at "+
			"most %d more served (refill)", len(kinds), burst, served, limited, refill)
	}
	if m, err := c.next(300 * time.Millisecond); err == nil {
		return fmt.Errorf("an answer more than the %d messages: %s", len(kinds), brief(m))
	}
	time.Sleep(time.Duration(float64(time.Second)/rate) + 100*time.Millisecond)
	if err := c.send(Msg{"type": "ping", "id": "after"}); err != nil {
		return err
	}
	m, err := c.waitFor(reply, "answer after refill", func(m Msg) bool { return isType("pong")(m) || isType("error")(m) })
	if err != nil {
		return err
	}
	if str(m, "type") != "pong" || str(m, "id") != "after" {
		return fmt.Errorf("after refill: got %s, want pong id after", brief(m))
	}
	return nil
}

// Keepalive constants (§3.1); never scaled.
const (
	pingEvery = 30 * time.Second
	idleClose = 60 * time.Second
)

// scKeepalive: the relay pings every 30 s and closes a connection it has received no frame from for 60 s; a client
// that only answers pings stays connected.
func scKeepalive(x *sc) error {
	silent, err := x.dial("Silent")
	if err != nil {
		return err
	}
	silent.mute.Store(true)
	t0 := time.Now()
	alive, err := x.dial("Alive")
	if err != nil {
		return err
	}
	if _, err := silent.waitFor(idleClose+3*time.Second, "close of a silent connection", func(Msg) bool { return false }); !errors.Is(err, errClosed) && !silent.isClosed() {
		return fmt.Errorf("silent connection still open %v after its last frame", idleClose+3*time.Second)
	}
	el := time.Since(t0)
	if el < idleClose-100*time.Millisecond || el > idleClose+3*time.Second {
		return fmt.Errorf("silent connection closed %v after its last frame, want %v", el.Round(time.Millisecond), idleClose)
	}
	if silent.pings.Load() == 0 {
		return fmt.Errorf("silent connection got no ping frame in %v", el.Round(time.Millisecond))
	}
	time.Sleep(time.Until(t0.Add(idleClose + 3*time.Second)))
	if n := alive.pings.Load(); n < 2 {
		return fmt.Errorf("connection open %v got %d ping frames, want one every %v", time.Since(t0).Round(time.Second), n, pingEvery)
	}
	_ = alive.send(Msg{"type": "ping", "id": "still"})
	if _, err := alive.waitFor(reply, "pong on a connection that only answered pings", isType("pong")); err != nil {
		return err
	}
	return nil
}

// scIPConnections: every address group of a client IP (§3.1) holds at most maxConnectionsPerIp connections times its
// factor: an IPv4 address and an IPv6 /64 the limit, a /56 ipv6Factor56 times it, a /48 ipv6Factor48 times it. The
// next upgrade gets HTTP 429 and other groups are unaffected; a closed connection frees its place; an IPv4-mapped
// IPv6 address is its IPv4 address; a refused upgrade counts in no group.
func scIPConnections(x *sc) error {
	max := int(x.lim("maxConnectionsPerIp"))
	f56, f48 := int(x.lim("ipv6Factor56")), int(x.lim("ipv6Factor48"))
	fill := func(n int, addrs func(i int) string) ([]*Client, error) {
		var held []*Client
		for i := range n {
			c, err := x.dialFrom(fmt.Sprintf("C%d", i), addrs(i))
			if err != nil {
				return nil, err
			}
			held = append(held, c)
		}
		return held, nil
	}
	// IPv4, half of it in the IPv4-mapped form.
	addr := nextAddr()
	held, err := fill(max, func(i int) string {
		if i%2 == 1 {
			return mapped(addr)
		}
		return addr
	})
	if err != nil {
		return err
	}
	if err := refusedUpgradeFrom(x, x.url, "Over", addr, 429); err != nil {
		return err
	}
	if err := refusedUpgradeFrom(x, x.url, "OverMapped", mapped(addr), 429); err != nil {
		return fmt.Errorf("IPv4-mapped form of an IPv4 address at its limit: %w", err)
	}
	if _, err := x.dial("Elsewhere"); err != nil {
		return fmt.Errorf("another IP while one is at its limit: %w", err)
	}
	held[0].close()
	if err := poll(reloadWait, func() (bool, error) {
		_, err := x.dialFrom("Again", mapped(addr))
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("upgrade after one of the IP's connections closed: %w", err)
	}
	// IPv6: the first /64 of a /56 fills up and refuses one more, which must count nowhere; the /56's next /64s
	// then fill it to its own limit exactly.
	n := newV6Net()
	if _, err := fill(max, func(i int) string { return n.at(0, 0, i) }); err != nil {
		return err
	}
	if err := refusedUpgradeFrom(x, x.url, "Over6", n.at(0, 0, max), 429); err != nil {
		return fmt.Errorf("IPv6 in a /64 at its limit: %w", err)
	}
	var second []*Client
	for h := 1; h < f56; h++ {
		cs, err := fill(max, func(i int) string { return n.at(0, h, i) })
		if err != nil {
			return fmt.Errorf("/64 number %d of a /56 that holds %d connections, after a refused upgrade: %w",
				h+1, max*f56, err)
		}
		second = append(second, cs...)
	}
	if err := refusedUpgradeFrom(x, x.url, "Over56", n.at(0, f56, 0), 429); err != nil {
		return fmt.Errorf("a new /64 in a /56 with %d connections (ipv6Factor56 %d): %w", max*f56, f56, err)
	}
	if _, err := x.dialFrom("Other56", n.at(1, 0, 0)); err != nil {
		return fmt.Errorf("another /56 of the same /48: %w", err)
	}
	if len(second) > 0 {
		second[0].close()
		if err := poll(reloadWait, func() (bool, error) {
			_, err := x.dialFrom("Again56", n.at(0, f56, 0))
			return err == nil, nil
		}); err != nil {
			return fmt.Errorf("new /64 after a connection of its full /56 closed: %w", err)
		}
	}
	// The /48 holds ipv6Factor48 times the limit, spread over its /56s at their own limit.
	m := newV6Net()
	for k := range f48 {
		if _, err := fill(max, func(i int) string { return m.at(k/f56, k%f56, i) }); err != nil {
			return fmt.Errorf("/64 number %d of a /48 that holds %d connections: %w", k+1, max*f48, err)
		}
	}
	if err := refusedUpgradeFrom(x, x.url, "Over48", m.at(255, 0, 0), 429); err != nil {
		return fmt.Errorf("a new /56 in a /48 with %d connections (ipv6Factor48 %d): %w", max*f48, f48, err)
	}
	if _, err := x.dialFrom("Other48", newV6Net().at(0, 0, 0)); err != nil {
		return fmt.Errorf("another /48: %w", err)
	}
	return nil
}

// scXFFUntrusted: a relay not configured to trust a proxy ignores X-Forwarded-For and counts the TCP peer.
func scXFFUntrusted(x *sc) error {
	p, err := x.startRelay(x.scale)
	if err != nil {
		return err
	}
	max := int(x.lim("maxConnectionsPerIp"))
	for i := range max {
		if _, err := x.dialAt(p.url(), fmt.Sprintf("C%d", i), nextAddr()); err != nil {
			return err
		}
	}
	return refusedUpgrade(x, p.url(), "Over", 429)
}

// scMaxConnections: a relay at maxConnections refuses the upgrade with HTTP 503, reports its limit, and accepts again
// once a connection closes.
func scMaxConnections(x *sc) error {
	p, err := x.startRelay(x.scale, "--max-connections", "3")
	if err != nil {
		return err
	}
	info, _, err := getJSON(p.httpBase() + "/v1/info")
	if err != nil {
		return err
	}
	if v := obj(info, "limits")["maxConnections"]; v != 3.0 {
		return fmt.Errorf("/v1/info limits.maxConnections = %v with --max-connections 3", v)
	}
	var held []*Client
	for i := range 3 {
		c, err := x.dialAt(p.url(), fmt.Sprintf("C%d", i), nextAddr())
		if err != nil {
			return err
		}
		held = append(held, c)
	}
	if err := refusedUpgrade(x, p.url(), "C3", 503); err != nil {
		return err
	}
	held[0].close()
	return poll(reloadWait, func() (bool, error) {
		_, err := x.dialAt(p.url(), "C4", nextAddr())
		return err == nil, nil
	})
}

// refusedUpgrade dials once and expects the relay to answer the upgrade with status.
func refusedUpgrade(x *sc, url, name string, status int) error {
	return refusedUpgradeFrom(x, url, name, nextAddr(), status)
}

func refusedUpgradeFrom(x *sc, url, name, addr string, status int) error {
	_, err := x.dialAt(url, name, addr)
	var de *dialError
	if !errors.As(err, &de) || de.status != status {
		return fmt.Errorf("%s: upgrade over the limit: %v, want HTTP %d without upgrading", name, err, status)
	}
	return nil
}

// closedWith waits for the relay to close c's connection with the given status, with no error or closed before it;
// strict: with no message at all before it.
func closedWith(c *Client, code int, strict bool) error {
	for {
		m, err := c.next(reply)
		if errors.Is(err, errClosed) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: connection still open: %v", c.name, err)
		}
		if strict || isType("closed")(m) || isType("error")(m) {
			return fmt.Errorf("%s: got %s before the close", c.name, brief(m))
		}
	}
	var ce *websocket.CloseError
	if !errors.As(c.closeErr, &ce) || ce.Code != code {
		return fmt.Errorf("%s: connection ended with %v, want close status %d", c.name, c.closeErr, code)
	}
	return nil
}
