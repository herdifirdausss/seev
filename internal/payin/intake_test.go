package payin

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/pkg/database"
)

func TestCreateTopupIntentPausedBeforeDomainSideEffects(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	db := database.NewFromSQL(sqlDB, config.PostgresConfig{Host: "localhost"}.Pkg())
	mock.ExpectQuery(`SELECT paused, revision, updated_by, updated_at FROM payin_intake_control`).WillReturnRows(sqlmock.NewRows([]string{"paused", "revision", "updated_by", "updated_at"}).AddRow(true, 4, "operator", time.Now()))

	module := &Module{db: db}
	_, err = module.CreateTopupIntent(context.Background(), uuid.New(), decimal.NewFromInt(100))
	require.ErrorIs(t, err, ErrIntakePaused)
	require.NoError(t, mock.ExpectationsWereMet())
}
