// Package assurance owns durable, read-only cross-service reconciliation.
package assurance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	assurancerules "github.com/herdifirdausss/seev/internal/assurance/rules"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/database"
)

var (
	runDuration        = prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: "assurance", Name: "run_duration_seconds", Help: "Duration of assurance runs."})
	runFailures        = prometheus.NewCounter(prometheus.CounterOpts{Namespace: "assurance", Name: "run_failures_total", Help: "Assurance runs that failed before cursor advancement."})
	recordsScanned     = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "assurance", Name: "records_scanned_total", Help: "Records read from owner services."}, []string{"source"})
	findingsBySeverity = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "assurance", Name: "findings", Help: "Current finding count by severity and rule."}, []string{"severity", "rule"})
	moneyAtRisk        = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "assurance", Name: "money_at_risk_minor", Help: "Open finding amount in minor units by currency."}, []string{"currency"})
	alertDeliveries    = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "assurance", Name: "alert_deliveries_total", Help: "Assurance alert delivery attempts."}, []string{"result", "severity"})
)

func init() {
	for _, metric := range []prometheus.Collector{runDuration, runFailures, recordsScanned, findingsBySeverity, moneyAtRisk, alertDeliveries} {
		_ = prometheus.Register(metric)
	}
}

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

func (m *Module) scanPayin(ctx context.Context, runID uuid.UUID, cutoff time.Time, backfill bool) (int, error) {
	if m.payin == nil {
		return 0, errors.New("payin assurance client is unavailable")
	}
	cur, err := m.cursor(ctx, "payin")
	if err != nil {
		return 0, err
	}
	total := 0
	for {
		rpcCtx, cancel := context.WithTimeout(ctx, m.cfg.RPCTimeout)
		request := &payinv1.ListAssuranceRecordsRequest{PageSize: uint32(m.cfg.PageSize), Cutoff: timestamppb.New(cutoff)}
		if cur.Valid {
			request.CursorUpdatedAt = timestamppb.New(cur.UpdatedAt)
			request.CursorId = cur.ID.String()
		}
		response, callErr := m.payin.ListAssuranceRecords(rpcCtx, request)
		cancel()
		if callErr != nil {
			return total, fmt.Errorf("payin assurance RPC: %w", callErr)
		}
		if response == nil {
			return total, errors.New("payin assurance RPC returned nil response")
		}
		if err := validatePayinPage(response.GetRecords(), cutoff); err != nil {
			return total, err
		}
		if len(response.GetRecords()) == 0 && response.GetHasMore() {
			return total, errors.New("payin assurance RPC reported has_more with an empty page")
		}
		if cur.Valid && len(response.GetRecords()) > 0 {
			firstTime, firstID, err := payinCursor(response.GetRecords()[0])
			if err != nil {
				return total, err
			}
			if firstTime.Before(cur.UpdatedAt) || (firstTime.Equal(cur.UpdatedAt) && firstID.String() <= cur.ID.String()) {
				return total, errors.New("payin assurance RPC did not advance cursor")
			}
		}
		if err := m.provePayin(ctx, response.GetRecords(), backfill, runID); err != nil {
			return total, err
		}
		total += len(response.GetRecords())
		if len(response.GetRecords()) > 0 {
			last := response.GetRecords()[len(response.GetRecords())-1]
			lastUpdated, lastID, err := payinCursor(last)
			if err != nil {
				return total, err
			}
			if err := m.recordPage(ctx, runID); err != nil {
				return total, err
			}
			if err := m.advanceCursor(ctx, "payin", lastUpdated, lastID.String(), runID, backfill && !response.GetHasMore()); err != nil {
				return total, err
			}
			cur = cursorValue{Valid: true, UpdatedAt: lastUpdated, ID: lastID}
		}
		if !response.GetHasMore() {
			break
		}
	}
	if total == 0 && backfill {
		if err := m.markBackfillComplete(ctx, "payin", runID); err != nil {
			return total, err
		}
	}
	if total == 0 {
		if err := m.recordPage(ctx, runID); err != nil {
			return total, err
		}
	}
	recordsScanned.WithLabelValues("payin").Add(float64(total))
	return total, nil
}

