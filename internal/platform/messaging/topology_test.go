package messaging

import (
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueueConfigDefaultsAndOverrides(t *testing.T) {
	defaultCfg := QueueConfig{Queue: "payments", RoutingKeys: []string{"payment.*"}}
	assert.Equal(t, "events.dlx", defaultCfg.dlxName("events"))
	assert.Equal(t, "payments.dlq", defaultCfg.dlqName())
	assert.Equal(t, amqp.ExchangeTopic, defaultCfg.exchangeType())
	assert.True(t, defaultCfg.durable())
	require.NoError(t, defaultCfg.Validate())

	nonDurable := false
	override := QueueConfig{
		Exchange: "money", ExchangeType: amqp.ExchangeDirect,
		Queue: "payments", RoutingKeys: []string{"payment.created"},
		DLX: "dead", DLQ: "dead.queue", Durable: &nonDurable,
		MessageTTL: time.Hour, MaxPriority: 5,
	}
	assert.Equal(t, "dead", override.dlxName("events"))
	assert.Equal(t, "dead.queue", override.dlqName())
	assert.Equal(t, amqp.ExchangeDirect, override.exchangeType())
	assert.False(t, override.durable())
}

func TestQueueConfigValidate(t *testing.T) {
	tests := []QueueConfig{
		{},
		{Queue: "q"},
		{Queue: "q", RoutingKeys: []string{"ok", ""}},
	}
	for _, cfg := range tests {
		require.ErrorIs(t, cfg.Validate(), ErrInvalidTopology)
	}
}
