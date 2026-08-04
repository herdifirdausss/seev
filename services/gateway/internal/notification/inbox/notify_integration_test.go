//go:build integration

// Package notify_test drives services/gateway/internal/notification.Module end to end against a
// REAL RabbitMQ and a real ledger.Module posting to a real Postgres
// (docs/roadmap/archive/25 Task T4) — proves the whole vertical: outbox relay
// publishes TransactionPosted -> notify's consumer receives it -> a
// notification row appears for the right user(s) -> redelivery of the same
// event is deduped, not double-inserted. This is the first integration test
// in the repo that stands up a real message broker rather than only real
// Postgres.
package notify_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	rmqcontainer "github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/herdifirdausss/seev/contracts/events/ledger"
	"github.com/herdifirdausss/seev/internal/platform/config"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/messaging"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/internal/testkit"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/model"
	"github.com/herdifirdausss/seev/services/gateway/notification"
	"github.com/herdifirdausss/seev/services/ledger"
)

// notifyTestDigestRing is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3's (K7) fixed test
// key — ledger.NewModule requires a real, non-nil idempotency digest ring.
func notifyTestDigestRing(t *testing.T) *cryptox.DigestRing {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 29)
	}
	ring, err := cryptox.NewDigestRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func notifyTestCryptoxRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 59)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "..")
}

func setupNotifyTestDBs(t *testing.T) (ledgerDB, gatewayDB *database.DBSQL) {
	t.Helper()
	ledgerDB, gatewayDB, _ = setupNotifyTestDBsWithConfig(t)
	return ledgerDB, gatewayDB
}

// setupNotifyTestDBsWithConfig is setupNotifyTestDBs plus the gateway
// connection's own config.PostgresConfig, for tests that need a second
// connection under a different role (e.g. retention_integration_test.go's
// TestRetention_Notifications_DirectDeleteStillForbidden — same pattern as
// services/ledger/internal/retention_integration_test.go's setupLedgerOnlyDBWithConfig).
func setupNotifyTestDBsWithConfig(t *testing.T) (ledgerDB, gatewayDB *database.DBSQL, gatewayConfig config.PostgresConfig) {
	t.Helper()
	ctx := context.Background()

	const ledgerDBName, gatewayDBName = "seev_ledger", "seev_gateway"
	const dbUser, dbPassword = "test", "secret"

	container, err := pgcontainer.Run(ctx,
		"postgres:16.14-alpine",
		pgcontainer.WithDatabase(ledgerDBName),
		pgcontainer.WithUsername(dbUser),
		pgcontainer.WithPassword(dbPassword),
		pgcontainer.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	ledgerConfig := config.PostgresConfig{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: ledgerDBName, SSLMode: "disable", MaxOpenConns: 10,
	}
	ledgerDB, err = database.New(ctx, ledgerConfig.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ledgerDB.Close() })
	_, err = ledgerDB.ExecContext(ctx, `CREATE DATABASE seev_gateway`)
	require.NoError(t, err)

	ledgerDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPassword, host, port.Port(), ledgerDBName)
	gatewayDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPassword, host, port.Port(), gatewayDBName)
	require.NoError(t, testutil.ApplyMigration(migrationsSourceURL(t), "ledger", ledgerDSN))
	require.NoError(t, testutil.ApplyMigration(migrationsSourceURL(t), "gateway", gatewayDSN))

	gatewayConfig = ledgerConfig
	gatewayConfig.DB = gatewayDBName
	gatewayDB, err = database.New(ctx, gatewayConfig.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = gatewayDB.Close() })
	return ledgerDB, gatewayDB, gatewayConfig
}

