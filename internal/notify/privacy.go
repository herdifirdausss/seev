// Package notify owns the Gateway side of A8 export and account-closure
// processing. Notification content is user-visible history, while recipient
// ciphertext, device tokens, provider payloads, and raw event payloads are
// explicitly excluded from exports and erased during closure.
package notify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/pkg/privacyexport"
)

// PrivacyPrepareClosure: notifications are a read-log of already-posted
// events. There is no Gateway-owned in-flight financial action that can be
// stranded by account closure.
func (m *Module) PrivacyPrepareClosure(ctx context.Context, subjectID uuid.UUID) (blocked bool, reasons []string, err error) {
	return false, nil, nil
}

// PrivacyCommitClosure pseudonymizes user references in Gateway-owned
// notification state in one transaction. Provider credentials are not moved
// to the surrogate: push endpoints are removed and email/device ciphertext in
// delivery evidence is erased. The operation is idempotent because a retry
// only sees rows still owned by subjectID.
func (m *Module) PrivacyCommitClosure(ctx context.Context, subjectID, surrogateID uuid.UUID) (resultHash string, affectedCount int, err error) {
	h := sha256.New()
	err = m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		// A generated surrogate normally has no existing notification settings,
		// but make the conflict behavior explicit for retries or operator-driven
		// recovery where a surrogate row already exists.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM notif_user_settings
			WHERE user_id=$1 AND EXISTS (SELECT 1 FROM notif_user_settings WHERE user_id=$2)`, subjectID, surrogateID); err != nil {
			return fmt.Errorf("notify closure commit: remove conflicting settings: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM notif_preferences p
			WHERE p.user_id=$1 AND EXISTS (
				SELECT 1 FROM notif_preferences s
				WHERE s.user_id=$2 AND s.category=p.category AND s.channel=p.channel
			)`, subjectID, surrogateID); err != nil {
			return fmt.Errorf("notify closure commit: remove conflicting preferences: %w", err)
		}

		// Push endpoints are revocable channel credentials, not financial
		// evidence. Removing them avoids moving a token or a keyed fingerprint
		// to the surrogate and is safe for a repeated closure request.
		if result, err := tx.ExecContext(ctx, `DELETE FROM notif_device_endpoints WHERE user_id=$1`, subjectID); err != nil {
			return fmt.Errorf("notify closure commit: device endpoints: %w", err)
		} else if count, countErr := result.RowsAffected(); countErr == nil {
			affectedCount += int(count)
		}

		statements := []string{
			`UPDATE notif_user_settings SET user_id=$1, updated_at=now() WHERE user_id=$2`,
			`UPDATE notif_preferences SET user_id=$1, updated_at=now() WHERE user_id=$2`,
			`UPDATE notif_digest_windows SET user_id=$1, updated_at=now() WHERE user_id=$2`,
			`UPDATE notif_deliveries SET user_id=$1, recipient_ciphertext=NULL, recipient_key_version=NULL, recipient_fingerprint=NULL, updated_at=now() WHERE user_id=$2`,
			// payload is a legacy raw-event column. New rows already contain {},
			// and closure removes the old raw body before pseudonymization.
			`UPDATE notif_notifications SET user_id=$1, payload='{}'::jsonb, updated_at=now() WHERE user_id=$2`,
		}
		for _, statement := range statements {
			result, err := tx.ExecContext(ctx, statement, surrogateID, subjectID)
			if err != nil {
				return fmt.Errorf("notify closure commit: %w", err)
			}
			if count, countErr := result.RowsAffected(); countErr == nil {
				affectedCount += int(count)
			}
		}

		rows, err := tx.QueryContext(ctx, `
			SELECT 'settings', user_id::text FROM notif_user_settings WHERE user_id=$1
			UNION ALL SELECT 'preference', id::text FROM notif_preferences WHERE user_id=$1
			UNION ALL SELECT 'digest_window', id::text FROM notif_digest_windows WHERE user_id=$1
			UNION ALL SELECT 'delivery', id::text FROM notif_deliveries WHERE user_id=$1
			UNION ALL SELECT 'notification', id::text FROM notif_notifications WHERE user_id=$1
			ORDER BY 1,2`, surrogateID)
		if err != nil {
			return fmt.Errorf("notify closure commit result: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var table, id string
			if err := rows.Scan(&table, &id); err != nil {
				return fmt.Errorf("notify closure commit scan: %w", err)
			}
			h.Write([]byte(table))
			h.Write([]byte{0})
			h.Write([]byte(id))
			h.Write([]byte{0})
		}
		return rows.Err()
	})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), affectedCount, nil
}

