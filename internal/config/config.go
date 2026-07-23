package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/logger"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	App       AppConfig
	Postgres  PostgresConfig
	Redis     RedisConfig
	RabbitMQ  RabbitMQConfig
	JWT       JWTConfig
	Logger    LoggerConfig
	Worker    WorkerConfig
	Ledger    LedgerConfig
	Tracing   TracingConfig
	Vendor    VendorConfig
	Auth      AuthConfig
	Fraud     FraudConfig
	Breaker   BreakerConfig
	AdminBFF  AdminBFFConfig
	Assurance AssuranceConfig

	// Cross-process endpoints introduced by the service extraction phases.
	GRPCPort          string
	InternalGRPCToken string
	LedgerGRPCAddr    string
	PayinGRPCAddr     string
	PayoutGRPCAddr    string
	FraudGRPCAddr     string
	LedgerUserAPIURL  string

	// TLSCertDir is where cmd/certgen (docs/roadmap/archive/49 K3) writes ca.pem plus
	// one <service>.pem/<service>-key.pem pair per identity — every
	// process loads exactly its own pair plus the shared CA from here via
	// pkg/tlsx. Never a per-file env var: one directory convention keeps
	// compose/lib.sh wiring to a single mount/volume.
	TLSCertDir string
}

// AuthConfig configures the auth module (docs/roadmap/archive/25 Task T1).
type AuthConfig struct {
	// DefaultCurrency is the currency ProvisionUser uses for a newly
	// registered user's account set. Must be an enabled currency.
	DefaultCurrency string
	// BootstrapAdminEmail/Password, when both set, idempotently create the
	// first admin account at startup (docs/roadmap/archive/25 T1 step 6) — chosen over
	// a seed migration so no password hash is ever committed to VCS.
	BootstrapAdminEmail      string
	BootstrapAdminPassword   string
	BootstrapMakerEmail      string
	BootstrapMakerPassword   string
	BootstrapCheckerEmail    string
	BootstrapCheckerPassword string
	// KYCProviderURL enables the HTTP provider adapter. Empty keeps the
	// deterministic mock provider used by offline development and CI.
	KYCProviderURL   string
	KYCProviderToken string
	KYCProviderName  string
}

// VendorConfig configures the payin webhook vendor registry (docs/roadmap/archive/22
// Task T1/T3, decision K-T6). Default (no env set) = MockvendorEnabled
// false, so cmd/gateway registers zero vendors and every /webhooks/{vendor}
// request 404s — byte-identical to before this feature existed. Adding a
// real vendor later is a new field here + one registration line in
// cmd/gateway/main.go, never a change to internal/payin.
type VendorConfig struct {
	MockvendorEnabled bool
	MockvendorSecret  string
	// TopupIntentTTL is how long a payin topup intent (docs/roadmap/archive/25 Task
	// T3) stays 'pending' before being treated as expired — a settled
	// webhook arriving after this window is a business failure (money
	// never posts), not silently accepted.
	TopupIntentTTL time.Duration
	// Mockvendor2Enabled/Secret register a SECOND mock vendor (docs/roadmap/archive/40
	// Task T4) — exists purely to demonstrate real failover between two
	// registered vendors; default disabled, byte-identical to before this
	// feature existed.
	Mockvendor2Enabled bool
	Mockvendor2Secret  string
}

// BreakerConfig tunes the per-vendor circuit breaker (docs/roadmap/archive/40 Task T1,
// internal/vendorgw.HealthTracker) shared by payin-service and
// payout-service.
type BreakerConfig struct {
	// FailureThreshold consecutive transport/infra failures trip the
	// circuit open. <=0 falls back to HealthTracker's own default (5).
	FailureThreshold int
	// Cooldown is how long the circuit stays open before a single
	// half-open probe is allowed through. <=0 falls back to
	// HealthTracker's own default (30s).
	Cooldown time.Duration
	// Distributed opts into the Redis-backed DistributedBreaker
	// (docs/roadmap/archive/45 Task T2/K3) instead of the per-process HealthTracker —
	// default false (compatible with today's behavior) until the
	// integration/chaos gate for it has passed. Has no effect if Redis
	// itself is disabled/unreachable at startup: the service falls back to
	// a plain HealthTracker rather than failing to start.
	Distributed bool
}

// TracingConfig controls OpenTelemetry trace export (docs/roadmap/archive/12 Task T5).
type TracingConfig struct {
	// OTLPEndpoint, if non-empty, installs a real TracerProvider exporting
	// to this OTLP gRPC endpoint (e.g. "localhost:4317" for a local
	// Jaeger/Tempo). Empty = no provider is installed at all — the
	// existing span-creation code (internal/ledger/service/handle's
	// tracer.Start calls) keeps running against the SDK's global no-op
	// tracer, which is zero-overhead. This is why "remove the
	// instrumentation" was never on the table: it's already there and
	// free until someone actually wants to look at it.
	OTLPEndpoint string
	// SampleRatio is read from OTEL_TRACES_SAMPLER_ARG (docs/roadmap/archive/43 K3) —
	// the sampler strategy itself (ParentBased(TraceIDRatioBased(...))) is
	// fixed in code, not selectable via OTEL_TRACES_SAMPLER; that env var
	// is set in compose only for documentation/OTel-convention clarity.
	SampleRatio float64
	// Insecure selects a plaintext OTLP gRPC connection, read from
	// OTEL_EXPORTER_OTLP_INSECURE (docs/roadmap/archive/43 K3) — every environment
	// this repo targets uses a local, unencrypted Tempo on the private
	// Compose network, so this defaults to true.
	Insecure bool
}

