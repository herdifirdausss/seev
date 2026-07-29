package api

import "testing"

func TestMapPayinStatus(t *testing.T) {
	cases := map[string]string{
		"pending": "pending",
		"settled": "settled",
		"expired": "failed",
		"":        "failed",
		"bogus":   "failed",
	}
	for internal, want := range cases {
		if got := mapPayinStatus(internal); got != want {
			t.Errorf("mapPayinStatus(%q) = %q, want %q", internal, got, want)
		}
	}
}

func TestMapPayoutStatus(t *testing.T) {
	cases := map[string]string{
		"created":        "pending",
		"held":           "pending",
		"submitted":      "processing",
		"vendor_pending": "processing",
		"settled":        "settled",
		"failed":         "failed",
		"cancelled":      "failed",
		"rejected":       "failed",
		"":               "failed",
		"bogus":          "failed",
	}
	for internal, want := range cases {
		if got := mapPayoutStatus(internal); got != want {
			t.Errorf("mapPayoutStatus(%q) = %q, want %q", internal, got, want)
		}
	}
}