type privacyExportSettings struct {
	Type              string    `json:"type"`
	UserID            string    `json:"user_id"`
	Locale            string    `json:"locale"`
	Timezone          string    `json:"timezone"`
	QuietHoursEnabled bool      `json:"quiet_hours_enabled"`
	QuietHoursStart   *string   `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd     *string   `json:"quiet_hours_end,omitempty"`
	DailyDigestHour   string    `json:"daily_digest_hour"`
	Version           int64     `json:"version"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// PrivacyExportPage returns a deterministic, bounded union of the Gateway
// notification records that are safe for a user export. It intentionally
// omits recipient ciphertext, device tokens, provider payloads, raw event
// bodies, and rendered HTML.
func (m *Module) PrivacyExportPage(ctx context.Context, subjectID uuid.UUID, cutoff time.Time, offset, pageSize int) ([]json.RawMessage, string, error) {
	if pageSize <= 0 {
		pageSize = privacyexport.DefaultPageSize
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT export_kind, encoded
		FROM (
			SELECT 'notification' AS export_kind, n.id::text AS sort_id, n.created_at AS sort_at,
				jsonb_build_object(
					'type','notification','id',n.id,'notification_type',COALESCE(NULLIF(n.kind,''),n.type),
					'title',n.title,'body',n.body,'read_at',n.read_at,'created_at',n.created_at,
					'category',NULLIF(n.category,''),'priority',NULLIF(n.priority,''),'deep_link',n.deep_link
				)::text AS encoded
			FROM notif_notifications n
			WHERE n.user_id=$1 AND n.created_at <= $2

			UNION ALL
			SELECT 'notification_settings', s.user_id::text, s.updated_at,
				jsonb_build_object(
					'type','notification_settings','user_id',s.user_id,'locale',s.locale,
					'timezone',s.timezone,'quiet_hours_enabled',s.quiet_hours_enabled,
					'quiet_hours_start',s.quiet_hours_start,'quiet_hours_end',s.quiet_hours_end,
					'daily_digest_hour',s.daily_digest_hour,'version',s.version,'updated_at',s.updated_at
				)::text
			FROM notif_user_settings s
			WHERE s.user_id=$1 AND s.updated_at <= $2

			UNION ALL
			SELECT 'notification_preference', p.id::text, p.created_at,
				jsonb_build_object(
					'type','notification_preference','id',p.id,'category',p.category,
					'channel',p.channel,'mode',p.mode,'version',p.version,'updated_at',p.updated_at
				)::text
			FROM notif_preferences p
			WHERE p.user_id=$1 AND p.created_at <= $2

			UNION ALL
			SELECT 'notification_device', d.id::text, d.created_at,
				jsonb_build_object(
					'type','notification_device','id',d.id,'platform',d.platform,
					'device_name',d.device_name,'token_suffix',d.token_suffix,'status',d.status,
					'last_success_at',d.last_success_at,'last_failure_at',d.last_failure_at,
					'last_failure_code',d.last_failure_code,'created_at',d.created_at,
					'updated_at',d.updated_at,'revoked_at',d.revoked_at
				)::text
			FROM notif_device_endpoints d
			WHERE d.user_id=$1 AND d.created_at <= $2

			UNION ALL
			SELECT 'notification_delivery', d.id::text, d.created_at,
				jsonb_build_object(
					'type','notification_delivery','id',d.id,'notification_id',d.notification_id,
					'channel',d.channel,'status',d.status,'attempt_count',d.attempt_count,
					'provider_message_id',d.provider_message_id,'last_error_code',d.last_error_code,
					'delivered_at',d.delivered_at,'suppressed_at',d.suppressed_at,'dead_at',d.dead_at,
					'created_at',d.created_at,'updated_at',d.updated_at
				)::text
			FROM notif_deliveries d
			WHERE d.user_id=$1 AND d.created_at <= $2

			UNION ALL
			SELECT 'notification_digest_window', w.id::text, w.created_at,
				jsonb_build_object(
					'type','notification_digest_window','id',w.id,'channel',w.channel,
					'local_window_date',w.local_window_date,'timezone',w.timezone,
					'scheduled_at',w.scheduled_at,'status',w.status,'item_count',w.item_count,
					'created_at',w.created_at,'updated_at',w.updated_at
				)::text
			FROM notif_digest_windows w
			WHERE w.user_id=$1 AND w.created_at <= $2
		) exported
		ORDER BY sort_at, sort_id, export_kind
		LIMIT $3 OFFSET $4`, subjectID, cutoff, pageSize+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("notify export: page: %w", err)
	}
	defer rows.Close()

	result := make([]json.RawMessage, 0, pageSize+1)
	for rows.Next() {
		var kind, encoded string
		if err := rows.Scan(&kind, &encoded); err != nil {
			return nil, "", fmt.Errorf("notify export: scan: %w", err)
		}
		result = append(result, json.RawMessage(encoded))
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("notify export: iterate: %w", err)
	}
	hasMore := len(result) > pageSize
	if hasMore {
		result = result[:pageSize]
	}
	return result, privacyexport.Next(offset, len(result), hasMore), nil
}

// PrivacyExportRows retains the owner interface used by the closure
// orchestrator while reusing the bounded page implementation above.
func (m *Module) PrivacyExportRows(ctx context.Context, subjectID uuid.UUID, cutoff time.Time) ([]json.RawMessage, error) {
	var all []json.RawMessage
	for offset := 0; ; {
		page, next, err := m.PrivacyExportPage(ctx, subjectID, cutoff, offset, privacyexport.DefaultPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" {
			return all, nil
		}
		offset += len(page)
	}
}

// Kept as a compile-time reminder that settings are intentionally hand-built
// export DTOs rather than model structs that could grow secret fields.
var _ = privacyExportSettings{}
