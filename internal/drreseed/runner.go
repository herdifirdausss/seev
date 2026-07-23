package drreseed

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/fraud"
	"github.com/herdifirdausss/seev/pkg/cache"
)

// Run connects to the ledger/fraud databases (read-only) and the target
// Redis (read-write — this is the one thing drreseed is allowed to
// write, unlike drverify), then reconstructs policy counters and fraud
// velocity/dedup state. Always returns a non-nil *Report.
func Run(ctx context.Context, cfg Config) *Report {
	report := &Report{GeneratedAt: time.Now().UTC()}

	ledgerDB, err := sql.Open("pgx", cfg.LedgerDSN)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("open ledger DSN: %v", err))
		return report
	}
	defer ledgerDB.Close()
	if err := ledgerDB.PingContext(ctx); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("ping ledger: %v", err))
		return report
	}

	// The fraud database itself holds no data this reconstruction reads
	// FROM (the actual evidence — outbox_events/ledger_transactions —
	// lives in seev_ledger, matching exactly what the live consumer
	// itself reads from the AMQP message derived from that same outbox).
	// Connecting to it here is a prerequisite health check, not a data
	// source: reconstructing fraud's own Redis state while the fraud
	// database itself is unreachable/unrestored would be reseeding a
	// service that cannot even come up — fail closed before writing
	// anything.
	fraudDB, err := sql.Open("pgx", cfg.FraudDSN)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("open fraud DSN: %v", err))
		return report
	}
	defer fraudDB.Close()
	if err := fraudDB.PingContext(ctx); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("ping fraud: %v", err))
		return report
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("ping redis: %v", err))
		return report
	}

	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.UTC
	}
	counter := cache.NewRedisCounter(rdb)
	if err := ReconstructPolicyCounters(ctx, ledgerDB, counter, loc, report); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("policy counters: %v", err))
	}

	velocityStore := fraud.NewRedisVelocityStore(rdb)
	if err := ReconstructFraudVelocity(ctx, ledgerDB, velocityStore, report); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("fraud velocity: %v", err))
	}

	return report
}
