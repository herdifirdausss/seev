// Package transport is the HTTP transport layer for the ledger module. It
// depends only on sibling subpackages (processors, model, apperror) — never
// on the ledger root package — so ledger.Module can depend on transport
// without an import cycle (docs/plan/05 Task 1b.4).
package transport

//go:generate mockgen -source=service.go -destination=service_mock.go -package=transport

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
)

// Service is the subset of ledger.Module's behavior the HTTP layer needs.
// ledger.Module satisfies this interface structurally.
type Service interface {
	Post(ctx context.Context, cmd processors.Command) error

	ProvisionUser(ctx context.Context, userID uuid.UUID, currency string) ([]model.Account, error)
	CreatePocket(ctx context.Context, userID uuid.UUID, currency, pocketCode string) (model.Account, error)
	ListAccounts(ctx context.Context, userID uuid.UUID) ([]model.Account, error)
	// GetUserCurrency resolves the currency of a user's cash (or, if
	// pocketCode is non-empty, pocket) account — used by the public
	// router's fee policy (docs/plan/18 Task T2) to pick the right
	// (type, gateway, currency) fee rule before ResolveAccounts runs.
	GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error)
	GetBalance(ctx context.Context, accountID uuid.UUID) (model.AccountBalance, error)
	// GetBalanceAsOf returns an account's balance at the end of a past
	// calendar day, computed from a snapshot + delta rather than a full
	// replay (docs/plan/15 Task T1).
	GetBalanceAsOf(ctx context.Context, accountID uuid.UUID, asOf time.Time) (model.AccountBalance, error)
	GetTransaction(ctx context.Context, txID uuid.UUID) (model.LedgerTransaction, error)
	ListEntries(ctx context.Context, accountID uuid.UUID, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) ([]model.LedgerEntry, error)
	// Statement returns an account's opening/closing balance and entries for
	// a period (docs/plan/15 Task T2).
	Statement(ctx context.Context, accountID uuid.UUID, from, to time.Time) (model.Statement, error)

	// CanAccessAccount reports whether userID owns accountID.
	CanAccessAccount(ctx context.Context, accountID, userID uuid.UUID) (bool, error)
	// CanAccessTransaction reports whether userID owns any account touched
	// by the transaction.
	CanAccessTransaction(ctx context.Context, txID, userID uuid.UUID) (bool, error)

	// ReplayDeadEvent and ReplayDeadEvents back the admin outbox
	// dead-letter replay endpoints (docs/plan/12 Task T3) — internal
	// router only, admin-gated.
	ReplayDeadEvent(ctx context.Context, id uuid.UUID) error
	ReplayDeadEvents(ctx context.Context, olderThan time.Time) (int, error)
	// ListDeadOutboxEvents backs the admin outbox dead-letter list endpoint
	// (docs/plan/25 Task T5) — internal router only, admin-gated.
	ListDeadOutboxEvents(ctx context.Context, limit, offset int) ([]model.DeadOutboxEvent, error)

	// CreateAdjustment/ApproveAdjustment/RejectAdjustment/GetAdjustment/
	// ListAdjustments back the maker-checker adjustment endpoints
	// (docs/plan/16 Task T1) — internal router only, admin-gated. This is
	// the ONLY reachable path to adjustment_credit/adjustment_debit; direct
	// POST /transactions with those types is rejected (decision K8).
	CreateAdjustment(ctx context.Context, requestedBy, adjType string, amount decimal.Decimal, targetUserID uuid.UUID, metadata map[string]any, reason string) (uuid.UUID, error)
	ApproveAdjustment(ctx context.Context, id uuid.UUID, approverID string) (uuid.UUID, error)
	RejectAdjustment(ctx context.Context, id uuid.UUID, approverID string) error
	GetAdjustment(ctx context.Context, id uuid.UUID) (model.PendingAdjustment, error)
	ListAdjustments(ctx context.Context, status string, limit int) ([]model.PendingAdjustment, error)

	// ImportReconBatch/GetReconBatchReport/ResolveReconItem back the
	// reconciliation endpoints (docs/plan/16 Task T2) — internal router
	// only, admin-gated. ResolveReconItem creates a pending adjustment via
	// the same maker-checker path as CreateAdjustment — it never moves
	// money by itself.
	ImportReconBatch(ctx context.Context, gateway string, reportDate time.Time, filename string, rows []model.ReconImportRow, createdBy string) (uuid.UUID, error)
	GetReconBatchReport(ctx context.Context, batchID uuid.UUID, matchStatus string, limit, offset int) (model.ReconBatchReport, error)
	ResolveReconItem(ctx context.Context, itemID uuid.UUID, requestedBy, adjType string, amount decimal.Decimal, reason string) (uuid.UUID, error)
	// ListReconBatches backs the admin recon batch list endpoint
	// (docs/plan/25 Task T5) — internal router only, admin-gated.
	ListReconBatches(ctx context.Context, limit, offset int) ([]model.ReconBatch, error)

	// CreateSchedule/ListSchedules/PauseSchedule/ResumeSchedule/CancelSchedule
	// back the scheduled-transaction endpoints (docs/plan/19 Task T1) —
	// public router, a user manages only their own schedules.
	// RunSchedulesNow is the internal-router-only, admin-gated ops/testing
	// trigger for the daily schedule runner.
	CreateSchedule(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal, targetUserID uuid.UUID, pocketCode string, metadata map[string]any, kind string, runAtDate time.Time, dayOfMonth *int, createdBy string) (uuid.UUID, error)
	ListSchedules(ctx context.Context, userID uuid.UUID) ([]model.ScheduledTransaction, error)
	PauseSchedule(ctx context.Context, id, userID uuid.UUID) error
	ResumeSchedule(ctx context.Context, id, userID uuid.UUID) error
	CancelSchedule(ctx context.Context, id, userID uuid.UUID) error
	RunSchedulesNow(ctx context.Context, asOf time.Time) (executed, failed int, err error)

	// ImportDisbursementBatch/RunDisbursement/GetDisbursementReport back
	// batch disbursement (docs/plan/19 Task T2) — internal router only,
	// admin-gated in every handler.
	ImportDisbursementBatch(ctx context.Context, filename string, rows []model.DisbursementImportRow, createdBy string) (uuid.UUID, error)
	RunDisbursement(ctx context.Context, batchID uuid.UUID, retryFailed bool) (model.DisbursementRunResult, error)
	GetDisbursementReport(ctx context.Context, batchID uuid.UUID, status string, limit, offset int) (model.DisbursementBatchReport, error)

	// SetSavingsConfig/ListSavingsConfigs back the interest accrual admin
	// endpoints (docs/plan/19 Task T3) — internal router only, admin-gated.
	SetSavingsConfig(ctx context.Context, accountID uuid.UUID, annualRateBps int, enabled bool) error
	ListSavingsConfigs(ctx context.Context) ([]model.SavingsConfig, error)

	// GetDailyPositionReport/GetDailyMutationReport/GetReconSummaryReport
	// back the regulatory reporting endpoint (docs/plan/20 Task T2) —
	// internal router only, admin-gated, read-only over the three
	// migrations/000018 views.
	GetDailyPositionReport(ctx context.Context, from, to time.Time) ([]model.ReportDailyPosition, error)
	GetDailyMutationReport(ctx context.Context, from, to time.Time) ([]model.ReportDailyMutation, error)
	GetReconSummaryReport(ctx context.Context, from, to time.Time) ([]model.ReportReconSummary, error)
}
