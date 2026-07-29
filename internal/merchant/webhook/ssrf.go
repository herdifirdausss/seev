package webhook

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// TM-16: outbound webhook endpoint URLs are merchant-supplied and could
// target internal/link-local/metadata IP ranges (SSRF). Enforcement is
// re-checked at EVERY dispatch attempt, not only at endpoint creation
// (docs/reference/c1-b2b-design.md §4) — resolveAndDial below re-resolves
// DNS on every call, so a hostname that answered safely at creation but
// was rebound to a private address before delivery is still caught.

const (
	dialTimeout        = 5 * time.Second
	responseTimeout    = 10 * time.Second
	maxResponseBody    = 64 * 1024 // 64 KiB — response-body limit (T7 acceptance)
	deliveryHTTPMethod = http.MethodPost
)

// isPublicIP rejects loopback, private (RFC1918/RFC4193), link-local
// (including the 169.254.169.254 cloud metadata address — it falls in
// 169.254.0.0/16, IsLinkLocalUnicast already covers it; kept as an
// explicit named check below for auditability, not because the range
// check alone would miss it), unspecified, and multicast addresses.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	// Explicit, human-greppable metadata-endpoint reject — belt-and-braces
	// with the IsLinkLocalUnicast check above.
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return false
	}
	return true
}

// resolveAndDial resolves addr's host to a concrete IP, validates it, and
// dials THAT VALIDATED IP directly rather than the original hostname —
// dialing the hostname a second time would let an attacker's DNS server
// answer safely for the resolver used at validation time and unsafely for
// whatever the OS resolver uses at connect time (classic DNS-rebinding
// TOCTOU). Dialing the already-resolved IP closes that window.
func resolveAndDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("merchant/webhook: split host port: %w", err)
	}
	resolveCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	if err != nil {
		return nil, fmt.Errorf("merchant/webhook: resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("merchant/webhook: %q resolved to no addresses", host)
	}
	for _, ip := range ips {
		if !isPublicIP(ip.IP) {
			return nil, fmt.Errorf("merchant/webhook: %q resolved to a non-public address %s, refusing to dial (SSRF defense)", host, ip.IP)
		}
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

// noRedirect makes an *http.Client refuse to follow ANY redirect (T7
// acceptance: "redirects are not followed") — the 3xx response itself is
// returned to the caller rather than being chased, which would otherwise
// reopen the exact SSRF window resolveAndDial just closed (a redirect
// Location can point anywhere, bypassing the original URL's own
// validation).
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// safeClient builds a delivery HTTP client for environment ("sandbox" |
// "live") — live mode dials through resolveAndDial's SSRF guard; sandbox
// mode uses a plain dialer (docs/reference/c1-b2b-design.md §4: "SSRF
// validation before dispatch (live mode only)" — a sandbox tenant may
// legitimately target a local receiver). Both modes share the same
// no-redirect policy and bounded timeout regardless of environment —
// those are not SSRF-specific, they're baseline delivery hygiene.
func safeClient(environment string) *http.Client {
	transport := &http.Transport{}
	if environment == "sandbox" {
		transport.DialContext = (&net.Dialer{Timeout: dialTimeout}).DialContext
	} else {
		transport.DialContext = resolveAndDial
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       responseTimeout,
		CheckRedirect: noRedirect,
	}
}
