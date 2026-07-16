package generalutil

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestMetaString(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		meta := map[string]any{
			"name": "alice",
		}

		v, err := MetaString(meta, "name")

		assert.NoError(t, err)
		assert.Equal(t, "alice", v)
	})

	t.Run("missing key", func(t *testing.T) {
		meta := map[string]any{}

		_, err := MetaString(meta, "name")

		assert.Error(t, err)
	})

	t.Run("wrong type", func(t *testing.T) {
		meta := map[string]any{
			"name": 123,
		}

		_, err := MetaString(meta, "name")

		assert.Error(t, err)
	})
}

func TestMetaUUID(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		id := uuid.New()

		meta := map[string]any{
			"id": id.String(),
		}

		v, err := MetaUUID(meta, "id")

		assert.NoError(t, err)
		assert.Equal(t, id, v)
	})

	t.Run("missing key", func(t *testing.T) {
		meta := map[string]any{}

		_, err := MetaUUID(meta, "id")

		assert.Error(t, err)
	})

	t.Run("invalid uuid", func(t *testing.T) {
		meta := map[string]any{
			"id": "not-a-uuid",
		}

		_, err := MetaUUID(meta, "id")

		assert.Error(t, err)
	})
}

func TestMetaDecimal(t *testing.T) {
	t.Run("string success", func(t *testing.T) {
		meta := map[string]any{
			"amount": "10.25",
		}

		v, err := MetaDecimal(meta, "amount")

		assert.NoError(t, err)
		assert.True(t, v.Equal(decimal.RequireFromString("10.25")))
	})

	t.Run("string invalid", func(t *testing.T) {
		meta := map[string]any{
			"amount": "invalid",
		}

		_, err := MetaDecimal(meta, "amount")

		assert.Error(t, err)
	})

	t.Run("float64", func(t *testing.T) {
		meta := map[string]any{
			"amount": float64(5.5),
		}

		v, err := MetaDecimal(meta, "amount")

		assert.NoError(t, err)
		assert.True(t, v.Equal(decimal.NewFromFloat(5.5)))
	})

	t.Run("int", func(t *testing.T) {
		meta := map[string]any{
			"amount": int(5),
		}

		v, err := MetaDecimal(meta, "amount")

		assert.NoError(t, err)
		assert.True(t, v.Equal(decimal.NewFromInt(5)))
	})

	t.Run("int64", func(t *testing.T) {
		meta := map[string]any{
			"amount": int64(7),
		}

		v, err := MetaDecimal(meta, "amount")

		assert.NoError(t, err)
		assert.True(t, v.Equal(decimal.NewFromInt(7)))
	})

	t.Run("missing key", func(t *testing.T) {
		meta := map[string]any{}

		_, err := MetaDecimal(meta, "amount")

		assert.Error(t, err)
	})

	t.Run("unsupported type", func(t *testing.T) {
		meta := map[string]any{
			"amount": true,
		}

		_, err := MetaDecimal(meta, "amount")

		assert.Error(t, err)
	})
}
