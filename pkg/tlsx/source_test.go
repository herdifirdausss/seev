package tlsx

import (
	"crypto/x509"
	"path/filepath"
	"testing"
	"time"
)

func TestCertSource_LoadsIdentityFromLeaf(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	ca.writeTo(t, dir)
	ca.issue(t, dir, "ledger", IdentityLedger, time.Hour)

	src, err := newCertSource(filepath.Join(dir, "ledger.pem"), filepath.Join(dir, "ledger-key.pem"), filepath.Join(dir, "ca.pem"), nil, time.Hour)
	if err != nil {
		t.Fatalf("NewCertSource: %v", err)
	}
	defer src.Stop()

	if got := src.Identity(); got != IdentityLedger {
		t.Fatalf("Identity() = %q, want %q", got, IdentityLedger)
	}
	if src.CAPool() == nil {
		t.Fatal("CAPool() = nil")
	}
	cert, err := src.GetCertificate(nil)
	if err != nil || cert == nil {
		t.Fatalf("GetCertificate() = %v, %v", cert, err)
	}
}

func TestCertSource_RejectsCertWithoutURISAN(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	ca.writeTo(t, dir)
	// The CA's own certificate carries no URI SAN — reuse it (paired with
	// the CA's own matching key, so tls.LoadX509KeyPair itself succeeds)
	// as a bogus "leaf" purely to exercise identityOf's guard.
	writeTestPEM(t, filepath.Join(dir, "bogus.pem"), "CERTIFICATE", ca.cert.Raw)
	keyDER, err := x509.MarshalECPrivateKey(ca.key)
	if err != nil {
		t.Fatalf("marshal CA key: %v", err)
	}
	writeTestPEM(t, filepath.Join(dir, "bogus-key.pem"), "EC PRIVATE KEY", keyDER)

	_, err = newCertSource(filepath.Join(dir, "bogus.pem"), filepath.Join(dir, "bogus-key.pem"), filepath.Join(dir, "ca.pem"), nil, time.Hour)
	if err == nil {
		t.Fatal("expected NewCertSource to reject a leaf with no URI SAN, got nil error")
	}
}

func TestCertSource_HotReloadsOnFileChange(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	ca.writeTo(t, dir)
	ca.issue(t, dir, "ledger", IdentityLedger, time.Hour)

	// Fast poll interval so the test doesn't wait on the production
	// default (docs/plan/45's own precedent for testing poll-based
	// reload: parameterize interval, never mutate it post-construction).
	src, err := newCertSource(filepath.Join(dir, "ledger.pem"), filepath.Join(dir, "ledger-key.pem"), filepath.Join(dir, "ca.pem"), nil, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewCertSource: %v", err)
	}
	defer src.Stop()

	if got := src.Identity(); got != IdentityLedger {
		t.Fatalf("initial Identity() = %q, want %q", got, IdentityLedger)
	}

	// Reissue as a DIFFERENT identity in place — proves the process
	// picks up a rotated cert without restart (docs/plan/49 K2/K9).
	// Sleep past a filesystem mtime granularity floor before rewriting so
	// the poll loop's mtime-comparison actually observes a change.
	time.Sleep(1100 * time.Millisecond)
	ca.issue(t, dir, "ledger", IdentityGateway, time.Hour)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if src.Identity() == IdentityGateway {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Identity() never reloaded to %q, still %q after deadline", IdentityGateway, src.Identity())
}

func TestCertSource_KeepsPreviousCertOnReloadFailure(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	ca.writeTo(t, dir)
	ca.issue(t, dir, "ledger", IdentityLedger, time.Hour)

	src, err := newCertSource(filepath.Join(dir, "ledger.pem"), filepath.Join(dir, "ledger-key.pem"), filepath.Join(dir, "ca.pem"), nil, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewCertSource: %v", err)
	}
	defer src.Stop()

	time.Sleep(1100 * time.Millisecond)
	// Corrupt the cert file in place — a reload attempt must fail closed
	// (keep serving the last-known-good cert) rather than leave the
	// process with no certificate at all.
	writeTestPEM(t, filepath.Join(dir, "ledger.pem"), "CERTIFICATE", []byte("not a real certificate"))

	time.Sleep(200 * time.Millisecond)
	if got := src.Identity(); got != IdentityLedger {
		t.Fatalf("Identity() = %q after a failed reload, want unchanged %q", got, IdentityLedger)
	}
}

