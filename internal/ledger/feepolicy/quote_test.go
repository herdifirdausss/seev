package feepolicy

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateQuote_ResolvesFeeAndPersists(t *testing.T) {
	policy, mock := testPolicy(t)
	userID := uuid.New()
	amount := decimal.NewFromInt(100_000)

	expectRule(mock, userID, "transfer_p2p", "", "IDR", 500, 0, "platform")
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO fee_quotes")).
		WithArgs(sqlmock.AnyArg(), userID, "transfer_p2p", "", "IDR", amount, decimal.NewFromInt(500), "platform", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	q, err := policy.CreateQuote(context.Background(), userID, "transfer_p2p", "", "IDR", amount, 0)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, q.ID)
	assert.True(t, q.FeeAmount.Equal(decimal.NewFromInt(500)))
	assert.Equal(t, "platform", q.FeeGateway)
	assert.WithinDuration(t, time.Now().Add(DefaultQuoteTTL), q.ExpiresAt, 2*time.Second)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateQuote_NoRuleMatched_ZeroFeeStillQuoted(t *testing.T) {
	policy, mock := testPolicy(t)
	userID := uuid.New()
	amount := decimal.NewFromInt(100_000)

	mock.ExpectQuery(regexp.QuoteMeta(resolveQuery)).
		WithArgs("money_in", "IDR", userID, "").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO fee_quotes")).
		WithArgs(sqlmock.AnyArg(), userID, "money_in", "", "IDR", amount, decimal.Zero, "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	q, err := policy.CreateQuote(context.Background(), userID, "money_in", "", "IDR", amount, time.Minute)
	require.NoError(t, err)
	assert.True(t, q.FeeAmount.IsZero())
	assert.Equal(t, "", q.FeeGateway)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConsumeQuote_HappyPath(t *testing.T) {
	policy, mock := testPolicy(t)
	quoteID, userID := uuid.New(), uuid.New()
	amount := decimal.NewFromInt(100_000)

	mock.ExpectQuery(regexp.QuoteMeta(consumeQuoteQuery)).
		WithArgs(quoteID, userID, "transfer_p2p", "IDR", amount, "tx:abc").
		WillReturnRows(sqlmock.NewRows([]string{"fee_amount", "fee_gateway"}).AddRow(500, "platform"))

	fee, feeGateway, err := policy.ConsumeQuote(context.Background(), policy.db, quoteID, userID, "transfer_p2p", "IDR", amount, "tx:abc")
	require.NoError(t, err)
	assert.True(t, fee.Equal(decimal.NewFromInt(500)))
	assert.Equal(t, "platform", feeGateway)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConsumeQuote_TrulyExpiredOrConsumedOrMissing_ErrQuoteExpired(t *testing.T) {
	policy, mock := testPolicy(t)
	quoteID, userID := uuid.New(), uuid.New()
	amount := decimal.NewFromInt(100_000)

	mock.ExpectQuery(regexp.QuoteMeta(consumeQuoteQuery)).
		WithArgs(quoteID, userID, "transfer_p2p", "IDR", amount, "tx:abc").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(classifyQuoteQuery)).
		WithArgs(quoteID, userID).
		WillReturnError(sql.ErrNoRows)

	_, _, err := policy.ConsumeQuote(context.Background(), policy.db, quoteID, userID, "transfer_p2p", "IDR", amount, "tx:abc")
	assert.ErrorIs(t, err, ErrQuoteExpired)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConsumeQuote_ExistsButAmountMismatch_ErrQuoteMismatch_NotBurned(t *testing.T) {
	policy, mock := testPolicy(t)
	quoteID, userID := uuid.New(), uuid.New()
	wrongAmount := decimal.NewFromInt(100_001) // off by one

	mock.ExpectQuery(regexp.QuoteMeta(consumeQuoteQuery)).
		WithArgs(quoteID, userID, "transfer_p2p", "IDR", wrongAmount, "tx:abc").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(classifyQuoteQuery)).
		WithArgs(quoteID, userID).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	_, _, err := policy.ConsumeQuote(context.Background(), policy.db, quoteID, userID, "transfer_p2p", "IDR", wrongAmount, "tx:abc")
	assert.ErrorIs(t, err, ErrQuoteMismatch, "a mismatched attempt must NOT burn the quote — the classify query proving it's still unconsumed is what selects this sentinel")
	require.NoError(t, mock.ExpectationsWereMet())
}
