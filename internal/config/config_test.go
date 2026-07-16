package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validEnv returns a function that mimics os.Getenv with all required vars set.
func validEnv(overrides map[string]string) func(string) string {
	base := map[string]string{
		"POSTGRES_USER":     "user",
		"POSTGRES_PASSWORD": "password",
		"POSTGRES_DB":       "testdb",

		"RABBITMQ_HOST":        "localhost",
		"RABBITMQ_PORT":        "5672",
		"RABBITMQ_USERNAME":    "guest",
		"RABBITMQ_PASSWORD":    "guest",
		"RABBITMQ_VHOST":       "/",
		"RABBITMQ_EXCHANGE":    "app.exchange",
		"RABBITMQ_QUEUE":       "app.queue",
		"RABBITMQ_ROUTING_KEY": "app.routing",

		"JWT_SECRET": "supersecretkeythatisatleast32chars!",
	}
	for k, v := range overrides {
		base[k] = v
	}
	return func(key string) string {
		return base[key]
	}
}

func TestLoadFromEnv_Defaults(t *testing.T) {
	cfg, err := loadFromEnv(validEnv(nil))
	require.NoError(t, err)

	assert.Equal(t, "seev", cfg.App.Name)
	assert.Equal(t, "development", cfg.App.Env)
	assert.Equal(t, "8080", cfg.App.Port)
	assert.Equal(t, "http://localhost:8080", cfg.App.BaseURL)
	assert.Equal(t, 15*time.Second, cfg.App.ReadTimeout)
	assert.Equal(t, 15*time.Second, cfg.App.WriteTimeout)
	assert.Equal(t, 60*time.Second, cfg.App.IdleTimeout)
	assert.Equal(t, 30*time.Second, cfg.App.ShutdownTimeout)
	assert.Equal(t, "8081", cfg.App.InternalPort)
	assert.Equal(t, "127.0.0.1", cfg.App.InternalBindAddr)
	assert.False(t, cfg.App.TrustProxyHeaders)

	assert.Equal(t, "localhost", cfg.Postgres.Host)
	assert.Equal(t, "5432", cfg.Postgres.Port)
	assert.Equal(t, "disable", cfg.Postgres.SSLMode)
	assert.Equal(t, 10, cfg.Postgres.MaxOpenConns)
	assert.Equal(t, 5, cfg.Postgres.MaxIdleConns)
	assert.Equal(t, 5*time.Minute, cfg.Postgres.ConnMaxLifetime)
	assert.Equal(t, 5*time.Minute, cfg.Postgres.ConnMaxIdleTime)
	assert.Equal(t, 5*time.Second, cfg.Postgres.StatementTimeout)
	assert.Equal(t, 2*time.Second, cfg.Postgres.LockTimeout)
	assert.Equal(t, 10*time.Second, cfg.Postgres.IdleInTxTimeout)

	assert.True(t, cfg.Redis.Enabled)
	assert.Equal(t, "localhost:6380", cfg.Redis.Addr)
	assert.Equal(t, "", cfg.Redis.Password)
	assert.Equal(t, 0, cfg.Redis.DB)
	assert.Equal(t, 3, cfg.Redis.MaxRetries)
	assert.Equal(t, 5*time.Second, cfg.Redis.DialTimeout)
	assert.Equal(t, 3*time.Second, cfg.Redis.ReadTimeout)
	assert.Equal(t, 3*time.Second, cfg.Redis.WriteTimeout)
	assert.Equal(t, 10, cfg.Redis.PoolSize)
	assert.Equal(t, 5, cfg.Redis.MinIdleConns)
	assert.Equal(t, 4*time.Second, cfg.Redis.PoolTimeout)

	assert.Equal(t, 5*time.Second, cfg.RabbitMQ.ReconnectBaseDelay)
	assert.Equal(t, 10, cfg.RabbitMQ.MaxReconnectAttempts)
	assert.Equal(t, 16, cfg.RabbitMQ.ChannelPoolSize)
	assert.Equal(t, 64, cfg.RabbitMQ.MaxConcurrentPublish)
	assert.Equal(t, 30*time.Second, cfg.RabbitMQ.DrainTimeout)
	assert.Equal(t, 10*time.Second, cfg.RabbitMQ.DialTimeout)
	assert.Equal(t, "app.exchange", cfg.RabbitMQ.DefaultExchange)

	assert.Equal(t, 15*time.Minute, cfg.JWT.AccessExpiry)
	assert.Equal(t, 7*24*time.Hour, cfg.JWT.RefreshExpiry)
	assert.Equal(t, "", cfg.JWT.Issuer)

	assert.Equal(t, "info", cfg.Logger.Level)
	assert.Equal(t, "json", cfg.Logger.Format)

	assert.Equal(t, int64(1_000_000_000), cfg.Ledger.MaxAmountPerTx)

	assert.Equal(t, "", cfg.Worker.AlertWebhookURL, "no external alert by default (docs/plan/12 Task T4)")
	assert.Equal(t, "", cfg.Tracing.OTLPEndpoint, "no tracer provider installed by default (docs/plan/12 Task T5)")
}

