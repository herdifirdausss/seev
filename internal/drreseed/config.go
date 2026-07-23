// Package drreseed implements docs/roadmap/active/50 T5 (K10): deterministic
// reconstruction of Redis state that is not itself backed up — policy
// counters and fraud velocity/dedup keys — from the PostgreSQL data a
// restore (T3) already brought back. Redis and RabbitMQ start with fresh,
// empty volumes on any drill (K10 item 2); this package rebuilds only
// what PostgreSQL has durable evidence for, and fails closed rather than
// guessing when that evidence is missing (K10: "If required source
// evidence is unavailable, the fraud path remains fail-closed and the
// gate fails").
package drreseed

import (
	"fmt"
	"os"
)

// defaultRedisAddr is docker-compose.yml's own real dev-stack address —
// refusing it by default (K10: "never the production Redis address by
// default") means an operator must take the explicit extra step of
// setting DRRESEED_ALLOW_DEFAULT_REDIS=1 to point this tool at anything
// that looks like the real service, mirroring
// scripts/restore-cluster.sh's own refuse-the-obvious-unsafe-default
// guard (K7).
const defaultRedisAddr = "redis:6379"

// Config holds the two read-only PostgreSQL DSNs K10 specifies plus the
// Redis reconstruction target.
type Config struct {
	LedgerDSN string
	FraudDSN  string
	RedisAddr string

	AllowDefaultRedis bool
}

// Load reads Config from the environment.
func Load() (Config, error) {
	cfg := Config{
		LedgerDSN:         os.Getenv("LEDGER_DSN"),
		FraudDSN:          os.Getenv("FRAUD_DSN"),
		RedisAddr:         os.Getenv("REDIS_ADDR"),
		AllowDefaultRedis: os.Getenv("DRRESEED_ALLOW_DEFAULT_REDIS") == "1",
	}
	var missing []string
	if cfg.LedgerDSN == "" {
		missing = append(missing, "LEDGER_DSN")
	}
	if cfg.FraudDSN == "" {
		missing = append(missing, "FRAUD_DSN")
	}
	if cfg.RedisAddr == "" {
		missing = append(missing, "REDIS_ADDR")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required env vars: %v", missing)
	}
	if cfg.RedisAddr == defaultRedisAddr && !cfg.AllowDefaultRedis {
		return Config{}, fmt.Errorf("refusing REDIS_ADDR=%q — this is the real dev stack's own address, never a drill target (K10). Set DRRESEED_ALLOW_DEFAULT_REDIS=1 to override", defaultRedisAddr)
	}
	return cfg, nil
}
