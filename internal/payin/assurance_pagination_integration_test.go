//go:build integration

package payin_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
)

func TestListAssuranceRecords_KeysetPaginationWithEqualTimestamps(t *testing.T) {
	db := setupPayinTestDB(t)
	module := newPayinModule(db)
	ctx := context.Background()
	updated := time.Now().UTC().Add(-3 * time.Minute)

	const total = 600
	for i := 0; i < total; i++ {
		id := uuid.New()
		eventID := fmt.Sprintf("assurance-page-%03d-%s", i, id)
		_, err := db.ExecContext(ctx, `
			INSERT INTO payin_webhook_events
			(id, vendor, vendor_event_id, external_ref, user_id, amount, currency, raw, status, request_id, created_at, updated_at)
			VALUES ($1, 'mockvendor', $2, $2, $3, 1000, 'IDR', '{}'::jsonb, 'failed', 'integration-page', $4, $4)`,
			id, eventID, uuid.New(), updated)
		require.NoError(t, err)
	}

	cutoff := timestamppb.New(time.Now().UTC())
	first, err := module.ListAssuranceRecords(ctx, &payinv1.ListAssuranceRecordsRequest{PageSize: 500, Cutoff: cutoff})
	require.NoError(t, err)
	require.Len(t, first.GetRecords(), 500)
	require.True(t, first.GetHasMore())

	second, err := module.ListAssuranceRecords(ctx, &payinv1.ListAssuranceRecordsRequest{
		PageSize:        500,
		Cutoff:          cutoff,
		CursorUpdatedAt: first.GetNextUpdatedAt(),
		CursorId:        first.GetNextId(),
	})
	require.NoError(t, err)
	require.Len(t, second.GetRecords(), total-500)
	require.False(t, second.GetHasMore())

	seen := make(map[string]struct{}, total)
	for _, page := range [][]*payinv1.AssuranceRecord{first.GetRecords(), second.GetRecords()} {
		for _, record := range page {
			_, duplicate := seen[record.GetId()]
			require.False(t, duplicate, "keyset pagination duplicated %s", record.GetId())
			seen[record.GetId()] = struct{}{}
		}
	}
	require.Len(t, seen, total)
}
