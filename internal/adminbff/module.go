package adminbff

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/herdifirdausss/seev/internal/adminbff/client"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

const sessionCookieName = "admin_session"

var (
	adminSessionKey  = struct{}{}
	csrfFailureTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "adminbff", Name: "csrf_failures_total", Help: "Rejected admin BFF requests due to missing or invalid CSRF tokens.",
	})
	auditWriteFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "adminbff", Name: "audit_write_failures_total", Help: "Admin BFF audit writes that failed while the mutation continued.",
	})
)

func init() {
	prometheus.Register(csrfFailureTotal)
	prometheus.Register(auditWriteFailuresTotal)
}

type Module struct {
	repo      SessionRepository
	audit     auditWriter
	auth      *AuthClient
	clients   client.Clients
	cfg       config.AdminBFFConfig
	logger    *slog.Logger
	lock      scheduler.LockProvider
	scheduler *scheduler.Scheduler
	startOnce sync.Once
}

func NewModule(db database.DatabaseSQL, cfg config.AdminBFFConfig, logger *slog.Logger) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	lock := scheduler.NewMemoryLock(2 * time.Minute)
	return &Module{repo: NewSessionRepository(db), auth: NewAuthClient(cfg.AuthServiceURL), clients: client.NewClients(cfg.AuthServiceURL, cfg.AuthAdminServiceURL, cfg.LedgerServiceURL, cfg.PayinServiceURL, cfg.PayoutServiceURL, cfg.FraudServiceURL, cfg.GatewayServiceURL), cfg: cfg, logger: logger,
		audit: newAuditRepository(db),
		lock:  lock, scheduler: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics())}
}

func (m *Module) Start() error {
	var startErr error
	m.startOnce.Do(func() {
		startErr = m.scheduler.Cron("adminbff-session-cleanup", "*/5 * * * *", func(ctx context.Context) error {
			return m.repo.CleanupSessions(ctx, time.Now())
		})
	})
	return startErr
}

func (m *Module) Stop() {
	m.scheduler.Stop()
	if stopper, ok := m.lock.(interface{ Stop() }); ok {
		stopper.Stop()
	}
}

func (m *Module) AdminRouter() http.Handler {
	mux := http.NewServeMux()
	// The BFF exposes a stable operator namespace while each downstream keeps
	// its existing internal admin route. No domain repository is opened here.
	mux.Handle("/api/v1/admin/ledger/", m.proxy("ledger", m.clients.Ledger, "/api/v1/admin/ledger/", "/api/v1/admin/ledger/"))
	mux.Handle("/api/v1/admin/policy/", m.proxy("ledger", m.clients.Ledger, "/api/v1/admin/policy/", "/api/v1/admin/policy/"))
	mux.Handle("/api/v1/admin/payin/", m.proxy("payin", m.clients.Payin, "/api/v1/admin/payin/", "/admin/payin/"))
	mux.Handle("/api/v1/admin/payout/", m.proxy("payout", m.clients.Payout, "/api/v1/admin/payout/", "/admin/payout/"))
	mux.Handle("/api/v1/admin/fraud/", m.proxy("fraud", m.clients.Fraud, "/api/v1/admin/fraud/", "/api/v1/admin/fraud/"))
	mux.Handle("/api/v1/admin/kyc/", m.proxy("auth", m.clients.AuthAdmin, "/api/v1/admin/kyc/", "/api/v1/admin/kyc/"))
	mux.Handle("/api/v1/admin/gateway/", m.proxy("gateway", m.clients.Gateway, "/api/v1/admin/gateway/", "/api/v1/admin/gateway/"))
	mux.HandleFunc("/api/v1/admin/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/" {
			htmlHeader(w)
			http.NotFound(w, r)
			return
		}
		htmlHeader(w)
		_, _ = w.Write([]byte("<main><h1>Admin console</h1><nav><a href=\"/api/v1/admin/payout/requests\">Payout</a> <a href=\"/api/v1/admin/ledger/adjustments\">Ledger</a> <a href=\"/api/v1/admin/kyc/submissions\">KYC</a></nav></main>"))
	})
	return mux
}

func (m *Module) proxy(target string, downstream *client.ServiceClient, publicPrefix, downstreamPrefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		suffix := strings.TrimPrefix(r.URL.Path, publicPrefix)
		path := downstreamPrefix + suffix
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		status, headers, responseBody, callErr := downstream.Do(r.Context(), token, r.Method, path, body)
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, target, http.StatusServiceUnavailable, map[string]any{"error": "unavailable"})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		m.AuditMutation(r.Context(), r, target, status, map[string]any{"downstream_status": status})
		w.WriteHeader(status)
		_, _ = w.Write(responseBody)
	})
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]string{"code": code, "message": message}})
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
