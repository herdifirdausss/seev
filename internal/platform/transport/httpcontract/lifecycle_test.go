package httpcontract

import (
	"strings"
	"testing"
	"time"
)

func TestRetirementRequiresReplacementAcknowledgementAndZeroUse(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	d := Deprecation{OperationID: "gatewayLegacyV1", DeprecatedAt: now.Add(-90 * 24 * time.Hour), Sunset: now.Add(-24 * time.Hour), MigrationURL: "https://example.invalid/migrate"}
	base := RetirementEvidence{Now: now, ReplacementOperationID: "gatewayCurrentV2", MigrationGuideURL: "https://example.invalid/migrate", AllConsumersAcknowledged: true, ZeroUseSince: now.Add(-31 * 24 * time.Hour)}
	if err := d.ValidateRetirement(base, 30*24*time.Hour); err != nil {
		t.Fatalf("complete retirement evidence rejected: %v", err)
	}
	for name, mutate := range map[string]func(*RetirementEvidence){
		"replacement": func(e *RetirementEvidence) { e.ReplacementOperationID = "" },
		"guide":       func(e *RetirementEvidence) { e.MigrationGuideURL = "" },
		"ack":         func(e *RetirementEvidence) { e.AllConsumersAcknowledged = false },
		"zero-use":    func(e *RetirementEvidence) { e.ZeroUseSince = now.Add(-29 * 24 * time.Hour) },
	} {
		evidence := base
		mutate(&evidence)
		if err := d.ValidateRetirement(evidence, 30*24*time.Hour); err == nil || strings.TrimSpace(err.Error()) == "" {
			t.Errorf("%s gate unexpectedly passed", name)
		}
	}
}