func (m *Module) scanPayout(ctx context.Context, runID uuid.UUID, cutoff time.Time, backfill bool) (int, error) {
	if m.payout == nil {
		return 0, errors.New("payout assurance client is unavailable")
	}
	cur, err := m.cursor(ctx, "payout")
	if err != nil {
		return 0, err
	}
	total := 0
	for {
		rpcCtx, cancel := context.WithTimeout(ctx, m.cfg.RPCTimeout)
		request := &payoutv1.ListAssuranceRecordsRequest{PageSize: uint32(m.cfg.PageSize), Cutoff: timestamppb.New(cutoff)}
		if cur.Valid {
			request.CursorUpdatedAt = timestamppb.New(cur.UpdatedAt)
			request.CursorId = cur.ID.String()
		}
		response, callErr := m.payout.ListAssuranceRecords(rpcCtx, request)
		cancel()
		if callErr != nil {
			return total, fmt.Errorf("payout assurance RPC: %w", callErr)
		}
		if response == nil {
			return total, errors.New("payout assurance RPC returned nil response")
		}
		if err := validatePayoutPage(response.GetRecords(), cutoff); err != nil {
			return total, err
		}
		if len(response.GetRecords()) == 0 && response.GetHasMore() {
			return total, errors.New("payout assurance RPC reported has_more with an empty page")
		}
		if cur.Valid && len(response.GetRecords()) > 0 {
			firstTime, firstID, err := payoutCursor(response.GetRecords()[0])
			if err != nil {
				return total, err
			}
			if firstTime.Before(cur.UpdatedAt) || (firstTime.Equal(cur.UpdatedAt) && firstID.String() <= cur.ID.String()) {
				return total, errors.New("payout assurance RPC did not advance cursor")
			}
		}
		if err := m.provePayout(ctx, response.GetRecords(), backfill, runID); err != nil {
			return total, err
		}
		total += len(response.GetRecords())
		if len(response.GetRecords()) > 0 {
			last := response.GetRecords()[len(response.GetRecords())-1]
			lastUpdated, lastID, err := payoutCursor(last)
			if err != nil {
				return total, err
			}
			if err := m.recordPage(ctx, runID); err != nil {
				return total, err
			}
			if err := m.advanceCursor(ctx, "payout", lastUpdated, lastID.String(), runID, backfill && !response.GetHasMore()); err != nil {
				return total, err
			}
			cur = cursorValue{Valid: true, UpdatedAt: lastUpdated, ID: lastID}
		}
		if !response.GetHasMore() {
			break
		}
	}
	if total == 0 && backfill {
		if err := m.markBackfillComplete(ctx, "payout", runID); err != nil {
			return total, err
		}
	}
	if total == 0 {
		if err := m.recordPage(ctx, runID); err != nil {
			return total, err
		}
	}
	recordsScanned.WithLabelValues("payout").Add(float64(total))
	return total, nil
}