// LedgerConfig holds ledger-module-specific tunables that must live outside
// internal/ledger itself (the module must not depend on the composition
// root's config type — see internal/ledger.WorkerConfig for the same
// pattern).
type LedgerConfig struct {
	// MaxAmountPerTx is a global safety ceiling (minor units) applied to
	// every posted transaction, independent of any future per-user/per-type
	// business limits (docs/roadmap/archive/08 S1). Not a business limit — a guard
	// against bugs/abuse (docs/roadmap/archive/10 Task T5).
	MaxAmountPerTx int64
	// FeeQuoteTTL is how long a fee quote (docs/roadmap/archive/38 Task T2) stays
	// consumable after creation. <=0 falls back to feepolicy.DefaultQuoteTTL.
	FeeQuoteTTL time.Duration
	// PolicyCacheTTL bounds how stale an in-process policy_limits cache
	// entry can be (docs/roadmap/archive/17 Task T1) — configurable primarily so
	// scripts/business-e2e.sh's KYC journey (docs/roadmap/archive/39 Task T6) can
	// observe a tier upgrade's new limit apply quickly instead of waiting
	// out the 60s production default (a real deployment never needs this
	// tight, since a tier upgrade taking up to a minute to reflect
	// everywhere is an accepted tradeoff, same as fee_rules staleness).
	PolicyCacheTTL time.Duration
}

// FraudConfig contains fraud-service rule configuration.
type FraudConfig struct {
	ScreeningMode               string
	ScreeningAmountThreshold    int64
	ScreeningVelocityMaxPerHour int64
}

// AdminBFFConfig contains only BFF-owned session and downstream transport
// settings. Domain service URLs are added as thin-client configuration in T4.
type AdminBFFConfig struct {
	AuthServiceURL      string
	AuthAdminServiceURL string
	JWTSecret           string
	JWTIssuer           string
	LedgerServiceURL    string
	PayinServiceURL     string
	PayoutServiceURL    string
	FraudServiceURL     string
	GatewayServiceURL   string
	SecureCookie        bool
	SessionIdleTTL      time.Duration
	SessionAbsoluteTTL  time.Duration
	DownstreamTokenTTL  time.Duration
}

// AssuranceConfig controls the read-only cross-service reconciliation worker.
type AssuranceConfig struct {
	ConsistencyDelay time.Duration
	Interval         time.Duration
	PageSize         int
	RPCTimeout       time.Duration
	Concurrency      int
	AlertWebhookURL  string
}

type AppConfig struct {
	Name            string
	Env             string
	Port            string
	BaseURL         string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration

	// RateLimitRequests/RateLimitPer/RateLimitBurst configure the
	// per-IP(+path) rate limiter (cmd/auth-service/router.go,
	// internal/handler/router.go). Defaults (10 per minute) match this
	// repo's original hardcoded values exactly — production behavior is
	// unchanged unless these env vars are set. Needed as an override
	// because this repo's own test harness (scripts/lib.sh) legitimately
	// fires many requests per minute from ONE machine across many chaos/
	// business-e2e/admin-e2e scenarios sharing that machine's one IP —
	// traffic the port-keyed rate limiter used to (incorrectly) never
	// actually enforce (docs/roadmap/archive/49 TM-11). Now that it's fixed, the test
	// harness needs its own realistic ceiling rather than production's.
	RateLimitRequests int
	RateLimitPer      time.Duration
	RateLimitBurst    int

	// AllowedOrigins is an explicit CORS origin allowlist read from
	// CORS_ALLOWED_ORIGINS (comma-separated). Empty by default — this API
	// is bearer-token only (no cookies set by auth-service/gateway), so an
	// empty allowlist is the correct "API-only" default rather than a
	// wildcard (docs/roadmap/archive/49 TM-06). Only used outside production; in
	// production CORS is pinned to App.BaseURL regardless (see
	// authCORS/corsConfig).
	AllowedOrigins []string

	// InternalPort/InternalBindAddr configure the second HTTP listener that
	// serves transaction types unsafe for direct end-user use, plus
	// /metrics and admin tooling (docs/roadmap/archive/10 Task T1). Bound to
	// 127.0.0.1 by default — never expose this to an untrusted network.
	InternalPort     string
	InternalBindAddr string

	// TrustProxyHeaders enables honoring X-Forwarded-Proto for HSTS
	// decisions (docs/roadmap/archive/10 Task T6). Only enable behind a TLS-terminating
	// reverse proxy that overwrites/strips this header from client input —
	// otherwise a client can spoof it.
	TrustProxyHeaders bool
}

