// Command loadprobe samples disposable PostgreSQL monitoring views. It is
// intentionally read-only and emits normalized JSON lines; query text and
// parameters never leave PostgreSQL.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Sample struct {
	Timestamp      time.Time       `json:"timestamp"`
	ActiveQueries  int             `json:"active_queries"`
	WaitingQueries int             `json:"waiting_queries"`
	OldestXactAge  float64         `json:"oldest_transaction_age_seconds"`
	BlockedLocks   int             `json:"blocked_locks"`
	DatabaseBytes  int64           `json:"database_bytes"`
	Statements     []StatementStat `json:"statements"`
	Errors         []string        `json:"errors,omitempty"`
}

type StatementStat struct {
	QueryID     int64   `json:"query_id"`
	Calls       int64   `json:"calls"`
	Rows        int64   `json:"rows"`
	TotalExecMS float64 `json:"total_exec_ms"`
	MeanExecMS  float64 `json:"mean_exec_ms"`
}

func main() {
	dsn := flag.String("dsn", os.Getenv("SEEV_LOAD_OBSERVER_DSN"), "disposable PostgreSQL DSN")
	interval := flag.Duration("interval", time.Second, "sampling interval")
	duration := flag.Duration("duration", 0, "sampling duration; zero means one sample")
	output := flag.String("out", "-", "JSONL output path")
	flag.Parse()
	if *dsn == "" {
		fail(fmt.Errorf("-dsn or SEEV_LOAD_OBSERVER_DSN is required"))
	}
	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := db.PingContext(ctx); err != nil {
		cancel()
		fail(fmt.Errorf("probe database unavailable: %w", err))
	}
	cancel()
	var writer *os.File
	if *output == "-" {
		writer = os.Stdout
	} else {
		writer, err = os.OpenFile(*output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			fail(err)
		}
		defer writer.Close()
	}
	encoder := json.NewEncoder(writer)
	deadline := time.Now().Add(*duration)
	for {
		sample := collect(context.Background(), db)
		if err := encoder.Encode(sample); err != nil {
			fail(err)
		}
		if *duration <= 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(*interval)
	}
}

func collect(ctx context.Context, db *sql.DB) Sample {
	sample := Sample{Timestamp: time.Now().UTC(), Statements: []StatementStat{}}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE state = 'active'), count(*) FILTER (WHERE wait_event IS NOT NULL), COALESCE(max(EXTRACT(EPOCH FROM now() - xact_start)), 0) FROM pg_stat_activity WHERE datname = current_database()`).Scan(&sample.ActiveQueries, &sample.WaitingQueries, &sample.OldestXactAge); err != nil {
		sample.Errors = append(sample.Errors, "pg_stat_activity")
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&sample.BlockedLocks); err != nil {
		sample.Errors = append(sample.Errors, "pg_locks")
	}
	if err := db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&sample.DatabaseBytes); err != nil {
		sample.Errors = append(sample.Errors, "database_size")
	}
	rows, err := db.QueryContext(ctx, `SELECT queryid, calls, rows, total_exec_time, mean_exec_time FROM pg_stat_statements ORDER BY total_exec_time DESC LIMIT 20`)
	if err != nil {
		sample.Errors = append(sample.Errors, "pg_stat_statements")
		return sample
	}
	defer rows.Close()
	for rows.Next() {
		var stat StatementStat
		if err := rows.Scan(&stat.QueryID, &stat.Calls, &stat.Rows, &stat.TotalExecMS, &stat.MeanExecMS); err != nil {
			sample.Errors = append(sample.Errors, "pg_stat_statements_row")
			break
		}
		sample.Statements = append(sample.Statements, stat)
	}
	if err := rows.Err(); err != nil {
		sample.Errors = append(sample.Errors, "pg_stat_statements_rows")
	}
	return sample
}

func fail(err error) { fmt.Fprintln(os.Stderr, "loadprobe:", err); os.Exit(1) }
