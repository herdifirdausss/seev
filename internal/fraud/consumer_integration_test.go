//go:build integration

package fraud

import (
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	rmqcontainer "github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/herdifirdausss/seev/internal/config"
	"github.com/herdifirdausss/seev/internal/fraud/rules"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

func TestVelocityConsumerRealRabbitMQIncrementsPostedCounterOnce(t *testing.T) {
	ctx := context.Background()
	container, err := rmqcontainer.Run(ctx, "rabbitmq:4.3.3-management-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	host, err := container.Host(ctx)
	require.NoError(t, err)
	mappedPort, err := container.MappedPort(ctx, rmqcontainer.DefaultAMQPPort)
	require.NoError(t, err)
	port, err := strconv.Atoi(mappedPort.Port())
	require.NoError(t, err)

	brokerCfg := config.RabbitMQConfig{
		Host: host, Port: port, Username: container.AdminUsername, Password: container.AdminPassword,
		DefaultExchange: "ledger.events.fraud.test", ReconnectBaseDelay: time.Second,
		MaxReconnectAttempts: 10, ChannelPoolSize: 4, MaxConcurrentPublish: 8,
		DrainTimeout: 5 * time.Second, DialTimeout: 10 * time.Second,
		PublishTimeout: 5 * time.Second, AppID: "fraud_integration_test",
	}
	broker, err := messaging.NewWithRegistry(ctx, brokerCfg.Broker(), prometheus.NewRegistry())
	require.NoError(t, err)
	t.Cleanup(func() { _ = broker.Close() })

	mini := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mini.Addr(), DB: 1})
	t.Cleanup(func() { _ = redisClient.Close() })
	store := NewRedisVelocityStore(redisClient)
	module := &Module{store: store, broker: broker, logger: slog.Default()}
	require.NoError(t, module.Start(ctx))
	t.Cleanup(module.Stop)

	userID := uuid.New()
	at := time.Now().UTC()
	event := events.NewTransactionPosted(uuid.New(), "transfer_p2p", "100", "IDR", nil, nil, nil, "", at, &userID, nil, "")
	eventID := uuid.NewString()
	options := messaging.PublishOptions{RoutingKey: events.TypeTransactionPosted, MessageID: eventID}
	require.NoError(t, broker.PublishTo(ctx, options, event))
	require.NoError(t, broker.PublishTo(ctx, options, event))

	key := rules.VelocityKey(userID.String(), at)
	require.Eventually(t, func() bool {
		count, getErr := store.Get(ctx, key)
		return getErr == nil && count == 1
	}, 10*time.Second, 100*time.Millisecond)
}
