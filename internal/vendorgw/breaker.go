package vendorgw

import (
	"log/slog"
	"sort"
	"sync"
	"time"
)

// HealthState is a circuit breaker's current state for one vendor
// (docs/plan/40 Task T1).
type HealthState string

const (
	StateClosed   HealthState = "closed"
	StateOpen     HealthState = "open"
	StateHalfOpen HealthState = "half_open"
)

// VendorHealth is a point-in-time snapshot of one vendor's breaker state —
// the shape the admin health endpoint (docs/plan/40 Task T5) serializes
// directly.
type VendorHealth struct {
	Vendor              string      `json:"vendor"`
	State               HealthState `json:"state"`
	ConsecutiveFailures int         `json:"consecutive_failures"`
	OpenedAt            time.Time   `json:"opened_at,omitempty"`     // zero if never opened
	LastProbeAt         time.Time   `json:"last_probe_at,omitempty"` // zero if no half-open probe issued yet
}

type vendorState struct {
	mu                  sync.Mutex
	state               HealthState
	consecutiveFailures int
	openedAt            time.Time
	lastProbeAt         time.Time
}

// HealthTracker is a per-vendor circuit breaker (docs/plan/40 Task T1) —
// in-memory, per PROCESS. Each replica of a multi-replica deployment trips
// independently: this is a documented, ACCEPTED limitation, not a bug — a
// vendor outage is still detected and worked around per-replica, just with
// slower convergence than a shared (e.g. Redis-backed) breaker would give
// (docs/plan/42's own future-work list already names this deferral).
// Nothing about the anti-double-payout guarantee (docs/plan/40 Task T3)
// depends on breaker state agreeing across replicas — the breaker is
// PURELY an availability optimization (skip a vendor already known to be
// down); the authoritative anti-double-payout rule lives entirely in
// payout_vendor_calls, independent of this type.
//
// State machine: closed -> open (after failureThreshold consecutive
// failures) -> half-open (after cooldown elapses, exactly one caller wins
// the single probe slot) -> closed (probe succeeds) or open again (probe
// fails, no need to re-accumulate the threshold — one failed probe is
// already proof the vendor is still down).
type HealthTracker struct {
	failureThreshold int
	cooldown         time.Duration
	logger           *slog.Logger

	mu      sync.Mutex
	vendors map[string]*vendorState
}

// NewHealthTracker constructs a tracker. failureThreshold<=0 defaults to 5,
// cooldown<=0 defaults to 30s (matching BREAKER_FAILURE_THRESHOLD /
// BREAKER_COOLDOWN's own env defaults in internal/config). nil logger
// defaults to slog.Default().
func NewHealthTracker(failureThreshold int, cooldown time.Duration, logger *slog.Logger) *HealthTracker {
	if failureThreshold <= 0 {
		failureThreshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthTracker{
		failureThreshold: failureThreshold,
		cooldown:         cooldown,
		logger:           logger,
		vendors:          make(map[string]*vendorState),
	}
}

func (t *HealthTracker) stateFor(vendor string) *vendorState {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.vendors[vendor]
	if !ok {
		v = &vendorState{state: StateClosed}
		t.vendors[vendor] = v
	}
	return v
}

// Allow reports whether a call to vendor should be attempted right now.
//
//   - closed: always true.
//   - open, cooldown not yet elapsed: false.
//   - open, cooldown elapsed: the CALLER THAT WINS THE LOCK transitions the
//     vendor to half-open and returns true — it IS the probe. Every other
//     concurrent caller serializes behind the same per-vendor mutex and
//     observes the (now) half-open state, so exactly one goroutine ever
//     gets true for a given cooldown expiry (the "single probe" DoD
//     requirement) — never two, regardless of how many goroutines call
//     Allow at once.
//   - half-open: false for anyone arriving while a probe is already in
//     flight — the probe's own outcome (RecordSuccess/RecordFailure)
//     is what resolves the state next, not a second concurrent attempt.
func (t *HealthTracker) Allow(vendor string) bool {
	v := t.stateFor(vendor)
	v.mu.Lock()
	defer v.mu.Unlock()

	switch v.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(v.openedAt) < t.cooldown {
			return false
		}
		v.state = StateHalfOpen
		v.lastProbeAt = time.Now()
		return true
	default: // StateHalfOpen
		return false
	}
}

// RecordSuccess reports that a call to vendor completed WITHOUT a
// transport/infra error — this includes a synchronous BUSINESS rejection
// (invalid destination, insufficient vendor-side funds, etc.): the vendor
// was reachable and responded, so from the breaker's point of view (pure
// availability, gotcha #13 master) this is success. Closes the circuit
// (or confirms a half-open probe) and resets the failure counter.
func (t *HealthTracker) RecordSuccess(vendor string) {
	v := t.stateFor(vendor)
	v.mu.Lock()
	defer v.mu.Unlock()

	wasOpenOrProbing := v.state != StateClosed
	v.consecutiveFailures = 0
	v.state = StateClosed
	v.openedAt = time.Time{}
	if wasOpenOrProbing {
		t.logger.Info("vendorgw: breaker closed", slog.String("vendor", vendor))
	}
}

// RecordFailure reports a transport/infra failure (network error, timeout,
// 5xx) for vendor — NEVER call this for a business rejection (gotcha #13
// master: business rejections must not trip the breaker). A half-open
// probe that fails re-opens immediately without needing to re-accumulate
// failureThreshold — one failed probe is already sufficient evidence the
// vendor is still down.
func (t *HealthTracker) RecordFailure(vendor string) {
	v := t.stateFor(vendor)
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.state == StateHalfOpen {
		v.state = StateOpen
		v.openedAt = time.Now()
		t.logger.Warn("vendorgw: half-open probe failed, breaker re-opened", slog.String("vendor", vendor))
		return
	}

	v.consecutiveFailures++
	if v.state == StateClosed && v.consecutiveFailures >= t.failureThreshold {
		v.state = StateOpen
		v.openedAt = time.Now()
		t.logger.Warn("vendorgw: breaker opened", slog.String("vendor", vendor), slog.Int("consecutive_failures", v.consecutiveFailures))
	}
}

// Snapshot returns every vendor this tracker has ever seen, sorted by name
// for deterministic output (admin endpoint, docs/plan/40 Task T5).
func (t *HealthTracker) Snapshot() []VendorHealth {
	t.mu.Lock()
	names := make([]string, 0, len(t.vendors))
	states := make(map[string]*vendorState, len(t.vendors))
	for name, v := range t.vendors {
		names = append(names, name)
		states[name] = v
	}
	t.mu.Unlock()

	sort.Strings(names)
	out := make([]VendorHealth, 0, len(names))
	for _, name := range names {
		v := states[name]
		v.mu.Lock()
		out = append(out, VendorHealth{
			Vendor: name, State: v.state, ConsecutiveFailures: v.consecutiveFailures,
			OpenedAt: v.openedAt, LastProbeAt: v.lastProbeAt,
		})
		v.mu.Unlock()
	}
	return out
}
