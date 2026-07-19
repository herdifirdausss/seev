package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/herdifirdausss/seev/pkg/logger"
)

// TestRouteAndSpanName_MethodAlreadyInPattern is the regression test for a
// bug caught tracing a real request through Tempo (docs/plan/43 Task T2):
// Go 1.22 ServeMux patterns registered with a method already have it baked
// into r.Pattern's text, so naively prepending r.Method again produced
// "POST POST /webhooks/{vendor}" instead of "POST /webhooks/{vendor}".
func TestRouteAndSpanName_MethodAlreadyInPattern(t *testing.T) {
	route, name := routeAndSpanName("POST", "POST /webhooks/{vendor}")
	assert.Equal(t, "/webhooks/{vendor}", route)
	assert.Equal(t, "POST /webhooks/{vendor}", name)
}

func TestRouteAndSpanName_MethodlessPattern(t *testing.T) {
	route, name := routeAndSpanName("GET", "/")
	assert.Equal(t, "/", route)
	assert.Equal(t, "GET /", name)
}

func TestRouteAndSpanName_EmptyPattern_Unmatched(t *testing.T) {
	route, name := routeAndSpanName("GET", "")
	assert.Equal(t, "unmatched", route)
	assert.Equal(t, "GET unmatched", name)
}

func TestWithTracing_SpanNamedAfterDispatch(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	origTracer := httpTracer
	httpTracer = provider.Tracer("test")
	t.Cleanup(func() { httpTracer = origTracer })

	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/{vendor}", WithTracing(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/mockvendor", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.NoError(t, provider.ForceFlush(context.Background()))
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "POST /webhooks/{vendor}", spans[0].Name)
}

// TestWithTracing_NestedMux_WithRoutePattern_ResolvesCorrectly is the
// regression test for the bug found tracing a REAL request through Tempo
// during docs/plan/43 Task T3 verification: payin-service's admin router
// (Chain(...)(root) wrapping the WHOLE dispatching mux, with WithLogger's
// own r.WithContext(...) sitting between WithTracing and the actual
// dispatch) reported route="unmatched" for a real, matched
// "/admin/payin/" request — r.Pattern read after next.ServeHTTP returns is
// stale/empty the moment ANY r.WithContext() copy happens in between.
// WithRoutePattern's pure mux.Handler(r) lookup, run BEFORE dispatch,
// sidesteps this entirely. This test reproduces the exact shape (Chain
// wraps a mux, WithLogger is one of the middlewares) that broke without it.
func TestWithTracing_NestedMux_WithRoutePattern_ResolvesCorrectly(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	origTracer := httpTracer
	httpTracer = provider.Tracer("test")
	t.Cleanup(func() { httpTracer = origTracer })

	root := http.NewServeMux()
	root.Handle("/admin/payin/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	chain := Chain(
		WithRequestID(),
		WithRoutePattern(root),
		WithTracing(slog.Default()),
		WithLogger(slog.Default()), // the middleware whose r.WithContext() broke the old approach
	)(root)

	req := httptest.NewRequest(http.MethodGet, "/admin/payin/events", nil)
	chain.ServeHTTP(httptest.NewRecorder(), req)

	require.NoError(t, provider.ForceFlush(context.Background()))
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /admin/payin/", spans[0].Name, "must resolve the real matched pattern, not fall back to unmatched")
}

func TestWithTracing_UninstrumentedPath_NoSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	origTracer := httpTracer
	httpTracer = provider.Tracer("test")
	t.Cleanup(func() { httpTracer = origTracer })

	handler := WithTracing(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/health", "/ready", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	require.NoError(t, provider.ForceFlush(context.Background()))
	assert.Empty(t, exporter.GetSpans(), "health/ready/metrics must never be traced")
}

func TestWithTracing_StoresSpanEnrichedLoggerInContext(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	origTracer := httpTracer
	httpTracer = provider.Tracer("test")
	t.Cleanup(func() { httpTracer = origTracer })

	var gotLogger *slog.Logger
	handler := WithTracing(slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLogger = logger.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.NotNil(t, gotLogger)
	assert.NotSame(t, slog.Default(), gotLogger, "WithTracing must store an enriched logger, not leave context empty")
}
