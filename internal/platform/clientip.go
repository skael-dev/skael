package platform

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/rs/zerolog/log"
)

const maxForwardedHops = 32

// TrustedProxies is the set of peers whose forwarding headers may be believed.
// The zero value trusts nobody — correct for a directly-exposed server.
type TrustedProxies struct {
	prefixes []netip.Prefix
}

// ParseTrustedProxies parses a comma-separated TRUSTED_PROXIES list of CIDR
// blocks or bare addresses. Unparseable entries are dropped with a warning —
// dropping is safe: a typo costs forwarded addresses, never spoofable ones.
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

// Empty reports whether no proxy is trusted.
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

// normalizePrefix unwraps a 4-in-6 prefix so ::ffff:10.0.0.7 matches 10.0.0.0/8.
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

// normalizeAddr strips the zone and unwraps 4-in-6 so one client gets one key.
func normalizeAddr(addr netip.Addr) netip.Addr {
	return addr.Unmap().WithZone("")
}

type clientIPKeyType struct{}

var clientIPKey = clientIPKeyType{}

// ClientIP resolves the client address and stores it on the context.
// Replaces chi's RealIP, which trusts forwarding headers unconditionally —
// here they are read only when the peer is a configured trusted proxy.
func ClientIP(trusted *TrustedProxies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey, resolveClientIP(r, trusted))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIPFromRequest returns the resolved client address, falling back to the
// socket address when the ClientIP middleware is not installed.
func ClientIPFromRequest(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok && ip != "" {
		return ip
	}
	return socketHost(r.RemoteAddr)
}

// socketHost strips the port and canonicalizes, so one client gets one bucket.
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

// resolveClientIP reads forwarding headers only when the peer is trusted.
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

// forwardedFor walks X-Forwarded-For right to left, skipping trusted proxies,
// and returns the first untrusted hop — the client.
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

// parseHeaderAddr reads one address from a forwarding header (bare, bracketed,
// or with port).
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
