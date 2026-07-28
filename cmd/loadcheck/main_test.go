package main

import "testing"

func TestSplitDatabaseNames(t *testing.T) {
	got := split("seev_load_ledger, seev_load_auth,,")
	if len(got) != 2 || got[1] != "seev_load_auth" {
		t.Fatalf("unexpected split: %#v", got)
	}
}
