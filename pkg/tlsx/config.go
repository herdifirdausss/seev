package tlsx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"
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
//
// ClientAuth is deliberately RequireAnyClientCert, not
// RequireAndVerifyClientCert: the latter has Go's tls package verify the
// client's chain against a ClientCAs pool fixed at construction time —
// under this repo's short-lived leaves and rotate-in-place CA (K3/K9),
// that pool would never see a rotated CA for the rest of the process's
// life, permanently rejecting every legitimately-reissued client after a
// single rotation (docs/plan/49 TM-13, found live by the T6 rotation
// drill — cert files and CertSource's in-memory state were both correctly
// current; only this static field was stale). VerifyConnection below does
// the chain verification itself, reading src.CAPool() fresh on every
// handshake — the same pattern ClientConfig already uses below.
func ServerConfig(src *CertSource, allowedClientIdentities []string) *tls.Config {
	allowed := toSet(allowedClientIdentities)
	return &tls.Config{
		MinVersion:     minTLSVersion,
		GetCertificate: src.GetCertificate,
		ClientAuth:     tls.RequireAnyClientCert,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if err := verifyChainAgainst(cs, src.CAPool(), x509.ExtKeyUsageClientAuth); err != nil {
				// The chain never verified, so the peer's claimed identity
				// cannot be trusted — see handshakeFailuresTotal's doc
				// comment on why "unknown" is used here, never a value
				// taken from the unverified certificate itself.
				handshakeFailuresTotal.WithLabelValues("unknown", "untrusted_ca").Inc()
				return err
			}
			if err := verifyPeerIdentity(cs, allowed); err != nil {
				// The chain DID verify here, so this identity was signed
				// by our own CA — it comes from certgen's closed
				// knownServices set (K3), safe as a label value.
				id, _ := identityOf(cs.PeerCertificates[0])
				handshakeFailuresTotal.WithLabelValues(id, "identity_not_allowed").Inc()
				return err
			}
			return nil
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
		// No RootCAs field: it would be a snapshot fixed at construction
		// time, same trap ServerConfig's old ClientCAs field fell into
		// (docs/plan/49 TM-13) — and it would go entirely unused anyway,
		// since InsecureSkipVerify below disables Go's built-in
		// verification path that RootCAs feeds. VerifyConnection calls
		// src.CAPool() itself, fresh on every handshake, instead.
		//
		// ServerName is required by crypto/tls to run its own default
		// hostname verification against the leaf's DNS SANs; this repo's
		// leaves carry no DNS SANs at all (identity is the URI SAN only,
		// docs/plan/49 K3/K4), so default verification would always fail.
		// VerifyConnection performs the ACTUAL identity + trust check
		// below instead — skip the built-in check entirely, not just the
		// hostname part of it.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			// InsecureSkipVerify above disables Go's ENTIRE built-in
			// verification (not just hostname matching) — cs.VerifiedChains
			// is always nil in that mode, so chain trust has to be checked
			// here manually before the identity check means anything.
			if err := verifyChainAgainst(cs, src.CAPool(), x509.ExtKeyUsageServerAuth); err != nil {
				return err
			}
			return verifyPeerIdentity(cs, allowed)
		},
	}
}

// HTTPClient is the *http.Client counterpart to ClientConfig (docs/plan/49
// K6) — the many internal HTTP callers (admin-bff's downstream clients,
// gateway's ledger proxy, every service's own -healthcheck probe) need a
// ready-to-use client rather than a raw *tls.Config.
func HTTPClient(src *CertSource, expectedServerIdentity string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: ClientConfig(src, expectedServerIdentity)},
	}
}

// verifyChainAgainst manually verifies the peer's certificate chain
// against roots for the given usage — read fresh from CertSource.CAPool()
// by both callers below, so a CA rotation is honored on the very next
// handshake rather than a snapshot frozen at *tls.Config construction
// time (docs/plan/49 TM-13).
func verifyChainAgainst(cs tls.ConnectionState, roots *x509.CertPool, usage x509.ExtKeyUsage) error {
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
		KeyUsages:     []x509.ExtKeyUsage{usage},
	}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("tlsx: peer certificate chain verification failed: %w", err)
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
