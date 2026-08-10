package messaging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/prometheus/client_golang/prometheus"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
)

func newUnitRabbitMQ(t *testing.T, channel *fakePublishChannel, confirms chan amqp.Confirmation) *RabbitMQ {
	t.Helper()
	cfg := BrokerConfig{
		DefaultExchange: "events", AppID: "unit-test", PublishTimeout: 20 * time.Millisecond,
		ChannelPoolSize: 1, MaxConcurrentPublish: 1, ReconnectBaseDelay: time.Millisecond,
		DrainTimeout: 20 * time.Millisecond,
	}.withDefaults()
	r := &RabbitMQ{
		cfg: cfg, metrics: newMetrics(prometheus.NewRegistry()),
		tracer: otel.Tracer("messaging-test"),
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		done:   make(chan struct{}), publishSem: make(chan struct{}, cfg.MaxConcurrentPublish),
	}
	r.chanPool = newConfirmChannelPool(1, func() (*confirmedChannel, error) {
		return &confirmedChannel{ch: channel, confirms: confirms}, nil
	})
	return r
}

func TestPublishToConfirmed(t *testing.T) {
	channel := &fakePublishChannel{}
	confirms := make(chan amqp.Confirmation, 1)
	confirms <- amqp.Confirmation{Ack: true}
	r := newUnitRabbitMQ(t, channel, confirms)
	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "request-1")

	err := r.PublishTo(ctx, PublishOptions{RoutingKey: "payment.created", MessageID: "message-1"}, map[string]int{"amount": 42})
	require.NoError(t, err)
	require.Len(t, channel.publishings, 1)
	got := channel.publishings[0]
	assert.Equal(t, "message-1", got.MessageId)
	assert.Equal(t, "request-1", got.CorrelationId)
	assert.Equal(t, "unit-test", got.AppId)
	assert.JSONEq(t, `{"amount":42}`, string(got.Body))
	assert.Equal(t, uint8(amqp.Persistent), got.DeliveryMode)
}

func TestPublishUsesDefaults(t *testing.T) {
	channel := &fakePublishChannel{}
	confirms := make(chan amqp.Confirmation, 1)
	confirms <- amqp.Confirmation{Ack: true}
	r := newUnitRabbitMQ(t, channel, confirms)
	require.NoError(t, r.Publish(context.Background(), "payment.updated", "payload"))
	require.NotEmpty(t, channel.publishings[0].MessageId)
}

func TestPublishFailureModes(t *testing.T) {
	publishFailure := errors.New("write failed")
	tests := []struct {
		name     string
		channel  *fakePublishChannel
		confirms func() chan amqp.Confirmation
		payload  any
		want     error
	}{
		{name: "marshal", channel: &fakePublishChannel{}, confirms: func() chan amqp.Confirmation { return make(chan amqp.Confirmation) }, payload: make(chan int)},
		{name: "publish", channel: &fakePublishChannel{publishErr: publishFailure}, confirms: func() chan amqp.Confirmation { return make(chan amqp.Confirmation) }, payload: "ok", want: publishFailure},
		{name: "nack", channel: &fakePublishChannel{}, confirms: func() chan amqp.Confirmation {
			ch := make(chan amqp.Confirmation, 1)
			ch <- amqp.Confirmation{Ack: false}
			return ch
		}, payload: "ok", want: ErrBrokerNacked},
		{name: "confirm closed", channel: &fakePublishChannel{}, confirms: func() chan amqp.Confirmation { ch := make(chan amqp.Confirmation); close(ch); return ch }, payload: "ok", want: ErrConfirmChannelClosed},
		{name: "confirm timeout", channel: &fakePublishChannel{}, confirms: func() chan amqp.Confirmation { return make(chan amqp.Confirmation) }, payload: "ok", want: ErrPublishTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newUnitRabbitMQ(t, tt.channel, tt.confirms())
			err := r.PublishTo(context.Background(), PublishOptions{RoutingKey: "rk", MessageID: "m1"}, tt.payload)
			require.Error(t, err)
			var structured *PublishError
			require.ErrorAs(t, err, &structured)
			if tt.want != nil {
				require.ErrorIs(t, err, tt.want)
			}
		})
	}
}

func TestPublishValidationAndCancellation(t *testing.T) {
	channel := &fakePublishChannel{}
	r := newUnitRabbitMQ(t, channel, make(chan amqp.Confirmation))
	require.Error(t, r.PublishTo(context.Background(), PublishOptions{}, "payload"))

	r.publishSem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Publish(ctx, "rk", "payload")
	require.ErrorIs(t, err, context.Canceled)
	<-r.publishSem
}

func TestPublishChannelFactoryFailure(t *testing.T) {
	want := errors.New("channel unavailable")
	r := newUnitRabbitMQ(t, &fakePublishChannel{}, make(chan amqp.Confirmation))
	r.chanPool = newConfirmChannelPool(1, func() (*confirmedChannel, error) { return nil, want })
	err := r.Publish(context.Background(), "rk", "payload")
	require.ErrorIs(t, err, want)
}
