package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ─── CORS ─────────────────────────────────────────────────────────────────────

// CORSConfig configures CORS behavior.
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// DefaultCORSConfig returns an API-only config: no origin is allowed by
// default (docs/roadmap/archive/49 TM-06 — a wildcard here previously let any origin
// call the API programmatically once a token was obtained through another
// channel). Callers that need browser access must set AllowedOrigins
// explicitly.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedOrigins:   nil,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Origin", "Authorization", "Content-Type", "Accept", "Traceparent", "X-Request-Id"},
		ExposedHeaders:   []string{"X-Request-Id", "Content-Length"},
		AllowCredentials: false,
		MaxAge:           int((12 * time.Hour).Seconds()),
	}
}

// WithCORS sets CORS headers on every response.
func WithCORS(cfg CORSConfig) Middleware {
	allowedOrigins := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowedOrigins[o] = true
	}

	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	exposed := strings.Join(cfg.ExposedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if allowedOrigins["*"] {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if allowedOrigins[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			w.Header().Set("Access-Control-Allow-Methods", methods)
			w.Header().Set("Access-Control-Allow-Headers", headers)
			if exposed != "" {
				w.Header().Set("Access-Control-Expose-Headers", exposed)
			}
			if cfg.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Max-Age", fmt.Sprintf("%d", cfg.MaxAge))

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
