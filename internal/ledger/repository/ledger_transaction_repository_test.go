package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
)

// ─── GetByID: uuid.Parse not uuid.MustParse (docs/plan/12 Task T6) ────────────

var txColumns = []string{
	"id", "idempotency_key", "idempotency_scope", "type", "status", "amount", "currency",
	"source_account_id", "destination_account_id", "error_message",
	"external_ref", "gateway", "created_at", "updated_at",
}

func TestGetByID_ValidRow_ParsesAccountIDs(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewTransactionRepository(db)
	ctx := context.Background()

	txID := uuid.New()
	srcID := uuid.New()
	dstID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(`SELECT id, idempotency_key`).
		WillReturnRows(sqlmock.NewRows(txColumns).
			AddRow(txID, "idem-1", "scope-1", "money_in", "posted", "1000", "IDR",
				srcID.String(), dstID.String(), nil, nil, nil, now, now))

	tx, err := repo.GetByID(ctx, txID)

	require.NoError(t, err)
	require.Equal(t, srcID, tx.SourceAccountID)
	require.Equal(t, dstID, tx.DestinationAccountID)
}

func TestGetByID_MalformedStoredSourceAccountID_ReturnsErrorNotPanic(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewTransactionRepository(db)
	ctx := context.Background()

	txID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(`SELECT id, idempotency_key`).
		WillReturnRows(sqlmock.NewRows(txColumns).
			AddRow(txID, "idem-1", "scope-1", "money_in", "posted", "1000", "IDR",
				"not-a-valid-uuid", nil, nil, nil, nil, now, now))

	var tx interface{}
	var err error
	require.NotPanics(t, func() {
		tx, err = repo.GetByID(ctx, txID)
		_ = tx
	})
	require.Error(t, err, "a corrupted stored UUID must return an error, not panic the process")
}

func TestGetByID_MalformedStoredDestinationAccountID_ReturnsErrorNotPanic(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewTransactionRepository(db)
	ctx := context.Background()

	txID := uuid.New()
	srcID := uuid.New()
	now := time.Now()

	mock.ExpectQuery(`SELECT id, idempotency_key`).
		WillReturnRows(sqlmock.NewRows(txColumns).
			AddRow(txID, "idem-1", "scope-1", "money_in", "posted", "1000", "IDR",
				srcID.String(), "also-not-a-uuid", nil, nil, nil, now, now))

	require.NotPanics(t, func() {
		_, err := repo.GetByID(ctx, txID)
		require.Error(t, err)
	})
}

func TestGetByID_NotFound_ReturnsSentinel(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewTransactionRepository(db)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT id, idempotency_key`).WillReturnError(sql.ErrNoRows)

	_, err := repo.GetByID(ctx, uuid.New())

	require.Error(t, err)
	require.True(t, errors.Is(err, apperror.ErrTransactionNotFound))
}
