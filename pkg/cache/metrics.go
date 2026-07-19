package cache

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// redisBackendActive is docs/plan/45 K6's redis_backend_active{primitive,
// backend} gauge — 1 for whichever of "redis"/"local" a FailoverLimiter/
// FailoverCounter is currently routing to for a given primitive, 0 for the
// other. primitive is a fixed, low-cardinality enum ("rate_limiter" |
// "policy_counter" | "fraud_velocity" — the latter set directly by
// internal/fraud, which imports this package), never derived from request
// input.
var redisBackendActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "cache",
	Name:      "redis_backend_active",
	Help:      "Which backend a failover-capable Redis primitive is currently using (docs/plan/45 K6).",
}, []string{"primitive", "backend"})
