//go:build integration

// Proves Plan 59 section 11.4's cross-user device isolation against a REAL
// Postgres — services/gateway/internal/notification/inbox/settings.go's device endpoints had zero
// test coverage before this file. Reuses setupNotifyTestDBs and adminDo
// (admin_integration_test.go, same package).
package notify_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	notify "github.com/herdifirdausss/seev/services/gateway/notification"
)

func TestDevicesHandler_CrossUserTokenConflictRejected_RealStack(t *testing.T) {
	_, gatewayDB := setupNotifyTestDBs(t)
	cfg := notify.DefaultConfig()
	cfg.EncryptionRing = notifyTestCryptoxRing(t)
	module := notify.NewConfiguredModule(gatewayDB, nil, cfg, nil, nil, nil, nil)
	handler := module.DevicesHandler()

	userA, userB := uuid.New(), uuid.New()
	const sharedRawToken = "cross-user-conflict-raw-token"

	registerA := adminDo(t, handler, http.MethodPost, "/notification-devices", userA.String(), "user",
		[]byte(`{"platform":"android","token":"`+sharedRawToken+`","device_name":"Phone A"}`))
	require.Equal(t, http.StatusCreated, registerA.Code, registerA.Body.String())

	// A different user registering the exact same raw token must be
	// rejected — a token cannot silently move between users.
	registerB := adminDo(t, handler, http.MethodPost, "/notification-devices", userB.String(), "user",
		[]byte(`{"platform":"android","token":"`+sharedRawToken+`","device_name":"Phone B"}`))
	require.Equal(t, http.StatusConflict, registerB.Code, registerB.Body.String())
	require.Contains(t, registerB.Body.String(), "NOTIFICATION_DEVICE_INVALID")

	// The same user re-registering their own token is an idempotent update,
	// not a conflict.
	reregisterA := adminDo(t, handler, http.MethodPost, "/notification-devices", userA.String(), "user",
		[]byte(`{"platform":"android","token":"`+sharedRawToken+`","device_name":"Phone A renamed"}`))
	require.Equal(t, http.StatusOK, reregisterA.Code, reregisterA.Body.String())

	listA := adminDo(t, handler, http.MethodGet, "/notification-devices", userA.String(), "user", nil)
	require.Equal(t, http.StatusOK, listA.Code)
	require.NotContains(t, listA.Body.String(), sharedRawToken, "the raw token must never be returned")

	var deviceCount int
	require.NoError(t, gatewayDB.QueryRowContext(t.Context(), `SELECT count(*) FROM notif_device_endpoints WHERE user_id=$1`, userB).Scan(&deviceCount))
	require.Equal(t, 0, deviceCount, "the rejected registration must not have created a row for user B")
}
