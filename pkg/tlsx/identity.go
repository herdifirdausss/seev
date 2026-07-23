// Package tlsx provides the mutual-TLS building blocks shared by
// pkg/grpcx and every internal HTTP server/client in this repo
// (docs/roadmap/archive/49 K2). Identity is a SPIFFE-style URI SAN
// ("spiffe://seev/<service>"), never a Common Name — pkg/tlsx never
// trusts anything about a peer beyond "this CA signed this cert and the
// cert's URI SAN is on my caller's allowlist".
package tlsx

// Identity constants are the closed set of SPIFFE-style URI SANs this
// repo issues certificates for (docs/roadmap/archive/49 K3). New services must add
// their identity here AND to cmd/certgen's known-service list — an
// identity that exists in a certificate but not in a caller's allowlist
// is simply rejected, never implicitly trusted.
const (
	IdentityGateway     = "spiffe://seev/gateway"
	IdentityAuth        = "spiffe://seev/auth"
	IdentityLedger      = "spiffe://seev/ledger"
	IdentityPayin       = "spiffe://seev/payin"
	IdentityPayout      = "spiffe://seev/payout"
	IdentityFraud       = "spiffe://seev/fraud"
	IdentityAdminBFF    = "spiffe://seev/admin-bff"
	IdentityAssurance   = "spiffe://seev/assurance"
	IdentityDevOperator = "spiffe://seev/dev-operator"
	IdentityPrometheus  = "spiffe://seev/prometheus"
	// IdentityBackupAgent is docs/roadmap/active/50 K13's operational agent — not a
	// domain service, but it still authenticates over the same mTLS fabric
	// as everything else here.
	IdentityBackupAgent = "spiffe://seev/backup-agent"
)
