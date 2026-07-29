package api

import "testing"

func TestValidateAmount(t *testing.T) {
	cases := map[string]bool{
		"50000":        true,
		"1":            true,
		"0":            false, // zero is not positive
		"-1":           false,
		"50000.5":      false, // not an integer
		"not-a-number": false,
		"":             false,
	}
	for raw, wantOK := range cases {
		_, ok := validateAmount(raw)
		if ok != wantOK {
			t.Errorf("validateAmount(%q) ok = %v, want %v", raw, ok, wantOK)
		}
	}
}

func TestValidateCurrency(t *testing.T) {
	cases := map[string]bool{
		"IDR":  true,
		"USD":  true,
		"idr":  false, // must be uppercase
		"ID":   false,
		"IDRR": false,
		"":     false,
	}
	for raw, want := range cases {
		if got := validateCurrency(raw); got != want {
			t.Errorf("validateCurrency(%q) = %v, want %v", raw, got, want)
		}
	}
}
