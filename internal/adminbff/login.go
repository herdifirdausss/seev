package adminbff

import (
	"context"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/herdifirdausss/seev/pkg/middleware"
)

const sessionCookieName = "admin_session"

type sessionContextKey struct{}

var adminSessionKey = sessionContextKey{}

var csrfFailureTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Namespace: "adminbff", Name: "csrf_failures_total", Help: "Rejected admin BFF requests due to missing or invalid CSRF tokens.",
})

func init() {
	_ = prometheus.Register(csrfFailureTotal)
}

func (m *Module) LoginPage() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		htmlHeader(w)
		_ = loginTemplate.Execute(w, nil)
	})
}

func (m *Module) LoginHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid login form", http.StatusBadRequest)
			return
		}
		user, err := m.auth.Login(r.Context(), strings.TrimSpace(r.FormValue("email")), r.FormValue("password"))
		if err != nil || !isAdminRole(user.Role) {
			// Deliberately generic: account existence and role are not disclosed.
			http.Error(w, "invalid operator credentials", http.StatusForbidden)
			return
		}
		session, err := m.newSession(user)
		if err != nil {
			http.Error(w, "could not start session", http.StatusInternalServerError)
			return
		}
		if err := m.repo.CreateSession(r.Context(), session); err != nil {
			http.Error(w, "could not start session", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: session.ID, Path: "/", HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, Expires: session.AbsoluteExpiresAt})
		http.Redirect(w, r, "/api/v1/admin/", http.StatusSeeOther)
	})
}

func (m *Module) LogoutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := SessionFromContext(r.Context()); s != nil {
			_ = m.repo.DeleteSession(r.Context(), s.ID)
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func (m *Module) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			redirectLogin(w, r)
			return
		}
		session, err := m.repo.GetSession(r.Context(), cookie.Value)
		if err != nil {
			redirectLogin(w, r)
			return
		}
		now := time.Now()
		expires := now.Add(m.cfg.SessionIdleTTL)
		if expires.After(session.AbsoluteExpiresAt) {
			expires = session.AbsoluteExpiresAt
		}
		if err := m.repo.TouchSession(r.Context(), session.ID, expires); err != nil {
			redirectLogin(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: session.ID, Path: "/", HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, Expires: expires})
		ctx := context.WithValue(r.Context(), adminSessionKey, &session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Module) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		session := SessionFromContext(r.Context())
		provided := r.Header.Get("X-CSRF-Token")
		if provided == "" {
			_ = r.ParseForm()
			provided = r.FormValue("csrf_token")
		}
		if session == nil || provided == "" || provided != session.CSRFToken {
			csrfFailureTotal.Inc()
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SessionFromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(adminSessionKey).(*Session)
	return s
}

// MintDownstreamToken creates the short-lived operator identity used by T4
// typed clients. The BFF never stores the auth-service access/refresh tokens.
func (m *Module) MintDownstreamToken(ctx context.Context) (string, error) {
	session := SessionFromContext(ctx)
	if session == nil {
		return "", ErrSessionNotFound
	}
	expires := time.Now().Add(m.cfg.DownstreamTokenTTL)
	return middleware.GenerateToken(m.cfg.JWTSecret, middleware.Claims{
		UserID: session.UserID.String(), Email: session.Email, Role: session.Role,
		Exp: expires.Unix(), Iss: m.cfg.JWTIssuer,
	})
}

func (m *Module) newSession(user AuthUser) (Session, error) {
	id, err := NewOpaqueToken(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := NewOpaqueToken(32)
	if err != nil {
		return Session{}, err
	}
	now := time.Now()
	return Session{ID: id, UserID: user.ID, Email: user.Email, Role: user.Role, CSRFToken: csrf,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(m.cfg.SessionIdleTTL), AbsoluteExpiresAt: now.Add(m.cfg.SessionAbsoluteTTL)}, nil
}

func isAdminRole(role string) bool {
	return role == "admin" || role == "admin_maker" || role == "admin_checker"
}

func redirectLogin(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Accept"), "text/html") || r.Header.Get("Accept") == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func htmlHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html><html lang="id"><head><meta charset="utf-8"><title>Admin login</title></head><body><main><h1>Admin console</h1><form method="post" action="/login"><label>Email <input type="email" name="email" required></label><label>Password <input type="password" name="password" required></label><button type="submit">Masuk</button></form></main></body></html>`))
