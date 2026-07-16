package apperror

import (
	"errors"
	"fmt"
)

// LedgerError is the structured error type for this package. [FIX #13]
// Callers use errors.As() to extract Code, Retryable flag, and HTTP status hints.
type LedgerError struct {
	Code      string
	Message   string
	Retryable bool
	Err       error
}

func (e *LedgerError) Error() string { return fmt.Sprintf("[%s] %s", e.Code, e.Message) }
func (e *LedgerError) Unwrap() error { return e.Err }

func NewBizErr(sentinel error, msg string) *LedgerError {
	return &LedgerError{Code: sentinel.Error(), Message: msg, Err: sentinel}
}

// Business sentinels: tx committed with status=failed, audit trail exists.
var (
	ErrInsufficientFunds  = errors.New("INSUFFICIENT_FUNDS")
	ErrAccountSuspended   = errors.New("ACCOUNT_SUSPENDED")
	ErrAccountClosed      = errors.New("ACCOUNT_CLOSED")
	ErrSelfTransfer       = errors.New("SELF_TRANSFER")
	ErrAmountTooSmall     = errors.New("AMOUNT_TOO_SMALL")
	ErrAmountTooLarge     = errors.New("AMOUNT_TOO_LARGE")
	ErrDailyLimitExceeded = errors.New("DAILY_LIMIT_EXCEEDED")
	ErrOriginalNotFound   = errors.New("ORIGINAL_TX_NOT_FOUND")
	ErrAlreadyReversed    = errors.New("ALREADY_REVERSED")
	ErrNotReversible      = errors.New("NOT_REVERSIBLE")
	ErrFeeExceedsAmount   = errors.New("FEE_EXCEEDS_AMOUNT")
	// ErrAlreadyClosed is returned when a lifecycle transaction (settle,
	// cancel, release, refund, reversal) targets an original transaction
	// that another transaction already closed first — either a genuine
	// double-attempt or the losing side of a race between two concurrent
	// closers (docs/plan/14 Task T2, decision K3). The atomic
	// TransactionRepository.CloseOriginal UPDATE is what makes this
	// race-proof; this sentinel is what the losing side gets back.
	ErrAlreadyClosed = errors.New("ALREADY_CLOSED")
	// ErrOriginalTypeMismatch is returned when a lifecycle transaction's
	// ReferenceID points at a transaction of the wrong type for that
	// operation (e.g. withdraw_settle referencing something other than a
	// withdraw_initiate) — closing the wrong kind of original silently would
	// corrupt the lifecycle state machine.
	ErrOriginalTypeMismatch = errors.New("ORIGINAL_TYPE_MISMATCH")
	// ErrLifecycleAmountMismatch is returned when a lifecycle transaction's
	// amount doesn't equal the original transaction's amount. MVP only
	// supports full-amount settle/cancel/release/refund/reversal — partial
	// amounts would require a sub-ledger of holds this schema doesn't have
	// (docs/plan/13, decision K3).
	ErrLifecycleAmountMismatch = errors.New("LIFECYCLE_AMOUNT_MISMATCH")
	// ErrSelfApproval is returned when the identity approving or rejecting a
	// pending adjustment matches the identity that requested it
	// (docs/plan/16 Task T1, decision K8) — checked in Go for a clear error
	// message; the DB CHECK constraint on pending_adjustments is the
	// backstop that holds even if this check is bypassed.
	ErrSelfApproval = errors.New("SELF_APPROVAL")
	// ErrAdjustmentAlreadyDecided is returned when an approve/reject targets
	// a pending_adjustments row no longer in status='pending' — either a
	// genuine double-decision or the losing side of a race between two
	// concurrent approvers (same atomic-UPDATE pattern as ErrAlreadyClosed,
	// docs/plan/14 Task T2 decision K3).
	ErrAdjustmentAlreadyDecided = errors.New("ADJUSTMENT_ALREADY_DECIDED")
	// ErrReconItemAlreadyResolved is returned when a resolve request targets
	// a recon_items row that already has resolved_by_adjustment_id set
	// (docs/plan/16 Task T2) — checked in Go via the atomic UPDATE ...
	// WHERE resolved_by_adjustment_id IS NULL guard (same K3 pattern as
	// ErrAlreadyClosed/ErrAdjustmentAlreadyDecided), so a double-resolve
	// (retry, or a race between two ops) never creates two pending
	// adjustments for the same discrepancy.
	ErrReconItemAlreadyResolved = errors.New("RECON_ITEM_ALREADY_RESOLVED")
	// ErrScreeningBlocked is returned when fraud-service's Verdict.Block is
	// true in 'block' mode. Originally raised from inside the posting
	// transaction (docs/plan/20 Task T1); docs/plan/37 moved the screening
	// call to the transport layer, BEFORE any transaction opens — the
	// error/HTTP-contract stays identical (transport maps it the same way),
	// only the timing of when it's raised changed: no partial
	// ledger_transactions row is ever created for a blocked attempt now,
	// since screening happens before Handle is even called.
	ErrScreeningBlocked = errors.New("SCREENING_BLOCKED")
	// ErrQuoteExpired is returned when a quote_id (docs/plan/38 Task T4)
	// doesn't resolve to a consumable row — not found, already consumed, or
	// past expires_at are deliberately collapsed into this single sentinel
	// (see feepolicy.ErrQuoteExpired's own doc comment for why: none of the
	// three is actionable differently by the client than "request a new
	// quote"). The posting transaction is rolled back entirely — no
	// ledger_transactions row exists for a rejected quote consumption
	// attempt, unlike a processor business-validation failure.
	ErrQuoteExpired = errors.New("QUOTE_EXPIRED")
	// ErrQuoteMismatch is returned when a quote_id resolves to a valid,
	// unconsumed, unexpired row, but the transaction attempting to consume
	// it doesn't match what was quoted (type/currency/amount differs).
	// Deliberately does NOT burn the quote — see
	// feepolicy.ErrQuoteMismatch's own doc comment.
	ErrQuoteMismatch = errors.New("QUOTE_MISMATCH")
	// ErrUnknownKycTier is returned by ApplyKycTier (docs/plan/39 Task T5)
	// when kyc_level matches zero rows in policy_tier_limits — a caller
	// input error (InvalidArgument at the gRPC layer), not a business-state
	// failure, since valid levels are DB-driven, not hardcoded in Go.
	ErrUnknownKycTier = errors.New("UNKNOWN_KYC_TIER")
)

