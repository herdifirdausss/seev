package auth

// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md K13's remaining privacy-request metrics
// (seev_retention_*/seev_object_outbox_* already exist from T1/T1.6) —
// package-level so they register once regardless of how many Modules a
// process constructs, mirroring internal/platform/lifecycle/retention/worker/internal/platform/lifecycle/objectoutbox's own
// convention. Labels are kind/status/owner/operation/result only: never a
// user id, request id, or free-text reason.
//
// seev_pii_backfill_rows_total{owner,field,result} from K13's own
// canonical list is now permanently moot, not deferred: "A8 T2.5b" (the
// contract migration) removed BackfillOnce and --backfill-cryptox
// entirely once every field's plaintext column was dropped — there is no
// longer a backfill operation left to ever instrument.

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var privacyRequestsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "seev",
	Subsystem: "privacy",
	Name:      "requests",
	Help:      "Current privacy_requests row count, by kind (export|closure) and status (docs/roadmap/archive/51 K13). Refreshed once per worker tick.",
}, []string{"kind", "status"})

var privacyRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "seev",
	Subsystem: "privacy",
	Name:      "request_duration_seconds",
	Help:      "Time from requested_at to a terminal status, by kind and result (docs/roadmap/archive/51 K13).",
	Buckets:   []float64{1, 5, 15, 30, 60, 300, 900, 3600},
}, []string{"kind", "result"})

var privacyOwnerCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "privacy",
	Name:      "owner_calls_total",
	Help:      "Cross-service owner prepare/commit calls made by the closure saga, by owner, operation, and result (docs/roadmap/archive/51 K13).",
}, []string{"owner", "operation", "result"})

var privacyObjectDeleteTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "privacy",
	Name:      "object_delete_total",
	Help:      "Export archive object-delete enqueue attempts, by kind and result (docs/roadmap/archive/51 K13) — a privacy-flow-scoped view of the generic seev_object_outbox_* metrics.",
}, []string{"kind", "result"})
