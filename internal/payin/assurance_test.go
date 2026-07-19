package payin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
)

func TestListAssuranceRecordsValidation(t *testing.T) {
	module := &Module{}
	tests := []struct {
		name string
		req  *payinv1.ListAssuranceRecordsRequest
		code codes.Code
	}{
		{name: "nil request", req: nil, code: codes.InvalidArgument},
		{name: "page too large", req: &payinv1.ListAssuranceRecordsRequest{PageSize: 501}, code: codes.InvalidArgument},
		{name: "missing cutoff", req: &payinv1.ListAssuranceRecordsRequest{}, code: codes.InvalidArgument},
		{name: "cursor pair required", req: &payinv1.ListAssuranceRecordsRequest{Cutoff: timestamppb.Now(), CursorId: "bad"}, code: codes.InvalidArgument},
		{name: "cursor UUID required", req: &payinv1.ListAssuranceRecordsRequest{Cutoff: timestamppb.Now(), CursorUpdatedAt: timestamppb.Now(), CursorId: "bad"}, code: codes.InvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := module.ListAssuranceRecords(context.Background(), test.req)
			require.Equal(t, test.code, status.Code(err))
		})
	}
}
