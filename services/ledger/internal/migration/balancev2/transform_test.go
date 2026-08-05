package balancev2

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func sampleSource() SourceRow {
	return SourceRow{
		AccountID:     uuid.New(),
		Currency:      "idr",
		AccountType:   "cash",
		Balance:       125_000,
		AllowNegative: false,
		SourceVersion: 10,
		UpdatedAt:     time.Now(),
	}
}

func TestTransform_AccountTypeRouting(t *testing.T) {
	cases := []struct {
		accountType string
		field       func(TargetRow) int64
	}{
		{"hold", func(t TargetRow) int64 { return t.ReservedAmount }},
		{"pending", func(t TargetRow) int64 { return t.PendingAmount }},
		{"frozen", func(t TargetRow) int64 { return t.RestrictedAmount }},
		{"cash", func(t TargetRow) int64 { return t.AvailableAmount }},
		{"pocket", func(t TargetRow) int64 { return t.AvailableAmount }},
		{"settlement", func(t TargetRow) int64 { return t.AvailableAmount }},
		{"escrow", func(t TargetRow) int64 { return t.AvailableAmount }},
	}
	for _, c := range cases {
		source := sampleSource()
		source.AccountType = c.accountType
		target, err := Transform(source, nil)
		require.NoErrorf(t, err, "account type %s", c.accountType)
		require.Equalf(t, source.Balance, c.field(target), "account type %s should route the full balance into its semantic amount", c.accountType)

		// Every other amount field must stay exactly zero (§6.2: "all other
		// amounts remain exactly zero").
		total := target.AvailableAmount + target.ReservedAmount + target.PendingAmount + target.RestrictedAmount
		require.Equal(t, source.Balance, total, "account type %s must not split balance across fields", c.accountType)
	}
}

func TestTransform_PreservesIdentityAndCurrency(t *testing.T) {
	source := sampleSource()
	txID := uuid.New()
	target, err := Transform(source, &txID)
	require.NoError(t, err)
	require.Equal(t, source.AccountID, target.AccountID)
	require.Equal(t, "IDR", target.Currency, "currency must be normalized to uppercase")
	require.Equal(t, source.SourceVersion, target.SourceVersion)
	require.Equal(t, source.AllowNegative, target.AllowNegative)
	require.NotNil(t, target.LastTransactionID)
	require.Equal(t, txID, *target.LastTransactionID)
	require.NotEmpty(t, target.ProjectionChecksum)
}

func TestTransform_RejectsInvalidInput(t *testing.T) {
	t.Run("nil account id", func(t *testing.T) {
		source := sampleSource()
		source.AccountID = uuid.Nil
		_, err := Transform(source, nil)
		require.Error(t, err)
	})
	t.Run("currency not three characters", func(t *testing.T) {
		source := sampleSource()
		source.Currency = "US"
		_, err := Transform(source, nil)
		require.Error(t, err)
	})
	t.Run("empty account type", func(t *testing.T) {
		source := sampleSource()
		source.AccountType = "  "
		_, err := Transform(source, nil)
		require.Error(t, err)
	})
	t.Run("negative source version", func(t *testing.T) {
		source := sampleSource()
		source.SourceVersion = -1
		_, err := Transform(source, nil)
		require.Error(t, err)
	})
	t.Run("unsupported account type", func(t *testing.T) {
		source := sampleSource()
		source.AccountType = "totally_unknown_type"
		_, err := Transform(source, nil)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrUnsupportedLegacyRow))
	})
}

func TestChecksum_DeterministicSameInput(t *testing.T) {
	source := sampleSource()
	target, err := Transform(source, nil)
	require.NoError(t, err)
	first := Checksum(target)
	for i := 0; i < 5; i++ {
		require.Equal(t, first, Checksum(target), "checksum must be a pure function of the target row (§6.4)")
	}
}

func TestChecksum_AnyFieldChangeAltersHash(t *testing.T) {
	base, err := Transform(sampleSource(), nil)
	require.NoError(t, err)
	baseSum := Checksum(base)

	mutations := map[string]func(TargetRow) TargetRow{
		"available amount": func(t TargetRow) TargetRow { t.AvailableAmount++; return t },
		"reserved amount":  func(t TargetRow) TargetRow { t.ReservedAmount = 1; return t },
		"pending amount":   func(t TargetRow) TargetRow { t.PendingAmount = 1; return t },
		"restricted amount": func(t TargetRow) TargetRow {
			t.RestrictedAmount = 1
			return t
		},
		"currency":       func(t TargetRow) TargetRow { t.Currency = "USD"; return t },
		"source version": func(t TargetRow) TargetRow { t.SourceVersion++; return t },
		"allow negative": func(t TargetRow) TargetRow { t.AllowNegative = !t.AllowNegative; return t },
		"account id": func(t TargetRow) TargetRow {
			t.AccountID = uuid.New()
			return t
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			mutated := mutate(base)
			require.NotEqualf(t, baseSum, Checksum(mutated), "%s must change the checksum", name)
		})
	}
}

