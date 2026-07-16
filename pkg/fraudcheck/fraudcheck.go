// Package fraudcheck is the single shared client contract every caller of
// fraud-service's Screen RPC uses — ledger's transport (P2P transfer),
// payin (topup), and payout (payout create) all wrap the SAME timeout/
// fail-open/fail-closed rules through this one implementation
// (docs/plan/37 Task T2), rather than each service re-deriving its own
// copy of internal/ledger/screening/grpchook.go's logic.
//
// Contract: Check returns a non-nil error ONLY for an infra failure
// (unreachable service, timeout, malformed response) — the caller decides
// whether to fail open or fail closed on that error (every current caller
// fails open: posting/topup/payout proceeds, logged as an ERROR). A nil
// error with Verdict.Block == true is a definite business decision the
// caller MUST honor (fail-closed): the transaction is rejected before any
// posting/hold/insert happens.
package fraudcheck

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/shopspring/decimal"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// screenTimeout bounds every Screen call — the same 500ms budget the
// in-transaction hook used before docs/plan/37 moved screening out of the
// posting DB transaction; unchanged here since callers still need a bound
// fast enough to not stall a user-facing request.
const screenTimeout = 500 * time.Millisecond

// screeningClientErrorsTotal counts EVERY Check call that returned an infra
// error, labeled by caller (ledger|payin|payout) — every one of these is a
// fail-open event for whichever caller hit it (docs/plan/20 Task T1's
// original tradeoff, now shared three ways instead of ledger-only). Ops
// should alert on a sustained rate here: it means screening coverage is
// degraded for that flow even though no request-facing error occurred.
var screeningClientErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "screening",
	Name:      "client_errors_total",
	Help:      "Total fraudcheck.Check infra errors (fail-open) by caller — the screening call itself failed, not a Block verdict.",
}, []string{"caller"})

// Verdict mirrors fraudv1.ScreenResponse — Block is a definite business
// decision, Reason is informational (attached to whatever business error
// the caller surfaces for a block, or ignored on allow).
type Verdict struct {
	Block  bool
	Reason string
}

// Client wraps a fraudv1.FraudServiceClient for one calling service. Caller
// identifies the calling service (ledger|payin|payout) for the
// screening_client_errors_total metric label — construct one Client per
// service at startup, not per-request.
type Client struct {
	grpc   fraudv1.FraudServiceClient
	caller string
}

// New builds a Client. grpc is typically a connection dialed via
// grpcx.DialLazy(FRAUD_GRPC_ADDR, INTERNAL_GRPC_TOKEN) — lazy so the
// service doesn't fail to start just because fraud-service isn't up yet
// (fail-open is the whole point).
func New(grpc fraudv1.FraudServiceClient, caller string) *Client {
	return &Client{grpc: grpc, caller: caller}
}

// Check screens one candidate transaction. flow identifies the calling
// surface ("p2p_transfer" | "topup" | "payout", docs/plan/37) and rides
// along purely for screening_events audit/trace correlation — rules never
// branch on it. request_id is read from ctx (docs/plan/36) and forwarded
// the same way.
func (c *Client) Check(ctx context.Context, flow, txType string, userID uuid.UUID, amount decimal.Decimal, currency string) (Verdict, error) {
	callCtx, cancel := context.WithTimeout(ctx, screenTimeout)
	defer cancel()

	response, err := c.grpc.Screen(callCtx, &fraudv1.ScreenRequest{
		TxType: txType, UserId: userID.String(), Amount: amount.String(), Currency: currency,
		RequestId: middleware.RequestIDFromCtx(ctx), Flow: flow,
	})
	if err != nil {
		screeningClientErrorsTotal.WithLabelValues(c.caller).Inc()
		return Verdict{}, fmt.Errorf("fraudcheck: screen %s: %w", flow, err)
	}
	return Verdict{Block: response.GetBlock(), Reason: response.GetReason()}, nil
}
