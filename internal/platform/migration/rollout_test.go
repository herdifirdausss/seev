package migrationkit

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePercentageBasisPoints_Bounds(t *testing.T) {
	require.NoError(t, ValidatePercentageBasisPoints(0))
	require.NoError(t, ValidatePercentageBasisPoints(BasisPoints))
	require.NoError(t, ValidatePercentageBasisPoints(2500))

	err := ValidatePercentageBasisPoints(-1)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidPercentage))

	err = ValidatePercentageBasisPoints(BasisPoints + 1)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidPercentage))
}

func TestSuggestedRamp_MatchesDocumentedStages(t *testing.T) {
	// §8.2: 0.1% / 1% / 5% / 10% / 25% / 50% / 100% expressed in basis points.
	require.Equal(t, []int{10, 100, 500, 1000, 2500, 5000, 10000}, SuggestedRamp())
}

func TestSuggestedRamp_StrictlyIncreasingAndInBounds(t *testing.T) {
	ramp := SuggestedRamp()
	require.NotEmpty(t, ramp)
	for i, pct := range ramp {
		require.NoError(t, ValidatePercentageBasisPoints(pct))
		if i > 0 {
			require.Greater(t, pct, ramp[i-1], "ramp stages must strictly increase")
		}
	}
	require.Equal(t, BasisPoints, ramp[len(ramp)-1], "ramp must end at 100%")
}
