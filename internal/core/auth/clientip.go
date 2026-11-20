package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// parseAddr reads an address as it comes from a header or a socket: with or without a port, IPv6 in brackets or
// not, an IPv4 address mapped into IPv6 as the plain IPv4 address.
func parseAddr(text string) (netip.Addr, bool) {
	text = strings.TrimSpace(text)
	if ap, err := netip.ParseAddrPort(text); err == nil {
		return ap.Addr().WithZone("").Unmap(), true
	}
	text = strings.TrimSuffix(strings.TrimPrefix(text, "["), "]")
	if a, err := netip.ParseAddr(text); err == nil {
		return a.WithZone("").Unmap(), true
	}
	return netip.Addr{}, false
}

// sameAddress reports whether two texts name the same client address; one that is no address never matches.
func sameAddress(a, b string) bool {
	x, okA := parseAddr(a)
	y, okB := parseAddr(b)
	return okA && okB && x == y
}

// connectionAddr is the address of the connection that carried the request: the browser, or the proxy in front of
// the server.
func connectionAddr(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func trusted(proxies []netip.Prefix, a netip.Addr) bool {
	for _, p := range proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ownAddresses lists the addresses of the network interfaces of this computer, loopback included.
func ownAddresses() []netip.Addr {
	nets, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make([]netip.Addr, 0, len(nets))
	for _, n := range nets {
		var ip net.IP
		switch v := n.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if a, ok := netip.AddrFromSlice(ip); ok {
			out = append(out, a.WithZone("").Unmap())
		}
	}
	return out
}

// clientIP is the address of the browser. It is the address of the connection, unless that is one of the configured
// reverse proxies: then it is the nearest address in the X-Forwarded-For header that is not a proxy itself. What
// the proxies did not add, the left side of the header, is never believed.
func clientIP(r *http.Request, proxies []netip.Prefix) string {
	peer := connectionAddr(r)
	if len(proxies) == 0 {
		return peer
	}
	if a, ok := parseAddr(peer); !ok || !trusted(proxies, a) {
		return peer
	}
	var hops []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		for _, hop := range strings.Split(line, ",") {
			if hop = strings.TrimSpace(hop); hop != "" {
				hops = append(hops, hop)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseAddr(hops[i])
		if !ok {
			return peer // a hop that is no address: do not guess
		}
		if !trusted(proxies, a) {
			return a.String()
		}
	}
	return peer
}

// addressDetail says how a request reached the server, for the log of a refused sign-in: the address of its
// connection (a proxy, when there is one), whether that is a trusted proxy and the X-Forwarded-For header that came
// with it. Both are text from the network, so they are cut short.
func addressDetail(r *http.Request, proxies []netip.Prefix) []any {
	conn := connectionAddr(r)
	isTrusted := false
	if a, ok := parseAddr(conn); ok {
		isTrusted = trusted(proxies, a)
	}
	out := []any{"connection", clip(conn, 64), "trusted_proxy", isTrusted}
	if fwd := strings.Join(r.Header.Values("X-Forwarded-For"), ", "); fwd != "" {
		out = append(out, "forwarded", clip(fwd, 256))
	}
	return out
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
