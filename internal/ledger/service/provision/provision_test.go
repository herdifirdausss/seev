package provision

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockDB(t *testing.T) (*database.DBSQL, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	return database.NewFromSQL(sqlDB, config.PostgresConfig{Host: "localhost"}.Pkg()), mock
}

func TestCreateUserAccounts_InvalidCurrency_Rejected(t *testing.T) {
	db, _ := newMockDB(t)
	svc := New(db)
	_, err := svc.CreateUserAccounts(context.Background(), uuid.New(), "USD")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreateUserAccounts_NilUserID_Rejected(t *testing.T) {
	db, _ := newMockDB(t)
	svc := New(db)
	_, err := svc.CreateUserAccounts(context.Background(), uuid.Nil, "IDR")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreateUserAccounts_ProvisionsStandardSet(t *testing.T) {
	db, mock := newMockDB(t)
	svc := New(db)
	userID := uuid.New()

	mock.ExpectBegin()
	for range standardAccountTypes {
		mock.ExpectQuery(`INSERT INTO accounts`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(uuid.New(), "active"))
		mock.ExpectExec(`INSERT INTO account_balances`).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	accounts, err := svc.CreateUserAccounts(context.Background(), userID, "IDR")
	require.NoError(t, err)
	assert.Len(t, accounts, len(standardAccountTypes))
	for i, acc := range accounts {
		assert.Equal(t, standardAccountTypes[i], acc.Type)
		assert.Equal(t, userID, acc.OwnerID)
		assert.Equal(t, "IDR", acc.Currency)
		assert.Equal(t, "active", acc.Status)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateUserAccounts_DBError_RollsBack(t *testing.T) {
	db, mock := newMockDB(t)
	svc := New(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO accounts`).WillReturnError(assert.AnError)
	mock.ExpectRollback()

	_, err := svc.CreateUserAccounts(context.Background(), uuid.New(), "IDR")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePocket_InvalidCode_Rejected(t *testing.T) {
	db, _ := newMockDB(t)
	svc := New(db)
	_, err := svc.CreatePocket(context.Background(), uuid.New(), "IDR", "Not Valid!")
	assert.ErrorIs(t, err, apperror.ErrValidation)
}

func TestCreatePocket_ValidCode_OK(t *testing.T) {
	db, mock := newMockDB(t)
	svc := New(db)
	userID := uuid.New()
	accID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO accounts`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(accID, "active"))
	mock.ExpectExec(`INSERT INTO account_balances`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	acc, err := svc.CreatePocket(context.Background(), userID, "IDR", "travel")
	require.NoError(t, err)
	assert.Equal(t, accID, acc.ID)
	assert.Equal(t, "travel", acc.PocketCode)
	assert.Equal(t, "pocket", acc.Type)
	assert.NoError(t, mock.ExpectationsWereMet())
}
