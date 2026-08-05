//go:build integration

// Proves A8/C3's privacy export and closure erasure guarantees against a
// REAL Postgres — services/gateway/internal/notification/inbox/privacy.go had zero test
// coverage before this file. Reuses setupNotifyTestDBs and adminDo
// (admin_integration_test.go, same package).
package notify_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	notify "github.com/herdifirdausss/seev/services/gateway/notification"
)

func TestPrivacyExportAndClosure_ErasesSecretsAndPseudonymizes_RealStack(t *testing.T) {
	_, gatewayDB := setupNotifyTestDBs(t)
	ctx := context.Background()

	cfg := notify.DefaultConfig()
	cfg.EncryptionRing = notifyTestCryptoxRing(t)
	module := notify.NewConfiguredModule(gatewayDB, nil, cfg, nil, nil, nil, nil)

	subjectID := uuid.New()

	const rawToken = "privacy-test-raw-push-token"
	deviceRec := adminDo(t, module.DevicesHandler(), http.MethodPost, "/notification-devices", subjectID.String(), "user",
		[]byte(`{"platform":"android","token":"`+rawToken+`","device_name":"Phone"}`))
	require.Equal(t, http.StatusCreated, deviceRec.Code, deviceRec.Body.String())

	notificationID := uuid.New()
	_, err := gatewayDB.ExecContext(ctx, `
		INSERT INTO notif_notifications (id, user_id, event_id, type, title, body, kind, category, created_at)
		VALUES ($1, $2, $3, 'money_in', 'Test', 'Test body', 'money.transfer.sent', 'money_movement', now())`,
		notificationID, subjectID, uuid.New())
	require.NoError(t, err)

	const fakeCiphertext = "fake-recipient-ciphertext"
	deliveryID := uuid.New()
	_, err = gatewayDB.ExecContext(ctx, `
		INSERT INTO notif_deliveries
			(id, notification_id, user_id, channel, status, template_version_id, locale,
			 recipient_ciphertext, recipient_key_version, recipient_fingerprint, rendered_text, content_hash)
		VALUES ($1, $2, $3, 'email', 'delivered', '20000000-0000-0000-0000-000000000002', 'en-US',
			$4, 1, $5, 'rendered body', $6)`,
		deliveryID, notificationID, subjectID, []byte(fakeCiphertext), []byte("fake-fingerprint"), []byte("fake-hash"))
	require.NoError(t, err)

	// Export must surface real history (proves the query isn't vacuous) but
	// never the raw token or the encrypted recipient blob.
	exportPage, _, err := module.PrivacyExportPage(ctx, subjectID, time.Now().Add(time.Hour), 0, 50)
	require.NoError(t, err)
	require.NotEmpty(t, exportPage)
	sawNotification, sawDevice := false, false
	for _, raw := range exportPage {
		body := string(raw)
		require.NotContains(t, body, rawToken)
		require.NotContains(t, body, fakeCiphertext)
		// Postgres's jsonb::text cast renders with a space after each colon
		// (e.g. `"type": "notification"`), unlike encoding/json's output.
		if strings.Contains(body, `"type": "notification",`) {
			sawNotification = true
		}
		if strings.Contains(body, `"type": "notification_device"`) {
			sawDevice = true
		}
	}
	require.True(t, sawNotification, "export must include the notification row")
	require.True(t, sawDevice, "export must include the device row")

	surrogateID := uuid.New()
	_, affected, err := module.PrivacyCommitClosure(ctx, subjectID, surrogateID)
	require.NoError(t, err)
	require.Greater(t, affected, 0)

	var deviceCount int
	require.NoError(t, gatewayDB.QueryRowContext(ctx, `SELECT count(*) FROM notif_device_endpoints WHERE user_id=$1`, subjectID).Scan(&deviceCount))
	require.Equal(t, 0, deviceCount, "device endpoints must be removed on closure, never moved to the surrogate")

	var deliveryOwner uuid.UUID
	var recipientCiphertext []byte
	require.NoError(t, gatewayDB.QueryRowContext(ctx, `SELECT user_id, recipient_ciphertext FROM notif_deliveries WHERE id=$1`, deliveryID).Scan(&deliveryOwner, &recipientCiphertext))
	require.Equal(t, surrogateID, deliveryOwner)
	require.Nil(t, recipientCiphertext, "recipient ciphertext must be erased, not carried to the surrogate")

	var notificationOwner uuid.UUID
	require.NoError(t, gatewayDB.QueryRowContext(ctx, `SELECT user_id FROM notif_notifications WHERE id=$1`, notificationID).Scan(&notificationOwner))
	require.Equal(t, surrogateID, notificationOwner)

	// Closure must be idempotent: a repeat call against the now-empty
	// subject touches nothing and still succeeds.
	_, secondAffected, err := module.PrivacyCommitClosure(ctx, subjectID, surrogateID)
	require.NoError(t, err)
	require.Equal(t, 0, secondAffected)
}
