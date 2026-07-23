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
	kycRescreenRunsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "auth", Subsystem: "kyc", Name: "sanctions_rescreen_runs_total",
		Help: "Periodic sanctions re-screen passes executed.",
	})
	kycRescreenSubjectsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "auth", Subsystem: "kyc", Name: "sanctions_rescreen_subjects_total",
		Help: "Approved KYC subjects submitted to periodic sanctions re-screening.",
	})
)
