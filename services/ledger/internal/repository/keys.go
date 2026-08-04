package repository

import (
	"sort"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func sortedDecimalKeys(values map[uuid.UUID]decimal.Decimal) []uuid.UUID {
	keys := make([]uuid.UUID, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		for index := range 16 {
			if keys[i][index] != keys[j][index] {
				return keys[i][index] < keys[j][index]
			}
		}
		return false
	})
	return keys
}
