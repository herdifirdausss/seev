package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingAcknowledger struct {
	acks         int
	nacks        int
	rejects      int
	lastRequeue  bool
	ackErr       error
	nackErr      error
	rejectionErr error
}

func (a *recordingAcknowledger) Ack(uint64, bool) error {
	a.acks++
	return a.ackErr
}

func (a *recordingAcknowledger) Nack(_ uint64, _ bool, requeue bool) error {
	a.nacks++
	a.lastRequeue = requeue
	return a.nackErr
}

func (a *recordingAcknowledger) Reject(uint64, bool) error {
	a.rejects++
	return a.rejectionErr
}

func deliveryWith(ack *recordingAcknowledger) amqp.Delivery {
	return amqp.Delivery{
		Acknowledger: ack, DeliveryTag: 1, MessageId: "message-1",
		RoutingKey: "payment.created", Headers: amqp.Table{},
	}
}

func TestHandleDeliveryOutcomes(t *testing.T) {
	handlerErr := errors.New("invalid payload")
	tests := []struct {
		name        string
		redelivered bool
		handler     HandlerFunc
		wantAck     int
		wantNack    int
		wantRequeue bool
	}{
		{name: "success", handler: func(context.Context, amqp.Delivery) error { return nil }, wantAck: 1},
		{name: "permanent error to dlq", handler: func(context.Context, amqp.Delivery) error { return handlerErr }, wantNack: 1},
		{name: "transient first delivery requeues", handler: func(context.Context, amqp.Delivery) error { return Retriable(handlerErr) }, wantNack: 1, wantRequeue: true},
		{name: "transient redelivery to dlq", redelivered: true, handler: func(context.Context, amqp.Delivery) error { return Retriable(handlerErr) }, wantNack: 1},
		{name: "panic to dlq", handler: func(context.Context, amqp.Delivery) error { panic("boom") }, wantNack: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newUnitRabbitMQ(t, &fakePublishChannel{}, make(chan amqp.Confirmation))
			ack := &recordingAcknowledger{}
			d := deliveryWith(ack)
			d.Redelivered = tt.redelivered
			r.handleDelivery(context.Background(), d, ConsumeOptions{Queue: "payments", PrefetchCount: 1, MaxDeliveryAttempts: 3}, tt.handler)
			assert.Equal(t, tt.wantAck, ack.acks)
			assert.Equal(t, tt.wantNack, ack.nacks)
			assert.Equal(t, tt.wantRequeue, ack.lastRequeue)
		})
	}
}

func TestHandleDeliveryMaxAttemptsAndClosing(t *testing.T) {
	r := newUnitRabbitMQ(t, &fakePublishChannel{}, make(chan amqp.Confirmation))
	ack := &recordingAcknowledger{}
	d := deliveryWith(ack)
	d.Headers["x-death"] = []any{
		amqp.Table{"count": int64(2)},
		amqp.Table{"count": int64(3)},
		"malformed",
	}
	called := false
	r.handleDelivery(context.Background(), d, ConsumeOptions{Queue: "payments", MaxDeliveryAttempts: 5}, func(context.Context, amqp.Delivery) error {
		called = true
		return nil
	})
	assert.False(t, called)
	assert.Equal(t, 1, ack.nacks)
	assert.False(t, ack.lastRequeue)
	assert.Equal(t, int64(5), totalDeathCount(d))

	r.closing = true
	ack = &recordingAcknowledger{}
	r.handleDelivery(context.Background(), deliveryWith(ack), ConsumeOptions{Queue: "payments", MaxDeliveryAttempts: 5}, func(context.Context, amqp.Delivery) error {
		t.Fatal("handler must not run while closing")
		return nil
	})
	assert.Equal(t, 1, ack.nacks)
	assert.True(t, ack.lastRequeue)
}

func TestHandleDeliveryRestoresCorrelationID(t *testing.T) {
	r := newUnitRabbitMQ(t, &fakePublishChannel{}, make(chan amqp.Confirmation))
	ack := &recordingAcknowledger{}
	d := deliveryWith(ack)
	d.CorrelationId = "request-42"
	r.handleDelivery(context.Background(), d, ConsumeOptions{Queue: "payments", MaxDeliveryAttempts: 5}, func(ctx context.Context, _ amqp.Delivery) error {
		assert.Equal(t, "request-42", fmt.Sprint(ctx.Value(middleware.RequestIDKey)))
		return nil
	})
	assert.Equal(t, 1, ack.acks)
}

func TestTotalDeathCountMalformedHeaders(t *testing.T) {
	assert.Zero(t, totalDeathCount(amqp.Delivery{Headers: amqp.Table{}}))
	assert.Zero(t, totalDeathCount(amqp.Delivery{Headers: amqp.Table{"x-death": "bad"}}))
	assert.Zero(t, totalDeathCount(amqp.Delivery{Headers: amqp.Table{"x-death": []any{amqp.Table{"count": "bad"}}}}))
}

func TestConsumeRejectsInvalidOptions(t *testing.T) {
	r := newUnitRabbitMQ(t, &fakePublishChannel{}, make(chan amqp.Confirmation))
	err := r.Consume(context.Background(), ConsumeOptions{}, func(context.Context, amqp.Delivery) error { return nil })
	require.ErrorIs(t, err, ErrInvalidConsumeOptions)
}
