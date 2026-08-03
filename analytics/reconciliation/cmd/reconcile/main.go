package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/analytics/reconciliation/internal/core"
	"github.com/herdifirdausss/seev/analytics/reconciliation/internal/reconcile"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type config struct {
	LedgerDSN       string
	ClickHouseURL   string
	ClickHouseUser  string
	ClickHousePass   string
	Environment     string
	Timeout         time.Duration
	ReportDate      string
}

type ledgerSummary struct {
	Count  int64
	Amount int64
	Latest time.Time
}

func main() {
	dryRun := flag.Bool("dry-run", false, "evaluate checks without persisting control rows")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	db, err := sql.Open("pgx", cfg.LedgerDSN)
	if err != nil {
		fatal(fmt.Errorf("open read-only Ledger connection: %w", err))
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		fatal(fmt.Errorf("ping Ledger read-only connection: %w", err))
	}
	if _, err := db.ExecContext(ctx, "SET default_transaction_read_only = on; SET statement_timeout = '5s'"); err != nil {
		fatal(fmt.Errorf("configure bounded read-only Ledger session: %w", err))
	}

	source, err := readLedgerSummary(ctx, db)
	if err != nil {
		fatal(err)
	}
	warehouseLatest, err := queryClickHouseTime(ctx, cfg, "SELECT coalesce(max(created_at_utc), toDateTime64('1970-01-01 00:00:00', 6, 'UTC')) FROM core.fact_ledger_transaction")
	if err != nil {
		fatal(fmt.Errorf("read warehouse Ledger cutoff: %w", err))
	}

	runID := uuid.New()
	cutoff := reconcile.Cutoff(source.Latest, warehouseLatest)
	source, err = readLedgerSummaryAt(ctx, db, cutoff)
	if err != nil {
		fatal(err)
	}
	cutoffLiteral := cutoff.Format("2006-01-02 15:04:05.000000")
	warehouse, err := queryClickHousePair(ctx, cfg, fmt.Sprintf("SELECT count(), coalesce(sum(amount_minor), 0) FROM core.fact_ledger_transaction WHERE is_posted = 1 AND created_at_utc <= toDateTime64('%s', 6, 'UTC')", cutoffLiteral))
	if err != nil {
		fatal(fmt.Errorf("read warehouse Ledger summary: %w", err))
	}
	sourceUnbalanced, err := readLedgerUnbalanced(ctx, db, cutoff)
	if err != nil {
		fatal(err)
	}
	warehouseUnbalanced, err := queryClickHouseInt64(ctx, cfg, fmt.Sprintf("SELECT count() FROM (SELECT transaction_id FROM core.fact_ledger_entry WHERE created_at_utc <= toDateTime64('%s', 6, 'UTC') GROUP BY transaction_id HAVING sumIf(amount_minor, direction = 'debit') != sumIf(amount_minor, direction = 'credit'))", cutoffLiteral))
	if err != nil {
		fatal(fmt.Errorf("read warehouse Ledger invariant: %w", err))
	}
	sourceFeeRevenue, err := readFeeRevenue(ctx, db, cutoff)
	if err != nil {
		fatal(err)
	}
	warehouseFeeRevenue, err := queryClickHouseInt64(ctx, cfg, fmt.Sprintf("SELECT coalesce(sum(recognized_fee_revenue_minor), 0) FROM core.fact_fee_revenue WHERE posted_at_utc <= toDateTime64('%s', 6, 'UTC')", cutoffLiteral))
	if err != nil {
		fatal(fmt.Errorf("read warehouse fee revenue: %w", err))
	}
	checks := []reconcile.Check{
		{Name: "ledger_posted_row_count", SourceService: "ledger", Source: "ledger_transactions", WarehouseModel: "core.fact_ledger_transaction", Expected: source.Count, Actual: warehouse[0], Critical: true, Details: "safe cutoff row count"},
		{Name: "ledger_posted_amount_minor", SourceService: "ledger", Source: "ledger_transactions", WarehouseModel: "core.fact_ledger_transaction", Expected: source.Amount, Actual: warehouse[1], Critical: true, Details: "safe cutoff integer minor-unit total"},
		{Name: "ledger_debit_credit_balance", SourceService: "ledger", Source: "ledger_entries", WarehouseModel: "core.fact_ledger_entry", Expected: sourceUnbalanced, Actual: warehouseUnbalanced, Critical: true, Details: "unbalanced transaction count at safe cutoff"},
		{Name: "recognized_fee_revenue_reconciliation", SourceService: "ledger", Source: "fee_accounts", WarehouseModel: "core.fact_fee_revenue", Expected: sourceFeeRevenue, Actual: warehouseFeeRevenue, Critical: true, Details: "posted fee-account integer minor-unit total at safe cutoff"},
	}
	result := reconcile.Evaluate(checks)
	if !*dryRun {
		if err := persistResult(ctx, cfg, runID, cutoff, result); err != nil {
			fatal(err)
		}
	}

	output := map[string]interface{}{
		"run_id":           runID.String(),
		"status":           result.CompletedStatus,
		"cutoff":           cutoff.Format(time.RFC3339Nano),
		"critical_failures": result.CriticalFailed,
		"warning_failures":  result.WarningFailed,
		"persisted":        !*dryRun,
	}
	encoded, _ := json.Marshal(output)
	fmt.Println(string(encoded))
	if result.CriticalFailed > 0 {
		os.Exit(3)
	}
}

