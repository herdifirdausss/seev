package ledgererr

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func withInfo(t *testing.T, code codes.Code, message, reason, domain string, metadata map[string]string) error {
	t.Helper()
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{
		Reason: reason, Domain: domain, Metadata: metadata,
	})
	require.NoError(t, err)
	return st.Err()
}

func TestFromStatusLedgerError(t *testing.T) {
	err := FromStatus(withInfo(t, codes.FailedPrecondition, "balance too low", "INSUFFICIENT_FUNDS", DomainLedger, nil))
	var ledgerError *LedgerError
	require.ErrorAs(t, err, &ledgerError)
	require.Equal(t, "INSUFFICIENT_FUNDS", ledgerError.Code)
	require.Equal(t, "balance too low", ledgerError.Message)
	require.False(t, ledgerError.Retryable)
}

func TestFromStatusRetryableMetadata(t *testing.T) {
	err := FromStatus(withInfo(t, codes.FailedPrecondition, "try again", "TEMPORARY", DomainLedger, map[string]string{"retryable": "true"}))
	var ledgerError *LedgerError
	require.ErrorAs(t, err, &ledgerError)
	require.True(t, ledgerError.Retryable)
}

func TestFromStatusAlreadyClosed(t *testing.T) {
	err := FromStatus(withInfo(t, codes.Aborted, "already closed", ReasonAlreadyClosed, DomainLedger, nil))
	require.ErrorIs(t, err, ErrAlreadyClosed)
}

func TestFromStatusLeavesUnmappedErrorsIntact(t *testing.T) {
	plain := errors.New("plain")
	require.Same(t, plain, FromStatus(plain))
	require.NoError(t, FromStatus(nil))

	infra := status.Error(codes.Unavailable, "down")
	require.Equal(t, infra, FromStatus(infra))
	wrongDomain := withInfo(t, codes.FailedPrecondition, "bad", "CODE", "another.domain", nil)
	require.Equal(t, wrongDomain, FromStatus(wrongDomain))
	notFound := status.Error(codes.NotFound, "missing")
	require.Equal(t, notFound, FromStatus(notFound))
}