// Structural sentinels: programming/infra errors, tx rolled back.
var (
	ErrValidation          = errors.New("VALIDATION_ERROR")
	ErrAccountNotFound     = errors.New("ACCOUNT_NOT_FOUND")
	ErrTransactionNotFound = errors.New("TRANSACTION_NOT_FOUND")
	ErrCurrencyMismatch    = errors.New("CURRENCY_MISMATCH")
	ErrUnknownProcessor    = errors.New("UNKNOWN_TX_TYPE")
	ErrUnbalancedEntries   = errors.New("UNBALANCED_ENTRIES")
	ErrEmptyIdempotencyKey = errors.New("EMPTY_IDEMPOTENCY_KEY")
	// ErrStatementRangeTooLarge is returned when a statement request
	// (docs/plan/15 Task T2) would return more than the configured row cap.
	// Never silently truncated — a statement that's quietly missing entries
	// is a financial bug, not a UX nicety, so this always surfaces as a
	// clear error asking the caller to narrow the period.
	ErrStatementRangeTooLarge = errors.New("STATEMENT_RANGE_TOO_LARGE")
	// ErrOutboxEventNotFound is returned when an admin outbox-replay request
	// (docs/plan/12 Task T3) targets an id that doesn't exist or isn't
	// currently in 'dead' status — replaying a 'pending'/'failed'/
	// 'published' event makes no sense, so it's treated the same as
	// not-found rather than silently no-op'd.
	ErrOutboxEventNotFound = errors.New("OUTBOX_EVENT_NOT_FOUND")
	// ErrPendingAdjustmentNotFound is returned when an approve/reject/get
	// request targets an id that doesn't exist in pending_adjustments.
	ErrPendingAdjustmentNotFound = errors.New("PENDING_ADJUSTMENT_NOT_FOUND")
	// ErrReconBatchNotFound / ErrReconItemNotFound (docs/plan/16 Task T2):
	// an admin recon request targets an id that doesn't exist.
	ErrReconBatchNotFound = errors.New("RECON_BATCH_NOT_FOUND")
	ErrReconItemNotFound  = errors.New("RECON_ITEM_NOT_FOUND")
	// ErrCSVTooManyRows is returned when an imported settlement CSV exceeds
	// the per-batch row cap (docs/plan/16 Task T2 step 3) — the caller must
	// split the file, never silently processed partially.
	ErrCSVTooManyRows = errors.New("CSV_TOO_MANY_ROWS")
	// ErrScheduledTransactionNotFound is returned when a pause/resume/cancel
	// request targets an id that doesn't exist (docs/plan/19 Task T1).
	ErrScheduledTransactionNotFound = errors.New("SCHEDULED_TRANSACTION_NOT_FOUND")
	// ErrScheduledTransactionNotOwned is returned when a caller tries to
	// pause/resume/cancel a schedule that belongs to a different user
	// (docs/plan/19 Task T1) — treated as not-found rather than forbidden,
	// same information-disclosure reasoning as CanAccessAccount elsewhere in
	// this module (don't confirm existence of another user's resource).
	ErrScheduledTransactionNotOwned = errors.New("SCHEDULED_TRANSACTION_NOT_OWNED")
	// ErrScheduledTransactionAlreadyTerminal is returned when
	// pause/resume/cancel targets a schedule not in the expected starting
	// status (already paused/cancelled/finished/failed, or the losing side
	// of a concurrent request) — same atomic-UPDATE K3 pattern as
	// ErrAdjustmentAlreadyDecided.
	ErrScheduledTransactionAlreadyTerminal = errors.New("SCHEDULED_TRANSACTION_ALREADY_TERMINAL")
	// ErrDisbursementBatchNotFound is returned when a run/resume/report
	// request targets a batch id that doesn't exist (docs/plan/19 Task T2).
	ErrDisbursementBatchNotFound = errors.New("DISBURSEMENT_BATCH_NOT_FOUND")
	// ErrSavingsConfigNotFound is returned when a savings config lookup
	// targets an account id that was never registered (docs/plan/19 Task T3).
	ErrSavingsConfigNotFound = errors.New("SAVINGS_CONFIG_NOT_FOUND")
)

// Idempotency sentinels.
var (
	ErrAlreadyPosted   = errors.New("ALREADY_POSTED")
	ErrPreviousFailed  = errors.New("PREVIOUS_ATTEMPT_FAILED")
	ErrStillProcessing = errors.New("STILL_PROCESSING")
)
