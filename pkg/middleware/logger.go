package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/herdifirdausss/seev/pkg/logger"
)

// ─── Structured Logger ────────────────────────────────────────────────────────

// WithLogger logs every request with structured fields and hands the real
// handler a fully-enriched, context-scoped logger. When WithTracing ran
// earlier in the chain (docs/roadmap/archive/43 K4's mandated order), the context
// already carries a logger with trace_id/span_id attached — WithLogger adds
// request_id on top and re-stores the result, so any downstream handler
// calling logger.FromContext(ctx) gets all three fields without doing
// anything itself (docs/roadmap/archive/43 K4). Falls back to log itself when nothing
// is in context (WithTracing absent, e.g. a route deliberately excluded
// from tracing, or standalone use in a test).
func WithLogger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &captureWriter{ResponseWriter: w, statusCode: http.StatusOK}

			reqLog := logger.FromContextOrDefault(r.Context(), log).With("request_id", RequestIDFromCtx(r.Context()))
			r = r.WithContext(logger.WithContext(r.Context(), reqLog))

			reqBody := logger.ReadAndMaskRequestBody(r, 16*1024)
			reqLog.Info("http request received",
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"client_ip", r.RemoteAddr,
				"user_agent", r.UserAgent(),
				"headers", logger.SanitizeHeaders(r.Header),
				"request_body", reqBody,
			)

			next.ServeHTTP(rw, r)

			duration := time.Since(start)

			respBody := logger.MaskResponseBody(
				rw.body.Bytes(),
				rw.Header().Get("Content-Type"),
			)

			fields := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.statusCode,
				"duration_ms", duration.Milliseconds(),
				"remote_addr", realIP(r),
				"response_body", respBody,
			}

			switch {
			case rw.statusCode >= 500:
				reqLog.Error("http request completed", fields...)
			case rw.statusCode >= 400:
				reqLog.Warn("http request completed", fields...)
			default:
				reqLog.Info("http request completed", fields...)
			}
		})
	}
}