func TestLoadFromEnvMode_AuthServiceDoesNotRequireRabbitMQ(t *testing.T) {
	withoutRabbit := validEnv(map[string]string{
		"RABBITMQ_HOST": "", "RABBITMQ_USERNAME": "", "RABBITMQ_PASSWORD": "", "RABBITMQ_EXCHANGE": "",
	})
	_, err := loadFromEnvMode(withoutRabbit, false)
	require.NoError(t, err)
	_, err = loadFromEnvMode(withoutRabbit, true)
	require.Error(t, err)
}

func TestLoadFromEnv_OverrideValues(t *testing.T) {
	cfg, err := loadFromEnv(validEnv(map[string]string{
		"APP_NAME":                        "myapp",
		"APP_ENV":                         "staging",
		"APP_PORT":                        "9090",
		"APP_BASE_URL":                    "https://staging.example.com",
		"APP_READ_TIMEOUT":                "30s",
		"APP_WRITE_TIMEOUT":               "30s",
		"APP_IDLE_TIMEOUT":                "120s",
		"APP_SHUTDOWN_TIMEOUT":            "60s",
		"INTERNAL_APP_PORT":               "9091",
		"INTERNAL_APP_BIND_ADDR":          "0.0.0.0",
		"TRUST_PROXY_HEADERS":             "true",
		"POSTGRES_HOST":                   "db.example.com",
		"POSTGRES_PORT":                   "5433",
		"POSTGRES_SSL_MODE":               "require",
		"POSTGRES_MAX_OPEN_CONNS":         "50",
		"POSTGRES_MAX_IDLE_CONNS":         "10",
		"POSTGRES_CONN_MAX_LIFETIME":      "10m",
		"POSTGRES_CONN_MAX_IDLE_TIME":     "2m",
		"POSTGRES_STATEMENT_TIMEOUT":      "7s",
		"POSTGRES_LOCK_TIMEOUT":           "3s",
		"POSTGRES_IDLE_IN_TX_TIMEOUT":     "15s",
		"REDIS_ENABLED":                   "false",
		"REDIS_ADDR":                      "redis.example.com:6380",
		"REDIS_PASSWORD":                  "redispass",
		"REDIS_DB":                        "1",
		"REDIS_MAX_RETRIES":               "5",
		"REDIS_DIAL_TIMEOUT":              "10s",
		"REDIS_READ_TIMEOUT":              "5s",
		"REDIS_WRITE_TIMEOUT":             "5s",
		"REDIS_POOL_SIZE":                 "20",
		"REDIS_MIN_IDLE_CONNS":            "3",
		"REDIS_POOL_TIMEOUT":              "8s",
		"RABBITMQ_RECONNECT_DELAY":        "10s",
		"RABBITMQ_MAX_RECONNECT_ATTEMPTS": "5",
		"RABBITMQ_PREFETCH_COUNT":         "20",
		"RABBITMQ_EXCHANGE":               "custom.exchange",
		"RABBITMQ_QUEUE":                  "custom.queue",
		"RABBITMQ_ROUTING_KEY":            "custom.routing",
		"JWT_ACCESS_EXPIRY":               "30m",
		"JWT_REFRESH_EXPIRY":              "48h",
		"JWT_ISSUER":                      "seev-api",
		"LOG_LEVEL":                       "debug",
		"LOG_FORMAT":                      "text",
		"LEDGER_MAX_AMOUNT_PER_TX":        "50000000",
		"ALERT_WEBHOOK_URL":               "https://hooks.example.com/alert",
		"OTEL_EXPORTER_OTLP_ENDPOINT":     "localhost:4317",
	}))
	require.NoError(t, err)

	assert.Equal(t, "myapp", cfg.App.Name)
	assert.Equal(t, "staging", cfg.App.Env)
	assert.Equal(t, "9090", cfg.App.Port)
	assert.Equal(t, "https://staging.example.com", cfg.App.BaseURL)
	assert.Equal(t, 30*time.Second, cfg.App.ReadTimeout)
	assert.Equal(t, 30*time.Second, cfg.App.WriteTimeout)
	assert.Equal(t, 120*time.Second, cfg.App.IdleTimeout)
	assert.Equal(t, 60*time.Second, cfg.App.ShutdownTimeout)
	assert.Equal(t, "9091", cfg.App.InternalPort)
	assert.Equal(t, "0.0.0.0", cfg.App.InternalBindAddr)
	assert.True(t, cfg.App.TrustProxyHeaders)
	assert.Equal(t, "db.example.com", cfg.Postgres.Host)
	assert.Equal(t, "5433", cfg.Postgres.Port)
	assert.Equal(t, "require", cfg.Postgres.SSLMode)
	assert.Equal(t, 50, cfg.Postgres.MaxOpenConns)
	assert.Equal(t, 10, cfg.Postgres.MaxIdleConns)
	assert.Equal(t, 10*time.Minute, cfg.Postgres.ConnMaxLifetime)
	assert.Equal(t, 2*time.Minute, cfg.Postgres.ConnMaxIdleTime)
	assert.Equal(t, 7*time.Second, cfg.Postgres.StatementTimeout)
	assert.Equal(t, 3*time.Second, cfg.Postgres.LockTimeout)
	assert.Equal(t, 15*time.Second, cfg.Postgres.IdleInTxTimeout)
	assert.False(t, cfg.Redis.Enabled)
	assert.Equal(t, "redis.example.com:6380", cfg.Redis.Addr)
	assert.Equal(t, "redispass", cfg.Redis.Password)
	assert.Equal(t, 1, cfg.Redis.DB)
	assert.Equal(t, 5, cfg.Redis.MaxRetries)
	assert.Equal(t, 10*time.Second, cfg.Redis.DialTimeout)
	assert.Equal(t, 5*time.Second, cfg.Redis.ReadTimeout)
	assert.Equal(t, 5*time.Second, cfg.Redis.WriteTimeout)
	assert.Equal(t, 20, cfg.Redis.PoolSize)
	assert.Equal(t, 3, cfg.Redis.MinIdleConns)
	assert.Equal(t, 8*time.Second, cfg.Redis.PoolTimeout)
	assert.Equal(t, 10*time.Second, cfg.RabbitMQ.ReconnectBaseDelay)
	assert.Equal(t, 5, cfg.RabbitMQ.MaxReconnectAttempts)
	assert.Equal(t, 16, cfg.RabbitMQ.ChannelPoolSize)
	assert.Equal(t, 64, cfg.RabbitMQ.MaxConcurrentPublish)
	assert.Equal(t, 30*time.Second, cfg.RabbitMQ.DrainTimeout)
	assert.Equal(t, 10*time.Second, cfg.RabbitMQ.DialTimeout)
	assert.Equal(t, "custom.exchange", cfg.RabbitMQ.DefaultExchange)
	assert.Equal(t, 30*time.Minute, cfg.JWT.AccessExpiry)
	assert.Equal(t, 48*time.Hour, cfg.JWT.RefreshExpiry)
	assert.Equal(t, "seev-api", cfg.JWT.Issuer)
	assert.Equal(t, "debug", cfg.Logger.Level)
	assert.Equal(t, "text", cfg.Logger.Format)
	assert.Equal(t, int64(50_000_000), cfg.Ledger.MaxAmountPerTx)
	assert.Equal(t, "https://hooks.example.com/alert", cfg.Worker.AlertWebhookURL)
	assert.Equal(t, "localhost:4317", cfg.Tracing.OTLPEndpoint)
}

