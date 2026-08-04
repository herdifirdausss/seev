package tracing

import (
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// LoggerWithSpan returns base enriched with trace_id/span_id fields when
// span carries a valid, sampled SpanContext — used by both HTTP and gRPC
// server middleware to correlate logs with traces (docs/roadmap/archive/43 K4/K9). When
// tracing is off (no TracerProvider installed, or this request wasn't
// sampled) span.SpanContext() is invalid and base is returned unchanged: no
// empty/zero trace_id ever leaks into a log line.
func LoggerWithSpan(base *slog.Logger, span trace.Span) *slog.Logger {
	sc := span.SpanContext()
	if !sc.IsValid() {
		return base
	}
	return base.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
}
