package tlsx

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// TestCertExpirySeconds_SetOnReload proves docs/plan/49 K10: loading a
// CertSource publishes its leaf's NotAfter as an absolute unix timestamp
// under its own identity label, and a subsequent rotation overwrites
// (never accumulates onto) that same label.
func TestCertExpirySeconds_SetOnReload(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	ca.writeTo(t, dir)
	ca.issue(t, dir, "ledger", IdentityLedger, 2*time.Hour)

	src, err := NewCertSource(filepath.Join(dir, "ledger.pem"), filepath.Join(dir, "ledger-key.pem"), filepath.Join(dir, "ca.pem"), nil)
	if err != nil {
		t.Fatalf("NewCertSource: %v", err)
	}
	defer src.Stop()

	leaf := loadTestCert(t, filepath.Join(dir, "ledger.pem"))
	got := testutil.ToFloat64(certExpirySeconds.WithLabelValues(IdentityLedger))
	assert.Equal(t, float64(leaf.NotAfter.Unix()), got)

	// Rotate to a longer TTL — the gauge under the SAME identity label
	// must move to the NEW expiry, not add a second series.
	time.Sleep(1100 * time.Millisecond)
	ca.issue(t, dir, "ledger", IdentityLedger, 6*time.Hour)

	const fastPoll = 20 * time.Millisecond
	src2, err := newCertSource(filepath.Join(dir, "ledger.pem"), filepath.Join(dir, "ledger-key.pem"), filepath.Join(dir, "ca.pem"), nil, fastPoll)
	if err != nil {
		t.Fatalf("NewCertSource (post-reissue): %v", err)
	}
	defer src2.Stop()
	newLeaf := loadTestCert(t, filepath.Join(dir, "ledger.pem"))
	got = testutil.ToFloat64(certExpirySeconds.WithLabelValues(IdentityLedger))
	assert.Equal(t, float64(newLeaf.NotAfter.Unix()), got)
	assert.NotEqual(t, float64(leaf.NotAfter.Unix()), got, "expiry must have moved to the reissued leaf's later NotAfter")
}

// TestHandshakeFailuresTotal_UntrustedCA proves a chain-verification
// failure is counted with reason=untrusted_ca and identity=unknown — the
// peer's claimed identity is never trusted as a label value here, since
// the chain never verified (docs/plan/49 K10, TM-13 context).
func TestHandshakeFailuresTotal_UntrustedCA(t *testing.T) {
	trustedDir := t.TempDir()
	trustedCA := newTestCA(t)
	trustedCA.writeTo(t, trustedDir)
	trustedCA.issue(t, trustedDir, "ledger", IdentityLedger, time.Hour)

	rogueDir := t.TempDir()
	rogueCA := newTestCA(t)
	rogueCA.writeTo(t, rogueDir)
	rogueCA.issue(t, rogueDir, "gateway", IdentityGateway, time.Hour)

	ledgerSrc, err := NewCertSource(filepath.Join(trustedDir, "ledger.pem"), filepath.Join(trustedDir, "ledger-key.pem"), filepath.Join(trustedDir, "ca.pem"), nil)
	if err != nil {
		t.Fatalf("NewCertSource(ledger): %v", err)
	}
	defer ledgerSrc.Stop()
	// Presents a rogue-signed leaf (untrusted from the SERVER's
	// perspective) but trusts trustedCA itself (so the CLIENT's own
	// verification of the server succeeds) — isolates the failure to
	// exactly the property under test: the server rejecting the client's
	// cert, not a mutual/two-sided CA mismatch.
	rogueClientSrc, err := NewCertSource(filepath.Join(rogueDir, "gateway.pem"), filepath.Join(rogueDir, "gateway-key.pem"), filepath.Join(trustedDir, "ca.pem"), nil)
	if err != nil {
		t.Fatalf("NewCertSource(rogue client): %v", err)
	}
	defer rogueClientSrc.Stop()

	before := testutil.ToFloat64(handshakeFailuresTotal.WithLabelValues("unknown", "untrusted_ca"))

	serverCfg := ServerConfig(ledgerSrc, []string{IdentityGateway})
	addr, result := listenTLS(t, serverCfg)
	if err := dial(addr, ClientConfig(rogueClientSrc, IdentityLedger)); err == nil {
		t.Fatal("dial from an untrusted CA unexpectedly succeeded")
	}
	_ = result()

	after := testutil.ToFloat64(handshakeFailuresTotal.WithLabelValues("unknown", "untrusted_ca"))
	assert.Equal(t, before+1, after)
}

// TestHandshakeFailuresTotal_IdentityNotAllowed proves a validly-signed
// but non-allowlisted peer is counted with reason=identity_not_allowed
// and the peer's real (CA-verified, therefore trustworthy) identity.
func TestHandshakeFailuresTotal_IdentityNotAllowed(t *testing.T) {
	ledger, _, fraud, _ := setupHandshakeFixture(t)

	before := testutil.ToFloat64(handshakeFailuresTotal.WithLabelValues(IdentityFraud, "identity_not_allowed"))

	serverCfg := ServerConfig(ledger, []string{IdentityGateway})
	addr, result := listenTLS(t, serverCfg)
	if err := dial(addr, ClientConfig(fraud, IdentityLedger)); err == nil {
		t.Fatal("dial with a non-allowlisted identity unexpectedly succeeded")
	}
	_ = result()

	after := testutil.ToFloat64(handshakeFailuresTotal.WithLabelValues(IdentityFraud, "identity_not_allowed"))
	assert.Equal(t, before+1, after)
}
