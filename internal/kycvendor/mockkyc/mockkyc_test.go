package mockkyc

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/kycvendor"
)

func TestProviderVerify_Modes(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		verdict string
	}{
		{name: "approve", mode: ModeApprove, verdict: kycvendor.VerdictApprove},
		{name: "reject", mode: ModeReject, verdict: kycvendor.VerdictReject},
		{name: "refer", mode: ModeRefer, verdict: kycvendor.VerdictRefer},
	}

	provider := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := provider.Verify(context.Background(), kycvendor.Submission{
				UserID: uuid.New(), LevelRequested: 1,
				Payload: map[string]any{"mock_mode": tt.mode},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.verdict, decision.Verdict)
		})
	}

	_, err := provider.Verify(context.Background(), kycvendor.Submission{
		LevelRequested: 1, Payload: map[string]any{"mock_mode": ModeTimeout},
	})
	assert.ErrorIs(t, err, ErrTimeout)
}

func TestProviderVerify_Level2AlwaysRefer(t *testing.T) {
	provider := New()
	for _, mode := range []string{ModeApprove, ModeReject, ModeRefer, ModeTimeout, ""} {
		decision, err := provider.Verify(context.Background(), kycvendor.Submission{
			LevelRequested: 2, Payload: map[string]any{"mock_mode": mode},
		})
		require.NoError(t, err)
		assert.Equal(t, kycvendor.VerdictRefer, decision.Verdict)
	}
}

func TestProviderVerify_DefaultApprovesL1(t *testing.T) {
	decision, err := New().Verify(context.Background(), kycvendor.Submission{LevelRequested: 1})
	require.NoError(t, err)
	assert.Equal(t, kycvendor.VerdictApprove, decision.Verdict)
}

func TestProviderVerify_UnknownModeReturnsError(t *testing.T) {
	_, err := New().Verify(context.Background(), kycvendor.Submission{
		LevelRequested: 1, Payload: map[string]any{"mock_mode": "unknown"},
	})
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrTimeout))
}
