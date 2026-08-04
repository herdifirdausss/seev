// Command loaddataset captures a machine-readable, hash-stamped summary of
// the REAL dataset a load-test run's self-seeding (tests/load/lib/seed.js,
// via real service APIs) actually produced in the disposable ledger
// database — closing the gap docs/performance/reports/2026-xx-baseline.md
// §24.1 names: "Produce a machine-readable seed manifest with counts,
// random seed, schema version, and content hash." It is a read-only
// observer, the same posture as tools/loadprobe, and deliberately does not
// duplicate tools/loadseed (a separate, synthetic-JSONL generator unrelated
// to the real API-seeded rows this tool reports on) or
// scripts/load-snapshot.sh (which hashes the raw pg_dump bytes for
// snapshot/restore integrity, not a semantic dataset summary).
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// tierBounds mirror docs/performance/reports/2026-xx-baseline.md §4.2's
// D0/D1/D2 table. A tolerance band is required because self-seeding via
// real APIs (rate-sized recipient/sender pools) does not hit an exact
// count the way a fixed synthetic generator would.
type tierBounds struct {
	Label            string
	MinAccounts      int64
	MaxAccounts      int64
	MinLedgerEntries int64
	MaxLedgerEntries int64
}

var tiers = map[string]tierBounds{
	"D0": {"D0 smoke", 500, 2_000, 30_000, 300_000},
	"D1": {"D1 baseline", 5_000, 20_000, 300_000, 3_000_000},
	"D2": {"D2 medium", 50_000, 200_000, 1_500_000, 15_000_000},
}

type Manifest struct {
	Tier                   string            `json:"tier,omitempty"`
	RunID                  string            `json:"run_id,omitempty"`
	GeneratedAt            time.Time         `json:"generated_at"`
	SchemaVersion          int64             `json:"schema_version"`
	SchemaDirty            bool              `json:"schema_dirty"`
	UserAccountCount       int64             `json:"user_account_count"`
	SystemAccountCount     int64             `json:"system_account_count"`
	LedgerTransactionCount int64             `json:"ledger_transaction_count"`
	LedgerEntryCount       int64             `json:"ledger_entry_count"`
	BalanceByCurrency      map[string]string `json:"balance_by_currency"`
	DatabaseBytes          int64             `json:"database_bytes"`
	ContentHash            string            `json:"content_hash"`
	TierConformance        string            `json:"tier_conformance"` // pass/fail/unchecked
	TierConformanceDetail  string            `json:"tier_conformance_detail,omitempty"`
	Errors                 []string          `json:"errors,omitempty"`
}

func main() {
	dsn := flag.String("dsn", os.Getenv("SEEV_LOAD_OBSERVER_DSN"), "disposable ledger DSN")
	tier := flag.String("tier", "", "declared dataset tier (D0/D1/D2) to check conformance against; empty = report only, no conformance check")
	runID := flag.String("run-id", os.Getenv("SEEV_LOAD_RUN_ID"), "run id recorded in the manifest")
	out := flag.String("out", "-", "manifest JSON output path")
	flag.Parse()
	if *dsn == "" {
		fail(fmt.Errorf("-dsn or SEEV_LOAD_OBSERVER_DSN is required"))
	}
	if *tier != "" {
		if _, ok := tiers[*tier]; !ok {
			fail(fmt.Errorf("unknown -tier %q, must be one of D0, D1, D2", *tier))
		}
	}
	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := db.PingContext(pingCtx); err != nil {
		pingCancel()
		fail(fmt.Errorf("dataset database unavailable: %w", err))
	}
	pingCancel()

	// A bounded context, not context.Background(): discovered live (a load
	// run's disposable stack was torn down mid-query, and an unbounded
	// context here left the process hanging indefinitely — no query-level
	// timeout, waiting on a connection that would never respond again — for
	// the harness's caller (scripts/load-test.sh) to notice or recover
	// from). These are simple aggregate queries; 30s is generous even under
	// load, and fits well inside the harness's own DRAIN_TIMEOUT_SECONDS
	// (default 90s) so a genuine failure here surfaces well before that.
	collectCtx, collectCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer collectCancel()
	manifest := collect(collectCtx, db, *tier, *runID)

	var writer *os.File
	if *out == "-" {
		writer = os.Stdout
	} else {
		writer, err = os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			fail(err)
		}
		defer writer.Close()
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		fail(err)
	}
	if manifest.TierConformance == "fail" {
		os.Exit(1)
	}
}

