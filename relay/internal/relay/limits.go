package relay

import (
	"net/netip"
	"sync"
	"time"
)

// tokenBucket is a token-bucket rate limiter without a mutex: the owner
// serialises access.
type tokenBucket struct {
	tokens, burst, perSec float64
	last                  time.Time
}

func newTokenBucket(burst, perSec float64, now time.Time) tokenBucket {
	return tokenBucket{tokens: burst, burst: burst, perSec: perSec, last: now}
}

func (b *tokenBucket) refill(now time.Time) {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed.Seconds()*b.perSec)
		b.last = now
	}
}

func (b *tokenBucket) allow(now time.Time) bool {
	b.refill(now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// full reports whether the bucket has refilled completely, so dropping it
// changes nothing.
func (b *tokenBucket) full(now time.Time) bool {
	b.refill(now)
	return b.tokens >= b.burst
}

// ipGroups are the address groups one client IP counts in, narrowest first:
// an IPv4 address alone, or an IPv6 address's /64, /56 and /48. One host
// usually holds a whole /64, and one customer a /56 or /48, so each wider
// group gets a multiple of the per-IP limit (Limits.factors). The zero value
// counts nowhere: a restored room has no creator.
type ipGroups [3]string

func groupsOf(addr netip.Addr) ipGroups {
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return ipGroups{addr.String()}
	}
	var g ipGroups
	for i, bits := range []int{64, 56, 48} {
		g[i] = netip.PrefixFrom(addr, bits).Masked().String()
	}
	return g
}

// connRefusal says why a connection was refused before the upgrade.
type connRefusal int

const (
	connAllowed connRefusal = iota
	connPerIP               // HTTP 429
	connTotal               // HTTP 503
)

// ipLimits holds the per-IP limits, the total connection count and the
// relay-wide join bucket. Groups live here only while they hold a
// connection, a pending socket, a room or a draining lookup bucket.
type ipLimits struct {
	cfg Limits

	mu      sync.Mutex
	total   int
	conns   map[string]int
	pending map[string]int // TLS sockets that are not WebSockets yet
	rooms   map[string]int
	lookups map[string]*tokenBucket
	joins   tokenBucket // every join on the relay
}

func newIPLimits(cfg Limits) *ipLimits {
	return &ipLimits{cfg: cfg, conns: make(map[string]int), pending: make(map[string]int), rooms: make(map[string]int),
		lookups: make(map[string]*tokenBucket),
		joins:   newTokenBucket(float64(cfg.JoinBurstTotal), cfg.JoinsPerSecondTotal, time.Now())}
}

// within reports whether every group of g is below limit times its factor.
func (l *ipLimits) within(counts map[string]int, g ipGroups, limit int) bool {
	factors := l.cfg.factors()
	for i, key := range g {
		if key != "" && counts[key] >= limit*factors[i] {
			return false
		}
	}
	return true
}

func add(counts map[string]int, g ipGroups, n int) {
	for _, key := range g {
		if key == "" {
			continue
		}
		if counts[key] += n; counts[key] <= 0 {
			delete(counts, key)
		}
	}
}

func (l *ipLimits) connect(g ipGroups) connRefusal {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.total >= l.cfg.MaxConnections:
		return connTotal
	case !l.within(l.conns, g, l.cfg.MaxConnectionsPerIP):
		return connPerIP
	}
	l.total++
	add(l.conns, g, 1)
	return connAllowed
}

func (l *ipLimits) disconnect(g ipGroups) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	add(l.conns, g, -1)
}

func (l *ipLimits) connections() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.total
}

// pend counts a pending socket of g unless g already holds as many as its
// WebSocket allowance, MaxConnectionsPerIP times the group's factor.
func (l *ipLimits) pend(g ipGroups) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.within(l.pending, g, l.cfg.MaxConnectionsPerIP) {
		return false
	}
	add(l.pending, g, 1)
	return true
}

func (l *ipLimits) unpend(g ipGroups) {
	l.mu.Lock()
	defer l.mu.Unlock()
	add(l.pending, g, -1)
}

// holdRoom reserves one of g's room slots until releaseRoom.
func (l *ipLimits) holdRoom(g ipGroups) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.within(l.rooms, g, l.cfg.MaxRoomsPerIP) {
		return false
	}
	add(l.rooms, g, 1)
	return true
}

func (l *ipLimits) releaseRoom(g ipGroups) {
	l.mu.Lock()
	defer l.mu.Unlock()
	add(l.rooms, g, -1)
}

// lookup charges one `join` to every group of g and to the relay. It takes
// nothing unless all of them have a token.
func (l *ipLimits) lookup(g ipGroups, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	factors := l.cfg.factors()
	var buckets []*tokenBucket
	for i, key := range g {
		if key == "" {
			continue
		}
		b := l.lookups[key]
		if b == nil {
			nb := newTokenBucket(float64(l.cfg.JoinBurst*factors[i]),
				l.cfg.JoinsPerSecond*float64(factors[i]), now)
			b = &nb
			l.lookups[key] = b
		}
		buckets = append(buckets, b)
	}
	buckets = append(buckets, &l.joins)
	for _, b := range buckets {
		if b.refill(now); b.tokens < 1 {
			return false
		}
	}
	for _, b := range buckets {
		b.tokens--
	}
	return true
}

// sweep drops lookup buckets that have refilled.
func (l *ipLimits) sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, b := range l.lookups {
		if b.full(now) {
			delete(l.lookups, key)
		}
	}
}
