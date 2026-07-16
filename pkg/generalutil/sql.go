package generalutil

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// buildArgs builds ($1,$2,...,$n) and []any from UUID slice.
// [FIX #8] Returns args in the SAME order as the placeholder string,
// preventing parameter mismatch after deduplication/sorting.
func BuildArgs(ids []uuid.UUID) (string, []any) {
	parts := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	return strings.Join(parts, ","), args
}
