package generalutil

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// ─── NewV7 (docs/plan/11 Task T4) ───────────────────────────────────────────────

func TestNewV7_ReturnsValidVersion7UUID(t *testing.T) {
	id := NewV7()
	assert.NotEqual(t, uuid.Nil, id)
	assert.Equal(t, uuid.Version(7), id.Version())
}

func TestNewV7_Unique(t *testing.T) {
	seen := make(map[uuid.UUID]bool)
	for i := 0; i < 1000; i++ {
		id := NewV7()
		assert.False(t, seen[id], "duplicate UUID generated")
		seen[id] = true
	}
}

func TestNewV7_MonotonicallyIncreasing(t *testing.T) {
	// v7's time-ordered prefix means sequentially generated IDs sort in
	// generation order — the entire point of using v7 for insert-heavy
	// tables (docs/plan/11 Task T4: keeps the btree insert-clustered).
	prev := NewV7()
	for i := 0; i < 100; i++ {
		next := NewV7()
		assert.True(t, prev.String() < next.String(),
			"expected %s < %s (v7 IDs generated in sequence must sort in generation order)", prev, next)
		prev = next
	}
}