type PostgresConfig struct {
	Host            string
	Port            string
	User            string
	Password        string
	DB              string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration

	// StatementTimeout/LockTimeout/IdleInTxTimeout are set server-side per
	// session via the DSN's `options` parameter (docs/roadmap/archive/11 Task T5).
	// On a resource-constrained box, an unbounded query or a transaction
	// that "forgot" to commit/rollback can otherwise hold a row lock (or a
	// connection out of the pool) indefinitely — these turn that into a
	// bounded, loud failure instead of a silent pileup that eventually
	// exhausts MaxOpenConns for every caller, not just the stuck one.
	StatementTimeout time.Duration
	LockTimeout      time.Duration
	IdleInTxTimeout  time.Duration
}

type RedisConfig struct {
	// Enabled defaults to true — safe for existing/multi-replica
	// deployments. Set REDIS_ENABLED=false for a single small instance to
	// run rate limiting and the scheduler lock in-memory instead
	// (docs/roadmap/archive/12 Task T1).
	Enabled      bool
	Addr         string
	Password     string
	Username     string
	DB           int
	MaxRetries   int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
	MinIdleConns int
	PoolTimeout  time.Duration
}

type RabbitMQConfig struct {
	Host                 string
	Port                 int
	Username             string
	Password             string
	VHost                string
	TLS                  *tls.Config
	DefaultExchange      string
	ReconnectBaseDelay   time.Duration
	MaxReconnectAttempts int
	ChannelPoolSize      int           //new
	MaxConcurrentPublish int           // new
	DrainTimeout         time.Duration //new
	PublishTimeout       time.Duration
	DialTimeout          time.Duration //new
	AppID                string
}

type JWTConfig struct {
	Secret        string
	AccessExpiry  time.Duration
	RefreshExpiry time.Duration
	Issuer        string
}

type LoggerConfig struct {
	Level   string
	Format  string
	AppName string
	Env     string
}

// Pkg maps composition-root PostgreSQL configuration to the shared package.
func (c PostgresConfig) Pkg() database.Config {
	return database.Config{
		Host: c.Host, Port: c.Port, User: c.User, Password: c.Password,
		DB: c.DB, SSLMode: c.SSLMode, MaxOpenConns: c.MaxOpenConns,
		MaxIdleConns: c.MaxIdleConns, ConnMaxLifetime: c.ConnMaxLifetime,
		ConnMaxIdleTime: c.ConnMaxIdleTime, StatementTimeout: c.StatementTimeout,
		LockTimeout: c.LockTimeout, IdleInTxTimeout: c.IdleInTxTimeout,
	}
}

// Pkg maps composition-root Redis configuration to the shared package.
func (c RedisConfig) Pkg() cache.Config {
	return cache.Config{
		Enabled: c.Enabled, Addr: c.Addr, Password: c.Password, Username: c.Username,
		DB: c.DB, MaxRetries: c.MaxRetries, DialTimeout: c.DialTimeout,
		ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout, PoolSize: c.PoolSize,
		MinIdleConns: c.MinIdleConns, PoolTimeout: c.PoolTimeout,
	}
}

// Pkg maps composition-root logger configuration to the shared package.
func (c LoggerConfig) Pkg() logger.Config {
	return logger.Config{Level: c.Level, Format: c.Format, AppName: c.AppName, Env: c.Env}
}

// Broker maps composition-root RabbitMQ configuration to the shared package.
func (c RabbitMQConfig) Broker() messaging.BrokerConfig {
	return messaging.BrokerConfig{
		Host: c.Host, Port: c.Port, VHost: c.VHost, Username: c.Username,
		Password: c.Password, TLS: c.TLS, AppID: c.AppID,
		DefaultExchange: c.DefaultExchange, DialTimeout: c.DialTimeout,
		PublishTimeout: c.PublishTimeout, ReconnectBaseDelay: c.ReconnectBaseDelay,
		MaxReconnectAttempts: c.MaxReconnectAttempts, ChannelPoolSize: c.ChannelPoolSize,
		MaxConcurrentPublish: c.MaxConcurrentPublish, DrainTimeout: c.DrainTimeout,
	}
}

// WorkerConfig tunes the ledger module's background workers (outbox relay +
// integrity verifier). See docs/roadmap/archive/06-phase-1-workers.md.
type WorkerConfig struct {
	Enabled            bool
	OutboxPollInterval time.Duration
	OutboxBatchSize    int
	// AlertWebhookURL, if non-empty, receives a POST for every integrity
	// discrepancy the verifier finds (docs/roadmap/archive/12 Task T4). Empty = no
	// external alert, log+metric only (backward compatible default).
	AlertWebhookURL string
}

// Load reads configuration from environment variables.
// Returns an error if any required variable is missing or any value is invalid.
func Load() (*Config, error) {
	return load(true)
}

// LoadAuthService loads only dependencies owned by auth-service. RabbitMQ
// configuration is intentionally optional because auth neither publishes nor
// consumes messages.
func LoadAuthService() (*Config, error) {
	return load(false)
}

// LoadPayinService excludes RabbitMQ/Redis-only validation; payin owns only
// Postgres and a ledger gRPC client.
func LoadPayinService() (*Config, error) { return load(false) }

// LoadPayoutService excludes RabbitMQ validation; payout owns Postgres,
// Redis DB 0 for its resume lock, and a ledger gRPC client.
func LoadPayoutService() (*Config, error) { return load(false) }