func (m *Module) provePayin(ctx context.Context, records []*payinv1.AssuranceRecord, suppressAlerts bool, runID uuid.UUID) error {
	if len(records) == 0 {
		return nil
	}
	if m.ledger == nil {
		return errors.New("ledger assurance client is unavailable")
	}
	selectors := make([]*ledgerv1.AssuranceSelector, 0, len(records))
	for _, record := range records {
		if record.GetLedgerType() == "" || record.GetLedgerGateway() == "" || record.GetLedgerExternalRef() == "" {
			continue
		}
		selectors = append(selectors, &ledgerv1.AssuranceSelector{Token: record.GetId(), Type: record.GetLedgerType(), Gateway: record.GetLedgerGateway(), ExternalRef: record.GetLedgerExternalRef()})
	}
	response, err := m.batchLedger(ctx, selectors)
	if err != nil {
		return err
	}
	proofByToken, err := transactionProofs(response)
	if err != nil {
		return err
	}
	byID := make(map[string]*payinv1.AssuranceRecord, len(records))
	for _, record := range records {
		byID[record.GetId()] = record
	}
	for _, record := range records {
		amount, parseErr := assurancerules.ParseMinor(record.GetAmount())
		if parseErr != nil {
			return parseErr
		}
		value := assurancerules.PayinRecord{ID: record.GetId(), RecordType: record.GetRecordType(), Status: record.GetStatus(), UserID: record.GetUserId(), AmountMinor: amount, Currency: record.GetCurrency(), Vendor: record.GetVendor(), Reference: record.GetReference(), ExternalRef: record.GetExternalRef(), SettledEventID: record.GetSettledEventId(), RequestIDPresent: record.GetRequestIdPresent(), Age: time.Since(record.GetCreatedAt().AsTime()), Ledger: proofByToken[record.GetId()], ConsistencyDelay: m.cfg.ConsistencyDelay}
		if linked := byID[record.GetSettledEventId()]; linked != nil {
			linkedAmount, linkedErr := assurancerules.ParseMinor(linked.GetAmount())
			if linkedErr != nil {
				return linkedErr
			}
			value.SettledWebhook = &assurancerules.PayinRecord{ID: linked.GetId(), RecordType: linked.GetRecordType(), Status: linked.GetStatus(), UserID: linked.GetUserId(), AmountMinor: linkedAmount, Currency: linked.GetCurrency(), Reference: linked.GetReference()}
		}
		seen := map[string]bool{}
		for _, finding := range assurancerules.EvaluatePayin(value) {
			seen[finding.Fingerprint] = true
			opened, err := m.upsertFinding(ctx, Finding{Fingerprint: finding.Fingerprint, Severity: finding.Severity, RuleCode: finding.RuleCode, ResourceID: finding.ResourceID, AmountMinor: finding.AmountMinor, Currency: finding.Currency, Evidence: finding.Evidence}, time.Now(), suppressAlerts)
			if err != nil {
				return fmt.Errorf("persist payin finding: %w", err)
			}
			if opened {
				if err := m.incrementRunFindings(ctx, runID); err != nil {
					return fmt.Errorf("record payin finding transition: %w", err)
				}
			}
		}
		if err := m.resolveResourceFindings(ctx, record.GetId(), seen); err != nil {
			return fmt.Errorf("resolve payin findings: %w", err)
		}
	}
	if err := m.advanceLedgerCursor(ctx, response, runID); err != nil {
		return err
	}
	return nil
}

func (m *Module) provePayout(ctx context.Context, records []*payoutv1.AssuranceRecord, suppressAlerts bool, runID uuid.UUID) error {
	if len(records) == 0 {
		return nil
	}
	if m.ledger == nil {
		return errors.New("ledger assurance client is unavailable")
	}
	selectors := make([]*ledgerv1.AssuranceSelector, 0, len(records)*2)
	for _, record := range records {
		for _, id := range []string{record.GetHoldTxId(), record.GetSettleTxId()} {
			if id == "" {
				continue
			}
			selectors = append(selectors, &ledgerv1.AssuranceSelector{Token: record.GetId(), TransactionId: id})
		}
	}
	response, err := m.batchLedger(ctx, selectors)
	if err != nil {
		return err
	}
	proofByToken, err := transactionProofs(response)
	if err != nil {
		return err
	}
	quoteIDs := make([]string, 0, len(records))
	for _, record := range records {
		if record.GetFeeQuoteId() != "" {
			quoteIDs = append(quoteIDs, record.GetFeeQuoteId())
		}
	}
	feeProofs, err := m.batchLedgerFeeQuotes(ctx, quoteIDs)
	if err != nil {
		return err
	}
	for _, record := range records {
		amount, parseErr := assurancerules.ParseMinor(record.GetAmount())
		if parseErr != nil {
			return parseErr
		}
		value := assurancerules.PayoutRecord{ID: record.GetId(), Status: record.GetStatus(), AmountMinor: amount, Currency: record.GetCurrency(), Vendor: record.GetVendor(), HoldTxID: record.GetHoldTxId(), SettleTxID: record.GetSettleTxId(), FeeQuoteID: record.GetFeeQuoteId(), FeeAmountMinor: parseMinorOrZero(record.GetFeeAmount()), FeeGateway: record.GetFeeGateway(), RequestIDPresent: record.GetRequestIdPresent(), Age: time.Since(record.GetCreatedAt().AsTime())}
		for _, proof := range proofByToken[record.GetId()] {
			if proof.ID == record.GetHoldTxId() {
				copy := proof
				value.Hold = &copy
			}
			if proof.ID == record.GetSettleTxId() {
				copy := proof
				value.Closing = &copy
				value.BookedFeeMinor = proof.BookedFeeMinor
				value.BookedFeeGateway = proof.BookedFeeGateway
			}
		}
		for _, call := range record.GetVendorCalls() {
			value.VendorCalls = append(value.VendorCalls, assurancerules.VendorCall{Attempt: int(call.GetAttempt()), Vendor: call.GetVendor(), Outcome: call.GetOutcome(), At: call.GetCreatedAt().AsTime()})
		}
		for _, command := range record.GetVendorCommands() {
			value.VendorCommands = append(value.VendorCommands, assurancerules.VendorCommand{ID: command.GetId(), Vendor: command.GetVendor(), Attempt: int(command.GetAttempt()), Status: command.GetStatus()})
		}
		if fee, ok := feeProofs[record.GetFeeQuoteId()]; ok {
			value.FeeQuote = &assurancerules.FeeProof{Exists: true, ConsumedByRef: fee.GetConsumedByRef(), AmountMinor: parseMinorOrZero(fee.GetFeeAmount()), Gateway: fee.GetFeeGateway(), TransactionType: fee.GetTransactionType()}
		}
		seen := map[string]bool{}
		for _, finding := range assurancerules.EvaluatePayout(value) {
			seen[finding.Fingerprint] = true
			opened, err := m.upsertFinding(ctx, Finding{Fingerprint: finding.Fingerprint, Severity: finding.Severity, RuleCode: finding.RuleCode, ResourceID: finding.ResourceID, AmountMinor: finding.AmountMinor, Currency: finding.Currency, Evidence: finding.Evidence}, time.Now(), suppressAlerts)
			if err != nil {
				return fmt.Errorf("persist payout finding: %w", err)
			}
			if opened {
				if err := m.incrementRunFindings(ctx, runID); err != nil {
					return fmt.Errorf("record payout finding transition: %w", err)
				}
			}
		}
		if err := m.resolveResourceFindings(ctx, record.GetId(), seen); err != nil {
			return fmt.Errorf("resolve payout findings: %w", err)
		}
	}
	if err := m.advanceLedgerCursor(ctx, response, runID); err != nil {
		return err
	}
	return nil
}