func collect(ctx context.Context, db *sql.DB, tier, runID string) Manifest {
	m := Manifest{RunID: runID, GeneratedAt: time.Now().UTC(), Tier: tier, BalanceByCurrency: map[string]string{}, TierConformance: "unchecked"}

	if err := db.QueryRowContext(ctx, `SELECT COALESCE(max(version),0), COALESCE(bool_or(dirty), false) FROM schema_migrations_ledger`).
		Scan(&m.SchemaVersion, &m.SchemaDirty); err != nil {
		m.Errors = append(m.Errors, "schema_migrations_ledger: "+err.Error())
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE owner_type = 'user'), count(*) FILTER (WHERE owner_type = 'system') FROM accounts`).
		Scan(&m.UserAccountCount, &m.SystemAccountCount); err != nil {
		m.Errors = append(m.Errors, "accounts: "+err.Error())
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_transactions`).Scan(&m.LedgerTransactionCount); err != nil {
		m.Errors = append(m.Errors, "ledger_transactions: "+err.Error())
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_entries`).Scan(&m.LedgerEntryCount); err != nil {
		m.Errors = append(m.Errors, "ledger_entries: "+err.Error())
	}
	if err := db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&m.DatabaseBytes); err != nil {
		m.Errors = append(m.Errors, "database_size: "+err.Error())
	}

	rows, err := db.QueryContext(ctx, `
		SELECT a.currency, sum(ab.balance)
		FROM account_balances ab JOIN accounts a ON a.id = ab.account_id
		GROUP BY a.currency ORDER BY a.currency`)
	if err != nil {
		m.Errors = append(m.Errors, "balance_by_currency: "+err.Error())
	} else {
		defer rows.Close()
		for rows.Next() {
			var currency string
			var balance int64
			if err := rows.Scan(&currency, &balance); err != nil {
				m.Errors = append(m.Errors, "balance_by_currency row: "+err.Error())
				break
			}
			m.BalanceByCurrency[currency] = fmt.Sprintf("%d", balance)
		}
		if err := rows.Err(); err != nil {
			m.Errors = append(m.Errors, "balance_by_currency rows: "+err.Error())
		}
	}

	m.ContentHash = contentHash(m)
	if tier != "" {
		checkTierConformance(&m, tiers[tier])
	}
	return m
}

// contentHash is a canonical sha256 over the fields that describe the
// dataset's SHAPE (counts, schema version) — deliberately excludes
// GeneratedAt/RunID/Errors/ContentHash itself so two manifests for
// dataset states that are logically identical hash identically regardless
// of when or under which run id they were captured.
func contentHash(m Manifest) string {
	currencies := make([]string, 0, len(m.BalanceByCurrency))
	for c := range m.BalanceByCurrency {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	var balanceSummary strings.Builder
	for _, c := range currencies {
		fmt.Fprintf(&balanceSummary, "%s=%s;", c, m.BalanceByCurrency[c])
	}
	canonical := fmt.Sprintf("schema_version=%d;user_accounts=%d;system_accounts=%d;ledger_transactions=%d;ledger_entries=%d;balances=%s",
		m.SchemaVersion, m.UserAccountCount, m.SystemAccountCount, m.LedgerTransactionCount, m.LedgerEntryCount, balanceSummary.String())
	sum := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func checkTierConformance(m *Manifest, bounds tierBounds) {
	var failures []string
	if m.UserAccountCount < bounds.MinAccounts || m.UserAccountCount > bounds.MaxAccounts {
		failures = append(failures, fmt.Sprintf("user_account_count=%d outside [%d,%d] for %s", m.UserAccountCount, bounds.MinAccounts, bounds.MaxAccounts, bounds.Label))
	}
	if m.LedgerEntryCount < bounds.MinLedgerEntries || m.LedgerEntryCount > bounds.MaxLedgerEntries {
		failures = append(failures, fmt.Sprintf("ledger_entry_count=%d outside [%d,%d] for %s", m.LedgerEntryCount, bounds.MinLedgerEntries, bounds.MaxLedgerEntries, bounds.Label))
	}
	if len(failures) == 0 {
		m.TierConformance = "pass"
		return
	}
	m.TierConformance = "fail"
	var detail strings.Builder
	for i, f := range failures {
		if i > 0 {
			detail.WriteString("; ")
		}
		detail.WriteString(f)
	}
	m.TierConformanceDetail = detail.String()
}

func fail(err error) { fmt.Fprintln(os.Stderr, "loaddataset:", err); os.Exit(1) }
