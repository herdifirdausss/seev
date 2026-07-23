package transport

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// allowedMetadataKeys are the only client-supplied metadata keys ever passed
// through to a processor on the PUBLIC router (docs/roadmap/archive/10 Task T3):
//   - "direction"     — required by transfer_pocket ("to_pocket"/"from_pocket")
//   - "note"          — free-text description, purely informational
//   - "external_ref"  — caller's own correlation id, purely informational
//
// Everything else — including fee_amount, fee_gateway, and gateway — is
// either dropped or replaced with a server-computed value. The internal
// router does not use this allowlist; it trusts its caller.
var allowedMetadataKeys = map[string]bool{
	"direction":    true,
	"note":         true,
	"external_ref": true,
}

// maxExternalRefLen bounds the "external_ref" metadata key before it's
// persisted to ledger_transactions.external_ref (docs/roadmap/archive/16 Task T2, K5)
// — rejected outright rather than silently truncated (same principle as
// amount integrality, docs/roadmap/archive/10 Task T4: a caller relying on the full
// value for reconciliation matching must know immediately, not discover a
// truncated correlation id days later during recon).
const maxExternalRefLen = 128

// buildMetadata validates/sanitizes req.Metadata before it reaches a
// processor. Gateway is checked against constant.ValidGateways on BOTH
// routers (a bad gateway value fails every processor lookup downstream
// anyway — this just fails fast with a clear 400). Fee handling differs:
// the public router never honors a client-supplied fee_amount/fee_gateway,
// computing it from h.feePolicy instead; the internal router passes
// metadata through unchanged for its trusted caller. When req.QuoteID is
// set (docs/roadmap/archive/38 Task T4), fee resolution is SKIPPED here entirely —
// execTransfer consumes the quote itself, inside the posting transaction,
// and stamps fee_amount/fee_gateway from the EXACT quoted values; this
// function must never also resolve-and-stamp a fee in that case, or the
// quoted fee could be silently overwritten by a fresh (possibly different)
// fee_rules lookup.
func (h *handler) buildMetadata(ctx context.Context, userID uuid.UUID, req postTransactionRequest, amount decimal.Decimal) (map[string]any, error) {
	gateway, _ := generalutil.MetaString(req.Metadata, "gateway")
	if gateway != "" && !constant.ValidGateways[gateway] {
		return nil, fmt.Errorf("unknown gateway %q", gateway)
	}
	if externalRef, _ := generalutil.MetaString(req.Metadata, "external_ref"); len(externalRef) > maxExternalRefLen {
		return nil, fmt.Errorf("external_ref must be at most %d characters", maxExternalRefLen)
	}

	if h.allowedTypes == nil {
		// Internal router: trusted caller.
		return req.Metadata, nil
	}

	// Public router: allowlist descriptive keys, compute fee server-side.
	out := make(map[string]any, len(req.Metadata)+3)
	for k, v := range req.Metadata {
		if allowedMetadataKeys[k] {
			out[k] = v
		}
	}
	// request_id is injected AFTER the allowlist strip above (a client can
	// never smuggle its own "request_id" metadata key through) — sourced
	// from ctx, which by the time this runs holds the edge-sanitized id
	// WithRequestID attached (docs/roadmap/archive/36 Task T5), so this is the
	// authoritative end-to-end trace anchor for the posted transaction.
	if id := middleware.RequestIDFromCtx(ctx); id != "" {
		out["request_id"] = id
	}
	// currency is best-effort (docs/roadmap/archive/18 Task T2): if it can't be
	// resolved (e.g. the account doesn't exist yet), fall back to "" —
	// Resolve simply matches no rule then, identical to "no fee configured"
	// for that currency. The real not-found error surfaces properly from
	// ResolveAccounts a moment later; this lookup must never block a
	// request just to price a fee.
	currency, cerr := h.svc.GetUserCurrency(ctx, userID, req.PocketCode)
	if cerr != nil {
		currency = ""
	}
	if h.feePolicy != nil && req.QuoteID == "" {
		if fee, feeGateway, ok := h.feePolicy.Resolve(ctx, userID, req.Type, gateway, currency, amount); ok {
			out["fee_amount"] = fee.String()
			out["fee_gateway"] = feeGateway
		}
	}
	return out, nil
}
