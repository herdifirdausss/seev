package interest

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateDaily_TableDriven(t *testing.T) {
	cases := []struct {
		name         string
		balanceMinor int64
		rateBps      int64
		openingCarry *big.Int
		wantExact    string
		wantOpening  string
		wantRecog    string
		wantClosing  string
	}{
		{
			name:         "normal day recognizes floor and keeps remainder as carry",
			balanceMinor: 1_000_000,
			rateBps:      500,
			openingCarry: big.NewInt(0),
			// 1,000,000 * 500 = 500,000,000 ; denominator 3,650,000
			// 3,650,000 * 136 = 496,400,000 ; remainder 3,600,000
			wantExact:   "500000000",
			wantOpening: "0",
			wantRecog:   "136",
			wantClosing: "3600000",
		},
		{
			name:         "zero balance leaves carry unchanged and recognizes zero",
			balanceMinor: 0,
			rateBps:      500,
			openingCarry: big.NewInt(42),
			wantExact:    "0",
			wantOpening:  "42",
			wantRecog:    "0",
			wantClosing:  "42",
		},
		{
			name:         "negative balance leaves carry unchanged and recognizes zero",
			balanceMinor: -1_000_000,
			rateBps:      500,
			openingCarry: big.NewInt(99),
			wantExact:    "0",
			wantOpening:  "99",
			wantRecog:    "0",
			wantClosing:  "99",
		},
		{
			name:         "zero rate leaves carry unchanged and recognizes zero",
			balanceMinor: 1_000_000,
			rateBps:      0,
			openingCarry: big.NewInt(7),
			wantExact:    "0",
			wantOpening:  "7",
			wantRecog:    "0",
			wantClosing:  "7",
		},
		{
			name:         "nil opening carry treated as zero",
			balanceMinor: 1_000_000,
			rateBps:      500,
			openingCarry: nil,
			wantExact:    "500000000",
			wantOpening:  "0",
			wantRecog:    "136",
			wantClosing:  "3600000",
		},
		{
			name:         "carry accumulates across days until it crosses the denominator",
			balanceMinor: 10,
			rateBps:      500,
			// 10 * 500 = 5,000 ; well under 3,650,000 alone, but a large
			// existing carry close to the denominator should push a day over.
			openingCarry: big.NewInt(3_649_996),
			wantExact:    "5000",
			wantOpening:  "3649996",
			// available = 3,649,996 + 5,000 = 3,654,996
			// 3,654,996 / 3,650,000 = 1 remainder 4,996
			wantRecog:   "1",
			wantClosing: "4996",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CalculateDaily(c.balanceMinor, c.rateBps, c.openingCarry)
			require.NoError(t, err)
			assert.Equal(t, c.wantExact, got.ExactNumerator.String())
			assert.Equal(t, c.wantOpening, got.OpeningCarryNumerator.String())
			assert.Equal(t, c.wantRecog, got.RecognizedMinor.String())
			assert.Equal(t, c.wantClosing, got.ClosingCarryNumerator.String())
			assert.Equal(t, "3650000", got.Denominator.String())
		})
	}
}

func TestCalculateDaily_InvalidCarry(t *testing.T) {
	cases := []struct {
		name  string
		carry *big.Int
	}{
		{"negative carry", big.NewInt(-1)},
		{"carry equal to denominator", big.NewInt(DailyDenominator)},
		{"carry greater than denominator", big.NewInt(DailyDenominator + 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CalculateDaily(1_000_000, 500, c.carry)
			assert.ErrorIs(t, err, ErrInvalidCarry)
		})
	}
}

func TestCalculateDaily_DoesNotMutateInputCarry(t *testing.T) {
	carry := big.NewInt(1_000)
	_, err := CalculateDaily(1_000_000, 500, carry)
	require.NoError(t, err)
	assert.Equal(t, "1000", carry.String(), "CalculateDaily must not mutate the caller's carry pointer")
}

func TestCalculateDaily_OverflowSafety(t *testing.T) {
	// Largest supported IDR minor-unit balance times the maximum allowed
	// annual rate (2000 bps, i.e. 20%) must not overflow int64 arithmetic
	// because the implementation uses big.Int throughout.
	const largeBalance = int64(1_000_000_000_000_000) // 1 quadrillion minor units
	const maxRateBps = int64(2000)
	got, err := CalculateDaily(largeBalance, maxRateBps, big.NewInt(0))
	require.NoError(t, err)

	wantNumerator := new(big.Int).Mul(big.NewInt(largeBalance), big.NewInt(maxRateBps))
	assert.Equal(t, wantNumerator.String(), got.ExactNumerator.String())

	wantRecognized := new(big.Int).Quo(wantNumerator, big.NewInt(DailyDenominator))
	assert.Equal(t, wantRecognized.String(), got.RecognizedMinor.String())
}

func TestCalculateDailyFromStrings(t *testing.T) {
	t.Run("valid carry string", func(t *testing.T) {
		got, err := CalculateDailyFromStrings(1_000_000, 500, "0")
		require.NoError(t, err)
		assert.Equal(t, "136", got.RecognizedMinor.String())
	})

	t.Run("empty carry string treated as zero", func(t *testing.T) {
		got, err := CalculateDailyFromStrings(1_000_000, 500, "")
		require.NoError(t, err)
		assert.Equal(t, "0", got.OpeningCarryNumerator.String())
	})

	t.Run("malformed carry string is rejected", func(t *testing.T) {
		_, err := CalculateDailyFromStrings(1_000_000, 500, "not-a-number")
		require.Error(t, err)
	})

	t.Run("out-of-range carry string is rejected", func(t *testing.T) {
		_, err := CalculateDailyFromStrings(1_000_000, 500, "-1")
		assert.ErrorIs(t, err, ErrInvalidCarry)
	})
}

func TestBigIntString(t *testing.T) {
	assert.Equal(t, "0", BigIntString(nil))
	assert.Equal(t, "42", BigIntString(big.NewInt(42)))
}
