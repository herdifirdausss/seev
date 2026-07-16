package generalutil

import "github.com/google/uuid"

func NullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// NullUUID returns nil for uuid.Nil so a SQL column ends up NULL rather than
// the all-zero UUID literal — used for ledger_transactions.source/
// destination_account_id, which are legitimately absent for processors whose
// money movement isn't a single source->destination pair (docs/plan/14 Task
// T1).
func NullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
