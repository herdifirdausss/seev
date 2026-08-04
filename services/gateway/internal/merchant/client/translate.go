package client

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// translateError maps a gRPC error from PayinService/PayoutService onto
// this package's own small sentinel vocabulary. Every branch other than
// NotFound/InvalidArgument collapses onto ErrOwnerUnavailable — see that
// var's own doc comment for why a wider taxonomy isn't worth it here.
func translateError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%w: %w", ErrOwnerUnavailable, err)
	}
	switch st.Code() {
	case codes.NotFound:
		return ErrNotFound
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", ErrValidation, st.Message())
	default:
		return fmt.Errorf("%w: %s", ErrOwnerUnavailable, st.Message())
	}
}
