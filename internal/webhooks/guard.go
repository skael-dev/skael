package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// ErrRefusedAddress reports an address a delivery may not reach.
var ErrRefusedAddress = errors.New("refused address")

// cgnat is shared address space. netip's IsPrivate does not include it, and
// some clouds route metadata services through it.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// refused reports whether a delivery may not connect to addr. Link-local stays
// refused even when private addresses are allowed: it holds cloud metadata
// services, and no self-hosted consumer lives there.
func refused(addr netip.Addr, allowPrivate bool) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() {
		return true
	}
	if allowPrivate {
		return false
	}
	return addr.IsPrivate() || addr.IsLoopback() || cgnat.Contains(addr)
}

// checkScheme applies the transport rule to one URL.
func checkScheme(u *url.URL, allowPrivate bool) error {
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && allowPrivate:
		return nil
	case u.Scheme == "http":
		return fmt.Errorf("%w: http is refused unless WEBHOOK_ALLOW_PRIVATE=true", ErrRefusedAddress)
	}
	return fmt.Errorf("%w: scheme %q", ErrRefusedAddress, u.Scheme)
}

// ValidateURL gives an early answer when an endpoint is saved. The check that
// protects anything is the dial control in NewClient, because a name can change
// what it resolves to after this returns.
func ValidateURL(ctx context.Context, raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: not a URL", ErrRefusedAddress)
	}
	if err := checkScheme(u, allowPrivate); err != nil {
		return err
	}
	host := u.Hostname()
	if addr, err := netip.ParseAddr(host); err == nil {
		if refused(addr, allowPrivate) {
			return fmt.Errorf("%w: %s", ErrRefusedAddress, addr)
		}
		return nil
	}
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(lookup, "ip", host)
	if err != nil {
		// A name that does not resolve yet is not a refused address. The dial
		// control still decides at delivery.
		return nil
	}
	for _, addr := range addrs {
		if refused(addr, allowPrivate) {
			return fmt.Errorf("%w: %s resolves to %s", ErrRefusedAddress, host, addr)
		}
	}
	return nil
}

// NewClient returns the HTTP client for deliveries. The control function runs
// on the resolved IP of every connection, including each redirect, so DNS
// rebinding and a redirect to a metadata service both fail at dial.
func NewClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: %s", ErrRefusedAddress, address)
			}
			if refused(ap.Addr(), allowPrivate) {
				return fmt.Errorf("%w: %s", ErrRefusedAddress, ap.Addr())
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			// No proxy: a proxy would make the dial control check the proxy
			// rather than the target.
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 5 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			// 301, 302 and 303 turn the POST into a GET with no body. A 2xx
			// reply to that would mark an event delivered that never arrived,
			// so the 3xx stands as the result, and the attempt fails.
			if req.Method != http.MethodPost {
				return http.ErrUseLastResponse
			}
			return checkScheme(req.URL, allowPrivate)
		},
	}
}
