package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestTokenBucket(t *testing.T) {
	now := time.Unix(0, 0)
	b := newTokenBucket(30, 10, now)
	for i := range 30 {
		if !b.allow(now) {
			t.Fatalf("message %d refused inside the burst", i+1)
		}
	}
	if b.allow(now) {
		t.Fatal("31st message allowed")
	}
	if !b.allow(now.Add(100*time.Millisecond)) || b.allow(now.Add(100*time.Millisecond)) {
		t.Fatal("refill is not 10/s")
	}
	if b.full(now.Add(time.Second)) || !b.full(now.Add(4*time.Second)) {
		t.Fatal("full must report a completely refilled bucket")
	}
}

func ip(s string) ipGroups { return groupsOf(netip.MustParseAddr(s)) }

func testLimits() Limits {
	return Limits{MaxConnections: 3, MaxConnectionsPerIP: 2, MaxRoomsPerIP: 1, IPv6Factor56: 4, IPv6Factor48: 16,
		JoinBurst: 2, JoinsPerSecond: 1, JoinBurstTotal: 1000, JoinsPerSecondTotal: 1000}
}

func TestIPLimits(t *testing.T) {
	l := newIPLimits(testLimits())
	a, b, c := ip("192.0.2.1"), ip("192.0.2.2"), ip("192.0.2.3")
	for _, want := range []connRefusal{connAllowed, connAllowed, connPerIP} {
		if got := l.connect(a); got != want {
			t.Fatalf("connect a: %v, want %v", got, want)
		}
	}
	if l.connect(b) != connAllowed || l.connect(c) != connTotal || l.connections() != 3 {
		t.Fatal("total limit")
	}
	l.disconnect(a)
	if l.connect(c) != connAllowed {
		t.Fatal("a closed connection must free its slot")
	}

	if !l.holdRoom(a) || l.holdRoom(a) || !l.holdRoom(b) {
		t.Fatal("rooms per IP")
	}
	l.releaseRoom(a)
	if !l.holdRoom(a) {
		t.Fatal("a deleted room must free its slot")
	}
	l.releaseRoom(ipGroups{}) // a restored room counts nowhere

	now := time.Unix(0, 0)
	if !l.lookup(a, now) || !l.lookup(a, now) || l.lookup(a, now) || !l.lookup(b, now) {
		t.Fatal("join burst per IP")
	}
	if !l.lookup(a, now.Add(time.Second)) {
		t.Fatal("join refill")
	}
	l.sweep(now.Add(time.Minute))
	if len(l.lookups) != 0 {
		t.Fatalf("refilled buckets must be dropped: %d left", len(l.lookups))
	}
}

// TestIPv6Groups: a /64 has the per-IP limit, its /56 four times it and its
// /48 sixteen times it, so many /64s of one block share the wider limits.
func TestIPv6Groups(t *testing.T) {
	cfg := testLimits()
	cfg.MaxConnections = 1000
	l := newIPLimits(cfg)
	if g := ip("2001:db8:aa:bb01:1:2:3:4"); g != (ipGroups{"2001:db8:aa:bb01::/64", "2001:db8:aa:bb00::/56",
		"2001:db8:aa::/48"}) {
		t.Fatalf("groups %v", g)
	}
	if g := ip("::ffff:192.0.2.9"); g != (ipGroups{"192.0.2.9"}) {
		t.Fatalf("an IPv4-mapped address is IPv4: %v", g)
	}
	// The /56 2001:db8:0:100::/56 holds 4 x 2 = 8 connections over its /64s.
	connected := 0
	for i := range 6 {
		g := ip(fmt.Sprintf("2001:db8:0:1%02x::1", i))
		for l.connect(g) == connAllowed {
			connected++
		}
	}
	if connected != 8 {
		t.Fatalf("one /56 got %d connections, want 8", connected)
	}
	// The /48 holds 16 x 2 = 32: three more full /56s, then nothing.
	for i := 2; i < 6; i++ {
		for j := range 4 {
			for l.connect(ip(fmt.Sprintf("2001:db8:0:%x%02x::1", i, j))) == connAllowed {
				connected++
			}
		}
	}
	if connected != 32 {
		t.Fatalf("one /48 got %d connections, want 32", connected)
	}
	if l.connect(ip("2001:db8:1::1")) != connAllowed {
		t.Fatal("another /48 is not limited")
	}
	// A refused request counts in no group: the /64 that was refused still
	// holds its own two connections only.
	if n := l.conns["2001:db8:0:105::/64"]; n != 0 {
		t.Fatalf("a refused /64 counts %d connections", n)
	}
}