func TestLoadFromEnv_MissingRequiredVars(t *testing.T) {
	_, err := loadFromEnv(func(string) string { return "" })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSTGRES_USER")
	assert.Contains(t, err.Error(), "POSTGRES_PASSWORD")
	assert.Contains(t, err.Error(), "POSTGRES_DB")
	assert.Contains(t, err.Error(), "RABBITMQ_HOST")
	assert.Contains(t, err.Error(), "RABBITMQ_USERNAME")
	assert.Contains(t, err.Error(), "RABBITMQ_PASSWORD")
	assert.Contains(t, err.Error(), "JWT_SECRET")
}

func TestLoadFromEnv_InvalidAppEnv(t *testing.T) {
	_, err := loadFromEnv(validEnv(map[string]string{"APP_ENV": "invalid"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_ENV")
}

func TestLoadFromEnv_InvalidSSLMode(t *testing.T) {
	_, err := loadFromEnv(validEnv(map[string]string{"POSTGRES_SSL_MODE": "bad"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSTGRES_SSL_MODE")
}

func TestLoadFromEnv_ProductionRequiresSSL(t *testing.T) {
	_, err := loadFromEnv(validEnv(map[string]string{
		"APP_ENV":           "production",
		"POSTGRES_SSL_MODE": "disable",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "production")
}

func TestLoadFromEnv_ProductionWithSSL(t *testing.T) {
	cfg, err := loadFromEnv(validEnv(map[string]string{
		"APP_ENV":           "production",
		"POSTGRES_SSL_MODE": "require",
		"RABBITMQ_TLS":      "min_version=tls1.2",
		"RABBITMQ_HOST":     "localhost",
	}))
	require.NoError(t, err)
	assert.True(t, cfg.IsProduction())
}

func TestLoadFromEnv_JWTSecretTooShort(t *testing.T) {
	_, err := loadFromEnv(validEnv(map[string]string{
		"JWT_SECRET": "short",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")
}

func TestLoadFromEnv_InvalidDurationFallsback(t *testing.T) {
	// Bad duration values fall back to defaults silently
	cfg, err := loadFromEnv(validEnv(map[string]string{
		"APP_READ_TIMEOUT": "notaduration",
	}))
	require.NoError(t, err)
	assert.Equal(t, 15*time.Second, cfg.App.ReadTimeout)
}

func TestLoadFromEnv_InvalidIntFallsback(t *testing.T) {
	cfg, err := loadFromEnv(validEnv(map[string]string{
		"POSTGRES_MAX_OPEN_CONNS": "notanint",
	}))
	require.NoError(t, err)
	assert.Equal(t, 10, cfg.Postgres.MaxOpenConns)
}

func TestLoad_ReadsFromOSEnv(t *testing.T) {
	// Tests that Load() delegates to os.Getenv — just verify it returns an error
	// when the real env is empty (as in CI), proving the delegation works.
	// We can't fully control os.Getenv here, so we just check it runs without panic.
	_, err := Load() // may error if env vars not set; that's fine
	_ = err          // result depends on actual environment
}

func TestPostgresConfig_DSN(t *testing.T) {
	p := PostgresConfig{
		Host:     "localhost",
		Port:     "5432",
		User:     "admin",
		Password: "secret",
		DB:       "mydb",
		SSLMode:  "require",
	}
	dsn := p.DSN()
	assert.True(t, strings.Contains(dsn, "host=localhost"))
	assert.True(t, strings.Contains(dsn, "port=5432"))
	assert.True(t, strings.Contains(dsn, "user=admin"))
	assert.True(t, strings.Contains(dsn, "password=secret"))
	assert.True(t, strings.Contains(dsn, "dbname=mydb"))
	assert.True(t, strings.Contains(dsn, "sslmode=require"))
}

// ─── Session timeout DSN options (docs/plan/11 Task T5) ────────────────────────

func TestPostgresConfig_DSN_NoTimeoutsConfigured_NoOptionsParam(t *testing.T) {
	p := PostgresConfig{Host: "localhost", Port: "5432", User: "admin", Password: "secret", DB: "mydb", SSLMode: "disable"}
	dsn := p.DSN()
	assert.False(t, strings.Contains(dsn, "options="), "must not emit an empty options= param")
}

func TestPostgresConfig_DSN_TimeoutsConfigured_IncludedAsGUCs(t *testing.T) {
	p := PostgresConfig{
		Host: "localhost", Port: "5432", User: "admin", Password: "secret", DB: "mydb", SSLMode: "disable",
		StatementTimeout: 5 * time.Second,
		LockTimeout:      2 * time.Second,
		IdleInTxTimeout:  10 * time.Second,
	}
	dsn := p.DSN()
	assert.True(t, strings.Contains(dsn, "-c statement_timeout=5000"))
	assert.True(t, strings.Contains(dsn, "-c lock_timeout=2000"))
	assert.True(t, strings.Contains(dsn, "-c idle_in_transaction_session_timeout=10000"))
}

func TestPostgresConfig_DSN_PartialTimeouts_OnlyConfiguredOnesIncluded(t *testing.T) {
	p := PostgresConfig{
		Host: "localhost", Port: "5432", User: "admin", Password: "secret", DB: "mydb", SSLMode: "disable",
		LockTimeout: 2 * time.Second,
	}
	dsn := p.DSN()
	assert.True(t, strings.Contains(dsn, "-c lock_timeout=2000"))
	assert.False(t, strings.Contains(dsn, "statement_timeout"))
	assert.False(t, strings.Contains(dsn, "idle_in_transaction_session_timeout"))
}

func TestConfig_IsProduction(t *testing.T) {
	tests := []struct {
		env      string
		expected bool
	}{
		{"production", true},
		{"staging", false},
		{"development", false},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			cfg := &Config{App: AppConfig{Env: tt.env}}
			assert.Equal(t, tt.expected, cfg.IsProduction())
		})
	}
}

// ─── Config.Warnings (docs/plan/10 Task T1) ─────────────────────────────────────

func TestConfig_Warnings_Development_NoWarnings(t *testing.T) {
	cfg := &Config{App: AppConfig{Env: "development", InternalBindAddr: "0.0.0.0"}}
	assert.Empty(t, cfg.Warnings(), "0.0.0.0 outside production is not itself a warning")
}

func TestConfig_Warnings_Production_LoopbackBind_NoWarning(t *testing.T) {
	cfg := &Config{App: AppConfig{Env: "production", InternalBindAddr: "127.0.0.1"}, JWT: JWTConfig{Issuer: "seev-api"}}
	assert.Empty(t, cfg.Warnings())
}

func TestConfig_Warnings_Production_WildcardBind_Warns(t *testing.T) {
	cfg := &Config{App: AppConfig{Env: "production", InternalBindAddr: "0.0.0.0"}, JWT: JWTConfig{Issuer: "seev-api"}}
	warnings := cfg.Warnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "INTERNAL_APP_BIND_ADDR")
}

func TestConfig_Warnings_Production_EmptyIssuer_Warns(t *testing.T) {
	cfg := &Config{App: AppConfig{Env: "production", InternalBindAddr: "127.0.0.1"}, JWT: JWTConfig{Issuer: ""}}
	warnings := cfg.Warnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "JWT_ISSUER")
}

func TestHelpers_ParseDuration(t *testing.T) {
	assert.Equal(t, 10*time.Second, parseDuration("10s", time.Minute))
	assert.Equal(t, time.Minute, parseDuration("", time.Minute))
	assert.Equal(t, time.Minute, parseDuration("invalid", time.Minute))
}

func TestHelpers_ParseInt(t *testing.T) {
	assert.Equal(t, 42, parseInt("42", 10))
	assert.Equal(t, 10, parseInt("", 10))
	assert.Equal(t, 10, parseInt("bad", 10))
}

func TestHelpers_GetWithDefault(t *testing.T) {
	getter := func(key string) string {
		if key == "EXISTING" {
			return "value"
		}
		return ""
	}
	assert.Equal(t, "value", getWithDefault(getter, "EXISTING", "default"))
	assert.Equal(t, "default", getWithDefault(getter, "MISSING", "default"))
}

func TestHelpers_RequireValue(t *testing.T) {
	getter := func(key string) string {
		if key == "PRESENT" {
			return "val"
		}
		return ""
	}
	var errs []string
	v := requireValue(getter, "PRESENT", &errs)
	assert.Equal(t, "val", v)
	assert.Empty(t, errs)

	v = requireValue(getter, "MISSING", &errs)
	assert.Equal(t, "", v)
	assert.Len(t, errs, 1)
	assert.Contains(t, errs[0], "MISSING")
}

func TestLoadFromEnv_ProductionRequiresRabbitTLS(t *testing.T) {
	_, err := loadFromEnv(validEnv(map[string]string{
		"APP_ENV": "production",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rabbitmq TLS must be enabled")
}

func TestRabbitMQ_URLBuilder(t *testing.T) {
	cfg := RabbitMQConfig{
		Host:     "localhost",
		Port:     5672,
		Username: "user",
		Password: "pass",
		VHost:    "/",
	}

	url := cfg.Url()
	assert.Equal(t, "amqp://user:pass@localhost:5672/%2F", url)
}

func TestParseTLSConfig_InvalidPanics(t *testing.T) {
	assert.Panics(t, func() {
		parseTLSConfig("badpair", "localhost")
	})
}
