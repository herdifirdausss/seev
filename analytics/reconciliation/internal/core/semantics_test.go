package core

import (
	"testing"
	"time"
)

func TestSignedEntryAndBalance(t *testing.T) {
	t.Parallel()
	if got := SignedEntry("debit", 125); got != -125 {
		t.Fatalf("debit sign = %d", got)
	}
	if got := SignedEntry("credit", 125); got != 125 {
		t.Fatalf("credit sign = %d", got)
	}
	if !Balanced(1000, 1000) || Balanced(1000, 999) {
		t.Fatal("balance invariant has incorrect result")
	}
}

func TestSafeCutoffAndDelta(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 8, 3, 10, 0, 0, 0, time.FixedZone("WIB", 7*60*60))
	b := a.Add(-time.Minute)
	if got := SafeCutoff(a, b); !got.Equal(b.UTC()) {
		t.Fatalf("safe cutoff = %s, want %s", got, b.UTC())
	}
	if got := Delta(10, 7); got != -3 {
		t.Fatalf("delta = %d", got)
	}
	if SeverityFor(1, true) != SeverityCritical || SeverityFor(0, true) != SeverityInfo {
		t.Fatal("severity mapping is incorrect")
	}
}

func TestPseudonymIsDeterministicAndDetailsAreRedacted(t *testing.T) {
	t.Parallel()
	a := Pseudonym([]byte("salt"), "same-id")
	b := Pseudonym([]byte("salt"), "same-id")
	if a == "" || a != b {
		t.Fatalf("pseudonym is not deterministic: %q %q", a, b)
	}
	if Pseudonym([]byte("other"), "same-id") == a {
		t.Fatal("different salt produced the same pseudonym")
	}
	if RedactDetails("source row user_id=abc") != "redacted sensitive detail" {
		t.Fatal("sensitive details were not redacted")
	}
	if RedactDetails("bounded count mismatch") == "redacted sensitive detail" {
		t.Fatal("safe details were redacted")
	}
}
