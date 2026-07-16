// Package ledgererr translates ledger gRPC statuses into stable client errors.
package ledgererr

import (
	"errors"
	"fmt"
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	ReasonAlreadyClosed = "ALREADY_CLOSED"
	DomainLedger        = "seev.ledger"
)

var ErrAlreadyClosed = errors.New(ReasonAlreadyClosed)

// LedgerError is the structured business error returned by the ledger service.
type LedgerError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *LedgerError) Error() string { return fmt.Sprintf("[%s] %s", e.Code, e.Message) }

// FromStatus reconstructs stable ledger errors from their gRPC wire form.
// Statuses without a recognized ledger ErrorInfo remain gRPC status errors so
// callers can classify infrastructure failures by their status code.
func FromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.Domain != DomainLedger {
			continue
		}
		if st.Code() == codes.Aborted && info.Reason == ReasonAlreadyClosed {
			return ErrAlreadyClosed
		}
		if st.Code() == codes.FailedPrecondition && info.Reason != "" {
			retryable, _ := strconv.ParseBool(info.Metadata["retryable"])
			return &LedgerError{Code: info.Reason, Message: st.Message(), Retryable: retryable}
		}
	}
	return err
}
