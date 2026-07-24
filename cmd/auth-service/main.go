package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"google.golang.org/grpc"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/auth"
	authrepository "github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/kycvendor"
	"github.com/herdifirdausss/seev/internal/kycvendor/httpkyc"
	"github.com/herdifirdausss/seev/internal/kycvendor/mockkyc"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/tlsx"
	"github.com/herdifirdausss/seev/pkg/tracing"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the auth-service liveness endpoint")
	backfillCryptox := flag.String("backfill-cryptox", "", "docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.5: run bounded cryptox backfill for auth_users/kyc_submissions and exit (all|users|kyc)")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if *backfillCryptox != "" {
		if err := runCryptoxBackfill(context.Background(), *backfillCryptox); err != nil {
			slog.Error("cryptox backfill failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("auth-service stopped", "error", err)
		os.Exit(1)
	}
}

// runCryptoxBackfill is docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.5's bounded
// backfill entrypoint: a one-shot mode that connects only to Postgres (no
// gRPC/Redis/HTTP — everything run() otherwise brings up), loops each
// target's own repository.BackfillOnce(ctx, batchSize) until it returns 0
// (that emptiness IS the completion proof, see UserRepository.BackfillOnce's
// own doc comment), and exits. Kept inside auth-service's own main.go
// rather than a separate cross-service CLI so it stays within
// TestModuleBoundaries' one-command-one-module rule — a shared
// cryptoxbackfillctl importing all five owners' internal/ packages at once
// would violate the same rule cmd/retentionctl avoids by staying
// SQL-generic instead.
func runCryptoxBackfill(ctx context.Context, target string) error {
	if target != "all" && target != "users" && target != "kyc" {
		return fmt.Errorf("unknown --backfill-cryptox %q (want all|users|kyc)", target)
	}
	cfg, err := config.LoadAuthService()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if len(cfg.Cryptox.Keys) == 0 {
		return fmt.Errorf("no CRYPTOX_KEY_V* configured, nothing to backfill against")
	}
	ring, err := cfg.Cryptox.Ring()
	if err != nil {
		return fmt.Errorf("build cryptox ring: %w", err)
	}
	lookup, err := cfg.Cryptox.Lookup()
	if err != nil {
		return fmt.Errorf("build cryptox lookup key: %w", err)
	}
	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()

	const batchSize = 500
	if target == "all" || target == "users" {
		if err := backfillLoop(ctx, "auth_users", batchSize, authrepository.NewUserRepository(db, ring, lookup).BackfillOnce); err != nil {
			return err
		}
	}
	if target == "all" || target == "kyc" {
		if err := backfillLoop(ctx, "kyc_submissions", batchSize, authrepository.NewKYCRepository(db, ring).BackfillOnce); err != nil {
			return err
		}
	}
	return nil
}

func backfillLoop(ctx context.Context, label string, batchSize int, once func(context.Context, int) (int, error)) error {
	total := 0
	for {
		n, err := once(ctx, batchSize)
		if err != nil {
			return fmt.Errorf("backfill %s (processed %d so far): %w", label, total, err)
		}
		total += n
		if n > 0 {
			slog.Info("cryptox backfill progress", "target", label, "batch", n, "total", total)
		}
		if n == 0 {
			break
		}
	}
	slog.Info("cryptox backfill done", "target", label, "total", total)
	return nil
}

// probeHealth dials the INTERNAL :8083 listener, which is mTLS since
// docs/roadmap/archive/49 K6 flips it — auth's PUBLIC :8082 has no separate
// healthcheck path and stays plain (anti-scope: edge-public exception).
func probeHealth(getenv func(string) string) error {
	port := getenv("INTERNAL_APP_PORT")
	if port == "" {
		port = "8083"
	}
	certDir := getenv("TLS_CERT_DIR")
	if certDir == "" {
		certDir = "deploy/certs"
	}
	certSrc, err := tlsx.LoadFromDir(certDir, "dev-operator", slog.Default())
	if err != nil {
		return fmt.Errorf("load healthcheck TLS identity: %w", err)
	}
	defer certSrc.Stop()
	client := tlsx.HTTPClient(certSrc, tlsx.IdentityAuth, 3*time.Second)
	response, err := client.Get("https://127.0.0.1:" + port + "/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}

func run(parent context.Context) error {
	cfg, err := config.LoadAuthService()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if os.Getenv("APP_PORT") == "" {
		cfg.App.Port = "8082"
	}
	if os.Getenv("INTERNAL_APP_PORT") == "" {
		cfg.App.InternalPort = "8083"
	}
	log := logger.New(cfg.Logger.Pkg())
	// docs/roadmap/archive/49 K3/K5: load this process's own identity + the shared CA
	// before anything else.
	certSrc, err := tlsx.LoadFromDir(cfg.TLSCertDir, "auth", log)
	if err != nil {
		return fmt.Errorf("load TLS certificates: %w", err)
	}
	defer certSrc.Stop()
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: "auth-service",
		Endpoint:    cfg.Tracing.OTLPEndpoint,
		SampleRatio: cfg.Tracing.SampleRatio,
		Insecure:    cfg.Tracing.Insecure,
	})
	if err != nil {
		log.Error("tracing: setup failed, continuing without a tracer provider", "error", err)
		shutdownTracing = func(context.Context) error { return nil }
	}

	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	var redisCache *cache.Cache
	if cfg.Redis.Enabled {
		cfg.Redis.DB = 0
		redisCache, err = cache.New(ctx, cfg.Redis.Pkg())
		if err != nil {
			_ = db.Close()
			return fmt.Errorf("connect redis: %w", err)
		}
	}
	ledgerConn, err := grpcx.Dial(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityLedger))
	if err != nil {
		closeAuthDependencies(log, nil, nil, redisCache, db, shutdownTracing)
		return fmt.Errorf("connect ledger-service: %w", err)
	}
	var fraudConn *grpc.ClientConn
	var closeFraud func() error
	if cfg.FraudGRPCAddr != "" {
		conn, dialErr := grpcx.DialLazy(ctx, cfg.FraudGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityFraud))
		if dialErr != nil {
			closeAuthDependencies(log, ledgerConn.Close, nil, redisCache, db, shutdownTracing)
			return fmt.Errorf("connect fraud-service: %w", dialErr)
		}
		fraudConn = conn
		closeFraud = fraudConn.Close
	}
	kycProvider := kycvendor.Provider(mockkyc.New())
	if cfg.Auth.KYCProviderURL != "" {
		configuredProvider, providerErr := httpkyc.New(cfg.Auth.KYCProviderURL, cfg.Auth.KYCProviderToken, cfg.Auth.KYCProviderName, nil)
		if providerErr != nil {
			closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
			return fmt.Errorf("configure kyc provider: %w", providerErr)
		}
		kycProvider = configuredProvider
	}
	module := auth.NewModule(db, ledgerclient.New(ledgerConn), auth.Config{
		JWTSecret: cfg.JWT.Secret, JWTIssuer: cfg.JWT.Issuer,
		AccessExpiry: cfg.JWT.AccessExpiry, RefreshExpiry: cfg.JWT.RefreshExpiry,
		DefaultCurrency: cfg.Auth.DefaultCurrency,
	}, log, kycProvider)
	var startRescreen func() error
	var stopRescreen func()
	if fraudConn != nil {
		sanctionsChecker := fraudcheck.New(fraudv1.NewFraudServiceClient(fraudConn), "auth")
		module.SetSanctionsChecker(sanctionsChecker)
		interval := 24 * time.Hour
		if raw := os.Getenv("KYC_RESCREEN_INTERVAL"); raw != "" {
			parsed, parseErr := time.ParseDuration(raw)
			if parseErr != nil {
				closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
				return fmt.Errorf("invalid KYC_RESCREEN_INTERVAL %q: %w", raw, parseErr)
			}
			if parsed <= 0 {
				closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
				return fmt.Errorf("invalid KYC_RESCREEN_INTERVAL %q: must be positive", raw)
			}
			interval = parsed
		}
		rescreenJob := module.NewKYCRescreenJob(redisClientClient(redisCache), sanctionsChecker, interval, log)
		startRescreen = func() error { return rescreenJob.Start(ctx) }
		stopRescreen = rescreenJob.Stop
	}
	// docs/roadmap/active/51 T2.2: replaces the old KYC_DOC_KEK raw-string env var
	// with the shared cluster-wide cryptox key ring (CRYPTOX_KEY_V1_FILE /
	// CRYPTOX_KEY_CURRENT_VERSION) — a missing/unconfigured ring is not an
	// error here (KYC document encryption stays optional outside
	// production, matching UploadKYCDocument's own existing
	// "storage is optional in this binary" convention); a malformed one is.
	if len(cfg.Cryptox.Keys) > 0 {
		ring, err := cfg.Cryptox.Ring()
		if err != nil {
			closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
			return fmt.Errorf("build cryptox ring: %w", err)
		}
		module.SetDocumentKeyRing(ring)

		// docs/roadmap/active/51 T2.3: same ring, plus the separate lookup key
		// (K2 — encryption keys and lookup keys must never be the same
		// key material) for auth_users.email/full_name and
		// kyc_submissions.payload field encryption. lookup may be nil
		// (CRYPTOX_LOOKUP_KEY unset) — GetUserByEmail then falls back to
		// the plaintext-only lookup path for every row, not just
		// pre-backfill ones (see repository.NewUserRepository's own doc
		// comment).
		lookup, err := cfg.Cryptox.Lookup()
		if err != nil {
			closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
			return fmt.Errorf("build cryptox lookup key: %w", err)
		}
		module.SetCryptoxRing(ring, lookup)
	}
	// docs/roadmap/active/51 T4 (K9): a dedicated export KEK, deliberately its own
	// key namespace (never Cryptox above) — a missing/unconfigured ring
	// is not an error (export creation stays optional outside
	// production, same "storage is optional in this binary" convention
	// every other ring here already follows); a malformed one is.
	if len(cfg.Export.Keys) > 0 {
		exportRing, err := cfg.Export.Ring()
		if err != nil {
			closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
			return fmt.Errorf("build export ring: %w", err)
		}
		module.SetExportKeyRing(exportRing)
	}
	// docs/roadmap/active/51 T5 (K10): a dedicated closure KEK, same optionality as
	// Export above.
	if len(cfg.Closure.Keys) > 0 {
		closureRing, err := cfg.Closure.Ring()
		if err != nil {
			closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
			return fmt.Errorf("build closure ring: %w", err)
		}
		module.SetClosureKeyRing(closureRing)
	}
	// docs/roadmap/active/51 T4b/T5b: owner clients are registered UNCONDITIONALLY
	// (not gated on cfg.Closure.Keys) — the SAME registry backs both the
	// export saga (buildAndUploadExport, gated independently by
	// EXPORT_KEK above) and the closure saga (gated independently by
	// CLOSURE_KEK above), so an export-only deployment (closure ring
	// unconfigured) still gets every owner's export data; only
	// StartClosureWorker/RequestClosure themselves stay gated on
	// m.closureRing being non-nil. The outbound clients share the
	// process's own mTLS identity (already loaded as certSrc) plus the
	// same INTERNAL_GRPC_TOKEN every other internal caller in this
	// codebase already uses — no new secret introduced for this feature.
	// Registration order is the closure saga's own owner-processing order
	// (internal/auth.RegisterClosureOwner's own doc comment) — ledger
	// first (money-safety, the most consequential owner to fail on), then
	// the four T5b owners.
	module.RegisterClosureOwner("ledger", auth.NewOwnerClosureClient(
		cfg.LedgerInternalAPIURL, cfg.InternalGRPCToken, tlsx.HTTPClient(certSrc, tlsx.IdentityLedger, 5*time.Second)))
	module.RegisterClosureOwner("payin", auth.NewOwnerClosureClient(
		cfg.PayinInternalAPIURL, cfg.InternalGRPCToken, tlsx.HTTPClient(certSrc, tlsx.IdentityPayin, 5*time.Second)))
	module.RegisterClosureOwner("payout", auth.NewOwnerClosureClient(
		cfg.PayoutInternalAPIURL, cfg.InternalGRPCToken, tlsx.HTTPClient(certSrc, tlsx.IdentityPayout, 5*time.Second)))
	module.RegisterClosureOwner("fraud", auth.NewOwnerClosureClient(
		cfg.FraudInternalAPIURL, cfg.InternalGRPCToken, tlsx.HTTPClient(certSrc, tlsx.IdentityFraud, 5*time.Second)))
	module.RegisterClosureOwner("gateway", auth.NewOwnerClosureClient(
		cfg.GatewayInternalAPIURL, cfg.InternalGRPCToken, tlsx.HTTPClient(certSrc, tlsx.IdentityGateway, 5*time.Second)))
	if err := module.EnsureBootstrapAdmin(ctx, cfg.Auth.BootstrapAdminEmail, cfg.Auth.BootstrapAdminPassword); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("ensure bootstrap admin: %w", err)
	}
	if err := module.EnsureBootstrapOperator(ctx, cfg.Auth.BootstrapMakerEmail, cfg.Auth.BootstrapMakerPassword, "admin_maker"); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("ensure bootstrap maker: %w", err)
	}
	if err := module.EnsureBootstrapOperator(ctx, cfg.Auth.BootstrapCheckerEmail, cfg.Auth.BootstrapCheckerPassword, "admin_checker"); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("ensure bootstrap checker: %w", err)
	}
	retryJob := module.NewKYCApplyRetryJob(redisClientClient(redisCache), log)
	if err := retryJob.Start(ctx); err != nil {
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("start kyc apply retry worker: %w", err)
	}
	stopRetention, err := module.StartRetentionRunner(redisClientClient(redisCache), log)
	if err != nil {
		retryJob.Stop()
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("start data retention worker: %w", err)
	}
	// docs/roadmap/active/51 T1.6 (K6): stopObjectOutbox is nil when no
	// document store is configured (matches UploadKYCDocument's own
	// "storage is optional in this binary" convention) — never nil
	// otherwise, so calling it unconditionally at shutdown below is safe.
	stopObjectOutbox, err := module.StartObjectOutboxWorker(ctx, log)
	if err != nil {
		stopRetention()
		retryJob.Stop()
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("start object delete outbox worker: %w", err)
	}
	if startRescreen != nil {
		if err := startRescreen(); err != nil {
			retryJob.Stop()
			closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
			return fmt.Errorf("start kyc sanctions rescreen worker: %w", err)
		}
	}
	// docs/roadmap/active/51 T4 (K9): stopPrivacyExport is nil when no document
	// store or export ring is configured — same optionality as
	// stopObjectOutbox above, never nil otherwise.
	stopPrivacyExport, err := module.StartPrivacyExportWorker(ctx, log)
	if err != nil {
		retryJob.Stop()
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("start privacy export worker: %w", err)
	}
	// docs/roadmap/active/51 T5 (K10): stopClosureWorker is nil when no closure ring
	// or ledger client is configured — same optionality as stopPrivacyExport.
	stopClosureWorker, err := module.StartClosureWorker(ctx, log)
	if err != nil {
		retryJob.Stop()
		closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
		return fmt.Errorf("start closure saga worker: %w", err)
	}

	// docs/roadmap/archive/49 K6: auth's public :8082 stays plain (anti-scope edge
	// exception); only the internal :8083 listener flips to mTLS.
	publicServer := newHTTPServer(cfg.App, ":"+cfg.App.Port, publicRouter(cfg, module, redisCache, log), nil)
	internalServer := newHTTPServer(cfg.App, cfg.App.InternalBindAddr+":"+cfg.App.InternalPort, internalRouter(cfg, module), tlsx.ServerConfig(certSrc, []string{
		tlsx.IdentityDevOperator, tlsx.IdentityPrometheus, tlsx.IdentityAdminBFF,
	}))
	errCh := make(chan error, 2)
	go serveHTTP(publicServer, errCh)
	go serveHTTP(internalServer, errCh)
	log.Info("auth-service started", "http", publicServer.Addr, "internal_http", internalServer.Addr)

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()
	retryJob.Stop()
	stopRetention()
	if stopObjectOutbox != nil {
		stopObjectOutbox()
	}
	if stopPrivacyExport != nil {
		stopPrivacyExport()
	}
	if stopClosureWorker != nil {
		stopClosureWorker()
	}
	if stopRescreen != nil {
		stopRescreen()
	}
	if err := publicServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	if err := internalServer.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	closeAuthDependencies(log, ledgerConn.Close, closeFraud, redisCache, db, shutdownTracing)
	return serveErr
}