func TestChecksumMatches(t *testing.T) {
	target, err := Transform(sampleSource(), nil)
	require.NoError(t, err)
	require.True(t, ChecksumMatches(target))

	target.AvailableAmount++
	require.False(t, ChecksumMatches(target), "mutating a field without recomputing the checksum must be detected")
}

func TestCompareRows_Match(t *testing.T) {
	source := sampleSource()
	target, err := Transform(source, nil)
	require.NoError(t, err)

	comparison := CompareRows(source, &target)
	require.Equal(t, "match", comparison.Result)
	require.Equal(t, "match", comparison.Classification)
	require.Equal(t, "none", comparison.Severity)
	require.Equal(t, int64(0), comparison.FieldMask)
}

func TestCompareRows_TargetMissing(t *testing.T) {
	comparison := CompareRows(sampleSource(), nil)
	require.Equal(t, "target_missing", comparison.Result)
	require.Equal(t, ClassificationBackfillMissing, comparison.Classification)
	require.Equal(t, "critical", comparison.Severity)
	require.Equal(t, int64(FieldTargetMissing), comparison.FieldMask)
}

func TestCompareRows_TargetAhead_IsVersionRegression(t *testing.T) {
	source := sampleSource()
	ahead := source
	ahead.SourceVersion = source.SourceVersion + 1
	target, err := Transform(ahead, nil)
	require.NoError(t, err)

	comparison := CompareRows(source, &target)
	require.Equal(t, "target_ahead", comparison.Result)
	require.Equal(t, ClassificationVersionRegression, comparison.Classification)
	require.Equal(t, "critical", comparison.Severity)
}

func TestCompareRows_TargetStale_SameDataOlderVersion(t *testing.T) {
	// A target whose only defect is an older-but-internally-consistent
	// version (balance/currency/type unchanged since that version) must be
	// classified purely as stale backfill, not corruption.
	source := sampleSource()
	stale := source
	stale.SourceVersion = source.SourceVersion - 5
	target, err := Transform(stale, nil)
	require.NoError(t, err)

	comparison := CompareRows(source, &target)
	require.Equal(t, "target_stale", comparison.Result)
	require.Equal(t, ClassificationStaleBackfill, comparison.Classification)
	require.Equal(t, "critical", comparison.Severity)
	require.Equal(t, int64(FieldVersion), comparison.FieldMask, "a purely stale row must not also carry a value/checksum bit")
}

func TestCompareRows_ChecksumTampered(t *testing.T) {
	source := sampleSource()
	target, err := Transform(source, nil)
	require.NoError(t, err)
	target.ProjectionChecksum[0] ^= 0xFF // flip a byte without touching the data fields

	comparison := CompareRows(source, &target)
	require.Equal(t, "value_mismatch", comparison.Result)
	require.Equal(t, ClassificationTargetCorruption, comparison.Classification)
	require.NotZero(t, comparison.FieldMask&FieldChecksum)
}

func TestCompareRows_ValueMismatch_ConsistentChecksum(t *testing.T) {
	// A target that is internally self-consistent (its own checksum matches
	// its own stored fields) but no longer reflects the source is real data
	// corruption, distinguishable from a checksum-column-only tamper by the
	// field mask.
	source := sampleSource()
	target, err := Transform(source, nil)
	require.NoError(t, err)
	target.AvailableAmount += 999
	target.ProjectionChecksum = Checksum(target)

	comparison := CompareRows(source, &target)
	require.Equal(t, "value_mismatch", comparison.Result)
	require.Equal(t, ClassificationTargetCorruption, comparison.Classification)
	require.Zero(t, comparison.FieldMask&FieldChecksum, "checksum itself is internally consistent, only the value is wrong")
	require.NotZero(t, comparison.FieldMask&FieldAvailable)
}

func TestCompareRows_CurrencyMismatch(t *testing.T) {
	source := sampleSource()
	target, err := Transform(source, nil)
	require.NoError(t, err)
	target.Currency = "USD"
	target.ProjectionChecksum = Checksum(target)

	comparison := CompareRows(source, &target)
	require.Equal(t, "currency_mismatch", comparison.Result)
	require.Equal(t, ClassificationTargetCorruption, comparison.Classification)
	require.NotZero(t, comparison.FieldMask&FieldCurrency)
}

func TestCompareRows_Unsupported(t *testing.T) {
	source := sampleSource()
	target, err := Transform(source, nil)
	require.NoError(t, err)

	source.AccountType = "totally_unknown_type"
	comparison := CompareRows(source, &target)
	require.Equal(t, "unsupported", comparison.Result)
	require.Equal(t, ClassificationUnsupportedLegacyRow, comparison.Classification)
	require.Equal(t, "critical", comparison.Severity)
}
