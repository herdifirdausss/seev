package ledger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
)

func TestBatchGetAssuranceTransactionsValidation(t *testing.T) {
	module := &Module{}
	tests := []struct {
		name string
		req  *ledgerv1.BatchGetAssuranceTransactionsRequest
		code codes.Code
	}{
		{name: "nil request", req: nil, code: codes.InvalidArgument},
		{name: "empty selector", req: &ledgerv1.BatchGetAssuranceTransactionsRequest{Selectors: []*ledgerv1.AssuranceSelector{{}}}, code: codes.InvalidArgument},
		{name: "partial correlation", req: &ledgerv1.BatchGetAssuranceTransactionsRequest{Selectors: []*ledgerv1.AssuranceSelector{{Type: "money_in"}}}, code: codes.InvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := module.BatchGetAssuranceTransactions(context.Background(), test.req)
			require.Equal(t, test.code, status.Code(err))
		})
	}
}