// TestRelayJoinBucket: a join takes a token from its groups and from the
// relay-wide bucket only when all of them have one.
func TestRelayJoinBucket(t *testing.T) {
	cfg := testLimits()
	cfg.JoinBurstTotal, cfg.JoinsPerSecondTotal = 3, 1
	l := newIPLimits(cfg)
	now := time.Now()
	a, b := ip("192.0.2.1"), ip("192.0.2.2")
	if !l.lookup(a, now) || !l.lookup(a, now) || !l.lookup(b, now) || l.lookup(b, now) {
		t.Fatal("relay-wide join burst")
	}
	if l.lookups[b[0]].tokens != 1 {
		t.Fatal("a join refused by the relay bucket took the address's token")
	}
	if !l.lookup(b, now.Add(time.Second)) {
		t.Fatal("relay-wide refill")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		remote, xff string
		trust       bool
		want        string
		wantErr     bool
	}{
		{"192.0.2.1:5000", "", false, "192.0.2.1", false},
		{"192.0.2.1:5000", "198.51.100.7", false, "192.0.2.1", false}, // header ignored without --trust-proxy
		{"192.0.2.1:5000", "198.51.100.7, 10.0.0.1", true, "198.51.100.7", false},
		{"192.0.2.1:5000", "", true, "192.0.2.1", false}, // no header: the socket's peer
		{"[2001:db8:1:2:3:4:5:6]:5000", "", false, "2001:db8:1:2:3:4:5:6", false},
		{"[::ffff:192.0.2.9]:5000", "", false, "192.0.2.9", false},
		{"192.0.2.1:5000", "not-an-ip", true, "invalid IP", true},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/v1/ws", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		got, err := clientIP(req, c.trust)
		if got.String() != c.want || (err != nil) != c.wantErr {
			t.Errorf("clientIP(%q, %q, %v) = %v, %v; want %q", c.remote, c.xff, c.trust, got, err, c.want)
		}
	}
}

func TestUpgradeRefusals(t *testing.T) {
	cfg := testConfig()
	cfg.TrustProxy = true
	cfg.Limits.MaxConnectionsPerIP = 2
	cfg.Limits.MaxConnections = 3
	tr := startRelay(t, cfg)
	from := func(ip string) http.Header { return http.Header{"X-Forwarded-For": {ip}} }
	first, _, err := tr.dialWith(from("10.1.1.1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.dialWith(from("10.1.1.1")); err != nil {
		t.Fatal(err)
	}
	if _, resp, err := tr.dialWith(from("10.1.1.1")); err == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third connection from one IP: %v %v, want 429", err, resp)
	}
	if _, _, err := tr.dialWith(from("10.2.2.2")); err != nil {
		t.Fatal(err)
	}
	if _, resp, err := tr.dialWith(from("10.3.3.3")); err == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("connection over the total: %v %v, want 503", err, resp)
	}
	first.ws.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, err := tr.dialWith(from("10.3.3.3"))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a closed connection did not free its slot: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRoomsPerIP(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.MaxRoomsPerIP = 1
	tr := startRelay(t, cfg)
	a, b := tr.dial(), tr.dial() // both from 127.0.0.1
	a.create("A", "Test/1.0")
	b.send(msg{"type": "create", "name": "B", "client": "Test/1.0"})
	b.expectError(errRateLimited)
	a.send(msg{"type": "leave"}) // the room empties and is deleted
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.send(msg{"type": "create", "name": "B", "client": "Test/1.0"})
		if m := b.next(); m["type"] == "joined" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("a deleted room did not free its slot: %v", m)
		}
	}
}

func TestJoinLookupLimit(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.JoinBurst = 2
	cfg.Limits.JoinsPerSecond = 0.001
	tr := startRelay(t, cfg)
	c := tr.dial()
	for range 2 {
		c.send(msg{"type": "join", "code": "ZZZZZZ", "name": "C", "client": "Test/1.0"})
		c.expectError(errRoomNotFound)
	}
	c.send(msg{"type": "join", "code": "ZZZZZZ", "name": "C", "client": "Test/1.0"})
	c.expectError(errRateLimited)
	c.send(msg{"type": "create", "name": "C", "client": "Test/1.0"}) // create doesn't draw on join lookups
	c.expect("joined")
}

func TestMessageRateLimit(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.MessageBurst = 3
	cfg.Limits.MessagesPerSecond = 0.001
	tr := startRelay(t, cfg)
	c := tr.dial()
	for i := range 3 {
		c.send(msg{"type": "ping", "id": i})
		c.expect("pong")
	}
	c.send(msg{"type": "ping", "id": 3})
	m := c.expect("error")
	if m["code"] != errRateLimited || m["re"] != "ping" {
		t.Fatalf("over the message rate: %v", m)
	}
}