// LoadFraudService validates RabbitMQ because fraud consumes ledger events.
func LoadFraudService() (*Config, error) { return load(true) }

// LoadAdminBFFService excludes RabbitMQ because the admin BFF is an
// HTTP-only aggregator with its own Postgres database (docs/roadmap/archive/47 K1-K3).
func LoadAdminBFFService() (*Config, error) { return load(false) }

// LoadAssuranceService excludes RabbitMQ/Redis because assurance owns only
// its database and uses read-only gRPC calls to domain services.
func LoadAssuranceService() (*Config, error) { return load(false) }

func load(requireRabbitMQ bool) (*Config, error) {
	env := os.Getenv("APP_ENV")

	switch env {
	case "production":
		_ = godotenv.Load(".env.production")
	case "staging":
		_ = godotenv.Load(".env.staging")
	default:
		_ = godotenv.Load(".env")
	}
	// docs/roadmap/archive/49 K7: overlays os.Getenv with Vault KV v2 values when
	// VAULT_ADDR/VAULT_TOKEN are set; a no-op wrapper otherwise, so every
	// existing env-only deployment (including CI/nightly, which never set
	// these) is byte-for-byte unaffected.
	getenv, err := vaultGetenv(os.Getenv)
	if err != nil {
		return nil, err
	}
	return loadFromEnvMode(getenv, requireRabbitMQ)
}

// loadFromEnv is the testable inner implementation that accepts a getter function.
func loadFromEnv(getenv func(string) string) (*Config, error) {
	return loadFromEnvMode(getenv, true)
}