func loadConfig() (config, error) {
	timeout := 30 * time.Second
	if raw := os.Getenv("ANALYTICS_RECONCILIATION_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("invalid ANALYTICS_RECONCILIATION_TIMEOUT: %w", err)
		}
		timeout = parsed
	}
	password := os.Getenv("ANALYTICS_CLICKHOUSE_PASSWORD")
	if file := os.Getenv("ANALYTICS_CLICKHOUSE_PASSWORD_FILE"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return config{}, fmt.Errorf("read ClickHouse password file: %w", err)
		}
		password = strings.TrimSpace(string(data))
	}
	cfg := config{
		LedgerDSN:      os.Getenv("ANALYTICS_LEDGER_DSN"),
		ClickHouseURL:  strings.TrimRight(defaultString(os.Getenv("ANALYTICS_CLICKHOUSE_URL"), "http://127.0.0.1:8123"), "/"),
		ClickHouseUser: defaultString(os.Getenv("ANALYTICS_CLICKHOUSE_USER"), "analytics_reconciliation"),
		ClickHousePass: password,
		Environment:   defaultString(os.Getenv("ANALYTICS_ENVIRONMENT"), "local-dev"),
		Timeout:       timeout,
	}
	if cfg.LedgerDSN == "" {
		return config{}, errors.New("ANALYTICS_LEDGER_DSN is required; reconciliation never guesses a source connection")
	}
	if cfg.ClickHousePass == "" {
		return config{}, errors.New("ANALYTICS_CLICKHOUSE_PASSWORD or ANALYTICS_CLICKHOUSE_PASSWORD_FILE is required")
	}
	return cfg, nil
}

func readLedgerSummary(ctx context.Context, db *sql.DB) (ledgerSummary, error) {
	var summary ledgerSummary
	const query = `SELECT count(*), coalesce(sum(amount), 0), coalesce(max(created_at), timestamptz 'epoch')
FROM ledger_transactions
WHERE status = 'posted'`
	if err := db.QueryRowContext(ctx, query).Scan(&summary.Count, &summary.Amount, &summary.Latest); err != nil {
		return ledgerSummary{}, fmt.Errorf("read bounded Ledger summary: %w", err)
	}
	return summary, nil
}

func readLedgerSummaryAt(ctx context.Context, db *sql.DB, cutoff time.Time) (ledgerSummary, error) {
	var summary ledgerSummary
	const query = `SELECT count(*), coalesce(sum(amount), 0), coalesce(max(created_at), timestamptz 'epoch')
FROM ledger_transactions
WHERE status = 'posted' AND created_at <= $1`
	if err := db.QueryRowContext(ctx, query, cutoff).Scan(&summary.Count, &summary.Amount, &summary.Latest); err != nil {
		return ledgerSummary{}, fmt.Errorf("read bounded Ledger summary at safe cutoff: %w", err)
	}
	return summary, nil
}

func readLedgerUnbalanced(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	var count int64
	const query = `SELECT count(*) FROM (
  SELECT transaction_id
  FROM ledger_entries
  WHERE created_at <= $1
  GROUP BY transaction_id
  HAVING coalesce(sum(amount) FILTER (WHERE direction = 'debit'), 0)
      != coalesce(sum(amount) FILTER (WHERE direction = 'credit'), 0)
) mismatches`
	if err := db.QueryRowContext(ctx, query, cutoff).Scan(&count); err != nil {
		return 0, fmt.Errorf("read bounded Ledger debit-credit invariant: %w", err)
	}
	return count, nil
}

func readFeeRevenue(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	var revenue int64
	const query = `SELECT coalesce(sum(CASE WHEN e.direction = 'credit' THEN e.amount ELSE -e.amount END), 0)
FROM ledger_entries e
JOIN accounts a ON a.id = e.account_id AND a.type = 'fee'
JOIN ledger_transactions t ON t.id = e.transaction_id AND t.status = 'posted'
WHERE e.created_at <= $1`
	if err := db.QueryRowContext(ctx, query, cutoff).Scan(&revenue); err != nil {
		return 0, fmt.Errorf("read bounded Ledger fee-account revenue: %w", err)
	}
	return revenue, nil
}

