package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	vendorv1 "github.com/herdifirdausss/seev/gen/vendorservice/v1"
	"github.com/herdifirdausss/seev/internal/config"
	internalvendor "github.com/herdifirdausss/seev/internal/vendorboundary"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/httpcontract"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/tlsx"
	"github.com/herdifirdausss/seev/pkg/tracing"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe VendorService health")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background()); err != nil {
		slog.Error("vendor-service stopped", "error", err)
		os.Exit(1)
	}
}

func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8098"
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
	response, err := tlsx.HTTPClient(certSrc, tlsx.IdentityVendor, 3*time.Second).Get("https://127.0.0.1:" + port + "/health")
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
		cfg.App.Port = "8098"
	}
	if os.Getenv("GRPC_PORT") == "" {
		cfg.GRPCPort = "9098"
	}
	log := logger.New(cfg.Logger.Pkg())
	certSrc, err := tlsx.LoadFromDir(cfg.TLSCertDir, "vendor", log)
	if err != nil {
		return fmt.Errorf("load TLS certificates: %w", err)
	}
	defer certSrc.Stop()
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{ServiceName: "vendor-service", Endpoint: cfg.Tracing.OTLPEndpoint, SampleRatio: cfg.Tracing.SampleRatio, Insecure: cfg.Tracing.Insecure})
	if err != nil {
		log.Warn("tracing setup failed; continuing without tracing", "error", err)
		shutdownTracing = func(context.Context) error { return nil }
	}

	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()

	// Security audit finding: vendor_callback_inbox.raw_body/selected_headers
	// had no cryptox protection at all, unlike every other raw-payload
	// column in the codebase — required unconditionally here, same
	// "money-safety, never optional" posture cmd/auth-service and
	// cmd/ledger-service already hold their own rings to.
	cryptoxRing, err := cfg.Cryptox.Ring()
	if err != nil {
		return fmt.Errorf("build cryptox ring: %w", err)
	}

	stopRetention, err := internalvendor.StartRetentionRunner(db, nil, log)
	if err != nil {
		return fmt.Errorf("start retention runner: %w", err)
	}
	defer stopRetention()

	grpcServer, err := grpcx.NewServer(log, cfg.InternalGRPCToken, tlsx.ServerConfig(certSrc, []string{tlsx.IdentityPayin, tlsx.IdentityPayout}))
	if err != nil {
		return fmt.Errorf("create grpc server: %w", err)
	}
	registry := internalvendor.NewRegistry()
	if cfg.Vendor.MockvendorEnabled {
		if err := registry.Add("mockvendor", internalvendor.NewMockAdapter("mockvendor", cfg.Vendor.MockvendorSecret)); err != nil {
			return fmt.Errorf("register mockvendor: %w", err)
		}
	}
	if cfg.Vendor.Mockvendor2Enabled {
		if err := registry.Add("mockvendor2", internalvendor.NewMockAdapter("mockvendor2", cfg.Vendor.Mockvendor2Secret)); err != nil {
			return fmt.Errorf("register mockvendor2: %w", err)
		}
	}
	vendorv1.RegisterVendorServiceServer(grpcServer, internalvendor.NewServer(registry, db))
	payinConn, err := grpcx.DialLazy(ctx, cfg.PayinGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityPayin))
	if err != nil {
		return fmt.Errorf("create payin callback client: %w", err)
	}
	defer func() { _ = payinConn.Close() }()
	payoutConn, err := grpcx.DialLazy(ctx, cfg.PayoutGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityPayout))
	if err != nil {
		_ = payinConn.Close()
		return fmt.Errorf("create payout callback client: %w", err)
	}
	defer func() { _ = payoutConn.Close() }()
	callbackHandler, err := internalvendor.NewCallbackHandler(db, cryptoxRing, registry, payinv1.NewPayinServiceClient(payinConn), payoutv1.NewPayoutServiceClient(payoutConn), os.Getenv("VENDOR_CALLBACK_CIDRS"), os.Getenv("VENDOR_CALLBACK_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:        ":" + cfg.App.Port,
		Handler:     vendorHTTPHandler(db, callbackHandler),
		ReadTimeout: cfg.App.ReadTimeout, WriteTimeout: cfg.App.WriteTimeout,
		IdleTimeout: cfg.App.IdleTimeout, ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes: 1 << 20,
		TLSConfig:      tlsx.ServerConfig(certSrc, []string{tlsx.IdentityDevOperator, tlsx.IdentityPrometheus}),
	}
	errCh := make(chan error, 2)
	go func() { errCh <- grpcServer.Serve(listener) }()
	go func() { errCh <- httpServer.ListenAndServeTLS("", "") }()
	log.Info("vendor-service started", "grpc", listener.Addr(), "admin_http", httpServer.Addr)
	select {
	case <-ctx.Done():
	case serveErr := <-errCh:
		if serveErr != nil && serveErr != grpc.ErrServerStopped {
			cancel()
			return serveErr
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	grpcServer.GracefulStop()
	_ = shutdownTracing(context.Background())
	return nil
}

func vendorHTTPHandler(db *database.DBSQL, callbacks http.Handler) http.Handler {
	mux := httpcontract.New(httpcontract.Options{Owner: "vendor", Audience: "vendor", Contract: "webhooks-v1"})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		if err := db.HealthCheck(context.Background()); err != nil {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.Handle("POST /webhooks/{vendor}", callbacks)
	return mux
}
