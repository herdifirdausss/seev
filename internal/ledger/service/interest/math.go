// Package interest contains the C5 monthly-capitalised savings domain.  The
// math in this file has no database or Ledger dependency, which makes the
// accounting invariant reusable by workers, previews, and reconciliation.
package interest

import (
	"errors"
	"math/big"
)

const (
	BasisPointsDenominator int64 = 10_000
	ACT365FDaysPerYear     int64 = 365
	DailyDenominator       int64 = BasisPointsDenominator * ACT365FDaysPerYear
)

var ErrInvalidCarry = errors.New("interest: carry must be non-negative and less than denominator")

// DailyCalculation is the exact integer result of one ACT/365F accrual day.
// All values are represented as big.Int so a large balance/rate cannot
// overflow before it is persisted as PostgreSQL NUMERIC.
type DailyCalculation struct {
	ExactNumerator        *big.Int
	Denominator           *big.Int
	OpeningCarryNumerator *big.Int
	RecognizedMinor       *big.Int
	ClosingCarryNumerator *big.Int
}

func (c DailyCalculation) Clone() DailyCalculation {
	return DailyCalculation{
		ExactNumerator:        new(big.Int).Set(c.ExactNumerator),
		Denominator:           new(big.Int).Set(c.Denominator),
		OpeningCarryNumerator: new(big.Int).Set(c.OpeningCarryNumerator),
		RecognizedMinor:       new(big.Int).Set(c.RecognizedMinor),
		ClosingCarryNumerator: new(big.Int).Set(c.ClosingCarryNumerator),
	}
}

// CalculateDaily computes balance * rate_bps / (10,000 * 365), carrying the
// fractional numerator from the previous day.  Zero and negative balances do
// not consume or create carry; the existing carry remains evidence of unpaid
// fractional interest.
func CalculateDaily(balanceMinor, annualRateBps int64, openingCarry *big.Int) (DailyCalculation, error) {
	denominator := big.NewInt(DailyDenominator)
	carry := new(big.Int)
	if openingCarry != nil {
		carry.Set(openingCarry)
	}
	if carry.Sign() < 0 || carry.Cmp(denominator) >= 0 {
		return DailyCalculation{}, ErrInvalidCarry
	}

	numerator := new(big.Int)
	if balanceMinor > 0 && annualRateBps > 0 {
		numerator.Mul(big.NewInt(balanceMinor), big.NewInt(annualRateBps))
	}
	available := new(big.Int).Add(carry, numerator)
	recognized := new(big.Int)
	closingCarry := new(big.Int)
	if balanceMinor > 0 && annualRateBps > 0 {
		recognized.QuoRem(available, denominator, closingCarry)
	} else {
		// A non-eligible day leaves carry unchanged.  This matters when a
		// temporary zero balance occurs between two positive snapshot days.
		closingCarry.Set(carry)
	}
	return DailyCalculation{
		ExactNumerator:        numerator,
		Denominator:           denominator,
		OpeningCarryNumerator: carry,
		RecognizedMinor:       recognized,
		ClosingCarryNumerator: closingCarry,
	}, nil
}

// CalculateDailyFromStrings is the persistence boundary helper for NUMERIC
// carry values.  It refuses malformed/non-integer values rather than silently
// rounding a financial record.
func CalculateDailyFromStrings(balanceMinor, annualRateBps int64, openingCarry string) (DailyCalculation, error) {
	carry := new(big.Int)
	if openingCarry != "" {
		if _, ok := carry.SetString(openingCarry, 10); !ok {
			return DailyCalculation{}, errors.New("interest: invalid carry numerator")
		}
	}
	return CalculateDaily(balanceMinor, annualRateBps, carry)
}

func BigIntString(value *big.Int) string {
	if value == nil {
		return "0"
	}
	return value.String()
}
