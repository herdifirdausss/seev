package migrationkit

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCohortBucket_DeterministicAndBounded(t *testing.T) {
	for i := 0; i < 200; i++ {
		key := uuid.NewString()
		first := CohortBucket(key, "ledger-balance-projection-v1-v2")
		for repeat := 0; repeat < 5; repeat++ {
			require.Equal(t, first, CohortBucket(key, "ledger-balance-projection-v1-v2"),
				"same key must always hash to the same bucket (§5.8/§8.4 stickiness)")
		}
		require.GreaterOrEqual(t, first, 0)
		require.Less(t, first, BasisPoints)
	}
}

func TestCohortBucket_DifferentMigrationNamesSeparateCohorts(t *testing.T) {
	// §5.8: the migration name is part of the hash so two migrations never
	// share the exact same cohort mapping for the same key.
	key := "account-" + uuid.NewString()
	differ := 0
	for i := 0; i < 20; i++ {
		a := CohortBucket(key, fmt.Sprintf("migration-a-%d", i))
		b := CohortBucket(key, fmt.Sprintf("migration-b-%d", i))
		if a != b {
			differ++
		}
	}
	require.Greater(t, differ, 0, "distinct migration names should not collapse to identical buckets across every sample")
}

func TestInCohort_ZeroAndFullPercentage(t *testing.T) {
	for i := 0; i < 50; i++ {
		key := uuid.NewString()
		require.False(t, InCohort(key, "m", 0), "0%% must never select an account")
		require.True(t, InCohort(key, "m", BasisPoints), "100%% must always select an account")
	}
}

func TestInCohort_MonotonicRamp(t *testing.T) {
	// §8.2's ramp (0.1/1/5/10/25/50/100%) only behaves as a ramp, not a
	// reshuffle, if every account already in a lower percentage stays in
	// every higher percentage — this is what makes cohorts "stable".
	ramp := SuggestedRamp()
	for i := 0; i < 500; i++ {
		key := "account-" + uuid.NewString()
		wasIn := false
		for _, pct := range ramp {
			nowIn := InCohort(key, "ledger-balance-projection-v1-v2", pct)
			if wasIn {
				require.Truef(t, nowIn, "account in cohort at a lower ramp stage dropped out at %d bps for key %s", pct, key)
			}
			wasIn = nowIn
		}
	}
}

func TestInCohort_NegativePercentageNeverSelects(t *testing.T) {
	require.False(t, InCohort("any-key", "m", -1))
}

func TestStableKey(t *testing.T) {
	require.Equal(t, "", StableKey())
	require.Equal(t, "a", StableKey("a"))
	require.Equal(t, "a\x00b\x00c", StableKey("a", "b", "c"))
	// Composition order matters: swapping parts must change the key so an
	// account+migration key can never collide with migration+account.
	require.NotEqual(t, StableKey("a", "b"), StableKey("b", "a"))
}