func loadFromEnvMode(getenv func(string) string, requireRabbitMQ bool) (*Config, error) {
	var errs []string

	cfg := &Config{
		App: AppConfig{
			Name:              getWithDefault(getenv, "APP_NAME", "seev"),
			Env:               getWithDefault(getenv, "APP_ENV", "development"),
			Port:              getWithDefault(getenv, "APP_PORT", "8080"),
			BaseURL:           getWithDefault(getenv, "APP_BASE_URL", "http://localhost:8080"),
			ReadTimeout:       parseDuration(getenv("APP_READ_TIMEOUT"), 15*time.Second),
			WriteTimeout:      parseDuration(getenv("APP_WRITE_TIMEOUT"), 15*time.Second),
			IdleTimeout:       parseDuration(getenv("APP_IDLE_TIMEOUT"), 60*time.Second),
			ShutdownTimeout:   parseDuration(getenv("APP_SHUTDOWN_TIMEOUT"), 30*time.Second),
			InternalPort:      getWithDefault(getenv, "INTERNAL_APP_PORT", "8081"),
			InternalBindAddr:  getWithDefault(getenv, "INTERNAL_APP_BIND_ADDR", "127.0.0.1"),
			TrustProxyHeaders: parseBool(getenv("TRUST_PROXY_HEADERS"), false),
			AllowedOrigins:    parseCommaList(getenv("CORS_ALLOWED_ORIGINS")),
			RateLimitRequests: parseInt(getenv("RATE_LIMIT_REQUESTS"), 10),
			RateLimitPer:      parseDuration(getenv("RATE_LIMIT_PER"), time.Minute),
			RateLimitBurst:    parseInt(getenv("RATE_LIMIT_BURST"), 10),
		},
		Postgres: PostgresConfig{
			Host:     getWithDefault(getenv, "POSTGRES_HOST", "localhost"),
			Port:     getWithDefault(getenv, "POSTGRES_PORT", "5432"),
			User:     requireValue(getenv, "POSTGRES_USER", &errs),
			Password: requireValue(getenv, "POSTGRES_PASSWORD", &errs),
			DB:       requireValue(getenv, "POSTGRES_DB", &errs),
			SSLMode:  getWithDefault(getenv, "POSTGRES_SSL_MODE", "disable"),
			// Defaults sized for a small single instance (docs/roadmap/archive/11 Task
			// T5) — override via env for a bigger box. Rule of thumb:
			// max_open ~= (vCPU * 2) + effective_spindle_count; also account
			// for Postgres's own max_connections being shared with the
			// migrate tool, any admin psql session, etc.
			MaxOpenConns:     parseInt(getenv("POSTGRES_MAX_OPEN_CONNS"), 10),
			MaxIdleConns:     parseInt(getenv("POSTGRES_MAX_IDLE_CONNS"), 5),
			ConnMaxLifetime:  parseDuration(getenv("POSTGRES_CONN_MAX_LIFETIME"), 5*time.Minute),
			ConnMaxIdleTime:  parseDuration(getenv("POSTGRES_CONN_MAX_IDLE_TIME"), 5*time.Minute),
			StatementTimeout: parseDuration(getenv("POSTGRES_STATEMENT_TIMEOUT"), 5*time.Second),
			LockTimeout:      parseDuration(getenv("POSTGRES_LOCK_TIMEOUT"), 2*time.Second),
			IdleInTxTimeout:  parseDuration(getenv("POSTGRES_IDLE_IN_TX_TIMEOUT"), 10*time.Second),
		},
		Redis: RedisConfig{
			Enabled:      parseBool(getenv("REDIS_ENABLED"), true),
			Addr:         getWithDefault(getenv, "REDIS_ADDR", "localhost:6380"),
			Username:     getWithDefault(getenv, "REDIS_USERNAME", ""),
			Password:     getenv("REDIS_PASSWORD"),
			DB:           parseInt(getenv("REDIS_DB"), 0),
			MaxRetries:   parseInt(getenv("REDIS_MAX_RETRIES"), 3),
			DialTimeout:  parseDuration(getenv("REDIS_DIAL_TIMEOUT"), 5*time.Second),
			ReadTimeout:  parseDuration(getenv("REDIS_READ_TIMEOUT"), 3*time.Second),
			WriteTimeout: parseDuration(getenv("REDIS_WRITE_TIMEOUT"), 3*time.Second),
			PoolSize:     parseInt(getenv("REDIS_POOL_SIZE"), 10),
			MinIdleConns: parseInt(getenv("REDIS_MIN_IDLE_CONNS"), 5),
			PoolTimeout:  parseDuration(getenv("REDIS_POOL_TIMEOUT"), 4*time.Second),
		},
		RabbitMQ: RabbitMQConfig{
			Host:     optionalRequired(getenv, "RABBITMQ_HOST", requireRabbitMQ, &errs),
			Port:     parseInt(getenv("RABBITMQ_PORT"), 5672),
			Username: optionalRequired(getenv, "RABBITMQ_USERNAME", requireRabbitMQ, &errs),
			Password: optionalRequired(getenv, "RABBITMQ_PASSWORD", requireRabbitMQ, &errs),
			VHost:    getWithDefault(getenv, "RABBITMQ_VHOST", "/"),

			DefaultExchange: optionalRequired(getenv, "RABBITMQ_EXCHANGE", requireRabbitMQ, &errs),

			ReconnectBaseDelay:   parseDuration(getenv("RABBITMQ_RECONNECT_DELAY"), 5*time.Second),
			MaxReconnectAttempts: parseInt(getenv("RABBITMQ_MAX_RECONNECT_ATTEMPTS"), 10),
			ChannelPoolSize:      parseInt(getenv("RABBITMQ_CHANNEL_POOL_SIZE"), 16),
			MaxConcurrentPublish: parseInt(getenv("RABBITMQ_MAX_CONCURRENT_PUBLISH"), 64),
			DrainTimeout:         parseDuration(getenv("RABBITMQ_DRAIN_TIMEOUT"), 30*time.Second),
			DialTimeout:          parseDuration(getenv("RABBITMQ_DIAL_TIMEOUT"), 10*time.Second),
			PublishTimeout:       parseDuration(getenv("RABBITMQ_PUBLISH_TIMEOUT"), 5*time.Second),
			AppID:                getWithDefault(getenv, "RABBITMQ_APP_ID", "app"),

			TLS: parseTLSConfig(
				getenv("RABBITMQ_TLS"),
				getenv("RABBITMQ_HOST"),
			),
		},
		JWT: JWTConfig{
			Secret: requireValue(getenv, "JWT_SECRET", &errs),
			// Short access-token TTL bounds stale KYC claims after a
			// limits-first downgrade. Hard policy_limits remain the source of
			// truth while the token catches up (Plan 46 T2).
			AccessExpiry:  parseDuration(getenv("JWT_ACCESS_EXPIRY"), 5*time.Minute),
			RefreshExpiry: parseDuration(getenv("JWT_REFRESH_EXPIRY"), 7*24*time.Hour),
			Issuer:        getenv("JWT_ISSUER"),
		},
		Logger: LoggerConfig{
			Level:   getWithDefault(getenv, "LOG_LEVEL", "info"),
			Format:  getWithDefault(getenv, "LOG_FORMAT", "json"),
			AppName: getWithDefault(getenv, "APP_NAME", "seev"),
			Env:     getWithDefault(getenv, "APP_ENV", "development"),
		},
		Worker: WorkerConfig{
			Enabled:            parseBool(getenv("WORKER_ENABLED"), true),
			OutboxPollInterval: parseDuration(getenv("OUTBOX_POLL_INTERVAL"), time.Second),
			OutboxBatchSize:    parseInt(getenv("OUTBOX_BATCH_SIZE"), 100),
			AlertWebhookURL:    getenv("ALERT_WEBHOOK_URL"),
		},
		Ledger: LedgerConfig{
			MaxAmountPerTx: parseInt64(getenv("LEDGER_MAX_AMOUNT_PER_TX"), 1_000_000_000),
			FeeQuoteTTL:    parseDuration(getenv("FEE_QUOTE_TTL"), 10*time.Minute),
			PolicyCacheTTL: parseDuration(getenv("POLICY_CACHE_TTL"), 60*time.Second),
		},
		Fraud: FraudConfig{
			ScreeningMode:               getWithDefault(getenv, "SCREENING_MODE", "off"),
			ScreeningAmountThreshold:    parseInt64(getenv("SCREENING_AMOUNT_THRESHOLD"), 0),
			ScreeningVelocityMaxPerHour: parseInt64(getenv("SCREENING_VELOCITY_MAX_PER_HOUR"), 0),
		},
		Tracing: TracingConfig{
			OTLPEndpoint: getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
			SampleRatio:  parseFloat(getenv("OTEL_TRACES_SAMPLER_ARG"), 0.10),
			Insecure:     parseBool(getenv("OTEL_EXPORTER_OTLP_INSECURE"), true),
		},
		Vendor: VendorConfig{
			MockvendorEnabled:  parseBool(getenv("VENDOR_MOCKVENDOR_ENABLED"), false),
			MockvendorSecret:   getenv("VENDOR_MOCKVENDOR_SECRET"),
			TopupIntentTTL:     parseDuration(getenv("TOPUP_INTENT_TTL"), 24*time.Hour),
			Mockvendor2Enabled: parseBool(getenv("MOCKVENDOR2_ENABLED"), false),
			Mockvendor2Secret:  getenv("MOCKVENDOR2_SECRET"),
		},
		Breaker: BreakerConfig{
			FailureThreshold: parseInt(getenv("BREAKER_FAILURE_THRESHOLD"), 5),
			Cooldown:         parseDuration(getenv("BREAKER_COOLDOWN"), 30*time.Second),
			Distributed:      parseBool(getenv("BREAKER_DISTRIBUTED"), false),
		},
		Auth: AuthConfig{
			DefaultCurrency:          getWithDefault(getenv, "DEFAULT_CURRENCY", "IDR"),
			BootstrapAdminEmail:      getenv("AUTH_BOOTSTRAP_ADMIN_EMAIL"),
			BootstrapAdminPassword:   getenv("AUTH_BOOTSTRAP_ADMIN_PASSWORD"),
			BootstrapMakerEmail:      getenv("AUTH_BOOTSTRAP_MAKER_EMAIL"),
			BootstrapMakerPassword:   getenv("AUTH_BOOTSTRAP_MAKER_PASSWORD"),
			BootstrapCheckerEmail:    getenv("AUTH_BOOTSTRAP_CHECKER_EMAIL"),
			BootstrapCheckerPassword: getenv("AUTH_BOOTSTRAP_CHECKER_PASSWORD"),
			KYCProviderURL:           getenv("KYC_PROVIDER_URL"),
			KYCProviderToken:         getenv("KYC_PROVIDER_TOKEN"),
			KYCProviderName:          getWithDefault(getenv, "KYC_PROVIDER_NAME", "http-kyc"),
		},
		AdminBFF: AdminBFFConfig{
			AuthServiceURL:      getWithDefault(getenv, "AUTH_SERVICE_URL", "http://localhost:8082"),
			AuthAdminServiceURL: getWithDefault(getenv, "AUTH_ADMIN_SERVICE_URL", "http://localhost:8083"),
			JWTSecret:           getenv("JWT_SECRET"),
			JWTIssuer:           getenv("JWT_ISSUER"),
			LedgerServiceURL:    getWithDefault(getenv, "LEDGER_SERVICE_URL", "http://localhost:8091"),
			PayinServiceURL:     getWithDefault(getenv, "PAYIN_SERVICE_URL", "http://localhost:8092"),
			PayoutServiceURL:    getWithDefault(getenv, "PAYOUT_SERVICE_URL", "http://localhost:8093"),
			FraudServiceURL:     getWithDefault(getenv, "FRAUD_SERVICE_URL", "http://localhost:8094"),
			GatewayServiceURL:   getWithDefault(getenv, "GATEWAY_SERVICE_URL", "http://localhost:8081"),
			SecureCookie:        parseBool(getenv("ADMIN_BFF_SECURE_COOKIE"), true),
			SessionIdleTTL:      parseDuration(getenv("ADMIN_BFF_SESSION_IDLE_TTL"), 30*time.Minute),
			SessionAbsoluteTTL:  parseDuration(getenv("ADMIN_BFF_SESSION_ABSOLUTE_TTL"), 8*time.Hour),
			DownstreamTokenTTL:  parseDuration(getenv("ADMIN_BFF_DOWNSTREAM_TOKEN_TTL"), time.Minute),
		},
		Assurance: AssuranceConfig{
			ConsistencyDelay: parseDuration(getenv("ASSURANCE_CONSISTENCY_DELAY"), 2*time.Minute),
			Interval:         parseDuration(getenv("ASSURANCE_INTERVAL"), time.Minute),
			PageSize:         parseInt(getenv("ASSURANCE_PAGE_SIZE"), 500),
			RPCTimeout:       parseDuration(getenv("ASSURANCE_RPC_TIMEOUT"), 3*time.Second),
			Concurrency:      parseInt(getenv("ASSURANCE_CONCURRENCY"), 2),
			AlertWebhookURL:  getenv("ALERT_WEBHOOK_URL"),
		},
		GRPCPort:          getWithDefault(getenv, "GRPC_PORT", "9091"),
		InternalGRPCToken: getenv("INTERNAL_GRPC_TOKEN"),
		LedgerGRPCAddr:    getWithDefault(getenv, "LEDGER_GRPC_ADDR", "localhost:9091"),
		PayinGRPCAddr:     getWithDefault(getenv, "PAYIN_GRPC_ADDR", "localhost:9092"),
		PayoutGRPCAddr:    getWithDefault(getenv, "PAYOUT_GRPC_ADDR", "localhost:9093"),
		FraudGRPCAddr:     getenv("FRAUD_GRPC_ADDR"),
		LedgerUserAPIURL:  getWithDefault(getenv, "LEDGER_USER_API_URL", "https://localhost:8090"),
		TLSCertDir:        getWithDefault(getenv, "TLS_CERT_DIR", "deploy/certs"),
	}

	if err := validate(cfg, requireRabbitMQ, &errs); err != nil {
		return nil, err
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return cfg, nil
}

func optionalRequired(getenv func(string) string, key string, required bool, errs *[]string) string {
	if required {
		return requireValue(getenv, key, errs)
	}
	return getenv(key)
}

func parseTLSConfig(s string, serverName string) *tls.Config {
	if s == "" {
		return nil
	}

	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	}

	for _, pair := range strings.Split(s, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			panic(fmt.Sprintf("invalid TLS option: %s", pair))
		}

		key := strings.ToLower(strings.TrimSpace(kv[0]))
		val := strings.ToLower(strings.TrimSpace(kv[1]))

		switch key {

		case "min_version":
			v, err := parseTLSVersion(val)
			if err != nil {
				panic(err)
			}
			cfg.MinVersion = v

		case "max_version":
			v, err := parseTLSVersion(val)
			if err != nil {
				panic(err)
			}
			cfg.MaxVersion = v

		default:
			panic(fmt.Sprintf("unknown TLS option: %s", key))
		}
	}

	return cfg
}

