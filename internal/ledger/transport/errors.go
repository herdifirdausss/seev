package transport

import (
	"errors"
	"net/http"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/pkg/response"
)

// writeError maps a service/repository error to the appropriate HTTP
// response, per the table in docs/plan/05 Task 1b.4. Internal error detail
// never reaches the client body — only apperror.* is client-safe.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, apperror.ErrValidation),
		errors.Is(err, apperror.ErrEmptyIdempotencyKey),
		errors.Is(err, apperror.ErrUnknownProcessor),
		errors.Is(err, apperror.ErrSelfTransfer),
		errors.Is(err, apperror.ErrStatementRangeTooLarge),
		errors.Is(err, apperror.ErrCSVTooManyRows):
		response.BadRequest(w, err.Error())

	case errors.Is(err, apperror.ErrSelfApproval):
		response.Forbidden(w, err.Error())

	case errors.Is(err, apperror.ErrAccountNotFound),
		errors.Is(err, apperror.ErrTransactionNotFound),
		errors.Is(err, apperror.ErrOriginalNotFound),
		errors.Is(err, apperror.ErrOutboxEventNotFound),
		errors.Is(err, apperror.ErrPendingAdjustmentNotFound),
		errors.Is(err, apperror.ErrReconBatchNotFound),
		errors.Is(err, apperror.ErrReconItemNotFound):
		response.NotFound(w, err.Error())

	case errors.Is(err, apperror.ErrInsufficientFunds),
		errors.Is(err, apperror.ErrAccountSuspended),
		errors.Is(err, apperror.ErrAccountClosed),
		errors.Is(err, apperror.ErrCurrencyMismatch),
		errors.Is(err, apperror.ErrAmountTooSmall),
		errors.Is(err, apperror.ErrAmountTooLarge),
		errors.Is(err, apperror.ErrDailyLimitExceeded),
		errors.Is(err, apperror.ErrFeeExceedsAmount),
		errors.Is(err, apperror.ErrAlreadyReversed),
		errors.Is(err, apperror.ErrNotReversible),
		errors.Is(err, apperror.ErrAlreadyClosed),
		errors.Is(err, apperror.ErrOriginalTypeMismatch),
		errors.Is(err, apperror.ErrLifecycleAmountMismatch),
		errors.Is(err, apperror.ErrUnbalancedEntries),
		errors.Is(err, apperror.ErrScreeningBlocked),
		errors.Is(err, apperror.ErrQuoteExpired),
		errors.Is(err, apperror.ErrQuoteMismatch):
		response.UnprocessableEntity(w, err.Error())

	case errors.Is(err, apperror.ErrStillProcessing),
		errors.Is(err, apperror.ErrPreviousFailed),
		errors.Is(err, apperror.ErrAdjustmentAlreadyDecided),
		errors.Is(err, apperror.ErrReconItemAlreadyResolved):
		response.Conflict(w, err.Error())

	default:
		response.InternalServerError(w, err)
	}
}