func (m *Module) batchLedger(ctx context.Context, selectors []*ledgerv1.AssuranceSelector) (*ledgerv1.BatchGetAssuranceTransactionsResponse, error) {
	combined := &ledgerv1.BatchGetAssuranceTransactionsResponse{}
	for start := 0; start < len(selectors); start += 500 {
		end := start + 500
		if end > len(selectors) {
			end = len(selectors)
		}
		rpcCtx, cancel := context.WithTimeout(ctx, m.cfg.RPCTimeout)
		response, err := m.ledger.BatchGetAssuranceTransactions(rpcCtx, &ledgerv1.BatchGetAssuranceTransactionsRequest{Selectors: selectors[start:end]})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("ledger assurance RPC: %w", err)
		}
		combined.Results = append(combined.Results, response.GetResults()...)
	}
	return combined, nil
}

func validatePayinPage(records []*payinv1.AssuranceRecord, cutoff time.Time) error {
	var previous time.Time
	var previousID string
	for _, record := range records {
		updated, id, err := payinCursor(record)
		if err != nil {
			return err
		}
		if updated.After(cutoff) {
			return fmt.Errorf("payin assurance record %s is newer than cutoff", id)
		}
		if !previous.IsZero() && (updated.Before(previous) || (updated.Equal(previous) && id.String() <= previousID)) {
			return fmt.Errorf("payin assurance page is not strictly ordered")
		}
		if record.GetCreatedAt() == nil || !record.GetCreatedAt().IsValid() {
			return fmt.Errorf("payin assurance record %s has invalid created_at", id)
		}
		previous, previousID = updated, id.String()
	}
	return nil
}

func validatePayoutPage(records []*payoutv1.AssuranceRecord, cutoff time.Time) error {
	var previous time.Time
	var previousID string
	for _, record := range records {
		updated, id, err := payoutCursor(record)
		if err != nil {
			return err
		}
		if updated.After(cutoff) {
			return fmt.Errorf("payout assurance record %s is newer than cutoff", id)
		}
		if !previous.IsZero() && (updated.Before(previous) || (updated.Equal(previous) && id.String() <= previousID)) {
			return fmt.Errorf("payout assurance page is not strictly ordered")
		}
		if record.GetCreatedAt() == nil || !record.GetCreatedAt().IsValid() {
			return fmt.Errorf("payout assurance record %s has invalid created_at", id)
		}
		previous, previousID = updated, id.String()
	}
	return nil
}

