package ledger

import "github.com/google/uuid"

// deduplicate preserves command account order. The order carries debit and
// credit meaning; database lock ordering is handled by the repository.
func deduplicate(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
