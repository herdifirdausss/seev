package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

const outboxLagThreshold = 5 * time.Minute

// Verifier runs scheduled integrity checks against the ledger's own
// verification functions (fn_verify_ledger_balance, v_account_balance_audit)
// and the outbox queue. It never repairs anything automatically — it only
// detects and logs/alerts (docs/plan/06 Task 1c.2).
type Verifier struct {
	verifyRepo repository.VerificationRepository
	outboxRepo repository.OutboxRepository
	logger     *slog.Logger
	sched      *scheduler.Scheduler
	loc        *time.Location
	// alertFn is called alongside logger.Error for every discrepancy found
	// by checkTrialBalance/checkProjectionAudit (docs/plan/12 Task T4). May
	// be nil — every call site nil-checks before invoking it, so an
	// unconfigured ALERT_WEBHOOK_URL is a true no-op (backward compatible
	// with pre-T4 behavior: log + metric only). A failed alert delivery is
	// logged and otherwise ignored — it must never stop the verifier from
	// continuing to run its checks.
	alertFn alerting.AlertFunc
}

// NewVerifier constructs a Verifier. lock should be scheduler.NewRedisLock in
// production (so only one replica runs each check) or scheduler.NewMemoryLock
// for a single-instance deployment. alertFn may be nil (no external alert,
// log+metric only — see docs/plan/12 Task T4).
func NewVerifier(verifyRepo repository.VerificationRepository, outboxRepo repository.OutboxRepository, lock scheduler.LockProvider, logger *slog.Logger, loc *time.Location, alertFn alerting.AlertFunc) *Verifier {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &Verifier{verifyRepo: verifyRepo, outboxRepo: outboxRepo, logger: logger, loc: loc, alertFn: alertFn,
		sched: scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc))}
}

// alert fires v.alertFn if configured, logging (not propagating) any
// delivery failure — a broken alert channel must never interrupt the
// verifier's own checks (docs/plan/12 Task T4).
func (v *Verifier) alert(ctx context.Context, severity, message string) {
	if v.alertFn == nil {
		return
	}
	if err := v.alertFn(ctx, severity, message); err != nil {
		v.logger.Error("ledger integrity: alert delivery failed", slog.Any("error", err))
	}
}

// Start registers the three scheduled checks. Call Stop to shut down.
func (v *Verifier) Start() error {
	if err := v.sched.Cron("ledger-trial-balance", "0 * * * *", v.checkTrialBalance); err != nil {
		return err
	}
	if err := v.sched.Cron("ledger-projection-audit", "0 2 * * *", v.checkProjectionAudit); err != nil {
		return err
	}
	if err := v.sched.Cron("ledger-outbox-lag", "*/5 * * * *", v.checkOutboxLag); err != nil {
		return err
	}
	return nil
}

// Stop stops the underlying scheduler, waiting for any in-flight check.
func (v *Verifier) Stop() {
	v.sched.Stop()
}

// checkTrialBalance proves Σdebit == Σcredit for every transaction posted in
// the last 2 hours (fn_verify_ledger_balance returns ONLY the unbalanced
// ones — any row here is a serious bug and must be investigated).
func (v *Verifier) checkTrialBalance(ctx context.Context) error {
	discrepancies, err := v.verifyRepo.TrialBalanceDiscrepancies(ctx)
	if err != nil {
		return err
	}

	for _, d := range discrepancies {
		v.logger.Error("ledger integrity: unbalanced transaction detected",
			slog.String("transaction_id", d.TransactionID),
			slog.Int64("sum_debit", d.SumDebit), slog.Int64("sum_credit", d.SumCredit), slog.Int64("diff", d.Diff))
		v.alert(ctx, "critical", fmt.Sprintf(
			"unbalanced transaction detected: transaction_id=%s sum_debit=%d sum_credit=%d diff=%d",
			d.TransactionID, d.SumDebit, d.SumCredit, d.Diff))
	}
	if len(discrepancies) > 0 {
		verificationDiscrepanciesTotal.WithLabelValues("trial_balance").Add(float64(len(discrepancies)))
	}
	return nil
}

// checkProjectionAudit proves account_balances.balance matches the balance
// computed from ledger_entries, for every account that moved in the last 24h.
func (v *Verifier) checkProjectionAudit(ctx context.Context) error {
	discrepancies, err := v.verifyRepo.ProjectionDiscrepancies(ctx)
	if err != nil {
		return err
	}

	for _, d := range discrepancies {
		v.logger.Error("ledger integrity: balance projection inconsistent",
			slog.String("account_id", d.AccountID),
			slog.Int64("stored_balance", d.StoredBalance), slog.Int64("computed_balance", d.ComputedBalance))
		v.alert(ctx, "critical", fmt.Sprintf(
			"balance projection inconsistent: account_id=%s stored_balance=%d computed_balance=%d",
			d.AccountID, d.StoredBalance, d.ComputedBalance))
	}
	if len(discrepancies) > 0 {
		verificationDiscrepanciesTotal.WithLabelValues("projection").Add(float64(len(discrepancies)))
	}
	return nil
}

// checkOutboxLag warns when the oldest pending outbox event has been
// waiting longer than outboxLagThreshold — a sign the relay isn't keeping up
// or has stalled.
func (v *Verifier) checkOutboxLag(ctx context.Context) error {
	count, oldest, err := v.outboxRepo.CountPending(ctx)
	if err != nil {
		return err
	}
	if count == 0 || oldest.IsZero() {
		return nil
	}
	lag := time.Since(oldest)
	if lag > outboxLagThreshold {
		verificationDiscrepanciesTotal.WithLabelValues("outbox_lag").Inc()
		v.logger.Warn("ledger integrity: outbox lag exceeds threshold",
			slog.Int("pending_count", count), slog.Duration("oldest_pending_age", lag))
	}
	return nil
}