func parseTLSVersion(v string) (uint16, error) {
	switch v {
	case "tls1.0":
		return tls.VersionTLS10, nil
	case "tls1.1":
		return tls.VersionTLS11, nil
	case "tls1.2":
		return tls.VersionTLS12, nil
	case "tls1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("invalid tls version: %s", v)
	}
}

func validate(cfg *Config, requireRabbitMQ bool, errs *[]string) error {
	if requireRabbitMQ {
		if err := cfg.RabbitMQ.Validate(); err != nil {
			*errs = append(*errs, err.Error())
		}
		if cfg.IsProduction() && cfg.RabbitMQ.TLS == nil {
			*errs = append(*errs, "rabbitmq TLS must be enabled in production")
		}
	}
	validEnvs := map[string]bool{"development": true, "staging": true, "production": true}
	if !validEnvs[cfg.App.Env] {
		*errs = append(*errs, "APP_ENV must be one of: development, staging, production")
	}

	validSSL := map[string]bool{"disable": true, "require": true, "verify-full": true}
	if !validSSL[cfg.Postgres.SSLMode] {
		*errs = append(*errs, "POSTGRES_SSL_MODE must be one of: disable, require, verify-full")
	}

	if (cfg.Auth.BootstrapAdminEmail == "") != (cfg.Auth.BootstrapAdminPassword == "") {
		*errs = append(*errs, "AUTH_BOOTSTRAP_ADMIN_EMAIL and AUTH_BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	if (cfg.Auth.BootstrapMakerEmail == "") != (cfg.Auth.BootstrapMakerPassword == "") {
		*errs = append(*errs, "AUTH_BOOTSTRAP_MAKER_EMAIL and AUTH_BOOTSTRAP_MAKER_PASSWORD must be set together")
	}
	if (cfg.Auth.BootstrapCheckerEmail == "") != (cfg.Auth.BootstrapCheckerPassword == "") {
		*errs = append(*errs, "AUTH_BOOTSTRAP_CHECKER_EMAIL and AUTH_BOOTSTRAP_CHECKER_PASSWORD must be set together")
	}

	if cfg.App.Env == "production" && cfg.Postgres.SSLMode == "disable" {
		*errs = append(*errs, "POSTGRES_SSL_MODE must not be 'disable' in production")
	}

	if len(cfg.JWT.Secret) < 32 {
		*errs = append(*errs, "JWT_SECRET must be at least 32 characters long")
	}

	// docs/roadmap/archive/49 TM-07: issuer validation used to be skippable by simply
	// leaving JWT_ISSUER unset, in every environment — a token signed with
	// the right secret but any issuer (or none) was accepted. Fail closed
	// at boot instead, mirroring JWT_SECRET above and the K5 internal-token
	// fail-closed pattern.
	if cfg.JWT.Issuer == "" {
		*errs = append(*errs, "JWT_ISSUER must be set — issuer validation cannot be skipped")
	}

	if cfg.Vendor.MockvendorEnabled && cfg.Vendor.MockvendorSecret == "" {
		*errs = append(*errs, "VENDOR_MOCKVENDOR_SECRET must be set when VENDOR_MOCKVENDOR_ENABLED=true — an empty HMAC secret would accept any signature")
	}

	if cfg.Vendor.Mockvendor2Enabled && cfg.Vendor.Mockvendor2Secret == "" {
		*errs = append(*errs, "MOCKVENDOR2_SECRET must be set when MOCKVENDOR2_ENABLED=true — an empty HMAC secret would accept any signature")
	}
	if cfg.Assurance.PageSize <= 0 || cfg.Assurance.PageSize > 500 {
		*errs = append(*errs, "ASSURANCE_PAGE_SIZE must be between 1 and 500")
	}
	if cfg.Assurance.Concurrency <= 0 || cfg.Assurance.Concurrency > 2 {
		*errs = append(*errs, "ASSURANCE_CONCURRENCY must be between 1 and 2")
	}
	if cfg.Assurance.ConsistencyDelay < 0 || cfg.Assurance.RPCTimeout <= 0 || cfg.Assurance.Interval <= 0 {
		*errs = append(*errs, "ASSURANCE interval, consistency delay, and RPC timeout are invalid")
	}

	if len(*errs) > 0 {
		return errors.New(strings.Join(*errs, "\n  - "))
	}
	return nil
}

// DSN returns a libpq-style PostgreSQL connection string. When any of
// StatementTimeout/LockTimeout/IdleInTxTimeout are set, they're passed as
// session-level GUCs via `options` (docs/roadmap/archive/11 Task T5) — every value
// here is our own validated duration config, not external input, so no
// escaping beyond libpq's own single-quoting is needed.
func (p *PostgresConfig) DSN() string {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		p.Host, p.Port, p.User, p.Password, p.DB, p.SSLMode,
	)
	if opts := p.sessionOptions(); opts != "" {
		dsn += fmt.Sprintf(" options='%s'", opts)
	}
	return dsn
}

