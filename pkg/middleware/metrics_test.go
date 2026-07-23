package middleware

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func observationCount(t *testing.T, method, route, statusCode string) float64 {
	t.Helper()
	hist := httpRequestDuration.WithLabelValues(method, route, statusCode).(prometheus.Metric)
	var m dto.Metric
	require.NoError(t, hist.Write(&m))
	return float64(m.GetHistogram().GetSampleCount())
}

func TestWithHTTPMetrics_ObservesStatusCodeAndRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /orders/{id}", WithHTTPMetrics()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	before := observationCount(t, "GET", "/orders/{id}", "200")

	req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, before+1, observationCount(t, "GET", "/orders/{id}", "200"))
}

func TestWithHTTPMetrics_5xxObserved(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("POST /boom", WithHTTPMetrics()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})))
	before := observationCount(t, "POST", "/boom", "500")

	req := httptest.NewRequest(http.MethodPost, "/boom", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, before+1, observationCount(t, "POST", "/boom", "500"))
}

func TestWithHTTPMetrics_UnmatchedRoute_NeverRawPath(t *testing.T) {
	// A bare handler (no ServeMux dispatch) never populates r.Pattern —
	// this must fall back to the literal "unmatched" label, never the raw
	// URL path (docs/roadmap/archive/43 anti-scope: no per-user/high-cardinality
	// labels; an attacker-controlled path must never become a label value).
	handler := WithHTTPMetrics()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	before := observationCount(t, "GET", "unmatched", "404")

	req := httptest.NewRequest(http.MethodGet, "/users/12345/secret-token-abc", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, before+1, observationCount(t, "GET", "unmatched", "404"))
	assert.Equal(t, float64(0), observationCount(t, "GET", "/users/12345/secret-token-abc", "404"),
		"raw path must never become a label value")
}

func TestWithHTTPMetrics_UninstrumentedPath_NoObservation(t *testing.T) {
	handler := WithHTTPMetrics()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	before := testutil.CollectAndCount(httpRequestDuration)

	for _, path := range []string{"/health", "/ready", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	assert.Equal(t, before, testutil.CollectAndCount(httpRequestDuration), "health/ready/metrics must never be observed")
}

// hijackableRecorder proves WithHTTPMetrics preserves http.Hijacker through
// httpsnoop (docs/roadmap/archive/43 K5) — a plain wrapper embedding http.ResponseWriter
// as an interface field would silently fail the type assertion below even
// though the underlying writer supports it.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}

// TestHTTPBuckets_HasExactTwoSecondBoundary is the regression test for a
// bug caught cross-checking a docs/roadmap/archive/43 Task T5 SLO recording rule's
// actual query output against Prometheus: the "vendor webhook latency" SLI
// is defined as an event-count ratio against a literal `le="2"` histogram
// bucket, but prometheus.DefBuckets only has 1 and 2.5 — no exact 2 — so
// that query silently matched zero series (not a query error, just an
// absent label value) until this was noticed. See httpBuckets' own doc
// comment.
func TestHTTPBuckets_HasExactTwoSecondBoundary(t *testing.T) {
	for _, b := range httpBuckets {
		if b == 2 {
			return
		}
	}
	t.Fatalf("httpBuckets must contain an exact 2-second boundary for the le=\"2\" SLO query to match; got %v", httpBuckets)
}

func TestWithHTTPMetrics_PreservesHijacker(t *testing.T) {
	var sawHijacker bool
	handler := WithHTTPMetrics()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawHijacker = w.(http.Hijacker)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	handler.ServeHTTP(&hijackableRecorder{httptest.NewRecorder()}, req)

	assert.True(t, sawHijacker, "http.Hijacker must survive the metrics wrapper")
}
