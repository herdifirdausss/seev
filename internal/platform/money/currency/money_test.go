package currency

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMinor(t *testing.T) {
	Load([]Currency{{Code: "IDR", MinorUnit: 0}, {Code: "USD", MinorUnit: 2}})

	tests := []struct {
		name string
		code string
		raw  string
		want Money
		err  error
	}{
		{name: "zero", code: "IDR", raw: "0", want: Money{Currency: "IDR", Minor: 0}},
		{name: "leading zeroes", code: "USD", raw: "00042", want: Money{Currency: "USD", Minor: 42}},
		{name: "maximum", code: "IDR", raw: "9223372036854775807", want: Money{Currency: "IDR", Minor: math.MaxInt64}},
		{name: "overflow", code: "IDR", raw: "9223372036854775808", err: ErrInvalidAmount},
		{name: "negative", code: "IDR", raw: "-1", err: ErrInvalidAmount},
		{name: "fraction", code: "IDR", raw: "1.0", err: ErrInvalidAmount},
		{name: "leading plus", code: "IDR", raw: "+1", err: ErrInvalidAmount},
		{name: "whitespace", code: "IDR", raw: " 1", err: ErrInvalidAmount},
		{name: "empty", code: "IDR", raw: "", err: ErrInvalidAmount},
		{name: "unknown currency", code: "EUR", raw: "1", err: ErrUnknownCurrency},
		{name: "noncanonical currency", code: "usd", raw: "1", err: ErrInvalidCurrency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMinor(tt.code, tt.raw)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMoneyAddAndSub(t *testing.T) {
	usd := func(minor int64) Money { return Money{Currency: "USD", Minor: minor} }

	tests := []struct {
		name  string
		left  Money
		right Money
		sub   bool
		want  Money
		err   error
	}{
		{name: "add", left: usd(7), right: usd(5), want: usd(12)},
		{name: "add maximum", left: usd(math.MaxInt64 - 1), right: usd(1), want: usd(math.MaxInt64)},
		{name: "add positive overflow", left: usd(math.MaxInt64), right: usd(1), err: ErrOverflow},
		{name: "add negative overflow", left: usd(math.MinInt64), right: usd(-1), err: ErrOverflow},
		{name: "subtract", left: usd(7), right: usd(5), sub: true, want: usd(2)},
		{name: "subtract negative", left: usd(7), right: usd(-5), sub: true, want: usd(12)},
		{name: "subtract positive overflow", left: usd(math.MinInt64), right: usd(1), sub: true, err: ErrOverflow},
		{name: "subtract negative overflow", left: usd(math.MaxInt64), right: usd(-1), sub: true, err: ErrOverflow},
		{name: "add currency mismatch", left: usd(1), right: Money{Currency: "IDR", Minor: 1}, err: ErrCurrencyMismatch},
		{name: "subtract currency mismatch", left: usd(1), right: Money{Currency: "IDR", Minor: 1}, sub: true, err: ErrCurrencyMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Money
			var err error
			if tt.sub {
				got, err = tt.left.Sub(tt.right)
			} else {
				got, err = tt.left.Add(tt.right)
			}
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMoneyRoundTripInvariant(t *testing.T) {
	values := []int64{math.MinInt64, -1_000_000, -1, 0, 1, 1_000_000, math.MaxInt64}
	for _, value := range values {
		if value == math.MinInt64 {
			continue // -MinInt64 cannot be represented for the inverse operation.
		}
		start := Money{Currency: "USD", Minor: value}
		delta := Money{Currency: "USD", Minor: -value}
		added, err := start.Add(delta)
		require.NoError(t, err)
		require.Equal(t, int64(0), added.Minor)
		roundTrip, err := added.Sub(delta)
		require.NoError(t, err)
		assert.Equal(t, start, roundTrip)
	}
}

func TestParseRateAndExactRepresentations(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		exact     string
		decimal18 string
		err       error
	}{
		{name: "integer", raw: "16000", exact: "16000", decimal18: "16000"},
		{name: "finite decimal", raw: "16000.125", exact: "16000.125", decimal18: "16000.125"},
		{name: "reduced rational", raw: "32000/2", exact: "16000", decimal18: "16000"},
		{name: "leading fractional zero", raw: "0.000000000000000001", exact: "0.000000000000000001", decimal18: "0.000000000000000001"},
		{name: "non terminating", raw: "1/3", exact: "1/3"},
		{name: "zero", raw: "0", err: ErrNonPositive},
		{name: "negative", raw: "-1", err: ErrNonPositive},
		{name: "zero denominator", raw: "1/0", err: ErrInvalidAmount},
		{name: "negative denominator", raw: "1/-2", err: ErrInvalidAmount},
		{name: "exponent", raw: "1e3", err: ErrInvalidAmount},
		{name: "plus", raw: "+1", err: ErrInvalidAmount},
		{name: "whitespace", raw: "1 ", err: ErrInvalidAmount},
		{name: "invalid numerator", raw: "x/2", err: ErrInvalidAmount},
		{name: "invalid", raw: "x", err: ErrInvalidAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, err := ParseRate(tt.raw)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			exact, err := rate.ExactString()
			require.NoError(t, err)
			assert.Equal(t, tt.exact, exact)
			assert.Equal(t, tt.exact, rate.String())
			if tt.decimal18 != "" {
				decimal, decimalErr := rate.DecimalString(18)
				require.NoError(t, decimalErr)
				assert.Equal(t, tt.decimal18, decimal)
			} else {
				require.ErrorIs(t, rateDecimalError(rate), ErrInvalidAmount)
			}
			roundTrip, err := ParseRate(exact)
			require.NoError(t, err)
			assert.Zero(t, rate.Value.Cmp(roundTrip.Value))
		})
	}
}

func rateDecimalError(rate Rate) error {
	_, err := rate.DecimalString(18)
	return err
}

func TestRateInvalidValues(t *testing.T) {
	invalid := []Rate{{}, {Value: new(big.Rat)}, {Value: new(big.Rat).SetInt64(-1)}}
	for index, rate := range invalid {
		if index == 0 {
			assert.Empty(t, rate.String())
		}
		_, err := rate.ExactString()
		require.ErrorIs(t, err, ErrInvalidAmount)
		_, err = rate.DecimalString(18)
		require.ErrorIs(t, err, ErrInvalidAmount)
	}

	rate, err := ParseRate("0.0000000000000000001")
	require.NoError(t, err)
	_, err = rate.DecimalString(18)
	require.ErrorIs(t, err, ErrInvalidAmount)
	_, err = rate.DecimalString(-1)
	require.ErrorIs(t, err, ErrInvalidAmount)
}

func TestConvertExactRoundingSpreadAndRemainder(t *testing.T) {
	Load([]Currency{{Code: "IDR", MinorUnit: 0}, {Code: "USD", MinorUnit: 2}, {Code: "JPY", MinorUnit: 0}})

	tests := []struct {
		name      string
		source    Money
		target    string
		rate      string
		spreadBPS int64
		want      Money
		remNum    string
		remDen    string
		err       error
	}{
		{name: "scale up exact", source: Money{Currency: "USD", Minor: 12345}, target: "IDR", rate: "16000", want: Money{Currency: "IDR", Minor: 1_975_200}, remNum: "0", remDen: "100"},
		{name: "scale down exact", source: Money{Currency: "IDR", Minor: 16000}, target: "USD", rate: "0.0000625", want: Money{Currency: "USD", Minor: 100}, remNum: "0", remDen: "16000"},
		{name: "round toward zero", source: Money{Currency: "USD", Minor: 101}, target: "JPY", rate: "3/2", want: Money{Currency: "JPY", Minor: 1}, remNum: "103", remDen: "200"},
		{name: "spread deducted", source: Money{Currency: "USD", Minor: 10000}, target: "JPY", rate: "2", spreadBPS: 250, want: Money{Currency: "JPY", Minor: 195}, remNum: "0", remDen: "2000"},
		{name: "same currency", source: Money{Currency: "USD", Minor: 1}, target: "USD", rate: "1", err: ErrCurrencyMismatch},
		{name: "zero source", source: Money{Currency: "USD", Minor: 0}, target: "JPY", rate: "1", err: ErrNonPositive},
		{name: "negative source", source: Money{Currency: "USD", Minor: -1}, target: "JPY", rate: "1", err: ErrNonPositive},
		{name: "zero target after rounding", source: Money{Currency: "USD", Minor: 1}, target: "JPY", rate: "1", err: ErrZeroTarget},
		{name: "invalid spread negative", source: Money{Currency: "USD", Minor: 100}, target: "JPY", rate: "1", spreadBPS: -1, err: ErrInvalidAmount},
		{name: "invalid spread upper bound", source: Money{Currency: "USD", Minor: 100}, target: "JPY", rate: "1", spreadBPS: 10000, err: ErrInvalidAmount},
		{name: "overflow", source: Money{Currency: "USD", Minor: math.MaxInt64}, target: "JPY", rate: "1000", err: ErrOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, err := ParseRate(tt.rate)
			require.NoError(t, err)
			got, remainder, err := Convert(tt.source, tt.target, rate, tt.spreadBPS)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.remNum, remainder.Numerator)
			assert.Equal(t, tt.remDen, remainder.Denominator)
		})
	}
}

func TestConvertWithMinorUnitsValidation(t *testing.T) {
	rate, err := ParseRate("1")
	require.NoError(t, err)

	_, _, err = ConvertWithMinorUnits(Money{Currency: "usd", Minor: 1}, "JPY", 2, 0, rate, 0)
	require.ErrorIs(t, err, ErrInvalidCurrency)
	_, _, err = ConvertWithMinorUnits(Money{Currency: "USD", Minor: 1}, "jpy", 2, 0, rate, 0)
	require.ErrorIs(t, err, ErrInvalidCurrency)
	_, _, err = ConvertWithMinorUnits(Money{Currency: "USD", Minor: 1}, "USD", 2, 2, rate, 0)
	require.ErrorIs(t, err, ErrCurrencyMismatch)
	_, _, err = ConvertWithMinorUnits(Money{Currency: "USD", Minor: 1}, "JPY", -1, 0, rate, 0)
	require.ErrorIs(t, err, ErrInvalidAmount)
	_, _, err = ConvertWithMinorUnits(Money{Currency: "USD", Minor: 1}, "JPY", 2, -1, rate, 0)
	require.ErrorIs(t, err, ErrInvalidAmount)
}

func FuzzMoneyAddSubRoundTrip(f *testing.F) {
	for _, seed := range []struct{ left, right int64 }{{0, 0}, {1, -1}, {math.MaxInt64, 0}, {math.MinInt64, 0}, {1_000_000, 9_999}} {
		f.Add(seed.left, seed.right)
	}
	f.Fuzz(func(t *testing.T, left, right int64) {
		a := Money{Currency: "IDR", Minor: left}
		b := Money{Currency: "IDR", Minor: right}
		sum, err := a.Add(b)
		if err != nil {
			require.True(t, errors.Is(err, ErrOverflow))
			return
		}
		roundTrip, err := sum.Sub(b)
		require.NoError(t, err)
		assert.Equal(t, a, roundTrip)
	})
}

func FuzzParseMinorRoundTrip(f *testing.F) {
	f.Add(uint64(0))
	f.Add(uint64(math.MaxInt64))
	f.Fuzz(func(t *testing.T, raw uint64) {
		if raw > math.MaxInt64 {
			t.Skip()
		}
		Load([]Currency{{Code: "IDR", MinorUnit: 0}})
		money, err := ParseMinor("IDR", new(big.Int).SetUint64(raw).String())
		require.NoError(t, err)
		assert.Equal(t, int64(raw), money.Minor)
	})
}
