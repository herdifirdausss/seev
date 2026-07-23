package adminbff

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/herdifirdausss/seev/internal/adminbff/client"
	adminweb "github.com/herdifirdausss/seev/internal/adminbff/web"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/scheduler"
	"github.com/herdifirdausss/seev/pkg/tlsx"
)

type Module struct {
	repo      SessionRepository
	audit     auditWriter
	auditRead auditReader
	auth      *AuthClient
	clients   client.Clients
	cfg       config.AdminBFFConfig
	logger    *slog.Logger
	lock      scheduler.LockProvider
	scheduler *scheduler.Scheduler
	startOnce sync.Once
}

// NewModule wires the admin BFF's downstream clients. certSrc is nil in
// tests that talk to plain httptest.Server instances (docs/roadmap/archive/49 K6) —
// every downstream target then gets client.DefaultHTTPClient() instead of
// an mTLS transport, matching those tests' plain HTTP fixtures exactly.
// In production certSrc is always set: auth's PUBLIC login endpoint stays
// plain (anti-scope edge exception); every other target — all genuinely
// internal — gets its own mTLS client keyed to ITS identity, since one
// shared client can't satisfy six different expected-server-identity
// checks.
func NewModule(db database.DatabaseSQL, cfg config.AdminBFFConfig, logger *slog.Logger, certSrc *tlsx.CertSource) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	lock := scheduler.NewMemoryLock(2 * time.Minute)
	auditRepo := newAuditRepository(db)
	internalClient := func(identity string) *http.Client {
		if certSrc == nil {
			return client.DefaultHTTPClient()
		}
		return tlsx.HTTPClient(certSrc, identity, 5*time.Second)
	}
	clients := client.Clients{
		Auth:      client.New("auth", cfg.AuthServiceURL, client.DefaultHTTPClient()),
		AuthAdmin: client.New("auth-admin", cfg.AuthAdminServiceURL, internalClient(tlsx.IdentityAuth)),
		Ledger:    client.New("ledger", cfg.LedgerServiceURL, internalClient(tlsx.IdentityLedger)),
		Payin:     client.New("payin", cfg.PayinServiceURL, internalClient(tlsx.IdentityPayin)),
		Payout:    client.New("payout", cfg.PayoutServiceURL, internalClient(tlsx.IdentityPayout)),
		Fraud:     client.New("fraud", cfg.FraudServiceURL, internalClient(tlsx.IdentityFraud)),
		Gateway:   client.New("gateway", cfg.GatewayServiceURL, internalClient(tlsx.IdentityGateway)),
	}
	return &Module{repo: NewSessionRepository(db), auth: NewAuthClient(cfg.AuthServiceURL), clients: clients, cfg: cfg, logger: logger,
		audit: auditRepo, auditRead: auditRepo,
		lock: lock, scheduler: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics())}
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
	mux.Handle("GET /api/v1/admin/maker", m.consolePage("maker"))
	mux.Handle("GET /api/v1/admin/payout", m.consolePage("payout"))
	mux.Handle("GET /api/v1/admin/recon", m.consolePage("recon"))
	mux.Handle("GET /api/v1/admin/catalog", m.consolePage("catalog"))
	mux.HandleFunc("GET /api/v1/admin/audit", m.auditListHandler)
	mux.Handle("/api/v1/admin/adjustments/", m.proxy("ledger", m.clients.Ledger, "/api/v1/admin/adjustments/", "/api/v1/ledger/admin/adjustments/"))
	mux.Handle("/api/v1/admin/adjustments", m.proxy("ledger", m.clients.Ledger, "/api/v1/admin/adjustments", "/api/v1/ledger/admin/adjustments"))
	mux.Handle("POST /api/v1/admin/adjustments/approve", m.adjustmentDecisionProxy("approve"))
	mux.Handle("POST /api/v1/admin/adjustments/reject", m.adjustmentDecisionProxy("reject"))
	mux.Handle("/api/v1/admin/recon/", m.proxy("ledger", m.clients.Ledger, "/api/v1/admin/recon/", "/api/v1/ledger/admin/recon/"))
	mux.Handle("/api/v1/admin/recon/batches", m.reconUploadProxy())
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
		_ = adminweb.Render(w, "dashboard", m.pageData(r, "Operations summary", "dashboard"))
	})
	return mux
}

func (m *Module) consolePage(page string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := adminweb.Render(w, page, m.pageData(r, page, page)); err != nil {
			m.logger.Error("adminbff: render page", "page", page, "error", err)
			http.Error(w, "could not render admin page", http.StatusInternalServerError)
		}
	})
}

func (m *Module) pageData(r *http.Request, title, page string) adminweb.PageData {
	session := SessionFromContext(r.Context())
	data := adminweb.PageData{Title: title, Page: page}
	if session != nil {
		data.CSRFToken, data.Role = session.CSRFToken, session.Role
		data.IsMaker = session.Role == "admin" || session.Role == "admin_maker"
		data.IsChecker = session.Role == "admin" || session.Role == "admin_checker"
	}
	return data
}
