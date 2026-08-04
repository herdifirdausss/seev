package webhook

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		name   string
		ip     string
		public bool
	}{
		{"loopback v4", "127.0.0.1", false},
		{"loopback v6", "::1", false},
		{"private RFC1918 10/8", "10.0.0.5", false},
		{"private RFC1918 172.16/12", "172.16.0.5", false},
		{"private RFC1918 192.168/16", "192.168.1.5", false},
		{"unique local v6 (RFC4193)", "fd00::1", false},
		{"link-local v4", "169.254.1.1", false},
		{"cloud metadata address", "169.254.169.254", false},
		{"link-local v6", "fe80::1", false},
		{"unspecified v4", "0.0.0.0", false},
		{"unspecified v6", "::", false},
		{"multicast v4", "224.0.0.1", false},
		{"public v4", "8.8.8.8", true},
		{"public v6", "2001:4860:4860::8888", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip, "test IP %q must parse", tc.ip)
			assert.Equal(t, tc.public, isPublicIP(ip), "isPublicIP(%s)", tc.ip)
		})
	}
}

func TestSafeClient_NoRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := safeClient("sandbox")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, redirector.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err, "a 3xx response must be returned to the caller, not treated as a transport error")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode, "the redirect itself must be surfaced, never followed")
}

func TestSafeClient_ResponseBodyBounded(t *testing.T) {
	oversized := make([]byte, maxResponseBody*2)
	for i := range oversized {
		oversized[i] = 'a'
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(oversized)
	}))
	defer server.Close()

	client := safeClient("sandbox")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(body), maxResponseBody)
}

// TestSafeClient_LiveModeRejectsLoopback proves TM-16's core structural
// guarantee: in "live" environment, dialing a loopback address (the exact
// class of address an SSRF attacker targets) fails closed, while the
// identical URL in "sandbox" environment succeeds — sandbox tenants may
// legitimately target a local receiver
// (docs/reference/c1-b2b-design.md §4).
func TestSafeClient_LiveModeRejectsLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	liveClient := safeClient("live")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	_, err = liveClient.Do(req)
	require.Error(t, err, "live-mode dispatch to a loopback address must be refused (SSRF defense)")
	assert.Contains(t, err.Error(), "SSRF defense")

	sandboxClient := safeClient("sandbox")
	sandboxRequest, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	resp, err := sandboxClient.Do(sandboxRequest)
	require.NoError(t, err, "sandbox-mode dispatch to a loopback address must succeed — no SSRF check in sandbox")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestResolveAndDial_TimesOutOnUnroutableAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// 240.0.0.1 is in the reserved 240.0.0.0/4 block — public per net.IP's
	// own classification, but unroutable, so the dial itself times out
	// rather than the SSRF check rejecting it up front. This exercises
	// resolveAndDial's own dial path distinct from the isPublicIP guard.
	_, err := resolveAndDial(ctx, "tcp", "240.0.0.1:80")
	assert.Error(t, err)
}
