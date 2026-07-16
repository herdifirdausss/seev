package currency

import (
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestIsValid_BootstrapDefaultIsIDR(t *testing.T) {
	Load([]Currency{{Code: "IDR", MinorUnit: 0}})
	assert.True(t, IsValid("IDR"))
	assert.False(t, IsValid("USD"))
}

func TestLoad_ReplacesRegistryEntirely(t *testing.T) {
	Load([]Currency{{Code: "IDR", MinorUnit: 0}, {Code: "USD", MinorUnit: 2}})
	assert.True(t, IsValid("IDR"))
	assert.True(t, IsValid("USD"))

	Load([]Currency{{Code: "USD", MinorUnit: 2}})
	assert.False(t, IsValid("IDR"), "a second Load must fully replace the registry, not merge")
	assert.True(t, IsValid("USD"))

	// restore for other tests in this file/package
	Load([]Currency{{Code: "IDR", MinorUnit: 0}, {Code: "USD", MinorUnit: 2}})
}

func TestMinorUnit_KnownAndUnknown(t *testing.T) {
	Load([]Currency{{Code: "IDR", MinorUnit: 0}, {Code: "USD", MinorUnit: 2}})

	exp, ok := MinorUnit("USD")
	assert.True(t, ok)
	assert.Equal(t, int16(2), exp)

	_, ok = MinorUnit("XYZ")
	assert.False(t, ok)
}

func TestToMajor_ZeroExponentIsNoOp(t *testing.T) {
	Load([]Currency{{Code: "IDR", MinorUnit: 0}})
	got := ToMajor(decimal.NewFromInt(150000), "IDR")
	assert.True(t, got.Equal(decimal.NewFromInt(150000)))
}

func TestToMajor_TwoExponentDivides(t *testing.T) {
	Load([]Currency{{Code: "USD", MinorUnit: 2}})
	got := ToMajor(decimal.NewFromInt(150000), "USD")
	assert.True(t, got.Equal(decimal.RequireFromString("1500.00")), "got %s", got)
}

func TestToMajor_UnknownCurrency_NoOpRatherThanPanic(t *testing.T) {
	Load([]Currency{{Code: "IDR", MinorUnit: 0}})
	got := ToMajor(decimal.NewFromInt(500), "ZZZ")
	assert.True(t, got.Equal(decimal.NewFromInt(500)), "an unregistered currency must degrade to no conversion, not crash a report")
}

func TestLoad_ConcurrentReadWrite_NoRace(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			Load([]Currency{{Code: "IDR", MinorUnit: 0}, {Code: "USD", MinorUnit: 2}})
		}()
		go func() {
			defer wg.Done()
			_ = IsValid("IDR")
			_, _ = MinorUnit("USD")
		}()
	}
	wg.Wait()
}
