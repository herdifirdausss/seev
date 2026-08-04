package transport

import (
	"errors"
	"net/http"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
)

// writeError maps a service/repository error to the appropriate HTTP
// response, per the table in docs/roadmap/archive/05 Task 1b.4. Internal error detail
// never reaches the client body — only apperror.* is client-safe.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, apperror.ErrValidation),
		errors.Is(err, apperror.ErrCurrencyInvalid),
		errors.Is(err, apperror.ErrEmptyIdempotencyKey),
		errors.Is(err, apperror.ErrUnknownProcessor),
		errors.Is(err, apperror.ErrSelfTransfer),
		errors.Is(err, apperror.ErrStatementRangeTooLarge),
		errors.Is(err, apperror.ErrCSVTooManyRows),
		errors.Is(err, apperror.ErrCurrencyNotEnabled),
		errors.Is(err, apperror.ErrFXRateInvalid):
		response.BadRequest(w, err.Error())

	case errors.Is(err, apperror.ErrSelfApproval):
		response.Forbidden(w, err.Error())

	case errors.Is(err, apperror.ErrAccountNotFound),
		errors.Is(err, apperror.ErrCurrencyAccountMissing),
		errors.Is(err, apperror.ErrTransactionNotFound),
		errors.Is(err, apperror.ErrOriginalNotFound),
		errors.Is(err, apperror.ErrOutboxEventNotFound),
		errors.Is(err, apperror.ErrPendingAdjustmentNotFound),
		errors.Is(err, apperror.ErrReconBatchNotFound),
		errors.Is(err, apperror.ErrReconItemNotFound),
		errors.Is(err, apperror.ErrChargebackDisputeNotFound),
		errors.Is(err, apperror.ErrFXQuoteNotFound),
		errors.Is(err, apperror.ErrFXConversionNotFound),
		errors.Is(err, apperror.ErrFXRateNotFound):
		response.NotFound(w, err.Error())

	case errors.Is(err, apperror.ErrInsufficientFunds),
		errors.Is(err, apperror.ErrAccountSuspended),
		errors.Is(err, apperror.ErrAccountClosed),
		errors.Is(err, apperror.ErrCurrencyMismatch),
		errors.Is(err, apperror.ErrCrossCurrencyTransferRequiresFX),
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
		errors.Is(err, apperror.ErrQuoteMismatch),
		errors.Is(err, apperror.ErrCurrencyDisabled),
		errors.Is(err, apperror.ErrCurrencyOperationDisabled),
		errors.Is(err, apperror.ErrCurrencyAccountInactive),
		errors.Is(err, apperror.ErrCurrencySystemAccountMissing),
		errors.Is(err, apperror.ErrCurrencyRouteUnavailable),
		errors.Is(err, apperror.ErrCurrencyLimitExceeded),
		errors.Is(err, apperror.ErrFXPairUnavailable),
		errors.Is(err, apperror.ErrFXDirectionDisabled),
		errors.Is(err, apperror.ErrFXRateUnavailable),
		errors.Is(err, apperror.ErrFXQuoteExpired),
		errors.Is(err, apperror.ErrFXQuoteAlreadyConsumed),
		errors.Is(err, apperror.ErrFXQuoteMismatch),
		errors.Is(err, apperror.ErrFXRateApprovalConflict),
		errors.Is(err, apperror.ErrFXConversionsPaused),
		errors.Is(err, apperror.ErrFXTargetAmountZero),
		errors.Is(err, apperror.ErrFXPositionLimitExceeded),
		errors.Is(err, apperror.ErrPolicyLimitExceeded),
		errors.Is(err, apperror.ErrMoneyOverflow):
		response.UnprocessableEntity(w, err.Error())

	case errors.Is(err, apperror.ErrStillProcessing),
		errors.Is(err, apperror.ErrPreviousFailed),
		errors.Is(err, apperror.ErrAdjustmentAlreadyDecided),
		errors.Is(err, apperror.ErrReconItemAlreadyResolved),
		errors.Is(err, apperror.ErrIdempotencyConflict),
		errors.Is(err, apperror.ErrChargebackDisputeAlreadyResolved),
		errors.Is(err, apperror.ErrDisbursementBatchAlreadyDecided),
		errors.Is(err, apperror.ErrDisbursementBatchNotApproved):
		response.Conflict(w, err.Error())

	default:
		response.InternalServerError(w, err)
	}
}
