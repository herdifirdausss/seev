package auth

// docs/roadmap/active/51-a8-data-lifecycle-privacy.md K13's remaining privacy-request metrics
// (seev_retention_*/seev_object_outbox_* already exist from T1/T1.6) —
// package-level so they register once regardless of how many Modules a
// process constructs, mirroring pkg/retentionworker/pkg/objectoutbox's own
// convention. Labels are kind/status/owner/operation/result only: never a
// user id, request id, or free-text reason.
//
// seev_pii_backfill_rows_total{owner,field,result} from K13's own
// canonical list is deliberately NOT implemented this pass: it would mean
// instrumenting BackfillOnce across four separate repository packages
// (auth, payin, payout, ledger/recon) for a one-time/operational CLI flow
// (--backfill-cryptox) already proven complete via T2.5's own live
// verification — lower incident-response value than the request-lifecycle
// metrics below, and a real cross-package lift. Flagged as a gap for "A8
// T5b"/a future pass, the same honest-gap convention T1 already used for
// seev_retention_oldest_eligible_age_seconds.

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var privacyRequestsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "privacy",
	Name:      "requests",
	Help:      "Current privacy_requests row count, by kind (export|closure) and status (docs/roadmap/active/51 K13). Refreshed once per worker tick.",
}, []string{"kind", "status"})

var privacyRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "seev",
	Subsystem: "privacy",
	Name:      "request_duration_seconds",
	Help:      "Time from requested_at to a terminal status, by kind and result (docs/roadmap/active/51 K13).",
	Buckets:   []float64{1, 5, 15, 30, 60, 300, 900, 3600},
}, []string{"kind", "result"})

var privacyOwnerCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "privacy",
	Name:      "owner_calls_total",
	Help:      "Cross-service owner prepare/commit calls made by the closure saga, by owner, operation, and result (docs/roadmap/active/51 K13).",
}, []string{"owner", "operation", "result"})

var privacyObjectDeleteTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "privacy",
	Name:      "object_delete_total",
	Help:      "Export archive object-delete enqueue attempts, by kind and result (docs/roadmap/active/51 K13) — a privacy-flow-scoped view of the generic seev_object_outbox_* metrics.",
}, []string{"kind", "result"})
