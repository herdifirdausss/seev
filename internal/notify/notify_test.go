package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/notify/model"
	"github.com/herdifirdausss/seev/internal/notify/repository"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newModule(t *testing.T) (*Module, *repository.MockRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	return &Module{repo: repo, logger: discardLogger()}, repo
}

func deliveryFor(t *testing.T, ev events.TransactionPosted, messageID string) amqp.Delivery {
	t.Helper()
	b, err := json.Marshal(ev)
	require.NoError(t, err)
	return amqp.Delivery{Body: b, MessageId: messageID}
}

func TestHandleDelivery_NonNotifiableType_NoOp(t *testing.T) {
	m, repo := newModule(t)
	txID := uuid.New()
	userID := uuid.New()
	ev := events.NewTransactionPosted(txID, "escrow_hold", "1000", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")

	// No repo.EXPECT() calls set up at all — a call to Insert would fail
	// the test via gomock's unexpected-call panic.
	err := m.handleDelivery(context.Background(), deliveryFor(t, ev, uuid.New().String()))
	assert.NoError(t, err)
	_ = repo
}

func TestHandleDelivery_MoneyIn_InsertsSingleRecipient(t *testing.T) {
	m, repo := newModule(t)
	txID := uuid.New()
	userID := uuid.New()
	msgID := uuid.New()
	ev := events.NewTransactionPosted(txID, "money_in", "500000", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")

	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, n model.Notification) (bool, error) {
			assert.Equal(t, userID, n.UserID)
			assert.Equal(t, msgID, n.EventID)
			assert.Equal(t, "money_in", n.Type)
			assert.NotEmpty(t, n.Title)
			assert.NotEmpty(t, n.Body)
			return true, nil
		})

	err := m.handleDelivery(context.Background(), deliveryFor(t, ev, msgID.String()))
	assert.NoError(t, err)
}

func TestHandleDelivery_TransferP2P_InsertsBothRecipients(t *testing.T) {
	m, repo := newModule(t)
	txID := uuid.New()
	sender := uuid.New()
	receiver := uuid.New()
	msgID := uuid.New()
	ev := events.NewTransactionPosted(txID, "transfer_p2p", "100000", "IDR", nil, nil, nil, "", time.Now(), &sender, &receiver, "")

	var gotUserIDs []uuid.UUID
	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(
		func(_ context.Context, n model.Notification) (bool, error) {
			gotUserIDs = append(gotUserIDs, n.UserID)
			assert.Equal(t, msgID, n.EventID)
			assert.Equal(t, "transfer_p2p", n.Type)
			return true, nil
		})

	err := m.handleDelivery(context.Background(), deliveryFor(t, ev, msgID.String()))
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{sender, receiver}, gotUserIDs)
}

func TestHandleDelivery_WithdrawSettleAndCancel_InsertSingleRecipient(t *testing.T) {
	for _, txType := range []string{"withdraw_settle", "withdraw_cancel"} {
		t.Run(txType, func(t *testing.T) {
			m, repo := newModule(t)
			txID := uuid.New()
			userID := uuid.New()
			msgID := uuid.New()
			ev := events.NewTransactionPosted(txID, txType, "200000", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")

			repo.EXPECT().Insert(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, n model.Notification) (bool, error) {
					assert.Equal(t, userID, n.UserID)
					assert.Equal(t, txType, n.Type)
					return true, nil
				})

			err := m.handleDelivery(context.Background(), deliveryFor(t, ev, msgID.String()))
			assert.NoError(t, err)
		})
	}
}

func TestHandleDelivery_DuplicateDelivery_DedupInsertReturnsFalse_StillAcks(t *testing.T) {
	m, repo := newModule(t)
	txID := uuid.New()
	userID := uuid.New()
	msgID := uuid.New()
	ev := events.NewTransactionPosted(txID, "money_in", "500000", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")

	// inserted=false, no error — a redelivery of an already-processed event.
	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(false, nil)

	err := m.handleDelivery(context.Background(), deliveryFor(t, ev, msgID.String()))
	assert.NoError(t, err, "a dedup no-op must still ack, not error")
}

func TestHandleDelivery_RepoInsertError_Propagates(t *testing.T) {
	m, repo := newModule(t)
	txID := uuid.New()
	userID := uuid.New()
	ev := events.NewTransactionPosted(txID, "money_in", "500000", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")

	repo.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(false, errors.New("db down"))

	err := m.handleDelivery(context.Background(), deliveryFor(t, ev, uuid.New().String()))
	assert.Error(t, err)
}

func TestHandleDelivery_MalformedBody_ReturnsError(t *testing.T) {
	m, _ := newModule(t)
	d := amqp.Delivery{Body: []byte("not json"), MessageId: uuid.New().String()}
	err := m.handleDelivery(context.Background(), d)
	assert.Error(t, err)
}

func TestHandleDelivery_InvalidMessageID_ReturnsError(t *testing.T) {
	m, _ := newModule(t)
	txID := uuid.New()
	userID := uuid.New()
	ev := events.NewTransactionPosted(txID, "money_in", "500000", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")
	err := m.handleDelivery(context.Background(), deliveryFor(t, ev, "not-a-uuid"))
	assert.Error(t, err)
}

func TestHandleDelivery_NoUserIDSet_ZeroRecipients_NoInsertCalls(t *testing.T) {
	m, repo := newModule(t)
	txID := uuid.New()
	ev := events.NewTransactionPosted(txID, "money_in", "500000", "IDR", nil, nil, nil, "", time.Now(), nil, nil, "")

	// No repo.EXPECT() — recipientsFor returns empty, Insert must never be called.
	err := m.handleDelivery(context.Background(), deliveryFor(t, ev, uuid.New().String()))
	assert.NoError(t, err)
	_ = repo
}

func TestListNotifications_DefaultsAndCapsLimit(t *testing.T) {
	m, repo := newModule(t)
	userID := uuid.New()

	repo.EXPECT().List(gomock.Any(), userID, 50, time.Time{}).Return(nil, nil)
	_, err := m.ListNotifications(context.Background(), userID, 0, time.Time{})
	require.NoError(t, err)

	repo.EXPECT().List(gomock.Any(), userID, 200, time.Time{}).Return(nil, nil)
	_, err = m.ListNotifications(context.Background(), userID, 999, time.Time{})
	require.NoError(t, err)

	repo.EXPECT().List(gomock.Any(), userID, 10, time.Time{}).Return(nil, nil)
	_, err = m.ListNotifications(context.Background(), userID, 10, time.Time{})
	require.NoError(t, err)
}

func TestMarkRead_NotMatched_ReturnsNotFound(t *testing.T) {
	m, repo := newModule(t)
	id, userID := uuid.New(), uuid.New()
	repo.EXPECT().MarkRead(gomock.Any(), id, userID).Return(false, nil)

	err := m.MarkRead(context.Background(), id, userID)
	assert.ErrorIs(t, err, ErrNotificationNotFound)
}

func TestMarkRead_Matched_ReturnsNil(t *testing.T) {
	m, repo := newModule(t)
	id, userID := uuid.New(), uuid.New()
	repo.EXPECT().MarkRead(gomock.Any(), id, userID).Return(true, nil)

	err := m.MarkRead(context.Background(), id, userID)
	assert.NoError(t, err)
}

func TestMarkRead_RepoError_Propagates(t *testing.T) {
	m, repo := newModule(t)
	id, userID := uuid.New(), uuid.New()
	repo.EXPECT().MarkRead(gomock.Any(), id, userID).Return(false, errors.New("db down"))

	err := m.MarkRead(context.Background(), id, userID)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotificationNotFound)
}