func payinCursor(record *payinv1.AssuranceRecord) (time.Time, uuid.UUID, error) {
	if record == nil || record.GetEffectiveUpdatedAt() == nil || !record.GetEffectiveUpdatedAt().IsValid() {
		return time.Time{}, uuid.Nil, errors.New("payin assurance record has invalid effective_updated_at")
	}
	id, err := uuid.Parse(record.GetId())
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("payin assurance record id: %w", err)
	}
	return record.GetEffectiveUpdatedAt().AsTime(), id, nil
}

func payoutCursor(record *payoutv1.AssuranceRecord) (time.Time, uuid.UUID, error) {
	if record == nil || record.GetEffectiveUpdatedAt() == nil || !record.GetEffectiveUpdatedAt().IsValid() {
		return time.Time{}, uuid.Nil, errors.New("payout assurance record has invalid effective_updated_at")
	}
	id, err := uuid.Parse(record.GetId())
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("payout assurance record id: %w", err)
	}
	return record.GetEffectiveUpdatedAt().AsTime(), id, nil
}

func transactionProofs(response *ledgerv1.BatchGetAssuranceTransactionsResponse) (map[string][]assurancerules.LedgerProof, error) {
	proofs := make(map[string][]assurancerules.LedgerProof)
	for _, result := range response.GetResults() {
		for _, tx := range result.GetTransactions() {
			if tx == nil || tx.GetUpdatedAt() == nil || !tx.GetUpdatedAt().IsValid() {
				return nil, errors.New("ledger assurance transaction has invalid updated_at")
			}
			amount, err := assurancerules.ParseMinor(tx.GetAmount())
			if err != nil {
				return nil, err
			}
			bookedFee, err := assurancerules.ParseMinor(tx.GetBookedFeeAmount())
			if err != nil {
				bookedFee = 0
			}
			proofs[result.GetToken()] = append(proofs[result.GetToken()], assurancerules.LedgerProof{ID: tx.GetId(), Type: tx.GetType(), Status: tx.GetStatus(), AmountMinor: amount, Currency: tx.GetCurrency(), Gateway: tx.GetGateway(), ExternalRef: tx.GetExternalRef(), OriginalReferenceID: tx.GetOriginalReferenceId(), LifecycleCloserID: tx.GetLifecycleCloserId(), BookedFeeMinor: bookedFee, BookedFeeGateway: tx.GetBookedFeeGateway()})
		}
	}
	return proofs, nil
}

func (m *Module) batchLedgerFeeQuotes(ctx context.Context, quoteIDs []string) (map[string]*ledgerv1.FeeQuoteProof, error) {
	result := make(map[string]*ledgerv1.FeeQuoteProof)
	for start := 0; start < len(quoteIDs); start += 500 {
		end := start + 500
		if end > len(quoteIDs) {
			end = len(quoteIDs)
		}
		rpcCtx, cancel := context.WithTimeout(ctx, m.cfg.RPCTimeout)
		response, err := m.ledger.BatchGetAssuranceTransactions(rpcCtx, &ledgerv1.BatchGetAssuranceTransactionsRequest{FeeQuoteIds: quoteIDs[start:end]})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("ledger fee quote assurance RPC: %w", err)
		}
		for _, proof := range response.GetFeeQuoteProofs() {
			result[proof.GetQuoteId()] = proof
		}
	}
	return result, nil
}

func parseMinorOrZero(value string) int64 {
	amount, err := assurancerules.ParseMinor(value)
	if err != nil {
		return 0
	}
	return amount
}

type cursorValue struct {
	Valid     bool
	UpdatedAt time.Time
	ID        uuid.UUID
}

func (m *Module) cursor(ctx context.Context, source string) (cursorValue, error) {
	var updated sql.NullTime
	var id uuid.NullUUID
	if err := m.db.QueryRowContext(ctx, `SELECT updated_at, resource_id FROM assurance_cursors WHERE source=$1`, source).Scan(&updated, &id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cursorValue{}, nil
		}
		return cursorValue{}, fmt.Errorf("read %s cursor: %w", source, err)
	}
	return cursorValue{Valid: updated.Valid && id.Valid, UpdatedAt: updated.Time, ID: id.UUID}, nil
}

