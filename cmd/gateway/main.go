package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/google/uuid"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/handler"
	"github.com/herdifirdausss/seev/internal/merchant"
	merchantapi "github.com/herdifirdausss/seev/internal/merchant/api"
	merchantclient "github.com/herdifirdausss/seev/internal/merchant/client"
	"github.com/herdifirdausss/seev/internal/merchant/idempotency"
	"github.com/herdifirdausss/seev/internal/merchant/quota"
	"github.com/herdifirdausss/seev/internal/notify"
	"github.com/herdifirdausss/seev/internal/server"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/grpcx"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/messaging"
	"github.com/herdifirdausss/seev/pkg/tlsx"
	"github.com/herdifirdausss/seev/pkg/tracing"
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

	// ─── TLS identity (docs/roadmap/archive/49 K3/K5) ─────────────────────────────────────
	certSrc, err := tlsx.LoadFromDir(cfg.TLSCertDir, "gateway", log)
	if err != nil {
		log.Error("failed to load TLS certificates", "error", err)
		os.Exit(1)
	}
	defer certSrc.Stop()

	// ─── Tracing (optional — docs/roadmap/archive/12 Task T5) ─────────────────────────────
	// A setup failure here is deliberately non-fatal: tracing is pure
	// observability, never load-bearing for moving money, so a
	// misconfigured OTEL_EXPORTER_OTLP_ENDPOINT must not take down the
	// payment system the way a misconfigured Postgres/RabbitMQ would.
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: "gateway-service",
		Endpoint:    cfg.Tracing.OTLPEndpoint,
		SampleRatio: cfg.Tracing.SampleRatio,
		Insecure:    cfg.Tracing.Insecure,
	})
	if err != nil {
		log.Error("tracing: setup failed, continuing without a tracer provider", "error", err)
	} else if cfg.Tracing.OTLPEndpoint != "" {
		log.Info("tracing: exporting to OTLP endpoint", "endpoint", cfg.Tracing.OTLPEndpoint, "sample_ratio", cfg.Tracing.SampleRatio)
	}

	// ─── PostgreSQL ───────────────────────────────────────────────────────────
	db, err := database.New(ctx, cfg.Postgres.Pkg())
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}

	// ─── Redis (optional — docs/roadmap/archive/12 Task T1) ───────────────────────────────
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
	ledgerConn, err := grpcx.Dial(ctx, cfg.LedgerGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityLedger))
	if err != nil {
		log.Error("failed to connect to ledger-service", "error", err)
		os.Exit(1)
	}
	ledgerProxy, err := newLedgerProxy(cfg.LedgerUserAPIURL, certSrc, log)
	if err != nil {
		log.Error("failed to configure ledger proxy", "error", err)
		os.Exit(1)
	}
	payinConn, err := grpcx.Dial(ctx, cfg.PayinGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityPayin))
	if err != nil {
		log.Error("failed to connect to payin-service", "error", err)
		os.Exit(1)
	}
	payoutConn, err := grpcx.Dial(ctx, cfg.PayoutGRPCAddr, cfg.InternalGRPCToken, tlsx.ClientConfig(certSrc, tlsx.IdentityPayout))
	if err != nil {
		log.Error("failed to connect to payout-service", "error", err)
		os.Exit(1)
	}
	payinGRPCClient := payinv1.NewPayinServiceClient(payinConn)
	payoutGRPCClient := payoutv1.NewPayoutServiceClient(payoutConn)

	// ─── Merchant/B2B module (Plan 57, roadmap track C1) ───────────────────────
	// cryptox is required unconditionally — webhook endpoint secrets (T7)
	// have no plaintext fallback, same "money-safety, never optional"
	// posture every other cryptox-dependent service in this repo already
	// enforces at boot. ledgerConn is reused directly (no second dial) for
	// account provisioning (T8's admin surface). This is the first wiring
	// of internal/merchant.Module into cmd/gateway — T2 through T7 built
	// the module, auth/quota/idempotency middleware, owner-service RPCs,
	// and the outbound webhook relay, but none of it was ever started here
	// before now.
	cryptoxRing, err := cfg.Cryptox.Ring()
	if err != nil {
		log.Error("failed to build cryptox ring", "error", err)
		os.Exit(1)
	}
	merchantLedgerClient := ledgerclient.New(ledgerConn)
	merchantModule := merchant.NewModule(db, cryptoxRing, cfg.Merchant.APIKeyPepper, merchantLedgerClient)
	stopWebhookRelay := merchantModule.StartWebhookRelay(ctx, merchant.DefaultWebhookRelayInterval)
	stopWebhookConsumer, err := merchantModule.StartWebhookConsumer(ctx, mq, log)
	if err != nil {
		log.Error("failed to start merchant webhook consumer", "error", err)
	}
	stopMerchantRetention, err := merchantModule.StartRetentionRunner(redisClientOrNil(redisCache), log)
	if err != nil {
		log.Error("failed to start merchant data retention worker", "error", err)
	}

	// ─── Merchant/B2B API HTTP surface (POST/GET /api/v1/b2b/payins, /payouts) ──
	// idempotencyLeaseOwner is observability-only (see
	// merchant.Module.StartRetentionRunner's identical instanceID
	// construction) — TakeoverExpiredLease's own WHERE clause is what
	// actually enforces exclusivity, not this value.
	idempotencyLeaseOwner, hostErr := os.Hostname()
	if hostErr != nil || idempotencyLeaseOwner == "" {
		idempotencyLeaseOwner = uuid.NewString()
	}
	idempotencyTTL, err := time.ParseDuration(cfg.Merchant.IdempotencyDefaultTTL)
	if err != nil {
		log.Error("failed to parse MERCHANT_IDEMPOTENCY_DEFAULT_TTL", "error", err)
		os.Exit(1)
	}
	b2bRouter := merchantapi.NewRouter(merchantapi.Deps{
		APIKeys:       merchantModule.APIKeys,
		Tenants:       merchantModule.Tenants,
		APIKeyPepper:  cfg.Merchant.APIKeyPepper,
		QuotaEnforcer: quota.NewEnforcer(merchantModule.Quotas, redisClientOrNil(redisCache)),
		Idempotency:   idempotency.NewService(merchantModule.Idempotency, idempotencyTTL, idempotencyLeaseOwner),
		Payin:         merchantclient.NewPayinClient(payinGRPCClient),
		Payout:        merchantclient.NewPayoutClient(payoutGRPCClient),
		Ledger:        merchantclient.NewLedgerClient(merchantLedgerClient),
		GlobalFlag:    merchantModule.GlobalFlag,
	})

	// Plan 57 T9: periodic gauge refresh for idempotency stuck-lease and
	// webhook backlog visibility (seev_merchant_idempotency_*,
	// seev_merchant_webhook_*) — see internal/merchant/metrics.go.
	stopMerchantObservability := merchantModule.StartObservabilityRefresher(ctx, merchant.DefaultObservabilityRefreshInterval, log)

	// ─── Payout module (docs/roadmap/archive/23 Task T3/T5, decision K-T3/K-T6) ──────────
	// StartWorkers launches the resume/polling job (Task T3
	// step 3) that re-drives crashed/stalled requests.
	// ─── Notify module (docs/roadmap/archive/25 Task T4) ──────────────────────────────────
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
	var stopRetention func()
	if stopRetention, err = notifyModule.StartRetentionRunner(redisClientOrNil(redisCache), log); err != nil {
		log.Error("failed to start data retention worker", "error", err)
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
		Payin:       payinGRPCClient,
		Payout:      payoutGRPCClient,
		Notify:      notifyModule,
		Merchant:    merchantModule,
		B2B:         b2bRouter,
	}

	// ─── Routers ──────────────────────────────────────────────────────────────
	// Two listeners: the public router only accepts transaction types safe
	// for direct end-user use; the internal router accepts everything
	// (money_in, refund, withdraw settlement, escrow release, fee_collect,
	// /metrics, admin tooling) and is bound to InternalBindAddr (127.0.0.1 by
	// default) — never expose it to an untrusted network (docs/roadmap/archive/10 T1).
	publicRouter := handler.NewRouter(cfg, deps, log)
	internalRouter := handler.NewInternalRouter(cfg, deps, log)

	// ─── Servers ──────────────────────────────────────────────────────────────
	// Public listener stays plain HTTP (docs/roadmap/archive/49 anti-scope: gateway
	// :8080 is one of the two deliberate edge-public exceptions). The
	// internal listener requires mTLS (K6) — admin-bff, Prometheus, and
	// dev-operator/harness are its only legitimate callers.
	publicSrv := server.New(cfg.App, publicRouter)
	internalSrv := server.NewWithAddrTLS(cfg.App, cfg.App.InternalBindAddr+":"+cfg.App.InternalPort, internalRouter, tlsx.ServerConfig(certSrc, []string{
		tlsx.IdentityDevOperator, tlsx.IdentityPrometheus, tlsx.IdentityAdminBFF,
		// docs/roadmap/archive/51 T4b/T5b: auth-service calls the new /privacy/
		// export+closure routes as the export/closure saga's coordinator.
		tlsx.IdentityAuth,
	}))

	// ─── Start + Graceful Shutdown ────────────────────────────────────────────
	if err := server.StartMulti(func() {
		// Cleanup runs after both servers stop accepting new connections.
		// Order matters: stop workers (so no new outbox claims/publishes
		// start) before closing the connections they depend on.
		log.Info("cleanup: stopping notify consumer")
		notifyModule.Stop()
		if stopRetention != nil {
			log.Info("cleanup: stopping data retention worker")
			stopRetention()
		}

		log.Info("cleanup: stopping merchant webhook relay")
		stopWebhookRelay()
		if stopWebhookConsumer != nil {
			log.Info("cleanup: stopping merchant webhook consumer")
			stopWebhookConsumer()
		}
		if stopMerchantRetention != nil {
			log.Info("cleanup: stopping merchant data retention worker")
			stopMerchantRetention()
		}
		log.Info("cleanup: stopping merchant observability refresher")
		stopMerchantObservability()

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

func redisClientOrNil(c *cache.Cache) *redis.Client {
	if c == nil {
		return nil
	}
	return c.Client()
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
