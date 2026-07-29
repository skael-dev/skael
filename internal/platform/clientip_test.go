package platform

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolve runs a request through the ClientIP middleware and returns the
// address the rest of the stack will see.
func resolve(t *testing.T, trustedProxies, remoteAddr string, headers map[string][]string) string {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	r.RemoteAddr = remoteAddr
	for name, values := range headers {
		for _, v := range values {
			r.Header.Add(name, v)
		}
	}

	var got string
	h := ClientIP(ParseTrustedProxies(trustedProxies))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = ClientIPFromRequest(r)
		assert.Equal(t, remoteAddr, r.RemoteAddr, "RemoteAddr must be left as the true socket address")
	}))
	h.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func realIP(v string) map[string][]string       { return map[string][]string{"X-Real-IP": {v}} }
func forwarded(v ...string) map[string][]string { return map[string][]string{"X-Forwarded-For": v} }

func TestClientIP_Resolution(t *testing.T) {
	cases := []struct {
		name    string
		trusted string
		peer    string
		headers map[string][]string
		want    string
		why     string
	}{
		{
			name: "untrusted peer sending X-Real-IP", trusted: "10.0.0.0/8",
			peer: "203.0.113.9:5000", headers: realIP("198.51.100.1"), want: "203.0.113.9",
			why: "a header from a peer we do not trust is client input, not routing information",
		},
		{
			name: "untrusted peer sending X-Forwarded-For", trusted: "10.0.0.0/8",
			peer: "203.0.113.9:5000", headers: forwarded("198.51.100.1"), want: "203.0.113.9",
		},
		{
			name: "trusted peer sending X-Real-IP", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: realIP("198.51.100.1"), want: "198.51.100.1",
		},
		{
			name: "trusted peer sending X-Forwarded-For", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: forwarded("198.51.100.1"), want: "198.51.100.1",
		},
		{
			name: "X-Forwarded-For wins over X-Real-IP", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000",
			headers: map[string][]string{
				"X-Forwarded-For": {"198.51.100.1"},
				"X-Real-IP":       {"198.51.100.2"},
			},
			want: "198.51.100.1",
		},
		{
			name:    "chain of several proxies takes the rightmost untrusted hop",
			trusted: "10.0.0.0/8, 172.16.0.0/12",
			peer:    "10.0.0.1:5000",
			headers: forwarded("198.51.100.1, 203.0.113.9, 172.16.0.5, 10.0.0.2"),
			want:    "203.0.113.9",
			why:     "everything left of the last untrusted hop is whatever the client chose to send",
		},
		{
			name:    "a forged trusted-looking hop prepended by the client is ignored",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.1:5000",
			headers: forwarded("10.0.0.99, 203.0.113.5"),
			want:    "203.0.113.5",
		},
		{
			name: "chain split across repeated headers", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: forwarded("198.51.100.1", "203.0.113.9, 10.0.0.2"),
			want: "203.0.113.9",
		},
		{
			name:    "chain that is entirely trusted falls back to the socket address",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.1:5000",
			headers: forwarded("10.0.0.3, 10.0.0.2"),
			want:    "10.0.0.1",
		},
		{
			name: "empty TRUSTED_PROXIES trusts no header", trusted: "",
			peer: "10.0.0.1:5000", headers: forwarded("198.51.100.1"), want: "10.0.0.1",
			why: "the safe default: a directly-exposed deployment must ignore forwarding headers",
		},
		{
			name: "whitespace-only TRUSTED_PROXIES trusts no header", trusted: "  ,  ",
			peer: "10.0.0.1:5000", headers: realIP("198.51.100.1"), want: "10.0.0.1",
		},
		{
			name: "unparseable TRUSTED_PROXIES entries are dropped", trusted: "not-an-ip, 999.1.1.1, 10.0.0.0/99",
			peer: "10.0.0.1:5000", headers: realIP("198.51.100.1"), want: "10.0.0.1",
			why: "a typo must cost accuracy, never safety",
		},
		{
			name: "valid entries survive alongside garbage", trusted: "garbage, ,10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: realIP("198.51.100.1"), want: "198.51.100.1",
		},
		{
			name: "malformed X-Real-IP from a trusted peer", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: realIP("not-an-ip"), want: "10.0.0.1",
		},
		{
			name: "malformed X-Forwarded-For from a trusted peer", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: forwarded("<script>, junk"), want: "10.0.0.1",
		},
		{
			name: "empty X-Forwarded-For from a trusted peer", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: forwarded("   "), want: "10.0.0.1",
		},
		{
			name: "a malformed hop right of a good one is not walked past", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: forwarded("203.0.113.5, junk"), want: "10.0.0.1",
		},
		{
			name: "bare address entry matches exactly", trusted: "192.168.1.5",
			peer: "192.168.1.5:5000", headers: realIP("198.51.100.1"), want: "198.51.100.1",
		},
		{
			name: "bare address entry does not match a neighbour", trusted: "192.168.1.5",
			peer: "192.168.1.6:5000", headers: realIP("198.51.100.1"), want: "192.168.1.6",
		},
		{
			name: "IPv6 peer trusted by CIDR", trusted: "fd00::/8",
			peer: "[fd00::1]:5000", headers: forwarded("2001:db8::5"), want: "2001:db8::5",
		},
		{
			name: "IPv6 peer trusted by bare address", trusted: "2001:db8::1",
			peer: "[2001:db8::1]:5000", headers: realIP("198.51.100.1"), want: "198.51.100.1",
		},
		{
			name: "untrusted IPv6 peer keeps its socket address", trusted: "fd00::/8",
			peer: "[2001:db8::9]:5000", headers: forwarded("198.51.100.1"), want: "2001:db8::9",
		},
		{
			name: "IPv6 header value carrying brackets and a port", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: forwarded("[2001:db8::5]:9000"), want: "2001:db8::5",
		},
		{
			name: "IPv6 header value in brackets without a port", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: forwarded("[2001:db8::5]"), want: "2001:db8::5",
		},
		{
			name: "IPv6 chain skips trusted hops", trusted: "fd00::/8",
			peer: "[fd00::1]:5000", headers: forwarded("2001:db8::1, 2001:db8::7, fd00::2"),
			want: "2001:db8::7",
		},
		{
			name: "4-in-6 peer matches an IPv4 trusted range", trusted: "10.0.0.0/8",
			peer: "[::ffff:10.0.0.7]:5000", headers: realIP("198.51.100.1"), want: "198.51.100.1",
		},
		{
			name: "4-in-6 header value is normalized to IPv4", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: realIP("::ffff:198.51.100.1"), want: "198.51.100.1",
		},
		{
			name: "IPv6 zone is stripped from the socket address", trusted: "10.0.0.0/8",
			peer: "[fe80::1%eth0]:5000", headers: nil, want: "fe80::1",
		},
		{
			name: "RemoteAddr without a port is used as-is", trusted: "10.0.0.0/8",
			peer: "203.0.113.9", headers: realIP("198.51.100.1"), want: "203.0.113.9",
		},
		{
			name: "unparseable RemoteAddr is never traded for a header", trusted: "10.0.0.0/8",
			peer: "@internal-socket", headers: realIP("198.51.100.1"), want: "@internal-socket",
		},
		{
			name: "no headers at all", trusted: "10.0.0.0/8",
			peer: "10.0.0.1:5000", headers: nil, want: "10.0.0.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolve(t, tc.trusted, tc.peer, tc.headers)
			assert.Equal(t, tc.want, got, tc.why)
		})
	}
}

