package currency

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Money is an exact amount in one currency's minor units. The currency is
// part of the value so callers cannot accidentally add unlike amounts.
type Money struct {
	Currency string
	Minor    int64
}

// Rate is an exact rational rate expressed as target major units per source
// major unit. It intentionally contains no float representation.
type Rate struct {
	Value *big.Rat
}

type RoundingMode string

const RoundTowardZero RoundingMode = "toward_zero"

// Remainder records the discarded fractional target minor units during an
// exact conversion. It is evidence, not an amount that may be posted.
type Remainder struct {
	Numerator   string
	Denominator string
}

var (
	ErrCurrencyMismatch = errors.New("currency mismatch")
	ErrOverflow         = errors.New("money overflow")
	ErrNonPositive      = errors.New("amount must be positive")
	ErrZeroTarget       = errors.New("converted target amount is zero")
)

func NewMoney(code string, minor int64) (Money, error) {
	canonical, err := Normalize(code)
	if err != nil {
		return Money{}, err
	}
	return Money{Currency: canonical, Minor: minor}, nil
}

// ParseMinor parses an unsigned decimal string containing integer minor units.
// JSON numbers and decimal fractions are intentionally rejected at this
// boundary.
func ParseMinor(code, raw string) (Money, error) {
	if raw == "" || raw[0] == '-' || strings.TrimSpace(raw) != raw {
		return Money{}, fmt.Errorf("%w: amount must be an unsigned decimal string", ErrInvalidAmount)
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return Money{}, fmt.Errorf("%w: amount must contain only digits", ErrInvalidAmount)
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	return NewMoney(code, value)
}

func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	maxInt64 := int64(^uint64(0) >> 1)
	minInt64 := -maxInt64 - 1
	if (other.Minor > 0 && m.Minor > maxInt64-other.Minor) ||
		(other.Minor < 0 && m.Minor < minInt64-other.Minor) {
		return Money{}, ErrOverflow
	}
	return Money{Currency: m.Currency, Minor: m.Minor + other.Minor}, nil
}

func (m Money) Sub(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	maxInt64 := int64(^uint64(0) >> 1)
	minInt64 := -maxInt64 - 1
	if (other.Minor > 0 && m.Minor < minInt64+other.Minor) ||
		(other.Minor < 0 && m.Minor > maxInt64+other.Minor) {
		return Money{}, ErrOverflow
	}
	return Money{Currency: m.Currency, Minor: m.Minor - other.Minor}, nil
}

// ParseRate parses a finite positive decimal or rational rate exactly.
// Accepted examples are "16000.125" and "32000/2"; exponent notation and
// binary floating point are not accepted.
func ParseRate(raw string) (Rate, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return Rate{}, fmt.Errorf("%w: rate must be an exact decimal string", ErrInvalidAmount)
	}
	if strings.ContainsAny(raw, "eE+") {
		return Rate{}, fmt.Errorf("%w: exponent and signed rate notation are not accepted", ErrInvalidAmount)
	}
	var rat *big.Rat
	if strings.Count(raw, "/") == 1 {
		parts := strings.SplitN(raw, "/", 2)
		numerator, ok := new(big.Int).SetString(parts[0], 10)
		if !ok {
			return Rate{}, fmt.Errorf("%w: invalid rate numerator", ErrInvalidAmount)
		}
		denominator, ok := new(big.Int).SetString(parts[1], 10)
		if !ok || denominator.Sign() <= 0 {
			return Rate{}, fmt.Errorf("%w: invalid rate denominator", ErrInvalidAmount)
		}
		rat = new(big.Rat).SetFrac(numerator, denominator)
	} else {
		var ok bool
		rat, ok = new(big.Rat).SetString(raw)
		if !ok {
			return Rate{}, fmt.Errorf("%w: invalid rate", ErrInvalidAmount)
		}
	}
	if rat.Sign() <= 0 {
		return Rate{}, fmt.Errorf("%w: rate must be positive", ErrNonPositive)
	}
	return Rate{Value: rat}, nil
}

func (r Rate) String() string {
	if r.Value == nil {
		return ""
	}
	if exact, err := r.DecimalString(18); err == nil {
		return exact
	}
	return r.Value.Num().String() + "/" + r.Value.Denom().String()
}

// ExactString returns a stable, lossless wire representation. Rates that fit
// within eighteen decimal places use the readable decimal form; all other
// rates use a reduced numerator/denominator form rather than being silently
// rounded for persistence or event delivery.
func (r Rate) ExactString() (string, error) {
	if r.Value == nil || r.Value.Sign() <= 0 {
		return "", fmt.Errorf("%w: rate is not a positive rational", ErrInvalidAmount)
	}
	if exact, err := r.DecimalString(18); err == nil {
		return exact, nil
	}
	return r.Value.Num().String() + "/" + r.Value.Denom().String(), nil
}