func (m *Module) advanceCursor(ctx context.Context, source string, updated time.Time, resourceID string, runID uuid.UUID, backfillComplete bool) error {
	id, err := uuid.Parse(resourceID)
	if err != nil {
		return fmt.Errorf("cursor resource id: %w", err)
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO assurance_cursors (source, updated_at, resource_id, backfill_complete, updated_by_run_id, updated_at_service) VALUES ($1,$2,$3,$4,$5,now()) ON CONFLICT (source) DO UPDATE SET updated_at=EXCLUDED.updated_at, resource_id=EXCLUDED.resource_id, backfill_complete=assurance_cursors.backfill_complete OR EXCLUDED.backfill_complete, updated_by_run_id=EXCLUDED.updated_by_run_id, updated_at_service=now()`, source, updated, id, backfillComplete, runID)
	if err != nil {
		return fmt.Errorf("advance %s cursor: %w", source, err)
	}
	return nil
}

func (m *Module) markBackfillComplete(ctx context.Context, source string, runID uuid.UUID) error {
	_, err := m.db.ExecContext(ctx, `INSERT INTO assurance_cursors (source, backfill_complete, updated_by_run_id, updated_at_service) VALUES ($1,true,$2,now()) ON CONFLICT (source) DO UPDATE SET backfill_complete=true, updated_by_run_id=EXCLUDED.updated_by_run_id, updated_at_service=now()`, source, runID)
	if err != nil {
		return fmt.Errorf("mark %s backfill complete: %w", source, err)
	}
	return nil
}

func (m *Module) recordPage(ctx context.Context, runID uuid.UUID) error {
	_, err := m.db.ExecContext(ctx, `UPDATE assurance_runs SET pages_scanned=pages_scanned+1 WHERE id=$1`, runID)
	if err != nil {
		return fmt.Errorf("record assurance page: %w", err)
	}
	return nil
}

func (m *Module) incrementRunFindings(ctx context.Context, runID uuid.UUID) error {
	_, err := m.db.ExecContext(ctx, `UPDATE assurance_runs SET findings_opened=findings_opened+1 WHERE id=$1`, runID)
	return err
}

func (m *Module) advanceLedgerCursor(ctx context.Context, response *ledgerv1.BatchGetAssuranceTransactionsResponse, runID uuid.UUID) error {
	var latest time.Time
	var latestID string
	for _, result := range response.GetResults() {
		for _, transaction := range result.GetTransactions() {
			updated := transaction.GetUpdatedAt().AsTime()
			if latest.IsZero() || updated.After(latest) || (updated.Equal(latest) && transaction.GetId() > latestID) {
				latest = updated
				latestID = transaction.GetId()
			}
		}
	}
	if latest.IsZero() || latestID == "" {
		return nil
	}
	return m.advanceCursor(ctx, "ledger", latest, latestID, runID, false)
}

// Finding is the persistence-safe representation used by the rule engine.
type Finding struct {
	Fingerprint string
	Severity    string
	RuleCode    string
	ResourceID  string
	AmountMinor int64
	Currency    string
	Evidence    map[string]string
}

func (m *Module) UpsertFinding(ctx context.Context, finding Finding, seenAt time.Time) error {
	_, err := m.upsertFinding(ctx, finding, seenAt, false)
	return err
}

func (m *Module) resolveResourceFindings(ctx context.Context, resourceID string, seen map[string]bool) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id, fingerprint FROM assurance_findings WHERE resource_id=$1 AND status IN ('open','acknowledged')`, resourceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type findingRef struct {
		id          uuid.UUID
		fingerprint string
	}
	var refs []findingRef
	for rows.Next() {
		var ref findingRef
		if err := rows.Scan(&ref.id, &ref.fingerprint); err != nil {
			return err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, ref := range refs {
		if seen[ref.fingerprint] {
			continue
		}
		if _, err := m.db.ExecContext(ctx, `UPDATE assurance_findings SET status='resolved', resolved_at=now() WHERE id=$1 AND status IN ('open','acknowledged')`, ref.id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) upsertFinding(ctx context.Context, finding Finding, seenAt time.Time, suppressAlert bool) (bool, error) {
	if finding.Fingerprint == "" || finding.RuleCode == "" || finding.ResourceID == "" {
		return false, errors.New("finding fingerprint, rule code, and resource id are required")
	}
	evidence, err := json.Marshal(finding.Evidence)
	if err != nil {
		return false, fmt.Errorf("marshal finding evidence: %w", err)
	}
	var existingID uuid.UUID
	var existingStatus, existingSeverity string
	var existingBaseline bool
	existingErr := m.db.QueryRowContext(ctx, `SELECT id, status, severity, baseline FROM assurance_findings WHERE fingerprint=$1`, finding.Fingerprint).Scan(&existingID, &existingStatus, &existingSeverity, &existingBaseline)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return false, fmt.Errorf("read finding state: %w", existingErr)
	}
	isNew := errors.Is(existingErr, sql.ErrNoRows)
	findingID := existingID
	if isNew {
		findingID = uuid.New()
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO assurance_findings (id, fingerprint, severity, rule_code, resource_id, amount_minor, currency, evidence, first_seen_at, last_seen_at, occurrence_count, status, baseline) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,1,'open',$10) ON CONFLICT (fingerprint) DO UPDATE SET severity=EXCLUDED.severity, amount_minor=EXCLUDED.amount_minor, currency=EXCLUDED.currency, evidence=EXCLUDED.evidence, last_seen_at=EXCLUDED.last_seen_at, occurrence_count=assurance_findings.occurrence_count+1, status=CASE WHEN assurance_findings.status='resolved' THEN 'open' ELSE assurance_findings.status END, resolved_at=CASE WHEN assurance_findings.status='resolved' THEN NULL ELSE assurance_findings.resolved_at END, baseline=EXCLUDED.baseline`, findingID, finding.Fingerprint, finding.Severity, finding.RuleCode, finding.ResourceID, finding.AmountMinor, finding.Currency, evidence, seenAt, suppressAlert)
	if err != nil {
		return false, err
	}
	shouldAlert := !suppressAlert && (isNew || existingStatus == "resolved" || existingBaseline || severityRank(finding.Severity) > severityRank(existingSeverity))
	if shouldAlert {
		message := fmt.Sprintf("assurance finding %s rule=%s resource=%s amount=%d currency=%s", finding.Severity, finding.RuleCode, finding.ResourceID, finding.AmountMinor, finding.Currency)
		if _, err := m.db.ExecContext(ctx, `INSERT INTO assurance_alert_deliveries (id, finding_id, severity, message, status) VALUES ($1,$2,$3,$4,'pending')`, uuid.New(), findingID, finding.Severity, message); err != nil {
			return false, fmt.Errorf("queue assurance alert: %w", err)
		}
	}
	return shouldAlert, nil
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}

func (m *Module) dispatchAlerts(ctx context.Context) error {
	if m.alertFn == nil {
		return nil
	}
	rows, err := m.db.QueryContext(ctx, `SELECT id, severity, message, attempts FROM assurance_alert_deliveries WHERE status='pending' AND next_attempt_at <= now() ORDER BY created_at, id LIMIT 50`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type delivery struct {
		id       uuid.UUID
		severity string
		message  string
		attempts int
	}
	var deliveries []delivery
	for rows.Next() {
		var item delivery
		if err := rows.Scan(&item.id, &item.severity, &item.message, &item.attempts); err != nil {
			return err
		}
		deliveries = append(deliveries, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range deliveries {
		if err := m.alertFn(ctx, item.severity, item.message); err != nil {
			alertDeliveries.WithLabelValues("failed", item.severity).Inc()
			backoff := time.Duration(1<<min(item.attempts, 6)) * time.Minute
			_, _ = m.db.ExecContext(ctx, `UPDATE assurance_alert_deliveries SET status='pending', attempts=attempts+1, next_attempt_at=now()+($2 * interval '1 second'), last_error=$3 WHERE id=$1`, item.id, backoff.Seconds(), err.Error())
			continue
		}
		alertDeliveries.WithLabelValues("delivered", item.severity).Inc()
		_, _ = m.db.ExecContext(ctx, `UPDATE assurance_alert_deliveries SET status='delivered', attempts=attempts+1, delivered_at=now(), last_error='' WHERE id=$1`, item.id)
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