// sessionOptions builds the `-c name=value` GUC list for DSN's `options`
// parameter, skipping any timeout that isn't configured (<= 0).
func (p *PostgresConfig) sessionOptions() string {
	var parts []string
	if p.StatementTimeout > 0 {
		parts = append(parts, fmt.Sprintf("-c statement_timeout=%d", p.StatementTimeout.Milliseconds()))
	}
	if p.LockTimeout > 0 {
		parts = append(parts, fmt.Sprintf("-c lock_timeout=%d", p.LockTimeout.Milliseconds()))
	}
	if p.IdleInTxTimeout > 0 {
		parts = append(parts, fmt.Sprintf("-c idle_in_transaction_session_timeout=%d", p.IdleInTxTimeout.Milliseconds()))
	}
	return strings.Join(parts, " ")
}

func (c *RabbitMQConfig) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("invalid rabbitmq port")
	}
	if c.VHost == "" {
		c.VHost = "/"
	}
	if c.ReconnectBaseDelay == 0 {
		c.ReconnectBaseDelay = time.Second
	}
	if c.MaxReconnectAttempts == 0 {
		c.MaxReconnectAttempts = 10
	}
	if c.PublishTimeout < time.Second {
		return fmt.Errorf("publish timeout too small")
	}
	if c.AppID == "" {
		c.AppID = "messaging"
	}
	return nil
}

