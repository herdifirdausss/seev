package mockvendor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/vendorgw"
)

// payoutDestination is mockvendor's made-up payout destination shape.
// MockMode selects test behavior (docs/plan/23 Task T2) — a real vendor's
// destination would never carry this field; it exists purely so a single
// PayoutProvider instance can exercise every scenario a test needs without
// being reconstructed per scenario.
type payoutDestination struct {
	BankCode  string `json:"bank_code"`
	AccountNo string `json:"account_no"`
	MockMode  string `json:"mock_mode,omitempty"` // "" (default) = instant-settle; see mode constants below
}

const (
	ModeInstantSettle = ""        // default — Submit immediately returns Settled
	ModeAsync         = "async"   // Submit returns Pending; test calls CompletePending to finish it
	ModeFail          = "fail"    // Submit returns Failed (business — vendor rejected the destination)
	ModeTimeout       = "timeout" // Submit returns an error every time (infra failure, for retry tests)
)

// PayoutProvider implements vendorgw.PayoutProvider for mockvendor.
//
// Idempotency (docs/plan/23 Task T2 DoD: "Submit idempoten") is a property
// of EVERY mode, not a separate mode of its own — Submit caches its result
// by idempotencyKey and returns the cached result on any later call with
// the same key, regardless of mock_mode. The doc's "duplicate-safe" is
// therefore a TEST SCENARIO (call Submit twice, assert identical result),
// not a mock_mode value.
type PayoutProvider struct {
	mu        sync.Mutex
	submitted map[string]vendorgw.PayoutResult
}

func NewPayoutProvider() *PayoutProvider {
	return &PayoutProvider{submitted: make(map[string]vendorgw.PayoutResult)}
}

func (p *PayoutProvider) Vendor() string { return VendorName }

func (p *PayoutProvider) Submit(_ context.Context, idempotencyKey string, _ decimal.Decimal, _ string, destination json.RawMessage) (vendorgw.PayoutResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.submitted[idempotencyKey]; ok {
		return existing, nil
	}

	var dest payoutDestination
	if err := json.Unmarshal(destination, &dest); err != nil {
		return vendorgw.PayoutResult{}, fmt.Errorf("mockvendor: parse destination: %w", err)
	}

	switch dest.MockMode {
	case ModeTimeout:
		// Never cached — a genuinely infra-down vendor can't remember
		// what it never received; every retry hits the same timeout
		// until the test (or a real vendor recovering) stops simulating it.
		return vendorgw.PayoutResult{}, fmt.Errorf("mockvendor: submit timed out (simulated)")

	case ModeAsync:
		result := vendorgw.PayoutResult{VendorRef: "vref-" + idempotencyKey, Status: vendorgw.PayoutPending}
		p.submitted[idempotencyKey] = result
		return result, nil

	case ModeFail:
		result := vendorgw.PayoutResult{
			VendorRef: "vref-" + idempotencyKey, Status: vendorgw.PayoutFailed,
			Reason: "mockvendor: destination rejected (simulated)",
		}
		p.submitted[idempotencyKey] = result
		return result, nil

	default: // ModeInstantSettle
		result := vendorgw.PayoutResult{VendorRef: "vref-" + idempotencyKey, Status: vendorgw.PayoutSettled}
		p.submitted[idempotencyKey] = result
		return result, nil
	}
}

func (p *PayoutProvider) Query(_ context.Context, idempotencyKey string) (vendorgw.PayoutResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	result, ok := p.submitted[idempotencyKey]
	if !ok {
		return vendorgw.PayoutResult{}, fmt.Errorf("mockvendor: unknown payout %s", idempotencyKey)
	}
	return result, nil
}

// CompletePending simulates the vendor eventually finishing a Pending
// payout (what a real async vendor's own backend does out of band) — test
// code calls this to move a "async" mode submission to its final state,
// then either delivers it via the webhook receiver (docs/plan/22) or lets
// the resume job's Query pick it up (docs/plan/23 Task T3).
func (p *PayoutProvider) CompletePending(idempotencyKey string, status vendorgw.PayoutStatus, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	existing := p.submitted[idempotencyKey]
	p.submitted[idempotencyKey] = vendorgw.PayoutResult{VendorRef: existing.VendorRef, Status: status, Reason: reason}
}
