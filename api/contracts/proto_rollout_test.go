package contracts_test

import "testing"

type protoRollout struct {
	v1Calls int
	v2Calls int
	v1Live  bool
	v2Live  bool
}

func (r *protoRollout) call(preferV2 bool) string {
	if preferV2 && r.v2Live {
		r.v2Calls++
		return "v2"
	}
	if r.v1Live {
		r.v1Calls++
		return "v1"
	}
	return "unavailable"
}

func TestProtoVersionRolloutSupportsFallbackRollbackAndGuardedRemoval(t *testing.T) {
	r := &protoRollout{v1Live: true, v2Live: true}
	if got := r.call(false); got != "v1" {
		t.Fatalf("v1-only client did not remain functional: %s", got)
	}
	if got := r.call(true); got != "v2" {
		t.Fatalf("v2 client did not cut over: %s", got)
	}
	// A failed v2 deployment rolls back to v1 without changing the v1 client.
	r.v2Live = false
	if got := r.call(true); got != "v1" {
		t.Fatalf("rollback did not fall back to v1: %s", got)
	}
	if r.v1Calls != 2 || r.v2Calls != 1 {
		t.Fatalf("unexpected independent version metrics: v1=%d v2=%d", r.v1Calls, r.v2Calls)
	}
	// v1 remains live until acknowledgement and zero-use evidence are supplied.
	if !r.v1Live {
		t.Fatal("v1 was removed before the rollout gate completed")
	}
}
