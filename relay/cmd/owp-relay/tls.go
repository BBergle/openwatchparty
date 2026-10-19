package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// maxPending bounds the public TLS listener's sockets that are not WebSockets
// yet: in the TLS handshake or reading their one request (the listener's
// server keeps no connection alive after a request that doesn't upgrade).
// When all are taken the relay stops accepting, and new connections wait in
// the kernel's accept queue until a socket upgrades or closes. A pending socket
// is closed after readTimeout without a handshake or a request, so idle
// sockets can delay new connections but never exhaust memory or file
// descriptors. One address group holds at most its WebSocket allowance of them
// (relay.Server.AdmitPending); a socket beyond that is closed at once, so a
// single address can't take every slot.
//
// 1024 because a pending socket costs the relay about 40 KiB (measured: RSS
// with 1000 sockets stalled mid-handshake, or after the handshake without a
// request), so all of them take about 40 MiB beside 10,000 WebSockets on a
// 1 GB server, and the listener's open sockets stay under --max-connections
// + 1024. A real client is pending for about two round trips (TLS 1.3
// handshake, then the upgrade request), well under half a second, so 1024
// slots let more than 2,000 clients connect per second: all 10,000 within a
// few seconds after a restart.
const maxPending = 1024

// certCheckInterval is how often the relay asks for each certificate and logs
// its expiry.
const certCheckInterval = 24 * time.Hour

// certManager gets certificates for exactly l.tlsDomains from
// l.acmeDirectory over TLS-ALPN-01, renews each in the background from
// renewBefore its expiry, and keeps them and the ACME account key in
// <state-dir>/acme.
func certManager(l listen, stateDir string) *autocert.Manager {
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(filepath.Join(stateDir, "acme")),
		HostPolicy: autocert.HostWhitelist(l.tlsDomains...),
		Client:     &acme.Client{DirectoryURL: l.acmeDirectory},
		Email:      l.acmeEmail,
	}
}

// tlsConfig is the public listener's TLS: version 1.2 or later, certificates
// from getCertificate only, and two application protocols, HTTP/1.1 (the
// WebSocket upgrade) and acme-tls/1 (the ACME challenge). No HTTP/2.
func tlsConfig(getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1", acme.ALPNProto},
		GetCertificate: getCertificate}
}

// renewBefore is when autocert starts renewing a certificate: a third of its
// lifetime before it expires (30 days for a 90-day certificate), at most 30
// days. After a failure it retries within the hour.
func renewBefore(leaf *x509.Certificate) time.Duration {
	return min(leaf.NotAfter.Sub(leaf.NotBefore)/3, 30*24*time.Hour)
}

// checkCertificates asks m for each name's certificate now and then every
// interval until ctx ends, and logs one line per name: the expiry date, marked
// overdue when renewal has been due for over a day, or the error. The first
// round starts issuance at startup rather than on the first client's
// handshake. A failed round is retried at the next one, and by any handshake
// for that name.
func checkCertificates(ctx context.Context, m *autocert.Manager, names []string, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		for _, name := range names {
			// What a client that supports ECDSA asks for, as every current one does.
			hello := &tls.ClientHelloInfo{ServerName: name,
				CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256}}
			cert, err := m.GetCertificate(hello)
			switch {
			case err != nil:
				log.Printf("relay: certificate for %s: %v", name, err)
			case time.Until(cert.Leaf.NotAfter) < renewBefore(cert.Leaf)-24*time.Hour:
				log.Printf("relay: certificate for %s valid until %s, renewal overdue", name,
					cert.Leaf.NotAfter.UTC().Format(time.DateOnly))
			default:
				log.Printf("relay: certificate for %s valid until %s", name, cert.Leaf.NotAfter.UTC().Format(time.DateOnly))
			}
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
	}
}

// tlsListener serves TLS on a TCP listener. It completes each handshake
// before hs sees the connection, within hs.ReadTimeout, and closes a failed
// one without an answer or a log line: net/http would answer plain HTTP with
// a 400 and log "TLS handshake error from <client address>". It holds at most
// maxPending sockets that are not WebSockets yet, and from each address only
// those admit counts.
type tlsListener struct {
	tcp       net.Listener
	config    *tls.Config
	handshake time.Duration
	admit     func(netip.Addr) (release func(), ok bool)
	pending   chan struct{} // one value per accepted socket that hasn't upgraded or closed
	conns     chan net.Conn // handshakes done
	errs      chan error    // accept errors, for hs to retry or stop on
	done      chan struct{}
	closeOnce sync.Once
}

// listenTLS returns the listener hs serves TLS on: ln's connections with
// config, at most maxPending of them pending and each admitted by admit. hs
// closes a connection after a request that doesn't upgrade.
func listenTLS(hs *http.Server, ln net.Listener, config *tls.Config, maxPending int,
	admit func(netip.Addr) (release func(), ok bool)) net.Listener {
	l := &tlsListener{tcp: ln, config: config, handshake: hs.ReadTimeout, admit: admit,
		pending: make(chan struct{}, maxPending), conns: make(chan net.Conn), errs: make(chan error),
		done: make(chan struct{})}
	hs.ConnState = l.connState
	hs.SetKeepAlivesEnabled(false)
	go l.acceptLoop()
	return l
}

func (l *tlsListener) acceptLoop() {
	for {
		select {
		case l.pending <- struct{}{}: // waits while maxPending sockets are pending
		case <-l.done:
			return
		}
		c, err := l.tcp.Accept()
		if err != nil {
			<-l.pending
			select {
			case l.errs <- err: // http.Server retries a temporary error after a pause and stops on others
			case <-l.done:
				return
			}
			continue
		}
		release, ok := l.admit(c.RemoteAddr().(*net.TCPAddr).AddrPort().Addr())
		if !ok {
			c.Close()
			<-l.pending
			continue
		}
		go l.serveHandshake(&pendingConn{Conn: c, pending: l.pending, release: release})
	}
}

func (l *tlsListener) serveHandshake(c *pendingConn) {
	tc := tls.Server(c, l.config)
	c.SetDeadline(time.Now().Add(l.handshake))
	if err := tc.Handshake(); err != nil {
		c.Close()
		return
	}
	c.SetDeadline(time.Time{}) // http.Server sets its own per request
	select {
	case l.conns <- tc:
	case <-l.done:
		tc.Close()
	}
}

func (l *tlsListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.errs:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *tlsListener) Close() error {
	err := net.ErrClosed
	l.closeOnce.Do(func() {
		close(l.done)
		err = l.tcp.Close()
	})
	return err
}

func (l *tlsListener) Addr() net.Addr { return l.tcp.Addr() }

// connState frees an upgraded socket's slot: from then on --max-connections
// and the per-IP limits govern it.
func (l *tlsListener) connState(c net.Conn, state http.ConnState) {
	if state == http.StateHijacked {
		c.(*tls.Conn).NetConn().(*pendingConn).free()
	}
}

// pendingConn is an accepted socket. It holds a slot of its listener's pending
// channel, and its address's count, until it upgrades or closes.
type pendingConn struct {
	net.Conn
	pending chan struct{}
	release func()
	freed   atomic.Bool
}

func (c *pendingConn) free() {
	if c.freed.CompareAndSwap(false, true) {
		c.release()
		<-c.pending
	}
}

func (c *pendingConn) Close() error {
	err := c.Conn.Close()
	c.free()
	return err
}
