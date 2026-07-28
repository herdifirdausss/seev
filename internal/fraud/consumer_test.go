package fraud

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

	"github.com/herdifirdausss/seev/internal/ledger/events"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type storeStub struct {
	eventID string
	key     string
	ttl     time.Duration
	err     error
}

func (s *storeStub) Get(context.Context, string) (int64, error) { return 0, nil }
func (s *storeStub) Record(_ context.Context, eventID, key string, ttl time.Duration) error {
	s.eventID, s.key, s.ttl = eventID, key, ttl
	return s.err
}

func delivery(t *testing.T, event events.TransactionPosted) amqp.Delivery {
	body, err := json.Marshal(event)
	require.NoError(t, err)
	return amqp.Delivery{Body: body, MessageId: uuid.NewString()}
}

func TestHandleDeliveryRecordsPostedUser(t *testing.T) {
	userID := uuid.New()
	at := time.Date(2026, 7, 15, 9, 30, 0, 0, time.FixedZone("WIB", 7*60*60))
	event := events.NewTransactionPosted(uuid.New(), "transfer_p2p", "100", "IDR", nil, nil, nil, "", at, &userID, nil, "")
	store := &storeStub{}
	m := &Module{store: store, logger: discardLogger()}
	d := delivery(t, event)
	require.NoError(t, m.handleDelivery(context.Background(), d))
	require.NotNil(t, event.EventID)
	assert.Equal(t, event.EventID.String(), store.eventID)
	assert.Equal(t, "fraud:velocity:"+userID.String()+":2026-07-15-02", store.key)
	assert.Equal(t, 2*time.Hour, store.ttl)
}

func TestHandleDeliveryWithoutUserIsNoOp(t *testing.T) {
	store := &storeStub{err: errors.New("must not be called")}
	event := events.NewTransactionPosted(uuid.New(), "fee_collect", "100", "IDR", nil, nil, nil, "", time.Now(), nil, nil, "")
	require.NoError(t, (&Module{store: store}).handleDelivery(context.Background(), delivery(t, event)))
	assert.Empty(t, store.eventID)
}

func TestHandleDeliveryDecodeAndStoreErrors(t *testing.T) {
	m := &Module{store: &storeStub{}}
	require.Error(t, m.handleDelivery(context.Background(), amqp.Delivery{Body: []byte("bad"), MessageId: "x"}))

	userID := uuid.New()
	event := events.NewTransactionPosted(uuid.New(), "money_in", "1", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")
	require.Error(t, (&Module{store: &storeStub{err: errors.New("redis down")}}).handleDelivery(context.Background(), delivery(t, event)))
}

func TestHandleDeliveryMalformedKnownVersionHasNoStoreSideEffect(t *testing.T) {
	userID := uuid.New()
	event := events.NewTransactionPosted(uuid.New(), "money_in", "1", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")
	event.Amount = "not-minor-units"
	store := &storeStub{err: errors.New("store must not be called")}
	assert.Error(t, (&Module{store: store}).handleDelivery(context.Background(), delivery(t, event)))
	assert.Empty(t, store.eventID)
}

func TestHandleDeliveryToleratesUnknownFieldsAndLogicalIDWithoutDeliveryID(t *testing.T) {
	userID := uuid.New()
	event := events.NewTransactionPosted(uuid.New(), "transfer_p2p", "1", "IDR", nil, nil, nil, "", time.Now(), &userID, nil, "")
	body, err := json.Marshal(map[string]any{"schema_version": event.SchemaVersion, "event_id": event.EventID, "tx_id": event.TxID, "transaction_type": event.TransactionType, "amount": event.Amount, "currency": event.Currency, "entries": event.Entries, "occurred_at": event.OccurredAt, "user_id": event.UserID, "unknown_optional_field": "ignored"})
	require.NoError(t, err)
	store := &storeStub{}
	require.NoError(t, (&Module{store: store, logger: discardLogger()}).handleDelivery(context.Background(), amqp.Delivery{Body: body}))
	assert.Equal(t, event.EventID.String(), store.eventID)
}
