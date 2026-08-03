// Package currency is the runtime registry of supported currencies and
// their minor-unit exponent (docs/roadmap/archive/18 Task T1, decision S2). Bootstraps
// with IDR only (this platform's original single-currency assumption,
// docs/roadmap/archive/01 decision D12) so callers work correctly before Load is ever
// called; internal/ledger.NewModule calls Load once at startup with the
// contents of the `currencies` table.
package currency

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/shopspring/decimal"
)

// Currency is one row of the runtime registry.
type Currency struct {
	Code      string
	MinorUnit int16
	// Status and Operations are optional metadata used by currency-aware
	// callers. Keeping them on the registry row avoids making every service
	// invent its own capability cache while preserving the original two-field
	// API for existing callers.
	Status     string
	Operations map[string]bool
}

var (
	ErrInvalidCurrency = errors.New("invalid currency")
	ErrUnknownCurrency = errors.New("unknown currency")
	ErrInvalidAmount   = errors.New("invalid minor-unit amount")
)

var codePattern = regexp.MustCompile(`^[A-Z]{3}$`)

var registry atomic.Pointer[map[string]Currency]

func init() {
	Load([]Currency{{Code: "IDR", MinorUnit: 0, Status: "active"}})
}

// Load atomically replaces the entire registry — the caller passes the
// full current list (not a diff). Safe to call while other goroutines read
// via IsValid/MinorUnit/ToMajor: readers either see the old registry or the
// new one in full, never a partial state.
func Load(list []Currency) {
	m := make(map[string]Currency, len(list))
	for _, c := range list {
		c.Code = strings.ToUpper(strings.TrimSpace(c.Code))
		if c.Status == "" {
			c.Status = "active"
		}
		if c.Operations != nil {
			ops := make(map[string]bool, len(c.Operations))
			maps.Copy(ops, c.Operations)
			c.Operations = ops
		}
		m[c.Code] = c
	}
	registry.Store(&m)
}

// Normalize validates a public currency code. Financial boundaries must not
// trim or silently case-fold input: callers should send the canonical code.
func Normalize(code string) (string, error) {
	if err := ValidateCode(code); err != nil {
		return "", err
	}
	if !IsValid(code) {
		return "", fmt.Errorf("%w: %s", ErrUnknownCurrency, code)
	}
	return code, nil
}

// ValidateCode checks only the wire shape of a currency code. It deliberately
// does not consult the runtime registry, so boundary services can reject
// malformed input without having to own the platform's currency catalogue.
func ValidateCode(code string) error {
	if !codePattern.MatchString(code) {
		return fmt.Errorf("%w: code must contain exactly three uppercase ASCII letters", ErrInvalidCurrency)
	}
	return nil
}

// IsValid reports whether code is a currently registered currency.
func IsValid(code string) bool {
	_, ok := lookup(code)
	return ok
}

// IsEnabled reports whether a registered currency accepts new intake.
func IsEnabled(code string) bool {
	c, ok := lookup(code)
	if !ok {
		return false
	}
	return c.Status == "" || c.Status == "active"
}

// Allows reports the operation capability for a currency. An omitted
// operation map preserves the legacy IDR behavior and means "allowed".
func Allows(code, operation string) bool {
	c, ok := lookup(code)
	if !ok || !IsEnabled(code) {
		return false
	}
	if len(c.Operations) == 0 {
		return true
	}
	return c.Operations[operation]
}

// MinorUnit returns code's minor-unit exponent (e.g. 2 for USD, 0 for IDR)
// and whether code is registered at all.
func MinorUnit(code string) (int16, bool) {
	c, ok := lookup(code)
	return c.MinorUnit, ok
}

// ToMajor converts a minor-unit integer amount to its major-unit decimal
// representation for DISPLAY/reporting only (e.g. 150000 IDR minor -> 150000
// major since MinorUnit=0; 150000 USD minor -> 1500.00 major since
// MinorUnit=2) — never used in the posting pipeline, which stays
// minor-unit-integer throughout (docs/roadmap/archive/18 T1 header: the wire/DB
// contract does not change). An unregistered code is treated as
// MinorUnit=0 (no conversion) rather than panicking — display code must
// degrade gracefully, not crash a report over one bad currency code.
func ToMajor(minor decimal.Decimal, code string) decimal.Decimal {
	exp, _ := MinorUnit(code)
	if exp <= 0 {
		return minor
	}
	return minor.Shift(-int32(exp))
}

// ValidatePositiveMinorAmount validates an amount at a service boundary where
// money is represented as a positive integer number of minor units. The
// Ledger persistence contract uses BIGINT, so accepting a larger decimal here
// would only defer an overflow/truncation failure to a later boundary.
func ValidatePositiveMinorAmount(amount decimal.Decimal) error {
	if !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) {
		return fmt.Errorf("%w: amount must be a positive integer minor-unit value", ErrInvalidAmount)
	}
	maxMinor := decimal.NewFromInt(int64(^uint64(0) >> 1))
	if amount.GreaterThan(maxMinor) {
		return fmt.Errorf("%w: amount exceeds signed 64-bit minor-unit range", ErrInvalidAmount)
	}
	return nil
}

func lookup(code string) (Currency, bool) {
	p := registry.Load()
	if p == nil {
		return Currency{}, false
	}
	c, ok := (*p)[code]
	return c, ok
}
