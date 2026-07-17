package feepolicy

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/database"
)

func testPolicy(t *testing.T) (*Policy, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	dbHandle := database.NewFromSQL(sqlDB, database.Config{})
	return New(dbHandle, repository.NewFeeRepository(dbHandle)), mock
}

func expectRule(mock sqlmock.Sqlmock, userID uuid.UUID, txType, gateway, currency string, flat, bps int64, feeGateway string) {
	mock.ExpectQuery(`SELECT flat_minor_units, percent_basis_pts, fee_gateway`).
		WithArgs(txType, currency, userID, gateway).
		WillReturnRows(sqlmock.NewRows([]string{"flat_minor_units", "percent_basis_pts", "fee_gateway"}).
			AddRow(flat, bps, feeGateway))
}

func TestResolveSpecificityMatrix(t *testing.T) {
	userID := uuid.New()
	levels := []struct {
		name string
		flat int64
	}{
		{name: "exact user and route", flat: 401},
		{name: "user default", flat: 302},
		{name: "route default", flat: 203},
		{name: "global default", flat: 104},
	}
	for _, tc := range levels {
		t.Run(tc.name, func(t *testing.T) {
			policy, mock := testPolicy(t)
			expectRule(mock, userID, "money_in", "bca", "IDR", tc.flat, 0, "platform")
			fee, gateway, ok := policy.Resolve(context.Background(), userID, "money_in", "bca", "IDR", decimal.NewFromInt(10_000))
			require.True(t, ok)
			require.Equal(t, decimal.NewFromInt(tc.flat), fee)
			require.Equal(t, "platform", gateway)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestResolveFlatPlusPercentTruncates(t *testing.T) {
	policy, mock := testPolicy(t)
	userID := uuid.New()
	expectRule(mock, userID, "transfer_p2p", "", "IDR", 100, 33, "")
	fee, gateway, ok := policy.Resolve(context.Background(), userID, "transfer_p2p", "", "IDR", decimal.NewFromInt(1_000))
	require.True(t, ok)
	require.Equal(t, decimal.NewFromInt(103), fee)
	require.Equal(t, "platform", gateway)
}

func TestResolveDisabledOrNoMatch(t *testing.T) {
	policy, mock := testPolicy(t)
	userID := uuid.New()
	mock.ExpectQuery(`SELECT flat_minor_units, percent_basis_pts, fee_gateway`).
		WithArgs("transfer_p2p", "IDR", userID, "").
		WillReturnError(sql.ErrNoRows)
	fee, gateway, ok := policy.Resolve(context.Background(), userID, "transfer_p2p", "", "IDR", decimal.NewFromInt(1_000))
	require.False(t, ok)
	require.True(t, fee.IsZero())
	require.Empty(t, gateway)
}

func TestResolveClamp(t *testing.T) {
	policy, mock := testPolicy(t)
	userID := uuid.New()
	expectRule(mock, userID, "transfer_p2p", "", "IDR", 1_000, 0, "platform")
	fee, gateway, ok := policy.Resolve(context.Background(), userID, "transfer_p2p", "", "IDR", decimal.NewFromInt(1_000))
	require.False(t, ok)
	require.True(t, fee.IsZero())
	require.Empty(t, gateway)
}
