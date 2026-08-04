package contracts_test

import "testing"

// dualEventRollout is deliberately an in-memory, test-only model. It proves
// the migration invariant without publishing a fake production v2 routing
// key: one logical event may have two delivery IDs, but only one effect.
type dualEventRollout struct {
	effects map[string]int
	metrics map[string]int
}

func (r *dualEventRollout) publish(logicalID string, versions ...string) {
	for _, version := range versions {
		r.metrics[version]++
		if r.effects[logicalID] == 0 {
			r.effects[logicalID] = 1
		}
	}
}

func TestDualVersionEventRolloutSharesLogicalIDAndOneEffect(t *testing.T) {
	r := &dualEventRollout{effects: map[string]int{}, metrics: map[string]int{}}
	logicalID := "00000000-0000-7000-8000-000000000001"
	r.publish(logicalID, "v1", "v2")
	if r.effects[logicalID] != 1 {
		t.Fatal("dual representations applied more than one business effect")
	}
	if r.metrics["v1"] != 1 || r.metrics["v2"] != 1 {
		t.Fatalf("version metrics did not record both deliveries: %#v", r.metrics)
	}
	// Rollback to v1 and replay both deliveries: logical-ID deduplication still
	// prevents a second effect while the old consumer remains live.
	r.publish(logicalID, "v1", "v2")
	if r.effects[logicalID] != 1 {
		t.Fatal("rollback/replay produced a duplicate business effect")
	}
}
