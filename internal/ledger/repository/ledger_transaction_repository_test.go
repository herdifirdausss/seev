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
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

// mockDigestRingForTest is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3's (K7) fixed test
// key for this file's sqlmock-based unit tests — none of them exercise
// Insert/FindConflictOrDuplicate (those need a real Postgres unique
// constraint to race against, covered by schema_contract_test.go's own
// integration tests instead), so the ring's actual key material is never
// observed here, only required by NewTransactionRepository's own
// non-nil constructor guard.
func mockDigestRingForTest(t *testing.T) *cryptox.DigestRing {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 53)
	}
	ring, err := cryptox.NewDigestRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

// ─── GetByID: uuid.Parse not uuid.MustParse (docs/roadmap/archive/12 Task T6) ────────────

var txColumns = []string{
	"id", "idempotency_key", "idempotency_scope", "type", "status", "amount", "currency",
	"source_account_id", "destination_account_id", "error_message",
	"external_ref", "gateway", "created_at", "updated_at",
}

func TestGetByID_ValidRow_ParsesAccountIDs(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewTransactionRepository(db, mockDigestRingForTest(t))
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
	repo := NewTransactionRepository(db, mockDigestRingForTest(t))
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
	repo := NewTransactionRepository(db, mockDigestRingForTest(t))
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
	repo := NewTransactionRepository(db, mockDigestRingForTest(t))
	ctx := context.Background()

	mock.ExpectQuery(`SELECT id, idempotency_key`).WillReturnError(sql.ErrNoRows)

	_, err := repo.GetByID(ctx, uuid.New())

	require.Error(t, err)
	require.True(t, errors.Is(err, apperror.ErrTransactionNotFound))
}
