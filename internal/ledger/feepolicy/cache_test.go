package feepolicy

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/database"
)

func testCachingRepo(t *testing.T, ttl time.Duration) (*CachingFeeRepository, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	dbHandle := database.NewFromSQL(sqlDB, database.Config{})
	return NewCachingFeeRepository(repository.NewFeeRepository(dbHandle), ttl), mock
}

func TestCachingFeeRepository_SecondCallWithinTTLSkipsDatabase(t *testing.T) {
	repo, mock := testCachingRepo(t, time.Minute)
	userID := uuid.New()
	expectRule(mock, userID, "transfer_p2p", "", "IDR", 500, 25, "platform")

	flat1, bps1, fg1, err1 := repo.ResolveRule(context.Background(), "transfer_p2p", "IDR", userID, "")
	require.NoError(t, err1)

	// A second identical call must NOT reach sqlmock at all — if it did,
	// mock.ExpectationsWereMet() below would fail because the single
	// ExpectQuery was already consumed and sqlmock has no unmet expectation
	// left to satisfy a second query.
	flat2, bps2, fg2, err2 := repo.ResolveRule(context.Background(), "transfer_p2p", "IDR", userID, "")
	require.NoError(t, err2)

	require.Equal(t, flat1, flat2)
	require.Equal(t, bps1, bps2)
	require.Equal(t, fg1, fg2)
	require.Equal(t, int64(500), flat1)
	require.Equal(t, int64(25), bps1)
	require.Equal(t, "platform", fg1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCachingFeeRepository_ExpiredEntryReQueries(t *testing.T) {
	repo, mock := testCachingRepo(t, time.Millisecond)
	userID := uuid.New()
	expectRule(mock, userID, "transfer_p2p", "", "IDR", 500, 0, "platform")
	expectRule(mock, userID, "transfer_p2p", "", "IDR", 700, 0, "platform")

	_, _, _, err := repo.ResolveRule(context.Background(), "transfer_p2p", "IDR", userID, "")
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)

	flat, _, _, err := repo.ResolveRule(context.Background(), "transfer_p2p", "IDR", userID, "")
	require.NoError(t, err)
	require.Equal(t, int64(700), flat, "expired entry must be re-fetched, not served stale")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCachingFeeRepository_NotFoundIsCached(t *testing.T) {
	repo, mock := testCachingRepo(t, time.Minute)
	userID := uuid.New()
	mock.ExpectQuery(`SELECT flat_minor_units, percent_basis_pts, fee_gateway`).
		WithArgs("money_in", "IDR", userID, "").
		WillReturnError(sql.ErrNoRows)

	_, _, _, err1 := repo.ResolveRule(context.Background(), "money_in", "IDR", userID, "")
	require.ErrorIs(t, err1, sql.ErrNoRows)

	// Second call must be served from cache (the single ExpectQuery above
	// only tolerates one call) — a cached "not found" must stay "not found",
	// not silently reinterpreted as anything else.
	_, _, _, err2 := repo.ResolveRule(context.Background(), "money_in", "IDR", userID, "")
	require.ErrorIs(t, err2, sql.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCachingFeeRepository_InfrastructureErrorNeverCached(t *testing.T) {
	repo, mock := testCachingRepo(t, time.Minute)
	userID := uuid.New()
	dbErr := sql.ErrConnDone
	mock.ExpectQuery(`SELECT flat_minor_units, percent_basis_pts, fee_gateway`).
		WithArgs("transfer_p2p", "IDR", userID, "").
		WillReturnError(dbErr)
	mock.ExpectQuery(`SELECT flat_minor_units, percent_basis_pts, fee_gateway`).
		WithArgs("transfer_p2p", "IDR", userID, "").
		WillReturnRows(sqlmock.NewRows([]string{"flat_minor_units", "percent_basis_pts", "fee_gateway"}).AddRow(500, 0, "platform"))

	_, _, _, err1 := repo.ResolveRule(context.Background(), "transfer_p2p", "IDR", userID, "")
	require.ErrorIs(t, err1, dbErr)

	// A transient error must NOT be cached — the retry must reach the
	// database again, not be served a memoized failure.
	flat, _, _, err2 := repo.ResolveRule(context.Background(), "transfer_p2p", "IDR", userID, "")
	require.NoError(t, err2)
	require.Equal(t, int64(500), flat)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCachingFeeRepository_DifferentKeysDoNotCollide(t *testing.T) {
	repo, mock := testCachingRepo(t, time.Minute)
	userA, userB := uuid.New(), uuid.New()
	expectRule(mock, userA, "transfer_p2p", "", "IDR", 500, 0, "platform")
	expectRule(mock, userB, "transfer_p2p", "", "IDR", 900, 0, "platform")

	flatA, _, _, err := repo.ResolveRule(context.Background(), "transfer_p2p", "IDR", userA, "")
	require.NoError(t, err)
	flatB, _, _, err := repo.ResolveRule(context.Background(), "transfer_p2p", "IDR", userB, "")
	require.NoError(t, err)

	require.Equal(t, int64(500), flatA)
	require.Equal(t, int64(900), flatB)
	require.NoError(t, mock.ExpectationsWereMet())
}
