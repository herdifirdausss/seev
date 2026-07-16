package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/handler"
	"github.com/herdifirdausss/seev/internal/notify"
	"github.com/herdifirdausss/seev/internal/server"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the gateway liveness endpoint")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(os.Getenv); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}

	ctx := context.Background()

	// ─── Config ───────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// ─── Logger ───────────────────────────────────────────────────────────────
	log := logger.New(cfg.Logger.Pkg())

	for _, w := range cfg.Warnings() {
		log.Warn("config: " + w)
	}

	// ─── Tracing (optional — docs/plan/12 Task T5) ─────────────────────────────
	// A setup failure here is deliberately non-fatal: tracing is pure
	// observability, never load-bearing for moving money, so a
	// misconfigured OTEL_EXPORTER_OTLP_ENDPOINT must not take down the
	// payment system the way a misconfigured Postgres/RabbitMQ would.
	shutdownTracing, err := setupTracing(ctx, cfg.Tracing.OTLPEndpoint)
	if err != nil {
		log.Error("tracing: setup failed, continuing without a tracer provider", "error", err)
	} else if cfg.Tracing.OTLPEndpoint != "" {
		log.Info("tracing: exporting to OTLP endpoint", "endpoint", cfg.Tracing.OTLPEndpoint)
	}

	// ─── PostgreSQL ───────────────────────────────────────────────────────────
	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}

	// ─── Redis (optional — docs/plan/12 Task T1) ───────────────────────────────
	// REDIS_ENABLED defaults to true (safe default for existing/multi-replica
	// deployments). Operators of a single small instance can set it to false;
	// rate limiting and the scheduler lock then fall back to in-memory
	// implementations. If Redis IS enabled but unreachable, that's a config
	// error, not a degrade-gracefully case — fail fast like every other
	// required dependency.
	var redisCache *cache.Cache
	if cfg.Redis.Enabled {
		redisCache, err = cache.New(ctx, cfg.Redis.Pkg())
		if err != nil {
			log.Error("failed to connect to redis", "error", err)
			os.Exit(1)
		}
	} else {
		log.Warn("redis: disabled (REDIS_ENABLED=false) — rate limiting and scheduler lock running in-memory, single-instance only")
	}

	// ─── RabbitMQ ─────────────────────────────────────────────────────────────
	mq, err := messaging.New(ctx, cfg.RabbitMQ.Broker())
	if err != nil {
		log.Error("failed to connect to rabbitmq", "error", err)
		os.Exit(1)
	}

	// ─── Remote ledger boundary ────────────────────────────────────────────────
	ledgerConn, err := grpcx.Dial(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		log.Error("failed to connect to ledger-service", "error", err)
		os.Exit(1)
	}
	ledgerProxy, err := newLedgerProxy(cfg.LedgerUserAPIURL, log)
	if err != nil {
		log.Error("failed to configure ledger proxy", "error", err)
		os.Exit(1)
	}
	payinConn, err := grpcx.Dial(ctx, cfg.PayinGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		log.Error("failed to connect to payin-service", "error", err)
		os.Exit(1)
	}
	payoutConn, err := grpcx.Dial(ctx, cfg.PayoutGRPCAddr, cfg.InternalGRPCToken)
	if err != nil {
		log.Error("failed to connect to payout-service", "error", err)
		os.Exit(1)
	}

	// ─── Payin module (docs/plan/22 Task T2/T3, decision K-T2/K-T6) ────────────
	// vendorRegistry starts empty and gains one entry per enabled vendor —
	// zero vendors enabled (the default) means every /webhooks/{vendor}
	// request 404s, byte-identical to before this feature existed. Adding a
	// real vendor later is one more `if cfg.Vendor.X.Enabled { ... }` block
	// here; internal/payin never changes (docs/plan/21 K-T6).
	// ─── Payout module (docs/plan/23 Task T3/T5, decision K-T3/K-T6) ──────────
	// Shares vendorRegistry with payin above — the same enabled vendor can
	// (and for mockvendor, does) implement both PayinVerifier and
	// PayoutProvider; the registry keeps the two lookups separate
	// internally. StartWorkers launches the resume/polling job (Task T3
	// step 3) that re-drives crashed/stalled requests.
	// ─── Notify module (docs/plan/25 Task T4) ──────────────────────────────────
	// The first RabbitMQ CONSUMER in this codebase — mq (messaging.Broker)
	// satisfies notify.Broker directly (Consumer + TopologyManager), same
	// "pass the concrete broker, narrowed by a local structural interface"
	// pattern payin/payout use for Poster. Start declares the queue
	// topology and launches the consumer goroutine; a failure here is
	// logged, not fatal — notifications are a nice-to-have, never
	// load-bearing for moving money the way Postgres/RabbitMQ connectivity
	// itself is.
	notifyModule := notify.NewModule(db, mq, log)
	if err := notifyModule.Start(ctx); err != nil {
		log.Error("failed to start notify consumer", "error", err)
	}

	// ─── Dependencies ─────────────────────────────────────────────────────────
	// deps.Cache stays nil when Redis is disabled — every consumer
	// (handler.NewRouter's rate limiter, Ready's health check) must
	// nil-check it rather than assume it's always populated.
	deps := &handler.Dependencies{
		DB:          db,
		Cache:       handler.CacheOrNil(redisCache),
		MQ:          mq,
		LedgerProxy: ledgerProxy,
		LedgerReady: ledgerReady(healthpb.NewHealthClient(ledgerConn)),
		Payin:       payinv1.NewPayinServiceClient(payinConn),
		Payout:      payoutv1.NewPayoutServiceClient(payoutConn),
		Notify:      notifyModule,
	}

	// ─── Routers ──────────────────────────────────────────────────────────────
	// Two listeners: the public router only accepts transaction types safe
	// for direct end-user use; the internal router accepts everything
	// (money_in, refund, withdraw settlement, escrow release, fee_collect,
	// /metrics, admin tooling) and is bound to InternalBindAddr (127.0.0.1 by
	// default) — never expose it to an untrusted network (docs/plan/10 T1).
	publicRouter := handler.NewRouter(cfg, deps, log)
	internalRouter := handler.NewInternalRouter(cfg, deps, log)

	// ─── Servers ──────────────────────────────────────────────────────────────
	publicSrv := server.New(cfg.App, publicRouter)
	internalSrv := server.NewWithAddr(cfg.App, cfg.App.InternalBindAddr+":"+cfg.App.InternalPort, internalRouter)

	// ─── Start + Graceful Shutdown ────────────────────────────────────────────
	if err := server.StartMulti(func() {
		// Cleanup runs after both servers stop accepting new connections.
		// Order matters: stop workers (so no new outbox claims/publishes
		// start) before closing the connections they depend on.
		log.Info("cleanup: stopping notify consumer")
		notifyModule.Stop()

		log.Info("cleanup: closing ledger grpc connection")
		if err := ledgerConn.Close(); err != nil {
			log.Error("cleanup: ledger grpc close error", "error", err)
		}
		log.Info("cleanup: closing payin grpc connection")
		if err := payinConn.Close(); err != nil {
			log.Error("cleanup: payin grpc close error", "error", err)
		}
		log.Info("cleanup: closing payout grpc connection")
		if err := payoutConn.Close(); err != nil {
			log.Error("cleanup: payout grpc close error", "error", err)
		}

		log.Info("cleanup: closing rabbitmq")
		if err := mq.Close(); err != nil {
			log.Error("cleanup: rabbitmq close error", "error", err)
		}

		if redisCache != nil {
			log.Info("cleanup: closing redis")
			if err := redisCache.Close(); err != nil {
				log.Error("cleanup: redis close error", "error", err)
			}
		}

		log.Info("cleanup: closing postgres")
		if err := db.Close(); err != nil {
			log.Error("cleanup: postgres close error", "error", err)
		}

		log.Info("cleanup: shutting down tracing")
		if err := shutdownTracing(context.Background()); err != nil {
			log.Error("cleanup: tracing shutdown error", "error", err)
		}
	}, publicSrv, internalSrv); err != nil {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

func probeHealth(getenv func(string) string) error {
	port := getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
