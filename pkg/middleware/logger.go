package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/herdifirdausss/seev/pkg/logger"
)

// ─── Structured Logger ────────────────────────────────────────────────────────

// WithLogger logs every request with structured fields.
func WithLogger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &captureWriter{ResponseWriter: w, statusCode: http.StatusOK}

			reqBody := logger.ReadAndMaskRequestBody(r, 16*1024)
			log.Info("http request received",
				"request_id", RequestIDFromCtx(r.Context()),
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
				"request_id", RequestIDFromCtx(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.statusCode,
				"duration_ms", duration.Milliseconds(),
				"remote_addr", realIP(r),
				"response_body", respBody,
			}

			switch {
			case rw.statusCode >= 500:
				log.Error("http request completed", fields...)
			case rw.statusCode >= 400:
				log.Warn("http request completed", fields...)
			default:
				log.Info("http request completed", fields...)
			}
		})
	}
}
