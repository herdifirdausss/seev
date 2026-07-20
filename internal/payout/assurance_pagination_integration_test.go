//go:build integration

package payout_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
)

func TestListAssuranceRecords_KeysetPaginationWithEqualTimestamps(t *testing.T) {
	db := setupPayoutTestDB(t)
	_, module, _ := newPayoutTestModules(db)
	ctx := context.Background()
	updated := time.Now().UTC().Add(-3 * time.Minute)

	const total = 600
	for i := 0; i < total; i++ {
		id := uuid.New()
		_, err := db.ExecContext(ctx, `
			INSERT INTO payout_requests
			(id, user_id, amount, currency, vendor, destination, status, created_by, created_at, updated_at)
			VALUES ($1, $2, 1000, 'IDR', 'mockvendor', '{"bank_code":"014","account_no":"1"}'::jsonb, 'rejected', 'integration-page', $3, $3)`,
			id, uuid.New(), updated)
		require.NoError(t, err, "row %d", i)
	}

	cutoff := timestamppb.New(time.Now().UTC())
	first, err := module.ListAssuranceRecords(ctx, &payoutv1.ListAssuranceRecordsRequest{PageSize: 500, Cutoff: cutoff})
	require.NoError(t, err)
	require.Len(t, first.GetRecords(), 500)
	require.True(t, first.GetHasMore())

	second, err := module.ListAssuranceRecords(ctx, &payoutv1.ListAssuranceRecordsRequest{
		PageSize:        500,
		Cutoff:          cutoff,
		CursorUpdatedAt: first.GetNextUpdatedAt(),
		CursorId:        first.GetNextId(),
	})
	require.NoError(t, err)
	require.Len(t, second.GetRecords(), total-500)
	require.False(t, second.GetHasMore())

	seen := make(map[string]struct{}, total)
	for _, page := range [][]*payoutv1.AssuranceRecord{first.GetRecords(), second.GetRecords()} {
		for _, record := range page {
			_, duplicate := seen[record.GetId()]
			require.False(t, duplicate, "keyset pagination duplicated %s", record.GetId())
			seen[record.GetId()] = struct{}{}
		}
	}
	require.Len(t, seen, total, fmt.Sprintf("expected %d unique payout records", total))
}
