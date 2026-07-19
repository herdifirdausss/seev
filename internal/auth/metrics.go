package auth

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	kycApplyRetriesQueuedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "auth", Subsystem: "kyc", Name: "apply_retry_queued_total",
		Help: "KYC approvals queued after the inline ledger policy application failed.",
	})
)
