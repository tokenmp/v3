// Package trustedip resolves the trusted client IP for an incoming HTTP
// request. It implements the only safe forwarding-header trust model:
//
//   - The TCP peer address (from net/http Request.RemoteAddr) is the baseline.
//   - X-Forwarded-For is consulted ONLY when the TCP peer belongs to an
//     explicitly configured trusted-proxy CIDR.
//   - When the peer is not trusted, ALL forwarding headers are ignored and the
//     peer address is used.
//   - X-Real-IP is NEVER used. It is a single-value header with no chain
//     provenance: a proxy that appends to XFF can be audited hop-by-hop, but
//     X-Real-IP offers no such guarantee and is trivially spoofable by any
//     hop that sets it. The canonical forwarded source is the well-formed
//     X-Forwarded-For chain from a trusted peer; when it is absent the TCP
//     peer is used.
//
// This replaces unconditional chi middleware.RealIP, which trusts forwarding
// headers from any peer and is therefore spoofable by any client.
package trustedip

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ErrInvalidCIDR is returned when a configured CIDR does not parse. It echoes
// only the (already non-secret) CIDR string, never a host or credential.
var ErrInvalidCIDR = errors.New("trustedip: invalid CIDR")

// Resolver resolves the client IP for a request given the TCP peer and any
// forwarding headers.
type Resolver struct {
	cidrs []*net.IPNet
}

// NewResolver parses trusted CIDRs. Each entry must be a valid CIDR notation
// (IPv4 or IPv6). An empty list means "no trusted proxy"; forwarding headers
// are always ignored and the peer IP is used.
func NewResolver(cidrs []string) (*Resolver, error) {
	r := &Resolver{}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidCIDR, c)
		}
		r.cidrs = append(r.cidrs, ipnet)
	}
	return r, nil
}

// Trusted reports whether any trusted CIDR is configured.
func (r *Resolver) Trusted() bool { return len(r.cidrs) > 0 }

type ctxKey struct{}

var ipKey ctxKey

// ClientIP resolves the client IP. remoteAddr is the TCP peer (host or
// host:port). xff is the raw X-Forwarded-For header value. When the peer is a
// trusted proxy, the rightmost non-trusted hop in a well-formed
// X-Forwarded-For chain is used; when XFF is absent or unusable the peer is
// used. X-Real-IP is deliberately ignored (see package doc).
//
// The returned net.IP is nil when the peer address cannot be parsed; callers
// should treat nil as "unknown" and fail closed on protected endpoints.
func (r *Resolver) ClientIP(remoteAddr, xff string) net.IP {
	peer := peerIP(remoteAddr)
	if peer == nil {
		// Cannot establish a baseline; do not trust headers.
		return nil
	}
	if !r.Trusted() {
		return peer
	}
	if !r.peerTrusted(peer) {
		// Peer is not a configured proxy; ignore ALL forwarding headers.
		return peer
	}
	// Peer is trusted: the ONLY accepted forwarded source is a well-formed
	// X-Forwarded-For chain. X-Real-IP is not consulted.
	if xff != "" {
		if ip := r.rightmostUntrusted(xff); ip != nil {
			return ip
		}
	}
	return peer
}

// peerTrusted reports whether peer falls within any trusted CIDR.
func (r *Resolver) peerTrusted(peer net.IP) bool {
	for _, c := range r.cidrs {
		if c.Contains(peer) {
			return true
		}
	}
	return false
}

// rightmostUntrusted walks X-Forwarded-For right-to-left and returns the
// first address that is NOT a trusted proxy. If every hop is trusted, the
// leftmost entry (the original client claim) is returned. Malformed entries
// are skipped.
func (r *Resolver) rightmostUntrusted(xff string) net.IP {
	hops := strings.Split(xff, ",")
	for i := len(hops) - 1; i >= 0; i-- {
		ip := parseIP(strings.TrimSpace(hops[i]))
		if ip == nil {
			continue
		}
		if !r.peerTrusted(ip) {
			return ip
		}
	}
	// All hops trusted: return the leftmost valid entry as the claimed client.
	for i := 0; i < len(hops); i++ {
		if ip := parseIP(strings.TrimSpace(hops[i])); ip != nil {
			return ip
		}
	}
	return nil
}

// Middleware sets the resolved client IP in the request context and replaces
// Request.RemoteAddr with the bare resolved IP (no port) so downstream code
// that reads RemoteAddr (e.g. session IP capture) sees the resolved client.
// It is a drop-in replacement for chi middleware.RealIP that is NOT
// spoofable by untrusted peers. X-Real-IP is never consulted.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ip := r.ClientIP(req.RemoteAddr, req.Header.Get("X-Forwarded-For"))
		if ip != nil {
			req = req.WithContext(context.WithValue(req.Context(), ipKey, ip))
			req.RemoteAddr = ip.String()
		} else {
			req = req.WithContext(context.WithValue(req.Context(), ipKey, net.IP(nil)))
		}
		next.ServeHTTP(w, req)
	})
}

// FromContext returns the resolved client IP stored by Middleware, or nil when
// no resolver ran. A non-nil net.IP is the authoritative client IP for
// rate-limit keying.
func FromContext(ctx context.Context) net.IP {
	v, _ := ctx.Value(ipKey).(net.IP)
	return v
}

// peerIP strips the port from a RemoteAddr and parses the host. It accepts
// bare IPs (no port) as well.
func peerIP(remoteAddr string) net.IP {
	if remoteAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port: treat the whole string as the host.
		host = remoteAddr
	}
	return parseIP(strings.TrimSpace(host))
}

// parseIP parses a single IP literal, rejecting any address that carries a
// zone id with a non-empty zone only when it would be ambiguous; net.ParseIP
// already rejects invalid input.
func parseIP(s string) net.IP {
	if s == "" {
		return nil
	}
	return net.ParseIP(s)
}