func redisClientClient(c *cache.Cache) *redis.Client {
	if c == nil {
		return nil
	}
	return c.Client()
}

func newHTTPServer(cfg config.AppConfig, addr string, handler http.Handler, tlsConfig *tls.Config) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
		IdleTimeout: cfg.IdleTimeout, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 1 << 20, TLSConfig: tlsConfig}
}

func serveHTTP(server *http.Server, errCh chan<- error) {
	var err error
	if server.TLSConfig != nil {
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- fmt.Errorf("http %s: %w", server.Addr, err)
	}
}

func closeAuthDependencies(log *slog.Logger, closeLedger func() error, closeFraud func() error, redisCache *cache.Cache, db *database.DBSQL, shutdownTracing func(context.Context) error) {
	if closeLedger != nil {
		if err := closeLedger(); err != nil {
			log.Error("close ledger grpc", "error", err)
		}
	}
	if closeFraud != nil {
		if err := closeFraud(); err != nil {
			log.Error("close fraud grpc", "error", err)
		}
	}
	if redisCache != nil {
		if err := redisCache.Close(); err != nil {
			log.Error("close redis", "error", err)
		}
	}
	if err := db.Close(); err != nil {
		log.Error("close postgres", "error", err)
	}
	if err := shutdownTracing(context.Background()); err != nil {
		log.Error("close tracing", "error", err)
	}
}
