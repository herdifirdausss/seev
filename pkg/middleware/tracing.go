package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/tracing"
)

// httpTracer is package-scoped, like internal/ledger/service/handle's own
// package-level tracer — the instrumentation library name identifies the
// component in trace backends regardless of which service imports it.
var httpTracer = otel.Tracer("github.com/herdifirdausss/seev/pkg/middleware")

// uninstrumentedPaths are never traced and never enter RED metrics
// (docs/roadmap/archive/43 K4/K5) — a Prometheus scrape every 15s per service would
// otherwise dominate trace/metric volume for zero diagnostic value.
var uninstrumentedPaths = map[string]bool{
	"/health":  true,
	"/ready":   true,
	"/metrics": true,
}

// WithTracing starts one span per HTTP request and stores a span-enriched
// logger in the request context (docs/roadmap/archive/43 K4) — install right after
// WithRoutePattern (see route.go) and WithRequestID, before
// WithHTTPMetrics/WithLogger, so both this middleware's own request/
// response log lines AND every handler that calls logger.FromContext(ctx)
// pick up trace_id/span_id automatically.
//
// The route comes from RouteFromCtx (WithRoutePattern's pure mux.Handler(r)
// lookup, run earlier in the chain), NOT from reading r.Pattern after
// next.ServeHTTP returns — that used to work for a leaf handler registered
// directly on the dispatching mux, but breaks silently the moment any
// middleware between here and the actual dispatch calls r.WithContext(...)
// (WithLogger does, to propagate its own enriched logger): the mutation
// net/http's ServeMux performs lands on that context-copy's Pattern field,
// invisible to this closure's own r. See route.go's own doc comment for the
// full story — caught only by tracing a real request through Tempo.
func WithTracing(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if uninstrumentedPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			route, name := routeAndSpanName(r.Method, effectiveRoutePattern(r))

			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := httpTracer.Start(ctx, name,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(r.Method),
					attribute.String("http.route", route),
				),
			)
			defer span.End()

			// request_id is attached later, by WithLogger — this only adds
			// trace_id/span_id, leaving request_id ownership in one place.
			ctx = logger.WithContext(ctx, tracing.LoggerWithSpan(log, span))

			rw := &captureWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rw, r.WithContext(ctx))

			span.SetAttributes(semconv.HTTPResponseStatusCode(rw.statusCode))
			if rw.statusCode >= 500 {
				span.SetStatus(codes.Error, http.StatusText(rw.statusCode))
			}
		})
	}
}

var httpMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	http.MethodConnect: true, http.MethodOptions: true, http.MethodTrace: true,
}

// routeAndSpanName derives the http.route attribute (path template only,
// per OTel semantic conventions) and the span name ("METHOD route") from
// net/http's r.Pattern. Go 1.22 ServeMux patterns registered with a method
// (e.g. "POST /webhooks/{vendor}") already have that method baked into
// r.Pattern's own text — naively prepending r.Method again produced
// "POST POST /webhooks/{vendor}" until this was caught by tracing a real
// request through Tempo during docs/roadmap/archive/43 Task T2 verification. A
// method-less pattern (e.g. the catch-all "/") has no such prefix, so the
// method still needs to be prepended for those.
func routeAndSpanName(method, pattern string) (route, name string) {
	if pattern == "" {
		return "unmatched", method + " unmatched"
	}
	if verb, rest, ok := strings.Cut(pattern, " "); ok && httpMethods[verb] {
		return rest, pattern
	}
	return pattern, method + " " + pattern
}
