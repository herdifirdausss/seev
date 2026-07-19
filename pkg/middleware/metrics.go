package middleware

import (
	"net/http"
	"strconv"

	"github.com/felixge/httpsnoop"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// httpBuckets is prometheus.DefBuckets with one bucket boundary inserted at
// exactly 2s — docs/plan/43 K6's "vendor webhook latency" SLI is defined as
// an event-count ratio against a literal `le="2"` bucket
// (`total - bucket(le="2")`), not a histogram_quantile estimate. DefBuckets
// alone has 1 and 2.5 but no exact 2 — the T5 recording rule for that SLI
// silently matched zero series (not an error, just an absent bucket label
// value) until this was caught cross-checking the recording rule's actual
// query output against Prometheus during T5 verification.
var httpBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2, 2.5, 5, 10}

// httpRequestDuration is package-level, registered once regardless of how
// many times WithHTTPMetrics is installed (docs/plan/43 K5) — no `service`
// label: the Prometheus scrape config's own `job` label already identifies
// which service a series came from.
var httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_server_request_duration_seconds",
	Help:    "HTTP server request duration in seconds, by method/route/status_code.",
	Buckets: httpBuckets,
}, []string{"method", "route", "status_code"})

// WithHTTPMetrics records one http_server_request_duration_seconds
// observation per request (docs/plan/43 K5) — install right after
// WithTracing (and WithRoutePattern, see route.go), before WithLogger,
// matching the mandated chain order. Uses httpsnoop.CaptureMetrics rather
// than a bare ResponseWriter wrapper so a handler further down the chain
// that needs http.Flusher/Hijacker/Pusher/io.ReaderFrom (SSE, websocket
// upgrade, proxying) keeps working — see docs/plan/43 K5's own note on
// this. The route comes from RouteFromCtx, not r.Pattern — see
// WithTracing's own doc comment for why reading r.Pattern after dispatch is
// unreliable once WithLogger's r.WithContext(...) sits in between.
func WithHTTPMetrics() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if uninstrumentedPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			route, _ := routeAndSpanName(r.Method, effectiveRoutePattern(r))
			m := httpsnoop.CaptureMetrics(next, w, r)
			httpRequestDuration.WithLabelValues(r.Method, route, strconv.Itoa(m.Code)).Observe(m.Duration.Seconds())
		})
	}
}
