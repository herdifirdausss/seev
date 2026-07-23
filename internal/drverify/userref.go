package drverify

import (
	"context"
	"database/sql"
	"fmt"
)

// userRefSource is one (service, query) pair collecting distinct user
// reference values that must exist in seev_auth.auth_users. There is no
// database-level FK for any of these — migrations/ledger/000001's own
// comment says why: "users are managed by the auth module ... integrity
// is enforced by the application" — which is exactly the gap K9's
// "impossible owner reference" check exists to close for the first time.
var userRefSources = []struct {
	service, query string
}{
	{"ledger", `SELECT DISTINCT owner_id::text FROM accounts WHERE owner_type <> 'system' AND owner_id IS NOT NULL`},
	{"ledger", `SELECT DISTINCT user_id::text FROM fee_rules WHERE user_id IS NOT NULL`},
	{"ledger", `SELECT DISTINCT user_id::text FROM fee_quotes`},
	{"payin", `SELECT DISTINCT user_id::text FROM payin_topup_intents`},
	{"payin", `SELECT DISTINCT user_id::text FROM payin_webhook_events`},
	{"payout", `SELECT DISTINCT user_id::text FROM payout_requests`},
}

// checkUserReferences collects every distinct owner/user reference across
// ledger, payin, and payout, then checks each exists in seev_auth's
// auth_users — K9's "impossible owner reference", fatal.
func checkUserReferences(ctx context.Context, dbs map[string]*sql.DB, cfg Config, report *Report) error {
	seen := make(map[string][]string) // userID -> services that referenced it
	for _, source := range userRefSources {
		db, ok := dbs[source.service]
		if !ok {
			continue
		}
		if err := readOnlyQuery(ctx, db, cfg, func(ctx context.Context, tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, source.query)
			if err != nil {
				return fmt.Errorf("collect user references: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return fmt.Errorf("scan user reference: %w", err)
				}
				seen[id] = append(seen[id], source.service)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
	}
	if len(seen) == 0 {
		return nil
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	authDB, ok := dbs["auth"]
	if !ok {
		return fmt.Errorf("auth database connection unavailable")
	}
	return readOnlyQuery(ctx, authDB, cfg, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id::text FROM auth_users WHERE id = ANY($1::uuid[])`, ids)
		if err != nil {
			return fmt.Errorf("check user existence: %w", err)
		}
		defer rows.Close()
		exists := make(map[string]bool, len(ids))
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan existing user: %w", err)
			}
			exists[id] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for id, services := range seen {
			if exists[id] {
				continue
			}
			for _, service := range services {
				report.addFinding(Finding{
					Code: "OWNER_REFERENCE_INVALID", Severity: SeverityFatal, Service: service,
					ResourceID: id,
					Message:    "referenced user_id/owner_id does not exist in seev_auth.auth_users",
				})
			}
		}
		return nil
	})
}