// DecimalString returns an exact base-10 representation when the rational
// denominator terminates within maxScale decimal places. It is used before a
// rate is persisted in NUMERIC(..., maxScale) so PostgreSQL can never silently
// round a non-terminating rational supplied by an operator.
func (r Rate) DecimalString(maxScale int) (string, error) {
	if r.Value == nil || r.Value.Sign() <= 0 || maxScale < 0 {
		return "", fmt.Errorf("%w: rate is not a positive rational", ErrInvalidAmount)
	}
	denominator := new(big.Int).Set(r.Value.Denom())
	two := big.NewInt(2)
	five := big.NewInt(5)
	one := big.NewInt(1)
	twoCount, fiveCount := 0, 0
	for new(big.Int).Mod(denominator, two).Sign() == 0 {
		denominator.Div(denominator, two)
		twoCount++
	}
	for new(big.Int).Mod(denominator, five).Sign() == 0 {
		denominator.Div(denominator, five)
		fiveCount++
	}
	if denominator.Cmp(one) != 0 {
		return "", fmt.Errorf("%w: rate denominator is non-terminating in base 10", ErrInvalidAmount)
	}
	scale := twoCount
	if fiveCount > scale {
		scale = fiveCount
	}
	if scale > maxScale {
		return "", fmt.Errorf("%w: rate scale exceeds %d decimal places", ErrInvalidAmount, maxScale)
	}
	numerator := new(big.Int).Set(r.Value.Num())
	if scale > twoCount {
		numerator.Mul(numerator, new(big.Int).Exp(two, big.NewInt(int64(scale-twoCount)), nil))
	}
	if scale > fiveCount {
		numerator.Mul(numerator, new(big.Int).Exp(five, big.NewInt(int64(scale-fiveCount)), nil))
	}
	digits := numerator.String()
	if scale == 0 {
		return digits, nil
	}
	for len(digits) <= scale {
		digits = "0" + digits
	}
	point := len(digits) - scale
	return digits[:point] + "." + digits[point:], nil
}

// Convert converts source minor units using an exact target-major-per-source-
// major rate. The optional spread is deducted from the reference rate in
// basis points. Positive results are rounded toward zero to target minor
// units, and the discarded fraction is returned for quote evidence.
func Convert(source Money, targetCode string, rate Rate, spreadBasisPoints int64) (Money, Remainder, error) {
	sourceCode, err := Normalize(source.Currency)
	if err != nil {
		return Money{}, Remainder{}, err
	}
	target, err := Normalize(targetCode)
	if err != nil {
		return Money{}, Remainder{}, err
	}
	if sourceCode == target {
		return Money{}, Remainder{}, ErrCurrencyMismatch
	}
	sourceExp, _ := MinorUnit(sourceCode)
	targetExp, _ := MinorUnit(target)
	return convertWithMinorUnits(source, target, sourceExp, targetExp, rate, spreadBasisPoints)
}

// ConvertWithMinorUnits performs the same exact conversion as Convert while
// taking the exponents from the caller's authoritative currency-policy
// snapshot. Ledger uses this form inside quote creation so a freshly loaded
// database row remains usable even before the process-wide display registry
// has been refreshed.
func ConvertWithMinorUnits(source Money, targetCode string, sourceMinorUnit, targetMinorUnit int16, rate Rate, spreadBasisPoints int64) (Money, Remainder, error) {
	if err := ValidateCode(source.Currency); err != nil {
		return Money{}, Remainder{}, err
	}
	if err := ValidateCode(targetCode); err != nil {
		return Money{}, Remainder{}, err
	}
	if source.Currency == targetCode {
		return Money{}, Remainder{}, ErrCurrencyMismatch
	}
	if sourceMinorUnit < 0 || targetMinorUnit < 0 {
		return Money{}, Remainder{}, fmt.Errorf("%w: minor-unit exponent cannot be negative", ErrInvalidAmount)
	}
	return convertWithMinorUnits(source, targetCode, sourceMinorUnit, targetMinorUnit, rate, spreadBasisPoints)
}

func convertWithMinorUnits(source Money, targetCode string, sourceExp, targetExp int16, rate Rate, spreadBasisPoints int64) (Money, Remainder, error) {
	if source.Minor <= 0 {
		return Money{}, Remainder{}, ErrNonPositive
	}
	if rate.Value == nil || rate.Value.Sign() <= 0 || spreadBasisPoints < 0 || spreadBasisPoints >= 10000 {
		return Money{}, Remainder{}, fmt.Errorf("%w: invalid conversion rate or spread", ErrInvalidAmount)
	}
	spread := new(big.Rat).SetFrac(big.NewInt(10000-spreadBasisPoints), big.NewInt(10000))
	clientRate := new(big.Rat).Mul(new(big.Rat).Set(rate.Value), spread)

	numerator := new(big.Int).Mul(big.NewInt(source.Minor), clientRate.Num())
	denominator := new(big.Int).Set(clientRate.Denom())
	if targetExp > sourceExp {
		numerator.Mul(numerator, pow10(int(targetExp-sourceExp)))
	} else if sourceExp > targetExp {
		denominator.Mul(denominator, pow10(int(sourceExp-targetExp)))
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if quotient.Sign() <= 0 {
		return Money{}, Remainder{}, ErrZeroTarget
	}
	if !quotient.IsInt64() {
		return Money{}, Remainder{}, ErrOverflow
	}
	return Money{Currency: targetCode, Minor: quotient.Int64()}, Remainder{
		Numerator: remainder.String(), Denominator: denominator.String(),
	}, nil
}

func pow10(exp int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil)
}