// TestClientIP_ChainDepthIsBounded proves a trusted peer cannot make the
// right-to-left walk cost an unbounded amount of work: past the hop cap the
// resolver gives up and uses the socket address.
func TestClientIP_ChainDepthIsBounded(t *testing.T) {
	chain := make([]string, 0, maxForwardedHops+10)
	for i := 0; i < maxForwardedHops+10; i++ {
		chain = append(chain, "10.0.0.2")
	}
	got := resolve(t, "10.0.0.0/8", "10.0.0.1:5000", forwarded(joinChain(chain)))
	assert.Equal(t, "10.0.0.1", got)
}

func joinChain(entries []string) string {
	out := ""
	for i, e := range entries {
		if i > 0 {
			out += ", "
		}
		out += e
	}
	return out
}

func TestClientIPFromRequest_FallsBackToSocketWithoutMiddleware(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	r.Header.Set("X-Real-IP", "198.51.100.2")

	assert.Equal(t, "203.0.113.9", ClientIPFromRequest(r),
		"without the middleware the socket address is the answer — never a header")
}

func TestParseTrustedProxies(t *testing.T) {
	assert.True(t, ParseTrustedProxies("").Empty())
	assert.True(t, ParseTrustedProxies("  , ,").Empty())
	assert.True(t, ParseTrustedProxies("nonsense/8").Empty())
	assert.True(t, (*TrustedProxies)(nil).Empty())
	assert.False(t, (*TrustedProxies)(nil).Contains(netip.MustParseAddr("10.0.0.1")),
		"a nil set trusts nobody rather than panicking")

	tp := ParseTrustedProxies("10.0.0.0/8, 192.168.1.5, fd00::/8, 2001:db8::1")
	assert.False(t, tp.Empty())
	for _, in := range []string{"10.255.0.1", "192.168.1.5", "fd00::99", "2001:db8::1", "::ffff:10.1.2.3"} {
		assert.Truef(t, tp.Contains(netip.MustParseAddr(in)), "%s should be trusted", in)
	}
	for _, out := range []string{"11.0.0.1", "192.168.1.6", "2001:db8::2", "203.0.113.1"} {
		assert.Falsef(t, tp.Contains(netip.MustParseAddr(out)), "%s should not be trusted", out)
	}

	// A host address given with mask bits set is accepted and masked, matching
	// how operators usually paste ranges.
	assert.True(t, ParseTrustedProxies("10.1.2.3/8").Contains(netip.MustParseAddr("10.9.9.9")))
}

// TestRequestLogger_LogsResolvedClientIP keeps the logs and the rate limiter
// telling the same story: a 429 has to be traceable to the address it was
// charged to.
func TestRequestLogger_LogsResolvedClientIP(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	r := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "198.51.100.4")

	h := ClientIP(ParseTrustedProxies("10.0.0.0/8"))(RequestLogger(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "ip:198.51.100.4", ipSubject(r),
				"the limiter must charge the resolved client address")
		})))
	h.ServeHTTP(httptest.NewRecorder(), r)

	var entry struct {
		IP string `json:"ip"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "198.51.100.4", entry.IP)
}
