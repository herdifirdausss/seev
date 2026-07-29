package api

// mapPayinStatus is the pure translation function docs/reference/c1-b2b-design.md
// §1.2 requires — PayinService's own internal statuses (internal/payin/model's
// TopupStatusPending/Settled/Expired) onto the locked, owner-neutral public
// enum. An unrecognized internal value maps to "failed" rather than
// panicking or leaking the raw string — a future internal status addition
// must never become a breaking B2B response change (§1's own stated goal).
func mapPayinStatus(internal string) string {
	switch internal {
	case "pending":
		return "pending"
	case "settled":
		return "settled"
	default: // "expired" and any future internal value
		return "failed"
	}
}

// mapPayoutStatus is §1.3's pure translation function — PayoutService's own
// internal statuses (internal/payout/model's Status* constants) onto the
// locked, owner-neutral public enum. An unrecognized internal value maps
// to "failed" for the same reason mapPayinStatus's default case does.
func mapPayoutStatus(internal string) string {
	switch internal {
	case "created", "held":
		return "pending"
	case "submitted", "vendor_pending":
		return "processing"
	case "settled":
		return "settled"
	default: // "failed", "cancelled", "rejected", and any future internal value
		return "failed"
	}
}
