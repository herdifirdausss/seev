package tlsx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// minTLSVersion matches internal/config's parseTLSConfig convention for
// DB/Redis connections (docs/plan/49 §2) — one floor for every TLS
// surface in this repo, service-plane included.
const minTLSVersion = tls.VersionTLS12

// ServerConfig builds a *tls.Config for a listener that requires and
// verifies a client certificate, then checks the client's URI SAN
// against allowedClientIdentities (docs/plan/49 K4 — identity is the URI
// SAN, never just "signed by our CA"). A connection from a validly-signed
// certificate whose identity isn't on the list is rejected just as hard
// as an unsigned one.
func ServerConfig(src *CertSource, allowedClientIdentities []string) *tls.Config {
	allowed := toSet(allowedClientIdentities)
	return &tls.Config{
		MinVersion:     minTLSVersion,
		GetCertificate: src.GetCertificate,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		ClientCAs:      src.CAPool(),
		VerifyConnection: func(cs tls.ConnectionState) error {
			// ClientCAs above is a snapshot from construction time; a CA
			// rotation is picked up on identity checks via CAPool() being
			// re-read here would require rebuilding the chain, which Go's
			// tls package already did against the ClientCAs pool in effect
			// for THIS handshake. Full CA hot-rotation is a T6 concern
			// (rotate CA -> reissue leaves -> old leaves stop verifying);
			// this function's job is strictly the identity allowlist.
			return verifyPeerIdentity(cs, allowed)
		},
	}
}

// ClientConfig builds a *tls.Config for dialing a specific server whose
// identity must equal expectedServerIdentity exactly — not "any identity
// in a list", since a client only ever intends to reach one server.
func ClientConfig(src *CertSource, expectedServerIdentity string) *tls.Config {
	allowed := toSet([]string{expectedServerIdentity})
	return &tls.Config{
		MinVersion:           minTLSVersion,
		GetClientCertificate: src.GetClientCertificate,
		RootCAs:              src.CAPool(),
		// ServerName is required by crypto/tls to run its own default
		// hostname verification against the leaf's DNS SANs; this repo's
		// leaves carry no DNS SANs at all (identity is the URI SAN only,
		// docs/plan/49 K3/K4), so default verification would always fail.
		// VerifyConnection performs the ACTUAL identity check below
		// instead — skip the built-in hostname check, not the whole
		// handshake's trust verification (RootCAs above still applies).
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			// InsecureSkipVerify above disables Go's ENTIRE built-in
			// verification (not just hostname matching) — cs.VerifiedChains
			// is always nil in that mode, so chain trust has to be checked
			// here manually before the identity check means anything.
			if err := verifyChainAgainst(cs, src.CAPool()); err != nil {
				return err
			}
			return verifyPeerIdentity(cs, allowed)
		},
	}
}

// verifyChainAgainst manually verifies the peer's certificate chain
// against roots — the client-side counterpart to what Go's tls package
// would have done automatically if InsecureSkipVerify were false (which
// it must be here; see ClientConfig's comment on why default hostname
// verification can never succeed against this repo's URI-only SANs).
func verifyChainAgainst(cs tls.ConnectionState, roots *x509.CertPool) error {
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("tlsx: no peer certificate presented")
	}
	leaf := cs.PeerCertificates[0]
	intermediates := x509.NewCertPool()
	for _, cert := range cs.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("tlsx: server certificate chain verification failed: %w", err)
	}
	return nil
}

func toSet(identities []string) map[string]bool {
	set := make(map[string]bool, len(identities))
	for _, id := range identities {
		set[id] = true
	}
	return set
}

func verifyPeerIdentity(cs tls.ConnectionState, allowed map[string]bool) error {
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("tlsx: no peer certificate presented")
	}
	id, err := identityOf(cs.PeerCertificates[0])
	if err != nil {
		return fmt.Errorf("tlsx: peer certificate identity: %w", err)
	}
	if !allowed[id] {
		return fmt.Errorf("tlsx: peer identity %q is not on the allowlist", id)
	}
	return nil
}
