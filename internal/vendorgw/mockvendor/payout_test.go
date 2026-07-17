package mockvendor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/vendorgw"
)

func destJSON(t *testing.T, mode string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"bank_code": "014", "account_no": "123", "mock_mode": mode})
	require.NoError(t, err)
	return b
}

func TestPayoutProvider_Vendor_ReturnsRegistryName(t *testing.T) {
	p := NewPayoutProvider(VendorName)
	assert.Equal(t, "mockvendor", p.Vendor())
}

func TestPayoutProvider_InstantSettle_ReturnsSettled(t *testing.T) {
	p := NewPayoutProvider(VendorName)
	result, err := p.Submit(context.Background(), "key-1", decimal.NewFromInt(100_000), "IDR", destJSON(t, ModeInstantSettle))
	require.NoError(t, err)
	assert.Equal(t, vendorgw.PayoutSettled, result.Status)
	assert.NotEmpty(t, result.VendorRef)
}

func TestPayoutProvider_Async_ReturnsPending_ThenCompletePendingSettles(t *testing.T) {
	p := NewPayoutProvider(VendorName)
	result, err := p.Submit(context.Background(), "key-2", decimal.NewFromInt(50_000), "IDR", destJSON(t, ModeAsync))
	require.NoError(t, err)
	assert.Equal(t, vendorgw.PayoutPending, result.Status)

	// Before completion, Query still reports Pending.
	q, err := p.Query(context.Background(), "key-2")
	require.NoError(t, err)
	assert.Equal(t, vendorgw.PayoutPending, q.Status)

	p.CompletePending("key-2", vendorgw.PayoutSettled, "")

	q2, err := p.Query(context.Background(), "key-2")
	require.NoError(t, err)
	assert.Equal(t, vendorgw.PayoutSettled, q2.Status)
}

func TestPayoutProvider_Fail_ReturnsFailedWithReason(t *testing.T) {
	p := NewPayoutProvider(VendorName)
	result, err := p.Submit(context.Background(), "key-3", decimal.NewFromInt(10_000), "IDR", destJSON(t, ModeFail))
	require.NoError(t, err)
	assert.Equal(t, vendorgw.PayoutFailed, result.Status)
	assert.NotEmpty(t, result.Reason)
}

func TestPayoutProvider_Timeout_ReturnsErrorEveryTime(t *testing.T) {
	p := NewPayoutProvider(VendorName)
	_, err := p.Submit(context.Background(), "key-4", decimal.NewFromInt(10_000), "IDR", destJSON(t, ModeTimeout))
	require.Error(t, err)

	// Retry with the same key must ALSO error — a timed-out submission is
	// never cached (nothing was actually received).
	_, err2 := p.Submit(context.Background(), "key-4", decimal.NewFromInt(10_000), "IDR", destJSON(t, ModeTimeout))
	require.Error(t, err2)
}

// TestPayoutProvider_Submit_IdempotentAcrossModes proves the "duplicate-safe"
// property from docs/plan/23 Task T2 test list: submitting the same key
// twice returns the IDENTICAL result both times, never a second transfer —
// verified across every terminal mode, not just the default.
func TestPayoutProvider_Submit_IdempotentAcrossModes(t *testing.T) {
	for _, mode := range []string{ModeInstantSettle, ModeAsync, ModeFail} {
		t.Run(mode, func(t *testing.T) {
			p := NewPayoutProvider(VendorName)
			key := "idem-key-" + mode
			first, err := p.Submit(context.Background(), key, decimal.NewFromInt(75_000), "IDR", destJSON(t, mode))
			require.NoError(t, err)

			second, err := p.Submit(context.Background(), key, decimal.NewFromInt(75_000), "IDR", destJSON(t, mode))
			require.NoError(t, err)

			assert.Equal(t, first, second, "a repeated Submit with the same idempotency key must return the identical result")
		})
	}
}

func TestPayoutProvider_Submit_ConcurrentSameKey_ExactlyOneEffectiveSubmission(t *testing.T) {
	p := NewPayoutProvider(VendorName)
	const concurrency = 20
	results := make([]vendorgw.PayoutResult, concurrency)
	errs := make([]error, concurrency)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = p.Submit(context.Background(), "concurrent-key", decimal.NewFromInt(1000), "IDR", destJSON(t, ModeInstantSettle))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "delivery %d", i)
		assert.Equal(t, results[0].VendorRef, results[i].VendorRef, "all concurrent submissions must observe the same vendor_ref")
	}
}

func TestPayoutProvider_Query_UnknownKey_Error(t *testing.T) {
	p := NewPayoutProvider(VendorName)
	_, err := p.Query(context.Background(), "never-submitted")
	assert.Error(t, err)
}

// TestPayoutProvider_IdempotencyCache_IsolatedAcrossVendorInstances proves
// docs/plan/40 Task T3's own required test: a payout that fails over from
// one vendor to another reuses the SAME idempotency key (the payout
// request ID, see orchestrate.go's submit()) against a DIFFERENT
// PayoutProvider instance — this must never read or leak the first
// vendor's cached result, since each named instance (docs/plan/40 Task T4's
// pulled-forward naming parameterization) owns its own independent
// submitted map.
func TestPayoutProvider_IdempotencyCache_IsolatedAcrossVendorInstances(t *testing.T) {
	vendorA := NewPayoutProvider("vendorA")
	vendorB := NewPayoutProvider("vendorB")
	const sharedKey = "shared-payout-id"

	rejectedA, err := vendorA.Submit(context.Background(), sharedKey, decimal.NewFromInt(100_000), "IDR", destJSON(t, ModeFail))
	require.NoError(t, err)
	require.Equal(t, vendorgw.PayoutFailed, rejectedA.Status)

	settledB, err := vendorB.Submit(context.Background(), sharedKey, decimal.NewFromInt(100_000), "IDR", destJSON(t, ModeInstantSettle))
	require.NoError(t, err)
	assert.Equal(t, vendorgw.PayoutSettled, settledB.Status,
		"vendorB must process the SAME key fresh, not read vendorA's cached Failed result")

	// vendorA's own cache must remain unaffected by vendorB's activity.
	requeryA, err := vendorA.Query(context.Background(), sharedKey)
	require.NoError(t, err)
	assert.Equal(t, vendorgw.PayoutFailed, requeryA.Status, "vendorA's cache must still show its own original result")
}
