// Package currency is the runtime registry of supported currencies and
// their minor-unit exponent (docs/plan/18 Task T1, decision S2). Bootstraps
// with IDR only (this platform's original single-currency assumption,
// docs/plan/01 decision D12) so callers work correctly before Load is ever
// called; internal/ledger.NewModule calls Load once at startup with the
// contents of the `currencies` table.
package currency

import (
	"errors"
	"sync/atomic"

	"github.com/shopspring/decimal"
)

// Currency is one row of the runtime registry.
type Currency struct {
	Code      string
	MinorUnit int16
}

var ErrInvalidCurrency = errors.New("invalid currency")

var registry atomic.Pointer[map[string]Currency]

func init() {
	Load([]Currency{{Code: "IDR", MinorUnit: 0}})
}

// Load atomically replaces the entire registry — the caller passes the
// full current list (not a diff). Safe to call while other goroutines read
// via IsValid/MinorUnit/ToMajor: readers either see the old registry or the
// new one in full, never a partial state.
func Load(list []Currency) {
	m := make(map[string]Currency, len(list))
	for _, c := range list {
		m[c.Code] = c
	}
	registry.Store(&m)
}

// IsValid reports whether code is a currently registered currency.
func IsValid(code string) bool {
	_, ok := lookup(code)
	return ok
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
// minor-unit-integer throughout (docs/plan/18 T1 header: the wire/DB
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

func lookup(code string) (Currency, bool) {
	p := registry.Load()
	if p == nil {
		return Currency{}, false
	}
	c, ok := (*p)[code]
	return c, ok
}
