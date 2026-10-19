package relay

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

var errClientAddress = errors.New("invalid client address")

// clientIP is the address the per-IP limits count: the socket's peer, or with
// trustProxy the first X-Forwarded-For hop (the proxy must overwrite the
// header, not append to it).
func clientIP(req *http.Request, trustProxy bool) (netip.Addr, error) {
	if trustProxy {
		if values := req.Header.Values("X-Forwarded-For"); len(values) > 0 {
			first, _, _ := strings.Cut(values[0], ",")
			addr, err := netip.ParseAddr(strings.TrimSpace(first))
			if err != nil || addr.Zone() != "" {
				return netip.Addr{}, errClientAddress
			}
			return addr.Unmap(), nil
		}
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, errClientAddress
	}
	return addr.Unmap().WithZone(""), nil
}
