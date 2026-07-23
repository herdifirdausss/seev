package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// newMockTx returns a *sql.Tx backed by sqlmock, for direct unit tests of
// repository methods that take a tx parameter instead of going through
// database.DatabaseSQL.
func newMockTx(t *testing.T) (*sql.Tx, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })

	mock.ExpectBegin()
	tx, err := sqlDB.Begin()
	require.NoError(t, err)

	return tx, mock
}

// ─── UpdateBalances non-integral guard (docs/roadmap/archive/10 Task T4) ─────────────────

func TestUpdateBalances_NonIntegralBalance_RejectedBeforeAnyQuery(t *testing.T) {
	tx, mock := newMockTx(t)
	repo := NewBalanceRepository(nil)

	accID := uuid.New()
	newBalances := map[uuid.UUID]decimal.Decimal{
		accID: decimal.RequireFromString("100.5"),
	}

	err := repo.UpdateBalances(context.Background(), tx, newBalances)

	require.Error(t, err)
	require.Contains(t, err.Error(), "non-integral")
	// The guard must fire BEFORE any SQL is sent — no UPDATE expectation
	// was ever registered, so ExpectationsWereMet would fail if one leaked.
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateBalances_IntegralBalance_ProceedsToQuery(t *testing.T) {
	tx, mock := newMockTx(t)
	repo := NewBalanceRepository(nil)

	accID := uuid.New()
	newBalances := map[uuid.UUID]decimal.Decimal{
		accID: decimal.NewFromInt(100),
	}

	mock.ExpectExec(`UPDATE account_balances`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateBalances(context.Background(), tx, newBalances)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
