package messaging

import (
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrokerConfigValidate(t *testing.T) {
	valid := BrokerConfig{Host: "rabbit", Username: "user", Password: "secret", DefaultExchange: "events"}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		mutate func(*BrokerConfig)
		field  string
	}{
		{name: "host", field: "Host", mutate: func(c *BrokerConfig) { c.Host = "" }},
		{name: "username", field: "Username", mutate: func(c *BrokerConfig) { c.Username = "" }},
		{name: "password", field: "Password", mutate: func(c *BrokerConfig) { c.Password = "" }},
		{name: "exchange", field: "DefaultExchange", mutate: func(c *BrokerConfig) { c.DefaultExchange = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			require.ErrorContains(t, cfg.Validate(), tt.field)
		})
	}
}

func TestBrokerConfigAddressAndDefaults(t *testing.T) {
	plain := BrokerConfig{Host: "rabbit", Username: "u", Password: "p", DefaultExchange: "events"}
	assert.Equal(t, 5672, plain.port())
	assert.Equal(t, "/", plain.vhost())
	assert.Equal(t, "amqp://u:p@rabbit:5672/", plain.url())
	assert.Equal(t, "rabbit:5672/", plain.safeAddr())

	secure := plain
	secure.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	secure.Port = 15671
	secure.VHost = "/finance"
	assert.Equal(t, 15671, secure.port())
	assert.Equal(t, "amqps://u:p@rabbit:15671/finance", secure.url())

	got := plain.withDefaults()
	assert.Equal(t, defaultDialTimeout, got.DialTimeout)
	assert.Equal(t, defaultPublishTimeout, got.PublishTimeout)
	assert.Equal(t, time.Second, got.ReconnectBaseDelay)
	assert.Equal(t, 10, got.MaxReconnectAttempts)
	assert.Equal(t, defaultChannelPoolSize, got.ChannelPoolSize)
	assert.Equal(t, defaultMaxConcurrentPublish, got.MaxConcurrentPublish)
	assert.Equal(t, defaultDrainTimeout, got.DrainTimeout)

	explicit := BrokerConfig{
		DialTimeout: 2 * time.Second, PublishTimeout: 3 * time.Second,
		ReconnectBaseDelay: 4 * time.Second, MaxReconnectAttempts: -1,
		ChannelPoolSize: 2, MaxConcurrentPublish: 3, DrainTimeout: 5 * time.Second,
	}
	assert.Equal(t, explicit, explicit.withDefaults())
}

func TestOptionsAndErrors(t *testing.T) {
	require.Error(t, (PublishOptions{}).validate())
	require.NoError(t, (PublishOptions{RoutingKey: "payment.created"}).validate())
	require.Error(t, (ConsumeOptions{}).validate())
	require.Error(t, (ConsumeOptions{Queue: "q"}).validate())
	require.NoError(t, (ConsumeOptions{Queue: "q", ConsumerTag: "worker"}).validate())

	opts := ConsumeOptions{}
	opts.applyDefaults()
	assert.Equal(t, 10, opts.PrefetchCount)
	assert.Equal(t, 5, opts.MaxDeliveryAttempts)

	cause := errors.New("network")
	pubErr := &PublishError{Exchange: "events", RoutingKey: "rk", MessageID: "m1", Cause: cause}
	assert.ErrorIs(t, pubErr, cause)
	assert.ErrorContains(t, pubErr, "message_id=m1")
	assert.Same(t, cause, pubErr.Unwrap())

	retry := Retriable(cause)
	assert.True(t, IsRetriable(retry))
	assert.False(t, IsRetriable(cause))
	assert.ErrorIs(t, retry, cause)
}
