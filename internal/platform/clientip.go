package platform

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/rs/zerolog/log"
)

// maxForwardedHops bounds how far right-to-left the X-Forwarded-For walk will
// go before giving up and falling back to the socket address. Real
// deployments chain a handful of proxies at most; the cap keeps a pathological
// header from costing more than a constant amount of work per request.
const maxForwardedHops = 32

// TrustedProxies is the set of peers whose forwarding headers may be believed.
// The zero value — and a nil *TrustedProxies — trusts nobody, which is the
// correct default: every X-Forwarded-For and X-Real-IP arriving at a
// directly-exposed server is written by the client itself.
type TrustedProxies struct {
	prefixes []netip.Prefix
}

// ParseTrustedProxies parses a comma-separated TRUSTED_PROXIES list. Entries
// may be CIDR blocks ("10.0.0.0/8", "fd00::/8") or bare addresses
// ("192.168.1.5", "2001:db8::1"), IPv4 or IPv6; a bare address is treated as a
// single-host range.
//
// Unparseable entries are dropped with a warning rather than failing startup.
// Dropping is the safe direction: an entry that is not understood simply does
// not become trusted, so a typo costs the operator forwarded addresses, never
// spoofable ones.
func ParseTrustedProxies(list string) *TrustedProxies {
	t := &TrustedProxies{}
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if p, err := netip.ParsePrefix(entry); err == nil {
			if np, ok := normalizePrefix(p); ok {
				t.prefixes = append(t.prefixes, np)
				continue
			}
		} else if addr, err := netip.ParseAddr(entry); err == nil {
			addr = normalizeAddr(addr)
			t.prefixes = append(t.prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		log.Warn().Str("entry", entry).Msg("ignoring unparseable TRUSTED_PROXIES entry")
	}
	return t
}

// Empty reports whether no proxy is trusted, in which case forwarding headers
// are ignored outright.
func (t *TrustedProxies) Empty() bool {
	return t == nil || len(t.prefixes) == 0
}

// Contains reports whether addr belongs to a trusted proxy range.
func (t *TrustedProxies) Contains(addr netip.Addr) bool {
	if t.Empty() || !addr.IsValid() {
		return false
	}
	addr = normalizeAddr(addr)
	for _, p := range t.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// normalizePrefix puts a configured range into the same form addresses are
// compared in: a 4-in-6 prefix becomes its plain IPv4 equivalent, so a
// dual-stack listener reporting a peer as ::ffff:10.0.0.7 still matches
// 10.0.0.0/8. It returns ok=false for a prefix that cannot be expressed that
// way, which the caller treats as an unusable entry.
func normalizePrefix(p netip.Prefix) (netip.Prefix, bool) {
	addr, bits := p.Addr(), p.Bits()
	if addr.Is4In6() {
		if bits < 96 {
			return netip.Prefix{}, false
		}
		addr, bits = addr.Unmap(), bits-96
	}
	np, err := addr.WithZone("").Prefix(bits)
	if err != nil {
		return netip.Prefix{}, false
	}
	return np, true
}

// normalizeAddr strips the IPv6 zone and unwraps a 4-in-6 address, so the same
// client always produces the same string no matter which form it arrived in.
func normalizeAddr(addr netip.Addr) netip.Addr {
	return addr.Unmap().WithZone("")
}

type clientIPKeyType struct{}

var clientIPKey = clientIPKeyType{}

// ClientIP returns middleware that resolves the address a request should be
// attributed to and stores it on the request context, where the rate limiter
// and the request logger both read it via ClientIPFromRequest.
//
// It deliberately replaces chi's RealIP, which overwrites RemoteAddr from
// X-Real-IP / X-Forwarded-For for anyone who sends them. Those headers are
// plain client input: trusting them unconditionally lets a single attacker
// present a fresh source address on every request, which defeats per-IP rate
// limiting entirely and lets the limiter's subject maps grow without bound.
// Here they are read only when the peer that opened the connection is itself
// a trusted proxy, and RemoteAddr is left untouched so the true socket address
// stays available.
func ClientIP(trusted *TrustedProxies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey, resolveClientIP(r, trusted))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIPFromRequest returns the address a request is attributed to: the
// value resolved by the ClientIP middleware, or — if that middleware is not
// installed — the socket address it connected from. The fallback never
// consults a header, so forgetting the middleware costs accuracy behind a
// proxy but can never be exploited.
func ClientIPFromRequest(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok && ip != "" {
		return ip
	}
	return socketHost(r.RemoteAddr)
}

// socketHost strips the port from a RemoteAddr and canonicalizes the address,
// so one client never occupies two rate-limit buckets by arriving as
// ::ffff:1.2.3.4 on a dual-stack listener and 1.2.3.4 on another. A value that
// is not an address at all (a Unix socket, say) is returned unchanged.
func socketHost(remoteAddr string) string {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return normalizeAddr(addr).String()
	}
	return host
}

// resolveClientIP applies the trust rule. The peer address — the real socket
// address, before any rewriting — decides whether the forwarding headers are
// read at all. Anything that fails to parse falls back to the socket address.
func resolveClientIP(r *http.Request, trusted *TrustedProxies) string {
	peer := socketHost(r.RemoteAddr)
	if trusted.Empty() {
		return peer
	}

	peerAddr, err := netip.ParseAddr(peer)
	if err != nil || !trusted.Contains(peerAddr) {
		return peer
	}

	if ip, ok := forwardedFor(r.Header.Values("X-Forwarded-For"), trusted); ok {
		return ip
	}
	if addr, ok := parseHeaderAddr(r.Header.Get("X-Real-IP")); ok {
		return addr.String()
	}
	return peer
}

// forwardedFor picks the client address out of an X-Forwarded-For chain.
//
// The chain is appended to by each proxy, so entries on the right were written
// by infrastructure closest to us and entries on the left are progressively
// less trustworthy — the leftmost is whatever the original client chose to
// send, which may be pure invention. So the walk goes right to left, skipping
// addresses that belong to trusted proxies, and stops at the first address
// that does not: that is the last hop no trusted proxy vouched for, i.e. the
// client. A chain that is empty, entirely trusted, or unparseable yields no
// answer, and the caller falls back to the socket address.
func forwardedFor(headers []string, trusted *TrustedProxies) (string, bool) {
	var chain []string
	for _, h := range headers {
		chain = append(chain, strings.Split(h, ",")...)
	}

	hops := 0
	for i := len(chain) - 1; i >= 0; i-- {
		addr, ok := parseHeaderAddr(chain[i])
		if !ok {
			// A malformed hop makes everything to its left unattributable:
			// stop rather than skip past it.
			return "", false
		}
		if hops++; hops > maxForwardedHops {
			return "", false
		}
		if !trusted.Contains(addr) {
			return addr.String(), true
		}
	}
	return "", false
}

// parseHeaderAddr reads one address as it may appear in a forwarding header:
// bare ("1.2.3.4", "2001:db8::1"), bracketed ("[2001:db8::1]"), or carrying a
// port ("1.2.3.4:5000", "[2001:db8::1]:5000"). It returns the canonical
// address, or ok=false if the value is not an address at all — including the
// "unknown" and obfuscated-identifier forms the spec allows, which name no one
// we could charge a request to.
func parseHeaderAddr(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, false
	}

	if addr, err := netip.ParseAddr(value); err == nil {
		return normalizeAddr(addr), true
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return normalizeAddr(addr), true
		}
	}
	// "[2001:db8::1]" without a port.
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		if addr, err := netip.ParseAddr(value[1 : len(value)-1]); err == nil {
			return normalizeAddr(addr), true
		}
	}
	return netip.Addr{}, false
}
