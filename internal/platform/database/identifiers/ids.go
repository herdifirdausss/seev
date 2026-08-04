package identifiers

import "github.com/google/uuid"

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
