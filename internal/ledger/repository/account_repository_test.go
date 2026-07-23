package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/pkg/database"
)

func newMockDB(t *testing.T) (*database.DBSQL, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	return database.NewFromSQL(sqlDB, config.PostgresConfig{Host: "localhost"}.Pkg()), mock
}

// ─── Account resolution caching (docs/roadmap/archive/11 Task T3) ─────────────────────────

func TestGetSystemAccountID_SecondCall_HitsCacheNotDB(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()
	wantID := uuid.New()

	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wantID))

	got1, err := repo.GetSystemAccountID(ctx, "settlement", "bca", "IDR")
	require.NoError(t, err)
	require.Equal(t, wantID, got1)

	// Second call for the SAME (type, qualifier, currency) must be served
	// from cache — only one query expectation was registered above, so a
	// second DB hit would fail ExpectationsWereMet.
	got2, err := repo.GetSystemAccountID(ctx, "settlement", "bca", "IDR")
	require.NoError(t, err)
	require.Equal(t, wantID, got2)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSystemAccountID_DifferentQualifier_MissesCache(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()
	bcaID, gopayID := uuid.New(), uuid.New()

	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(bcaID))
	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(gopayID))

	got1, err := repo.GetSystemAccountID(ctx, "settlement", "bca", "IDR")
	require.NoError(t, err)
	require.Equal(t, bcaID, got1)

	got2, err := repo.GetSystemAccountID(ctx, "settlement", "gopay", "IDR")
	require.NoError(t, err)
	require.Equal(t, gopayID, got2)

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetSystemAccountID_DifferentCurrency_MissesCache is T2's own required
// test (docs/roadmap/archive/18): the SAME (type, qualifier) pair must resolve to a
// DIFFERENT account, and hit the DB again, when currency differs — proving
// the cache key doesn't collide across currencies for the same gateway.
func TestGetSystemAccountID_DifferentCurrency_MissesCache(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()
	idrID, usdID := uuid.New(), uuid.New()

	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(idrID))
	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(usdID))

	got1, err := repo.GetSystemAccountID(ctx, "settlement", "bca", "IDR")
	require.NoError(t, err)
	require.Equal(t, idrID, got1)

	got2, err := repo.GetSystemAccountID(ctx, "settlement", "bca", "USD")
	require.NoError(t, err)
	require.Equal(t, usdID, got2)
	require.NotEqual(t, got1, got2)

	// Both must now be cache-served — no more query expectations queued.
	got1Again, err := repo.GetSystemAccountID(ctx, "settlement", "bca", "IDR")
	require.NoError(t, err)
	require.Equal(t, idrID, got1Again)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSystemAccountID_NotFound_NeverCached(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()

	// Two identical failed lookups must hit the DB TWICE — a "not found"
	// result must never be cached (a system account can legitimately be
	// provisioned by a later migration while the process is still running).
	mock.ExpectQuery(`SELECT id FROM accounts`).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT id FROM accounts`).WillReturnError(sql.ErrNoRows)

	_, err := repo.GetSystemAccountID(ctx, "settlement", "nonexistent", "IDR")
	require.Error(t, err)
	require.ErrorIs(t, err, apperror.ErrAccountNotFound)

	_, err = repo.GetSystemAccountID(ctx, "settlement", "nonexistent", "IDR")
	require.Error(t, err)
	require.ErrorIs(t, err, apperror.ErrAccountNotFound)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountID_SecondCall_HitsCacheNotDB(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()
	userID := uuid.New()
	wantID := uuid.New()

	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wantID))

	got1, err := repo.GetAccountID(ctx, userID, "cash")
	require.NoError(t, err)
	require.Equal(t, wantID, got1)

	got2, err := repo.GetAccountID(ctx, userID, "cash")
	require.NoError(t, err)
	require.Equal(t, wantID, got2)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPocketAccountID_SecondCall_HitsCacheNotDB(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()
	userID := uuid.New()
	wantID := uuid.New()

	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wantID))

	got1, err := repo.GetPocketAccountID(ctx, userID, "travel")
	require.NoError(t, err)
	require.Equal(t, wantID, got1)

	got2, err := repo.GetPocketAccountID(ctx, userID, "travel")
	require.NoError(t, err)
	require.Equal(t, wantID, got2)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPocketAccountID_DifferentUser_MissesCache(t *testing.T) {
	// Guards against a cache key collision bug — two different users with
	// the same pocket_code must not share a cache entry.
	db, mock := newMockDB(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()
	idA, idB := uuid.New(), uuid.New()

	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(idA))
	mock.ExpectQuery(`SELECT id FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(idB))

	gotA, err := repo.GetPocketAccountID(ctx, userA, "travel")
	require.NoError(t, err)
	require.Equal(t, idA, gotA)

	gotB, err := repo.GetPocketAccountID(ctx, userB, "travel")
	require.NoError(t, err)
	require.Equal(t, idB, gotB)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountCurrency_SecondCall_HitsCacheNotDB(t *testing.T) {
	db, mock := newMockDB(t)
	repo := NewAccountRepository(db)
	ctx := context.Background()
	accountID := uuid.New()

	mock.ExpectQuery(`SELECT currency FROM accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"currency"}).AddRow("IDR"))

	got1, err := repo.GetAccountCurrency(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, "IDR", got1)

	got2, err := repo.GetAccountCurrency(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, "IDR", got2)

	require.NoError(t, mock.ExpectationsWereMet())
}
