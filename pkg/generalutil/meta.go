package generalutil

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func MetaString(meta map[string]any, key string) (string, error) {
	v, ok := meta[key]
	if !ok {
		return "", fmt.Errorf("missing metadata key %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("metadata key %q is not a string (got %T)", key, v)
	}
	return s, nil
}

func MetaUUID(meta map[string]any, key string) (uuid.UUID, error) {
	s, err := MetaString(meta, key)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("metadata key %q invalid UUID: %w", key, err)
	}
	return id, nil
}

// MetaDecimal extracts a decimal.Decimal from metadata.
// Supported values are string, float64, int, and int64.
func MetaDecimal(meta map[string]any, key string) (decimal.Decimal, error) {
	v, ok := meta[key]
	if !ok {
		return decimal.Zero, fmt.Errorf("missing metadata key %q", key)
	}
	switch t := v.(type) {
	case string:
		d, err := decimal.NewFromString(t)
		if err != nil {
			return decimal.Zero, fmt.Errorf("metadata key %q invalid decimal: %w", key, err)
		}
		return d, nil
	case float64:
		// float64 → decimal: use the string round trip to preserve precision
		return decimal.NewFromFloat(t), nil
	case int:
		return decimal.NewFromInt(int64(t)), nil
	case int64:
		return decimal.NewFromInt(t), nil
	default:
		return decimal.Zero, fmt.Errorf("metadata key %q unsupported type %T", key, v)
	}
}
