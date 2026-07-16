package fraud

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/ledger/events"
)

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
	m := &Module{store: store}
	d := delivery(t, event)
	require.NoError(t, m.handleDelivery(context.Background(), d))
	assert.Equal(t, d.MessageId, store.eventID)
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
