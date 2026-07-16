package fraudcheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

type fakeFraudClient struct {
	lastRequest *fraudv1.ScreenRequest
	response    *fraudv1.ScreenResponse
	err         error
	delay       time.Duration
}

func (f *fakeFraudClient) Screen(ctx context.Context, in *fraudv1.ScreenRequest, _ ...grpc.CallOption) (*fraudv1.ScreenResponse, error) {
	f.lastRequest = in
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func TestCheck_BlockVerdictPassedThrough(t *testing.T) {
	fake := &fakeFraudClient{response: &fraudv1.ScreenResponse{Block: true, Reason: "over threshold"}}
	client := New(fake, "ledger")

	verdict, err := client.Check(context.Background(), "p2p_transfer", "transfer_p2p", uuid.New(), decimal.NewFromInt(100000), "IDR")
	require.NoError(t, err)
	assert.True(t, verdict.Block)
	assert.Equal(t, "over threshold", verdict.Reason)
}

func TestCheck_InfraErrorSurfaced(t *testing.T) {
	fake := &fakeFraudClient{err: errors.New("connection refused")}
	client := New(fake, "payin")

	_, err := client.Check(context.Background(), "topup", "money_in", uuid.New(), decimal.NewFromInt(50000), "IDR")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestCheck_TimeoutHonored(t *testing.T) {
	fake := &fakeFraudClient{delay: screenTimeout + 200*time.Millisecond}
	client := New(fake, "payout")

	start := time.Now()
	_, err := client.Check(context.Background(), "payout", "withdraw_initiate", uuid.New(), decimal.NewFromInt(1000), "IDR")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, screenTimeout+150*time.Millisecond, "Check must not wait past its own 500ms budget")
}

func TestCheck_InjectsRequestIDAndFlow(t *testing.T) {
	fake := &fakeFraudClient{response: &fraudv1.ScreenResponse{}}
	client := New(fake, "ledger")

	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "trace-xyz-1")
	_, err := client.Check(ctx, "p2p_transfer", "transfer_p2p", uuid.New(), decimal.NewFromInt(1000), "IDR")
	require.NoError(t, err)
	require.NotNil(t, fake.lastRequest)
	assert.Equal(t, "trace-xyz-1", fake.lastRequest.GetRequestId())
	assert.Equal(t, "p2p_transfer", fake.lastRequest.GetFlow())
}

func TestCheck_AllowVerdictNoBlock(t *testing.T) {
	fake := &fakeFraudClient{response: &fraudv1.ScreenResponse{Block: false}}
	client := New(fake, "ledger")

	verdict, err := client.Check(context.Background(), "p2p_transfer", "transfer_p2p", uuid.New(), decimal.NewFromInt(100), "IDR")
	require.NoError(t, err)
	assert.False(t, verdict.Block)
}
