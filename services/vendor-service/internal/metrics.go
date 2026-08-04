package vendorboundary

import "github.com/prometheus/client_golang/prometheus"

var (
	vendorOutboundAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev",
		Subsystem: "vendor",
		Name:      "outbound_attempts_total",
		Help:      "VendorService outbound vendor attempts by configured flow, vendor, operation, and outcome.",
	}, []string{"flow", "vendor", "operation", "outcome"})
	vendorCallbackDeliveries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev",
		Subsystem: "vendor",
		Name:      "callback_deliveries_total",
		Help:      "VendorService callback deliveries by configured vendor and durable outcome.",
	}, []string{"vendor", "outcome"})
	vendorCallbackDenied = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev",
		Subsystem: "vendor",
		Name:      "callback_denied_total",
		Help:      "VendorService callback requests rejected before owner delivery.",
	}, []string{"reason"})
)

func init() {
	prometheus.MustRegister(vendorOutboundAttempts, vendorCallbackDeliveries)
	prometheus.MustRegister(vendorCallbackDenied)
}
