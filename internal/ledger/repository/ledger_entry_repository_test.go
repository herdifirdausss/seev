package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// ─── InsertEntries batching (docs/roadmap/archive/11 Task T2) ─────────────────────────────

func TestInsertEntries_Empty_NoOp(t *testing.T) {
	tx, mock := newMockTx(t)
	repo := NewEntryRepository(nil)

	err := repo.InsertEntries(context.Background(), tx, uuid.New(), nil, nil)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet(), "no SQL should be sent for an empty entries slice")
}

func TestInsertEntries_SingleRoundTrip_ForMultipleEntries(t *testing.T) {
	tx, mock := newMockTx(t)
	repo := NewEntryRepository(nil)

	acc1, acc2, acc3 := uuid.New(), uuid.New(), uuid.New()
	txID := uuid.New()
	entries := []model.EntryInstruction{
		{AccountID: acc1, Direction: constant.Debit, Amount: decimal.NewFromInt(1000)},
		{AccountID: acc2, Direction: constant.Credit, Amount: decimal.NewFromInt(950)},
		{AccountID: acc3, Direction: constant.Credit, Amount: decimal.NewFromInt(50)},
	}
	newBalances := map[uuid.UUID]decimal.Decimal{
		acc1: decimal.NewFromInt(4000),
		acc2: decimal.NewFromInt(950),
		acc3: decimal.NewFromInt(50),
	}

	// A single ExecContext expectation proves all 3 entries went in one
	// multi-row INSERT, not 3 separate round trips.
	mock.ExpectExec(`INSERT INTO ledger_entries`).
		WillReturnResult(sqlmock.NewResult(0, 3))

	err := repo.InsertEntries(context.Background(), tx, txID, entries, newBalances)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInsertEntries_BatchTooLarge_Rejected(t *testing.T) {
	tx, mock := newMockTx(t)
	repo := NewEntryRepository(nil)

	entries := make([]model.EntryInstruction, maxEntriesBatch+1)
	for i := range entries {
		entries[i] = model.EntryInstruction{AccountID: uuid.New(), Direction: constant.Debit, Amount: decimal.NewFromInt(1)}
	}

	err := repo.InsertEntries(context.Background(), tx, uuid.New(), entries, map[uuid.UUID]decimal.Decimal{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "too large")
	require.NoError(t, mock.ExpectationsWereMet(), "no SQL should be sent when the batch is rejected upfront")
}
