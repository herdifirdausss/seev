package drreseed

import "testing"

func TestLoadRefusesDefaultRedisAddr(t *testing.T) {
	t.Setenv("LEDGER_DSN", "postgres://x")
	t.Setenv("FRAUD_DSN", "postgres://x")
	t.Setenv("REDIS_ADDR", defaultRedisAddr)

	if _, err := Load(); err == nil {
		t.Fatal("expected Load to refuse the default (real dev stack) Redis address, got nil error")
	}
}

func TestLoadAllowsDefaultRedisAddrWithOverride(t *testing.T) {
	t.Setenv("LEDGER_DSN", "postgres://x")
	t.Setenv("FRAUD_DSN", "postgres://x")
	t.Setenv("REDIS_ADDR", defaultRedisAddr)
	t.Setenv("DRRESEED_ALLOW_DEFAULT_REDIS", "1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with explicit override should succeed, got: %v", err)
	}
	if cfg.RedisAddr != defaultRedisAddr {
		t.Fatalf("RedisAddr = %q, want %q", cfg.RedisAddr, defaultRedisAddr)
	}
}

func TestLoadAllowsNonDefaultRedisAddr(t *testing.T) {
	t.Setenv("LEDGER_DSN", "postgres://x")
	t.Setenv("FRAUD_DSN", "postgres://x")
	t.Setenv("REDIS_ADDR", "seev-a7-drill-redis:6379")

	if _, err := Load(); err != nil {
		t.Fatalf("Load with a non-default Redis address should succeed, got: %v", err)
	}
}

func TestLoadRequiresAllDSNs(t *testing.T) {
	t.Setenv("REDIS_ADDR", "some-drill-redis:6379")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to fail with no DSNs set")
	}
}
