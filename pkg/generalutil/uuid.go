package generalutil

import (
	"sort"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// NewV7 generates a time-ordered (version 7) UUID for insert-heavy tables
// (docs/roadmap/archive/11 Task T4) — ledger_transactions.id, ledger_entries.id,
// outbox_events.id. A v4 (random) primary key scatters inserts across the
// whole btree, causing more page splits and worse buffer cache locality
// than a monotonically-ish increasing key at high insert volume; v7 keeps
// new rows clustered at the right edge of the index instead.
//
// Falls back to uuid.New() (v4) on error — NewV7 only fails if the OS's
// CSPRNG read fails, which is exceptionally rare and not worth propagating
// as a hard error through the entire posting pipeline. A v4 fallback is
// still a fully valid, unique primary key; it just loses the insert-order
// clustering benefit for that one row.
func NewV7() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}

// Deduplicate removes repeated IDs while preserving the first occurrence's
// position. Positional order must be preserved here (e.g. processors index
// ResolvedCommand.AccountIDs as [source, destination, fee]) — lock ordering
// is handled independently by the DB query's own ORDER BY (see
// repository.BalanceRepository.LockBalances), so resorting IDs at this layer
// would only scramble positional meaning without adding any deadlock-safety
// benefit. A prior sorting variant of this function was in fact the root
// cause of a real money-safety bug (silently swapped debit/credit direction
// whenever a system account's UUID happened to sort before the user's) and
// was removed entirely rather than left around unused.
func Deduplicate(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func SortedDecimalKeys(m map[uuid.UUID]decimal.Decimal) []uuid.UUID {
	keys := make([]uuid.UUID, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		for k := 0; k < 16; k++ {
			if keys[i][k] != keys[j][k] {
				return keys[i][k] < keys[j][k]
			}
		}
		return false
	})
	return keys
}
