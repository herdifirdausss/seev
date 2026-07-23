package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/shopspring/decimal"
)

// EntryInstruction is an in-memory debit/credit instruction produced by a
// processor's BuildEntries, before it is persisted as a ledger_entries row.
type EntryInstruction struct {
	AccountID uuid.UUID
	Direction constant.Direction
	Amount    decimal.Decimal
	Note      string
}

// LedgerEntryRecord is a row from ledger_entries, used by the Reversal processor.
type LedgerEntryRecord struct {
	EntryID   uuid.UUID
	AccountID uuid.UUID
	Direction constant.Direction
	Amount    decimal.Decimal
}

// LedgerEntry is the public read DTO for a ledger_entries row, returned by
// the entry-listing read API (GET /accounts/{id}/entries).
type LedgerEntry struct {
	ID            uuid.UUID
	TransactionID uuid.UUID
	AccountID     uuid.UUID
	Direction     constant.Direction
	Amount        decimal.Decimal
	BalanceAfter  decimal.Decimal
	Note          string
	CreatedAt     time.Time
}

// StatementEntry is one line of an account statement (docs/roadmap/archive/15 Task
// T2) — a ledger_entries row plus the type of transaction that produced it,
// in chronological order (statements read top-to-bottom by date, unlike
// LedgerEntry's newest-first keyset pagination).
type StatementEntry struct {
	ID              uuid.UUID
	TransactionID   uuid.UUID
	TransactionType string
	AccountID       uuid.UUID
	Direction       constant.Direction
	Amount          decimal.Decimal
	BalanceAfter    decimal.Decimal
	Note            string
	CreatedAt       time.Time
}

// Statement is the result of a period balance/entries query for one account
// (docs/roadmap/archive/15 Task T2). OpeningBalance/ClosingBalance are always integral
// minor-unit values, like every other monetary field in this codebase.
type Statement struct {
	AccountID      uuid.UUID
	Currency       string
	From, To       time.Time
	OpeningBalance decimal.Decimal
	ClosingBalance decimal.Decimal
	Entries        []StatementEntry
}
