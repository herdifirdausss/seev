package worker

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	kycApplyRetryAttemptsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "auth", Subsystem: "kyc", Name: "apply_retry_attempts_total",
		Help: "KYC apply retry attempts executed by the relay.",
	})
	kycApplyRetriesDeadTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "auth", Subsystem: "kyc", Name: "apply_retry_dead_total",
		Help: "KYC apply intents moved to dead after exhausting retries.",
	})
)
