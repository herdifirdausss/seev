// Package assurance owns durable, read-only cross-service reconciliation.
package assurance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/database"
)

type Module struct {
	db      database.DatabaseSQL
	cfg     config.AssuranceConfig
	logger  *slog.Logger
	payin   payinReader
	payout  payoutReader
	ledger  ledgerReader
	alertFn alerting.AlertFunc

	stopOnce sync.Once
	runMu    sync.Mutex
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// Narrow interfaces keep assurance decoupled from unrelated owner RPCs and
// make dependency failures easy to exercise in unit tests.
type payinReader interface {
	ListAssuranceRecords(context.Context, *payinv1.ListAssuranceRecordsRequest, ...grpc.CallOption) (*payinv1.ListAssuranceRecordsResponse, error)
}
type payoutReader interface {
	ListAssuranceRecords(context.Context, *payoutv1.ListAssuranceRecordsRequest, ...grpc.CallOption) (*payoutv1.ListAssuranceRecordsResponse, error)
}
type ledgerReader interface {
	BatchGetAssuranceTransactions(context.Context, *ledgerv1.BatchGetAssuranceTransactionsRequest, ...grpc.CallOption) (*ledgerv1.BatchGetAssuranceTransactionsResponse, error)
}

func NewModule(db database.DatabaseSQL, cfg config.AssuranceConfig, payin payinReader, payout payoutReader, ledger ledgerReader, alertFn alerting.AlertFunc, logger *slog.Logger) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	return &Module{db: db, cfg: cfg, logger: logger, payin: payin, payout: payout, ledger: ledger, alertFn: alertFn, stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

func (m *Module) Start(ctx context.Context) {
	go func() {
		defer close(m.doneCh)
		// A first run is deliberately asynchronous so the HTTP health endpoint
		// can come up while a historical backfill is in progress.
		if _, err := m.Run(ctx, "backfill"); err != nil && !errors.Is(err, context.Canceled) {
			m.logger.Error("assurance initial run failed", "error", err)
		}
		ticker := time.NewTicker(m.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			case <-ticker.C:
				if _, err := m.Run(ctx, "incremental"); err != nil && !errors.Is(err, context.Canceled) {
					m.logger.Error("assurance scheduled run failed", "error", err)
				}
			}
		}
	}()
}

func (m *Module) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	select {
	case <-m.doneCh:
	case <-time.After(5 * time.Second):
	}
}

type RunSummary struct {
	ID             uuid.UUID `json:"id"`
	Mode           string    `json:"mode"`
	Status         string    `json:"status"`
	Cutoff         time.Time `json:"cutoff_at"`
	RecordsScanned int       `json:"records_scanned"`
	PagesScanned   int       `json:"pages_scanned"`
	FindingsOpened int       `json:"findings_opened"`
	Baseline       bool      `json:"baseline"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
}

func (m *Module) Run(ctx context.Context, mode string) (RunSummary, error) {
	if !m.runMu.TryLock() {
		return RunSummary{}, errors.New("assurance run already active")
	}
	defer m.runMu.Unlock()
	started := time.Now()
	if mode == "" {
		mode = "manual"
	}
	if mode != "manual" && mode != "backfill" && mode != "incremental" {
		return RunSummary{}, fmt.Errorf("invalid assurance run mode %q", mode)
	}
	cutoff := started.Add(-m.cfg.ConsistencyDelay)
	run := RunSummary{ID: uuid.New(), Mode: mode, Status: "running", StartedAt: started, Cutoff: cutoff, Baseline: mode == "backfill"}
	if _, err := m.db.ExecContext(ctx, `INSERT INTO assurance_runs (id, mode, status, baseline, cutoff_at, started_at) VALUES ($1,$2,$3,$4,$5,$6)`, run.ID, run.Mode, run.Status, run.Baseline, cutoff, started); err != nil {
		return run, fmt.Errorf("create assurance run: %w", err)
	}
	defer func() { runDuration.Observe(time.Since(started).Seconds()) }()

	if n, err := m.scanPayin(ctx, run.ID, cutoff, mode == "backfill"); err != nil {
		return m.failRun(ctx, run, err)
	} else {
		run.RecordsScanned += n
	}
	if n, err := m.scanPayout(ctx, run.ID, cutoff, mode == "backfill"); err != nil {
		return m.failRun(ctx, run, err)
	} else {
		run.RecordsScanned += n
	}
	if mode == "backfill" {
		if err := m.markBackfillComplete(ctx, "ledger", run.ID); err != nil {
			return m.failRun(ctx, run, err)
		}
	}
	var opened, pages int
	if err := m.db.QueryRowContext(ctx, `SELECT findings_opened, pages_scanned FROM assurance_runs WHERE id=$1`, run.ID).Scan(&opened, &pages); err != nil {
		return m.failRun(ctx, run, fmt.Errorf("read assurance run progress: %w", err))
	}
	run.FindingsOpened = opened
	run.PagesScanned = pages
	// Alert delivery is secondary to proof persistence: a webhook outage must
	// not roll back a successful scan or advance decision.
	if err := m.dispatchAlerts(ctx); err != nil {
		m.logger.Error("assurance alert dispatch failed", "error", err)
	}
	if err := m.refreshMetrics(ctx); err != nil {
		m.logger.Error("assurance metrics refresh failed", "error", err)
	}
	run.Status = "succeeded"
	run.FinishedAt = time.Now()
	if _, err := m.db.ExecContext(ctx, `UPDATE assurance_runs SET status='succeeded', finished_at=$2, records_scanned=$3, findings_opened=$4 WHERE id=$1`, run.ID, run.FinishedAt, run.RecordsScanned, run.FindingsOpened); err != nil {
		return run, fmt.Errorf("finish assurance run: %w", err)
	}
	return run, nil
}

func (m *Module) failRun(ctx context.Context, run RunSummary, runErr error) (RunSummary, error) {
	run.Status = "failed"
	run.FinishedAt = time.Now()
	runFailures.Inc()
	_, _ = m.db.ExecContext(ctx, `UPDATE assurance_runs SET status='failed', finished_at=$2, records_scanned=$3, error_code='DEPENDENCY_OR_PERSISTENCE', error_message=$4 WHERE id=$1`, run.ID, run.FinishedAt, run.RecordsScanned, runErr.Error())
	return run, runErr
}
