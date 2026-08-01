package trustedip

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolver_UntrustedPeerIgnoresHeaders(t *testing.T) {
	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	// No trusted CIDRs → forwarding headers ignored.
	if got := r.ClientIP("203.0.113.9:1234", "10.0.0.1"); !got.Equal(net.ParseIP("203.0.113.9")) {
		t.Fatalf("got %v, want peer", got)
	}
}

func TestResolver_TrustedPeerAcceptsXFF(t *testing.T) {
	r, err := NewResolver([]string{"10.0.0.0/8", "2001:db8::/32"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	// Peer 10.0.0.5 is trusted; XFF rightmost untrusted is 203.0.113.7.
	got := r.ClientIP("10.0.0.5:443", "203.0.113.7, 10.0.0.5")
	if !got.Equal(net.ParseIP("203.0.113.7")) {
		t.Fatalf("got %v, want 203.0.113.7", got)
	}
	// IPv6 trusted peer.
	got = r.ClientIP("[2001:db8::1]:443", "203.0.113.9")
	if !got.Equal(net.ParseIP("203.0.113.9")) {
		t.Fatalf("ipv6 trusted: got %v", got)
	}
}

// TestResolver_XRealIPNeverUsed confirms X-Real-IP is deliberately ignored:
// even with a trusted peer and a forged X-Real-IP, the result is XFF-or-peer,
// never the X-Real-IP value. This guards against re-introducing the
// spoofable single-value header as a forwarded source.
func TestResolver_XRealIPNeverUsed(t *testing.T) {
	r, err := NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	// Trusted peer, XFF present, X-Real-IP forged to a trusted range — XFF wins.
	got := r.ClientIP("10.0.0.5:443", "203.0.113.7")
	if !got.Equal(net.ParseIP("203.0.113.7")) {
		t.Fatalf("XFF must win, got %v", got)
	}
	// Trusted peer, NO XFF, X-Real-IP set — must fall back to peer, NOT X-Real-IP.
	got = r.ClientIP("10.0.0.5:443", "")
	if !got.Equal(net.ParseIP("10.0.0.5")) {
		t.Fatalf("missing XFF must fall back to peer, got %v", got)
	}
}

func TestResolver_AllHopsTrustedReturnsLeftmost(t *testing.T) {
	r, err := NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	// Every hop is a trusted proxy; leftmost valid is the claimed client.
	got := r.ClientIP("10.0.0.5:443", "203.0.113.20, 10.0.0.6, 10.0.0.5")
	if !got.Equal(net.ParseIP("203.0.113.20")) {
		t.Fatalf("got %v, want leftmost client claim", got)
	}
}

func TestResolver_MalformedXFFFallsBack(t *testing.T) {
	r, err := NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	// Malformed XFF entries skipped; peer used when nothing valid remains.
	got := r.ClientIP("10.0.0.5:443", "not-an-ip, garbage")
	if !got.Equal(net.ParseIP("10.0.0.5")) {
		t.Fatalf("got %v, want peer fallback", got)
	}
}

func TestResolver_InvalidCIDR(t *testing.T) {
	if _, err := NewResolver([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestMiddleware_SetsContextAndRemoteAddr(t *testing.T) {
	r, err := NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	var gotIP net.IP
	var remoteAddr string
	h := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		gotIP = FromContext(req.Context())
		remoteAddr = req.RemoteAddr
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.77")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !gotIP.Equal(net.ParseIP("203.0.113.77")) {
		t.Fatalf("context ip = %v, want 203.0.113.77", gotIP)
	}
	if remoteAddr != "203.0.113.77" {
		t.Fatalf("RemoteAddr = %q, want resolved ip", remoteAddr)
	}
}

func TestMiddleware_UntrustedPeerDoesNotSpoof(t *testing.T) {
	r, err := NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	var remoteAddr string
	h := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		remoteAddr = req.RemoteAddr
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	// Attacker peer, not a trusted proxy, forges XFF and X-Real-IP.
	req.RemoteAddr = "198.51.100.1:5555"
	req.Header.Set("X-Forwarded-For", "10.0.0.99")
	req.Header.Set("X-Real-IP", "10.0.0.50")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if remoteAddr != "198.51.100.1" {
		t.Fatalf("forged headers accepted: RemoteAddr=%q", remoteAddr)
	}
}

// TestMiddleware_ForgedXRealIPIgnoredByTrustedPeer confirms that even when the
// peer IS trusted, a forged X-Real-IP is never honored; the canonical XFF
// chain (or peer when XFF is absent) is the sole forwarded source.
func TestMiddleware_ForgedXRealIPIgnoredByTrustedPeer(t *testing.T) {
	r, err := NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	var remoteAddr string
	h := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		remoteAddr = req.RemoteAddr
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.77")
	// Forged X-Real-IP that would be picked up if the resolver consulted it.
	req.Header.Set("X-Real-IP", "10.0.0.250")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if remoteAddr != "203.0.113.77" {
		t.Fatalf("XFF must be used, got RemoteAddr=%q", remoteAddr)
	}

	// Now drop XFF: must fall back to the trusted peer, NOT X-Real-IP.
	var remoteAddr2 string
	h2 := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		remoteAddr2 = req.RemoteAddr
	}))
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.RemoteAddr = "10.0.0.5:443"
	req2.Header.Set("X-Real-IP", "203.0.113.250")
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req2)
	if remoteAddr2 != "10.0.0.5" {
		t.Fatalf("missing XFF must fall back to peer, not X-Real-IP: %q", remoteAddr2)
	}
}
