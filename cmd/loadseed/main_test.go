package main

import (
	"encoding/json"
	"testing"
)

func TestSeedRowsAreDeterministicAndBalanced(t *testing.T) {
	a, b := ledgerRow(53, 7), ledgerRow(53, 7)
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if string(aa) != string(bb) {
		t.Fatal("same seed did not produce same row")
	}
	entries := a["entries"].([]map[string]any)
	if entries[0]["amount_minor"] != entries[1]["amount_minor"] {
		t.Fatal("ledger seed is unbalanced")
	}
}

func TestSeedOutputBoundary(t *testing.T) {
	if safeOutput("/Users/other/file") || safeOutput("artifacts/load") || !safeOutput("artifacts/load/run/seed.jsonl") {
		t.Fatal("unsafe output boundary")
	}
}