func (c *RabbitMQConfig) Url() string {
	scheme := "amqp"
	if c.TLS != nil {
		scheme = "amqps"
	}

	vhost := url.PathEscape(c.VHost)

	return fmt.Sprintf("%s://%s:%s@%s:%d/%s",
		scheme,
		c.Username,
		c.Password,
		c.Host,
		c.Port,
		vhost,
	)
}
func (c *RabbitMQConfig) SafeAddr() string {
	return fmt.Sprintf("%s:%d%s", c.Host, c.Port, c.VHost)
}

// IsProduction reports whether the app is running in production mode.
func (c *Config) IsProduction() bool {
	return c.App.Env == "production"
}

// Warnings returns non-fatal configuration concerns the caller should log at
// startup (docs/roadmap/archive/10 Task T1) — unlike validate(), these don't block
// startup because there are legitimate deployments that need them (e.g.
// container networks that require binding 0.0.0.0 with a security group
// providing the actual isolation).
func (c *Config) Warnings() []string {
	var warnings []string
	if c.IsProduction() && c.App.InternalBindAddr == "0.0.0.0" {
		warnings = append(warnings, "INTERNAL_APP_BIND_ADDR=0.0.0.0 in production — the internal ledger router (money_in, refund, withdraw settlement, /metrics, etc.) will be reachable from any network interface; ensure a firewall/security-group provides isolation instead")
	}
	// docs/roadmap/archive/49 TM-07: JWT_ISSUER emptiness used to be a soft warning
	// here. It's now a hard validate() failure (see validate()), so by the
	// time Warnings() runs post-Load() it can never be empty — no warning
	// left to give.
	return warnings
}

// ─── Private helpers ──────────────────────────────────────────────────────────

func getWithDefault(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseCommaList splits a comma-separated env value into a trimmed,
// non-empty slice. Returns nil (not an empty slice) when unset, so callers
// can distinguish "not configured" from "configured empty" if that ever
// matters (docs/roadmap/archive/49 TM-06 — CORS_ALLOWED_ORIGINS uses this).
func parseCommaList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func requireValue(getenv func(string) string, key string, errs *[]string) string {
	v := getenv(key)
	if v == "" {
		*errs = append(*errs, fmt.Sprintf("%s is required but not set", key))
	}
	return v
}

func parseInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func parseInt64(s string, fallback int64) int64 {
	if s == "" {
		return fallback
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func parseFloat(s string, fallback float64) float64 {
	if s == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	return f
}

func parseDuration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

func parseBool(s string, fallback bool) bool {
	if s == "" {
		return fallback
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return fallback
	}
	return b
}