func queryClickHousePair(ctx context.Context, cfg config, query string) ([2]int64, error) {
	body, err := clickHouseQuery(ctx, cfg, query+" FORMAT TabSeparated")
	if err != nil {
		return [2]int64{}, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	if !scanner.Scan() {
		return [2]int64{}, errors.New("ClickHouse returned no summary row")
	}
	parts := strings.Split(scanner.Text(), "\t")
	if len(parts) != 2 {
		return [2]int64{}, fmt.Errorf("ClickHouse summary has %d columns, want 2", len(parts))
	}
	var result [2]int64
	for index, part := range parts {
		value, parseErr := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if parseErr != nil {
			return [2]int64{}, fmt.Errorf("parse ClickHouse summary value: %w", parseErr)
		}
		result[index] = value
	}
	return result, nil
}

func queryClickHouseInt64(ctx context.Context, cfg config, query string) (int64, error) {
	body, err := clickHouseQuery(ctx, cfg, query+" FORMAT TabSeparated")
	if err != nil {
		return 0, err
	}
	line, ok := firstLine(body)
	if !ok {
		return 0, errors.New("ClickHouse returned no scalar row")
	}
	value, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ClickHouse scalar: %w", err)
	}
	return value, nil
}

func queryClickHouseTime(ctx context.Context, cfg config, query string) (time.Time, error) {
	body, err := clickHouseQuery(ctx, cfg, query+" FORMAT TabSeparated")
	if err != nil {
		return time.Time{}, err
	}
	line, ok := firstLine(body)
	if !ok {
		return time.Time{}, errors.New("ClickHouse returned no timestamp row")
	}
	for _, layout := range []string{"2006-01-02 15:04:05.000000", time.RFC3339Nano} {
		if value, parseErr := time.ParseInLocation(layout, strings.TrimSpace(line), time.UTC); parseErr == nil {
			return value.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse ClickHouse timestamp %q", strings.TrimSpace(line))
}

func firstLine(body []byte) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	if !scanner.Scan() {
		return "", false
	}
	return scanner.Text(), true
}

func clickHouseQuery(ctx context.Context, cfg config, query string) ([]byte, error) {
	return clickHouseRequest(ctx, cfg, query, nil)
}

func clickHouseInsert(ctx context.Context, cfg config, query string, data []byte) error {
	_, err := clickHouseRequest(ctx, cfg, query, data)
	return err
}

func clickHouseRequest(ctx context.Context, cfg config, query string, data []byte) ([]byte, error) {
	endpoint, err := url.Parse(cfg.ClickHouseURL)
	if err != nil {
		return nil, fmt.Errorf("parse ClickHouse URL: %w", err)
	}
	values := endpoint.Query()
	values.Set("query", query)
	endpoint.RawQuery = values.Encode()
	var body io.Reader
	if data != nil {
		body = strings.NewReader(string(data) + "\n")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build ClickHouse request: %w", err)
	}
	req.SetBasicAuth(cfg.ClickHouseUser, cfg.ClickHousePass)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read ClickHouse response: %w", err)
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("ClickHouse returned %s", response.Status)
	}
	return body, nil
}

func persistResult(ctx context.Context, cfg config, runID uuid.UUID, cutoff time.Time, result reconcile.Result) error {
	run := map[string]interface{}{
		"run_id": runID.String(), "environment": cfg.Environment, "status": result.CompletedStatus,
		"cutoff_type": "source_latest_time_minimum", "cutoff_value": cutoff.Format(time.RFC3339Nano),
		"started_at": time.Now().UTC().Format(time.RFC3339Nano), "finished_at": time.Now().UTC().Format(time.RFC3339Nano),
		"critical_failures": result.CriticalFailed, "warning_failures": result.WarningFailed,
		"details": "bounded read-only Ledger summary reconciliation",
	}
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if err := clickHouseInsert(ctx, cfg, "INSERT INTO control.reconciliation_runs FORMAT JSONEachRow", data); err != nil {
		return fmt.Errorf("persist reconciliation run: %w", err)
	}
	for _, check := range result.Checks {
		item := map[string]interface{}{
			"run_id": runID.String(), "check_name": check.Name, "source_service": check.SourceService,
			"source_table_or_metric": check.Source, "warehouse_model": check.WarehouseModel, "currency": check.Currency,
			"cutoff_type": "source_latest_time_minimum", "cutoff_value": cutoff.Format(time.RFC3339Nano),
			"expected_value": check.Expected, "actual_value": check.Actual, "delta_value": core.Delta(check.Expected, check.Actual),
			"severity": core.SeverityFor(core.Delta(check.Expected, check.Actual), check.Critical),
			"status": map[bool]string{true: "passed", false: "failed"}[check.Expected == check.Actual],
			"details": core.RedactDetails(check.Details), "created_at": time.Now().UTC().Format(time.RFC3339Nano),
		}
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if err := clickHouseInsert(ctx, cfg, "INSERT INTO control.reconciliation_items FORMAT JSONEachRow", data); err != nil {
			return fmt.Errorf("persist reconciliation item: %w", err)
		}
	}
	return nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
