package tlsx

// docs/plan/49 K10 — package-level metrics, registered once regardless of
// how many CertSources/tls.Configs a process builds.

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// certExpirySeconds reports the ABSOLUTE unix timestamp (not seconds
// remaining) each CertSource's own leaf certificate expires at, updated on
// every successful reload — an alerting rule computes remaining time as
// `tlsx_cert_expiry_seconds - time()` rather than this process
// continuously re-publishing an ever-decreasing value itself. identity is
// bounded: each running process owns exactly one SPIFFE identity (K3/K4),
// so this is one time series per process, never per-request/per-peer.
var certExpirySeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "tlsx_cert_expiry_seconds",
	Help: "Unix timestamp (seconds) this process's own mTLS leaf certificate expires at, by identity.",
}, []string{"identity"})

// handshakeFailuresTotal counts server-side mTLS handshakes rejected by
// ServerConfig's VerifyConnection — an untrusted/rotated-out CA
// (docs/plan/49 TM-13) or a validly-signed peer identity outside the
// allowlist (K4). Does NOT cover a connection presenting zero client
// certificates: Go's tls package aborts that case before VerifyConnection
// ever runs, and that failure mode belongs to connection-level metrics,
// not certificate hygiene.
//
// identity is only ever set to a real peer identity for the
// identity_not_allowed reason, where the chain has ALREADY verified
// against our own CA — so the value necessarily comes from certgen's
// closed knownServices set (K3), safe cardinality. For untrusted_ca, the
// chain never verified, so the peer's claimed identity cannot be trusted
// and is deliberately NOT used as a label value — an attacker presenting
// arbitrary forged identities in unverified certs must never be able to
// inject arbitrary Prometheus label values (unbounded-cardinality risk).
var handshakeFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "tlsx_handshake_failures_total",
	Help: "mTLS server-side handshake verification failures, by peer identity (when trusted) and reason.",
}, []string{"identity", "reason"})
