package middleware

import (
	"fmt"
	"net/http"
)

// ─── Security Headers ─────────────────────────────────────────────────────────
type SecurityHeadersConfig struct {
	EnableHSTS            bool
	HSTSMaxAge            int
	EnableCSP             bool
	ContentSecurityPolicy string
	// TrustProxyHeaders enables honoring X-Forwarded-Proto to detect HTTPS
	// when the app itself only ever terminates plain HTTP behind a
	// TLS-terminating reverse proxy (docs/roadmap/archive/10 Task T6). Only enable this
	// when the proxy is guaranteed to overwrite/strip the header from
	// client input — otherwise a client can spoof it. Default false: r.TLS
	// != nil is the only signal trusted.
	TrustProxyHeaders bool
}

func DefaultSecurityHeadersConfig() SecurityHeadersConfig {
	return SecurityHeadersConfig{
		EnableHSTS:            true,
		HSTSMaxAge:            31536000, // 1 year
		EnableCSP:             true,
		ContentSecurityPolicy: "default-src 'self'; frame-ancestors 'none'; base-uri 'self'",
	}
}

// WithSecurityHeaders adds standard hardening headers to every response.
func WithSecurityHeaders(cfg SecurityHeadersConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			// --- HSTS (ONLY over HTTPS, or a request the trusted proxy
			// reports as HTTPS — see TrustProxyHeaders doc above) ---
			isHTTPS := r.TLS != nil
			if !isHTTPS && cfg.TrustProxyHeaders && r.Header.Get("X-Forwarded-Proto") == "https" {
				isHTTPS = true
			}
			if cfg.EnableHSTS && isHTTPS {
				h.Set(
					"Strict-Transport-Security",
					fmt.Sprintf("max-age=%d; includeSubDomains", cfg.HSTSMaxAge),
				)
			}
			// --- CSP ---
			if cfg.EnableCSP && cfg.ContentSecurityPolicy != "" {
				h.Set("Content-Security-Policy", cfg.ContentSecurityPolicy)
			}
			next.ServeHTTP(w, r)
		})
	}
}
