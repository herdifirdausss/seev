package drverify

import (
	"fmt"
	"os"
	"time"
)

// Services is the fixed, ordered list of the eight authoritative
// databases (docs/roadmap/active/50 T0 Result) — matches
// scripts/backup-manifest.sh's/scripts/restore-cluster.sh's own list
// exactly, including the "adminbff" (not "admin-bff") spelling.
var Services = []string{"ledger", "auth", "payin", "payout", "fraud", "gateway", "adminbff", "assurance"}

// Config holds one explicit DSN per authoritative database (K9: "receives
// explicit DSNs for the eight restored databases") plus the bounded
// concurrency/timeout settings every connection is built with.
type Config struct {
	DSN map[string]string // keyed by Services entries

	// StatementTimeout/LockTimeout bound every read-only transaction
	// (K9 "statement timeouts"). Applied via `SET LOCAL` at the start of
	// each transaction rather than baked into the DSN, since an
	// operator-supplied DSN's format is not guaranteed (keyword/value or
	// URL) and SET LOCAL works identically either way.
	StatementTimeout time.Duration
	LockTimeout      time.Duration

	// MaxConcurrency bounds how many databases/checks run at once (K9
	// "bounded concurrency") — never unbounded fan-out across 8 live
	// connections plus however many payin/payout correlation goroutines.
	MaxConcurrency int

	// PageSize bounds how many rows a single query batch returns (K9
	// "bounded batches") — mirrors the 500-row page size
	// internal/assurance's own gRPC pagination already uses.
	PageSize int
}

// Load reads Config from the environment: <SERVICE>_DSN per entry in
// Services (e.g. LEDGER_DSN, AUTH_DSN), uppercased.
func Load() (Config, error) {
	cfg := Config{
		DSN:              make(map[string]string, len(Services)),
		StatementTimeout: parseDurationDefault("DRVERIFY_STATEMENT_TIMEOUT", 10*time.Second),
		LockTimeout:      parseDurationDefault("DRVERIFY_LOCK_TIMEOUT", 5*time.Second),
		MaxConcurrency:   parseIntDefault("DRVERIFY_MAX_CONCURRENCY", 4),
		PageSize:         parseIntDefault("DRVERIFY_PAGE_SIZE", 500),
	}
	var missing []string
	for _, service := range Services {
		key := envKey(service)
		dsn := os.Getenv(key)
		if dsn == "" {
			missing = append(missing, key)
			continue
		}
		cfg.DSN[service] = dsn
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required DSN env vars: %v", missing)
	}
	return cfg, nil
}

func envKey(service string) string {
	switch service {
	case "adminbff":
		return "ADMINBFF_DSN"
	default:
		upper := ""
		for _, r := range service {
			if r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			upper += string(r)
		}
		return upper + "_DSN"
	}
}

func parseDurationDefault(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func parseIntDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