// setupNotifyTestBroker starts a real RabbitMQ container and returns a
// connected *messaging.RabbitMQ — the same broker instance is shared by the
// ledger module (which publishes via the outbox relay) and the notify
// module (which consumes), exactly like services/gateway/cmd/gateway/main.go wires one `mq`
// into both.
func setupNotifyTestBroker(t *testing.T) *messaging.RabbitMQ {
	t.Helper()
	ctx := context.Background()

	// The module's default log wait looks for a version-specific line that is
	// absent in some RabbitMQ alpine images. AMQP listening is the readiness
	// condition the test actually needs, and avoids false negatives when the
	// broker is healthy but its startup log wording changes.
	container, err := rmqcontainer.Run(ctx, "rabbitmq:4.3.3-management-alpine",
		testcontainers.WithWaitStrategy(
			// The full integration race gate starts several Postgres
			// containers concurrently. Docker Desktop can take longer than the
			// normal broker warm-up while those containers contend for CPU; the
			// bounded five-minute ceiling keeps the test deterministic without
			// turning a genuinely stuck broker into an unbounded wait.
			wait.ForListeningPort(rmqcontainer.DefaultAMQPPort).WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	mappedPort, err := container.MappedPort(ctx, rmqcontainer.DefaultAMQPPort)
	require.NoError(t, err)
	port, err := strconv.Atoi(mappedPort.Port())
	require.NoError(t, err)

	// config.RabbitMQConfig's zero value is NOT usable directly — unlike
	// config.Load() (which fills these from env with sane defaults),
	// constructing the struct literal here bypasses that, and in
	// particular MaxConcurrentPublish=0 makes the publish semaphore an
	// unbuffered channel with zero capacity: every PublishTo call blocks
	// forever acquiring it, which silently wedges the outbox relay with
	// the claimed event stuck in 'processing' forever. Every timeout/pool
	// field below mirrors internal/platform/config.Load's own defaults.
	cfg := config.RabbitMQConfig{
		Host:                 host,
		Port:                 port,
		Username:             container.AdminUsername,
		Password:             container.AdminPassword,
		DefaultExchange:      "ledger.events.test",
		ReconnectBaseDelay:   time.Second,
		MaxReconnectAttempts: 10,
		ChannelPoolSize:      16,
		MaxConcurrentPublish: 64,
		DrainTimeout:         30 * time.Second,
		DialTimeout:          10 * time.Second,
		PublishTimeout:       5 * time.Second,
		AppID:                "notify_integration_test",
	}
	// A listening TCP socket only proves that RabbitMQ has bound its port.
	// During a busy parallel integration run, the broker can still reset the
	// first AMQP handshake while it finishes booting. Retry the real client
	// connection so readiness means "AMQP handshake completed", not merely
	// "the port accepted a TCP connection".
	var broker *messaging.RabbitMQ
	connectDeadline := time.Now().Add(time.Minute)
	for {
		broker, err = messaging.NewWithRegistry(ctx, cfg.Broker(), prometheus.NewRegistry())
		if err == nil || time.Now().After(connectDeadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = broker.Close() })

	return broker
}

func createUserCashAccount(t *testing.T, db *database.DBSQL, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	accountID := uuid.New()

	_, err := db.ExecContext(ctx, `
		INSERT INTO accounts (id, owner_id, owner_type, type, currency, status, created_by)
		VALUES ($1, $2, 'user', 'cash', 'IDR', 'active', 'notify_integration_test')`,
		accountID, userID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO account_balances (account_id) VALUES ($1)`, accountID)
	require.NoError(t, err)

	return accountID
}

// pollForNotification polls notif_notifications until a row for
// (userID, txType) appears or timeout elapses.
func pollForNotification(t *testing.T, db *database.DBSQL, userID uuid.UUID, txType string, timeout time.Duration) (id, eventID uuid.UUID, found bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		row := db.QueryRowContext(context.Background(), `
			SELECT id, event_id FROM notif_notifications
			WHERE user_id = $1 AND type = $2`, userID, txType)
		var gotID, gotEventID uuid.UUID
		if err := row.Scan(&gotID, &gotEventID); err == nil {
			return gotID, gotEventID, true
		} else if err != sql.ErrNoRows {
			require.NoError(t, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return uuid.Nil, uuid.Nil, false
}

func countNotifications(t *testing.T, db *database.DBSQL, userID uuid.UUID, txType string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(), `
		SELECT count(*) FROM notif_notifications WHERE user_id = $1 AND type = $2`, userID, txType).Scan(&n)
	require.NoError(t, err)
	return n
}

func pollForNotificationKind(t *testing.T, db *database.DBSQL, userID uuid.UUID, kind string, timeout time.Duration) (id, eventID uuid.UUID, found bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		row := db.QueryRowContext(context.Background(), `
			SELECT id, event_id FROM notif_notifications
			WHERE user_id = $1 AND kind = $2
			ORDER BY created_at DESC, id DESC LIMIT 1`, userID, kind)
		var gotID, gotEventID uuid.UUID
		if err := row.Scan(&gotID, &gotEventID); err == nil {
			return gotID, gotEventID, true
		} else if err != sql.ErrNoRows {
			require.NoError(t, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return uuid.Nil, uuid.Nil, false
}

// TestNotify_MoneyIn_RealStack_NotificationRowAppears_DuplicateDeliveryDedup
// is docs/roadmap/archive/25 Task T4's required real-stack integration test: post a
// real money_in through a real ledger.Module (real Postgres) with a real
// RabbitMQ broker wired all the way to services/gateway/internal/notification's consumer, prove a
// notification row appears within a few seconds, then manually redeliver
// the exact same outbox event (same MessageID/event_id, same routing key —
// what an at-least-once broker redelivery looks like from the consumer's
// point of view) and prove the row count stays at exactly 1.
func TestNotify_MoneyIn_RealStack_NotificationRowAppears_DuplicateDeliveryDedup(t *testing.T) {
	ledgerDB, gatewayDB := setupNotifyTestDBs(t)
	broker := setupNotifyTestBroker(t)
	ctx := context.Background()

	ledgerModule := ledger.NewModule(ledgerDB, broker, nil, ledger.WorkerConfig{
		Enabled:            true,
		OutboxPollInterval: 200 * time.Millisecond,
		OutboxBatchSize:    10,
	}, nil, decimal.NewFromInt(1_000_000_000), nil, nil, 0, notifyTestDigestRing(t), notifyTestCryptoxRing(t))
	ledgerModule.StartWorkers(ctx)
	t.Cleanup(ledgerModule.StopWorkers)

	notifyModule := notify.NewModule(gatewayDB, broker, nil)
	require.NoError(t, notifyModule.Start(ctx))
	t.Cleanup(notifyModule.Stop)

	userID := uuid.New()
	createUserCashAccount(t, ledgerDB, userID)
	require.NoError(t, ledgerModule.SetExecutionSubjectState(ctx, userID, "active", 1, nil))

	cmd := ledger.Command{
		IdempotencyKey:   "notify-test:money-in:" + userID.String(),
		IdempotencyScope: "notify_integration_test",
		Type:             "money_in",
		Amount:           decimal.NewFromInt(500000),
		UserID:           userID,
		Metadata:         map[string]any{"gateway": "bca"},
	}
	require.NoError(t, ledgerModule.Post(ctx, cmd))

	id, eventID, found := pollForNotification(t, gatewayDB, userID, "money_in", 15*time.Second)
	if !found {
		rows, _ := ledgerDB.QueryContext(ctx, `SELECT id, event_type, status, retry_count, COALESCE(last_error,'') FROM outbox_events ORDER BY created_at`)
		for rows.Next() {
			var outboxEventID, evType, status, lastErr string
			var retry int
			_ = rows.Scan(&outboxEventID, &evType, &status, &retry, &lastErr)
			t.Logf("outbox_events: id=%s type=%s status=%s retry=%d err=%q", outboxEventID, evType, status, retry, lastErr)
		}
		rows.Close()
	}
	require.True(t, found, "notification row for money_in must appear via the real outbox->rabbitmq->consumer path")
	assert.NotEqual(t, uuid.Nil, id)
	assert.Equal(t, 1, countNotifications(t, gatewayDB, userID, "money_in"))

	// ── Redelivery: republish the SAME outbox event (same event_id as
	// MessageID, same routing key) — proves the consumer's
	// ON CONFLICT (event_id, user_id) DO NOTHING dedup, not just "it
	// happened to only be delivered once".
	//
	// eventID here is notif_notifications.event_id, which services/gateway/internal/notification's
	// handleDelivery now populates from the payload's own deterministic
	// events.TransactionPosted.EventID (contracts/events/ledger/events.go's
	// documented "logical event ID" contract), NOT from outbox_events.id —
	// the two are only equal for legacy payloads with no EventID set. This
	// test creates exactly one outbox row, so fetch it directly instead of
	// by id=eventID.
	var rawPayload []byte
	require.NoError(t, ledgerDB.QueryRowContext(ctx,
		`SELECT payload FROM outbox_events WHERE event_type = $1 ORDER BY created_at DESC LIMIT 1`,
		events.TypeTransactionPosted).Scan(&rawPayload))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rawPayload, &payload))

	require.NoError(t, broker.PublishTo(ctx, messaging.PublishOptions{
		RoutingKey: "ledger.transaction.posted.v1",
		MessageID:  eventID.String(),
	}, payload))

	// Give the redelivered message time to be consumed and (correctly)
	// deduped, then assert the count never grows past 1.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
	}
	assert.Equal(t, 1, countNotifications(t, gatewayDB, userID, "money_in"),
		"redelivery of the same event_id must not produce a second notification row")
}

// TestNotify_DisputeLifecycle_RealStack_TransactionalOutboxToInAppDelivery
// is the live delivery evidence for the dispute gap: Ledger creates the case,
// the same DB transaction creates the lifecycle outbox event, the relay
// publishes it through RabbitMQ, Gateway consumes/plans it, and the in-app
// delivery is committed as delivered. Republish of the same logical event
// proves the notification inbox and logical notification are both idempotent.
func TestNotify_DisputeLifecycle_RealStack_TransactionalOutboxToInAppDelivery(t *testing.T) {
	ledgerDB, gatewayDB := setupNotifyTestDBs(t)
	broker := setupNotifyTestBroker(t)
	ctx := context.Background()

	ledgerModule := ledger.NewModule(ledgerDB, broker, nil, ledger.WorkerConfig{
		Enabled:            true,
		OutboxPollInterval: 200 * time.Millisecond,
		OutboxBatchSize:    10,
	}, nil, decimal.NewFromInt(1_000_000_000), nil, nil, 0, notifyTestDigestRing(t), notifyTestCryptoxRing(t))
	ledgerModule.StartWorkers(ctx)
	t.Cleanup(ledgerModule.StopWorkers)

	notifyModule := notify.NewModule(gatewayDB, broker, nil)
	require.NoError(t, notifyModule.Start(ctx))
	t.Cleanup(notifyModule.Stop)

	userID := uuid.New()
	createUserCashAccount(t, ledgerDB, userID)
	require.NoError(t, ledgerModule.SetExecutionSubjectState(ctx, userID, "active", 1, nil))
	cmd := ledger.Command{
		IdempotencyKey:   "notify-test:dispute:" + userID.String(),
		IdempotencyScope: "notify_integration_test",
		Type:             "money_in",
		Amount:           decimal.NewFromInt(500000),
		UserID:           userID,
		Metadata:         map[string]any{"gateway": "bca"},
	}
	require.NoError(t, ledgerModule.Post(ctx, cmd))

	var originalTxID uuid.UUID
	require.NoError(t, ledgerDB.QueryRowContext(ctx,
		`SELECT id FROM ledger_transactions WHERE idempotency_key = $1`, cmd.IdempotencyKey).Scan(&originalTxID))
	dueAt := time.Now().UTC().Add(15 * time.Minute)
	disputeID, err := ledgerModule.OpenChargebackDispute(ctx, originalTxID, "notify-dispute-1", "visa", "10.4",
		decimal.NewFromInt(500000), "IDR", &dueAt, "ops-1")
	require.NoError(t, err)

	notificationID, eventID, found := pollForNotificationKind(t, gatewayDB, userID, model.KindDisputeLifecycle, 15*time.Second)
	require.True(t, found, "dispute lifecycle notification must arrive through ledger outbox -> RabbitMQ -> Gateway consumer")
	assert.NotEqual(t, uuid.Nil, notificationID)
	assert.NotEqual(t, uuid.Nil, eventID)

	var kind, body, disputeStatus string
	require.NoError(t, gatewayDB.QueryRowContext(ctx,
		`SELECT kind, body, context->'dispute'->>'status' FROM notif_notifications WHERE id = $1`, notificationID).Scan(&kind, &body, &disputeStatus))
	assert.Equal(t, model.KindDisputeLifecycle, kind)
	assert.Equal(t, "open", disputeStatus)
	assert.Contains(t, body, "notify-dispute-1")

	var delivered int
	require.NoError(t, gatewayDB.QueryRowContext(ctx, `
		SELECT count(*) FROM notif_deliveries d
		JOIN notif_notifications n ON n.id = d.notification_id
		WHERE n.id = $1 AND d.channel = 'in_app' AND d.status = 'delivered'`, notificationID).Scan(&delivered))
	assert.Equal(t, 1, delivered, "in-app delivery must be committed as delivered")

	var rawPayload []byte
	require.NoError(t, ledgerDB.QueryRowContext(ctx, `
		SELECT payload FROM outbox_events
		WHERE aggregate_type = 'chargeback_dispute' AND aggregate_id = $1
		  AND event_type = $2`, disputeID, events.TypeDisputeLifecycle).Scan(&rawPayload))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rawPayload, &payload))
	require.NoError(t, broker.PublishTo(ctx, messaging.PublishOptions{
		RoutingKey: events.TypeDisputeLifecycle,
		MessageID:  eventID.String(),
	}, payload))
	time.Sleep(3 * time.Second)
	var notificationCount int
	require.NoError(t, gatewayDB.QueryRowContext(ctx, `
		SELECT count(*) FROM notif_notifications WHERE user_id = $1 AND kind = $2`, userID, model.KindDisputeLifecycle).Scan(&notificationCount))
	assert.Equal(t, 1, notificationCount, "same dispute lifecycle event must not create a second notification")

	require.NoError(t, ledgerModule.SubmitChargebackDisputeEvidence(ctx, disputeID, "evidence-ref-1", "ops-1"))
	require.Eventually(t, func() bool {
		var count int
		return gatewayDB.QueryRowContext(ctx, `
			SELECT count(*) FROM notif_notifications WHERE user_id = $1 AND kind = $2`, userID, model.KindDisputeLifecycle).Scan(&count) == nil && count == 2
	}, 15*time.Second, 200*time.Millisecond)

	require.NoError(t, ledgerModule.ResolveChargebackDispute(ctx, disputeID, "lost", "ops-2", "network upheld the claim"))
	require.Eventually(t, func() bool {
		var count int
		return gatewayDB.QueryRowContext(ctx, `
			SELECT count(*) FROM notif_notifications WHERE user_id = $1 AND kind = $2`, userID, model.KindDisputeLifecycle).Scan(&count) == nil && count == 3
	}, 15*time.Second, 200*time.Millisecond)
	var latestStatus string
	require.NoError(t, gatewayDB.QueryRowContext(ctx, `
		SELECT context->'dispute'->>'status' FROM notif_notifications
		WHERE user_id = $1 AND kind = $2 ORDER BY created_at DESC, id DESC LIMIT 1`, userID, model.KindDisputeLifecycle).Scan(&latestStatus))
	assert.Equal(t, "lost", latestStatus)
}
