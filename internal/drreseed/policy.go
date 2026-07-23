package drreseed

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/policy"
	"github.com/herdifirdausss/seev/pkg/cache"
)

// reachablePolicyTypes maps every transaction type the LIVE system ever
// actually calls policy.Record for onto which side of the transaction is
// "the user" whose counter it belongs to. policy.Record fires
// unconditionally (regardless of whether a limit is configured) for
// every type in internal/ledger/transport/http.go's publicUserTypes when
// a PolicyChecker is attached — that set is exactly these four types,
// confirmed from each processor's own ResolveAccounts:
// transfer_p2p/transfer_pocket/withdraw_initiate/escrow_hold are all
// "buyer/sender's own cash (or pocket) → somewhere else", i.e. source.
// money_in is deliberately NOT in this map: it is confirmed unreachable
// through the policy-checked public router (the internal router never
// receives a PolicyChecker at all — internal/ledger/transport/http.go's
// NewInternalRouterWithFeePolicy takes no policy argument), so the live
// system never records a money_in policy counter for any user — nothing
// to reconstruct there.
var reachablePolicyTypes = map[string]struct{ userSide string }{
	"transfer_p2p":      {"source"},
	"transfer_pocket":   {"source"},
	"withdraw_initiate": {"source"},
	"escrow_hold":       {"source"},
}

const (
	dailyCounterTTL   = 48 * time.Hour
	monthlyCounterTTL = 35 * 24 * time.Hour
)

// ReconstructPolicyCounters rebuilds today's and this month's policy
// counters (docs/roadmap/active/50 K10 item 2: "rebuild current policy counters
// from posted ledger transactions within the active daily/monthly
// windows") for every (user, type) pair with a qualifying posted
// transaction. loc must be Asia/Jakarta — the same location
// internal/policy's Engine anchors "today"/"this month" to
// (internal/ledger/ledger.go's own LoadLocation call); a UTC-fallback
// caller here would compute the wrong calendar day/month boundary and
// silently under- or over-count.
func ReconstructPolicyCounters(ctx context.Context, ledgerDB *sql.DB, counter cache.Counter, loc *time.Location, report *Report) error {
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	monthEnd := monthStart.AddDate(0, 1, 0)

	for txType, mapping := range reachablePolicyTypes {
		accountColumn := "source_account_id"
		if mapping.userSide == "destination" {
			accountColumn = "destination_account_id"
		}

		if err := reconstructWindow(ctx, ledgerDB, counter, txType, accountColumn, dayStart, dayEnd,
			func(userID uuid.UUID) string { return policy.DailyAmountKey(userID, txType, now) },
			func(userID uuid.UUID) string { return policy.DailyCountKey(userID, txType, now) },
			dailyCounterTTL, report); err != nil {
			return fmt.Errorf("reconstruct daily policy counters for %s: %w", txType, err)
		}
		if err := reconstructWindow(ctx, ledgerDB, counter, txType, accountColumn, monthStart, monthEnd,
			func(userID uuid.UUID) string { return policy.MonthlyAmountKey(userID, txType, now) },
			nil, monthlyCounterTTL, report); err != nil {
			return fmt.Errorf("reconstruct monthly policy counters for %s: %w", txType, err)
		}
	}
	return nil
}

// reconstructWindow aggregates one (type, account-role, time-window)
// slice of posted ledger_transactions per owning user and writes the
// resulting total(s) through cache.Counter.IncrBy — on a freshly emptied
// Redis this behaves exactly like a plain SET (IncrBy against a
// non-existent key both creates it at the given value and, via ExpireNX,
// sets its TTL), so no separate "absolute set" primitive is needed.
func reconstructWindow(ctx context.Context, ledgerDB *sql.DB, counter cache.Counter, txType, accountColumn string, windowStart, windowEnd time.Time, amountKey func(uuid.UUID) string, countKey func(uuid.UUID) string, ttl time.Duration, report *Report) error {
	query := fmt.Sprintf(`
		SELECT a.owner_id, SUM(lt.amount), COUNT(*)
		FROM ledger_transactions lt
		JOIN accounts a ON a.id = lt.%s
		WHERE lt.type = $1 AND lt.status = 'posted' AND lt.created_at >= $2 AND lt.created_at < $3
		  AND a.owner_id IS NOT NULL
		GROUP BY a.owner_id`, accountColumn)
	rows, err := ledgerDB.QueryContext(ctx, query, txType, windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("aggregate posted transactions: %w", err)
	}
	defer rows.Close()

	type aggregate struct {
		userID uuid.UUID
		amount int64
		count  int64
	}
	var aggregates []aggregate
	for rows.Next() {
		var a aggregate
		if err := rows.Scan(&a.userID, &a.amount, &a.count); err != nil {
			return fmt.Errorf("scan aggregate: %w", err)
		}
		aggregates = append(aggregates, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, a := range aggregates {
		if _, err := counter.IncrBy(ctx, amountKey(a.userID), a.amount, ttl); err != nil {
			return fmt.Errorf("write amount counter for user %s: %w", a.userID, err)
		}
		report.PolicyCountersWritten++
		if countKey != nil {
			if _, err := counter.IncrBy(ctx, countKey(a.userID), a.count, ttl); err != nil {
				return fmt.Errorf("write count counter for user %s: %w", a.userID, err)
			}
			report.PolicyCountersWritten++
		}
	}
	return nil
}
