package tlsx

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"path/filepath"
	"testing"
	"time"
)

// listenTLS starts a one-shot TLS echo listener using cfg and returns its
// address plus a function that blocks for the single accepted connection's
// handshake result (nil on success, the handshake/accept error otherwise).
func listenTLS(t *testing.T, cfg *tls.Config) (addr string, result func() error) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			done <- nil
			return
		}
		done <- tlsConn.HandshakeContext(context.Background())
	}()
	return ln.Addr().String(), func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for server-side handshake result")
			return nil
		}
	}
}

func dial(addr string, cfg *tls.Config) error {
	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Force the handshake to complete (tls.Dial already does, but be
	// explicit) and prove the connection is actually usable end to end.
	if err := conn.HandshakeContext(context.Background()); err != nil {
		return err
	}
	if _, err := io.WriteString(conn, "ping"); err != nil {
		return err
	}
	// TLS 1.3 defers full post-handshake authentication failures (e.g. a
	// server rejecting a missing/disallowed client cert) to an alert that
	// only surfaces once the client actually reads — tls.Dial returning
	// nil and even a successful Write are NOT proof the peer accepted the
	// connection (confirmed empirically while writing this test: a write
	// alone silently succeeded against a server that had already aborted
	// the handshake). A Read is what actually observes the alert.
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}

// setup issues a CA plus ledger/gateway/fraud leaves (fraud plays the role
// of "an identity that exists but is never on any allowlist below") and
// returns their CertSources.
func setupHandshakeFixture(t *testing.T) (ledger, gateway, fraud *CertSource, otherCA *testCA) {
	t.Helper()
	dir := t.TempDir()
	ca := newTestCA(t)
	ca.writeTo(t, dir)
	ca.issue(t, dir, "ledger", IdentityLedger, time.Hour)
	ca.issue(t, dir, "gateway", IdentityGateway, time.Hour)
	ca.issue(t, dir, "fraud", IdentityFraud, time.Hour)

	load := func(name string) *CertSource {
		src, err := NewCertSource(filepath.Join(dir, name+".pem"), filepath.Join(dir, name+"-key.pem"), filepath.Join(dir, "ca.pem"), nil)
		if err != nil {
			t.Fatalf("NewCertSource(%s): %v", name, err)
		}
		t.Cleanup(src.Stop)
		return src
	}
	return load("ledger"), load("gateway"), load("fraud"), newTestCA(t)
}

func TestServerConfig_AcceptsAllowlistedClientIdentity(t *testing.T) {
	ledger, gateway, _, _ := setupHandshakeFixture(t)

	serverCfg := ServerConfig(ledger, []string{IdentityGateway})
	addr, result := listenTLS(t, serverCfg)

	clientCfg := ClientConfig(gateway, IdentityLedger)
	if err := dial(addr, clientCfg); err != nil {
		t.Fatalf("dial with allowlisted identity failed: %v", err)
	}
	if err := result(); err != nil {
		t.Fatalf("server-side handshake failed for an allowlisted client: %v", err)
	}
}

func TestServerConfig_RejectsNonAllowlistedClientIdentity(t *testing.T) {
	ledger, _, fraud, _ := setupHandshakeFixture(t)

	// fraud is a real, CA-signed identity — just never granted access to
	// this particular listener (docs/roadmap/archive/49 K4: identity allowlist, not
	// "any cert our CA signed").
	serverCfg := ServerConfig(ledger, []string{IdentityGateway})
	addr, result := listenTLS(t, serverCfg)

	clientCfg := ClientConfig(fraud, IdentityLedger)
	if err := dial(addr, clientCfg); err == nil {
		t.Fatal("dial with non-allowlisted (but validly signed) identity unexpectedly succeeded")
	}
	if err := result(); err == nil {
		t.Fatal("server accepted a client identity outside its allowlist")
	}
}

func TestServerConfig_RejectsConnectionWithoutClientCert(t *testing.T) {
	ledger, _, _, _ := setupHandshakeFixture(t)

	serverCfg := ServerConfig(ledger, []string{IdentityGateway})
	addr, result := listenTLS(t, serverCfg)

	// A bare TLS client with no certificate and no server-identity
	// enforcement of its own — this simulates "some other process on the
	// docker network" probing the listener with no credentials at all.
	bareClientCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only: proving the SERVER refuses an uncredentialed peer
	if err := dial(addr, bareClientCfg); err == nil {
		t.Fatal("dial without a client certificate unexpectedly succeeded")
	}
	if err := result(); err == nil {
		t.Fatal("server accepted a connection with no client certificate")
	}
}

func TestClientConfig_RejectsWrongServerIdentity(t *testing.T) {
	_, gateway, fraud, _ := setupHandshakeFixture(t)

	// Server is actually "fraud", but the client expects to be talking to
	// "ledger" — a validly-signed cert presented by the WRONG service.
	serverCfg := ServerConfig(fraud, []string{IdentityGateway})
	addr, result := listenTLS(t, serverCfg)

	clientCfg := ClientConfig(gateway, IdentityLedger)
	if err := dial(addr, clientCfg); err == nil {
		t.Fatal("dial expecting ledger but reaching fraud unexpectedly succeeded")
	}
	_ = result()
}

func TestClientConfig_RejectsCertFromUntrustedCA(t *testing.T) {
	trustedDir := t.TempDir()
	trustedCA := newTestCA(t)
	trustedCA.writeTo(t, trustedDir)
	trustedCA.issue(t, trustedDir, "gateway", IdentityGateway, time.Hour)

	rogueDir := t.TempDir()
	rogueCA := newTestCA(t)
	rogueCA.writeTo(t, rogueDir)
	rogueCA.issue(t, rogueDir, "ledger", IdentityLedger, time.Hour)

	// Server PRESENTS a leaf signed by rogueCA, but its ClientCAs pool
	// trusts trustedCA — so the client's own credential verifies cleanly
	// server-side, isolating the failure to exactly one thing: the
	// client refusing to trust the CA that signed the server's cert.
	serverSrc, err := NewCertSource(
		filepath.Join(rogueDir, "ledger.pem"), filepath.Join(rogueDir, "ledger-key.pem"),
		filepath.Join(trustedDir, "ca.pem"), nil,
	)
	if err != nil {
		t.Fatalf("NewCertSource(rogue-signed server): %v", err)
	}
	t.Cleanup(serverSrc.Stop)
	serverCfg := ServerConfig(serverSrc, []string{IdentityGateway})
	addr, result := listenTLS(t, serverCfg)

	clientSrc, err := NewCertSource(filepath.Join(trustedDir, "gateway.pem"), filepath.Join(trustedDir, "gateway-key.pem"), filepath.Join(trustedDir, "ca.pem"), nil)
	if err != nil {
		t.Fatalf("NewCertSource(trusted client): %v", err)
	}
	t.Cleanup(clientSrc.Stop)

	clientCfg := ClientConfig(clientSrc, IdentityLedger)
	if err := dial(addr, clientCfg); err == nil {
		t.Fatal("dial trusting the wrong CA unexpectedly succeeded")
	}
	_ = result()
}

// TestServerConfig_HotRotatesCAWithoutRebuildingConfig proves the docs/roadmap/archive/49
// TM-13 fix, found live by the T6 rotation drill (scripts/rotation-drill.sh):
// ServerConfig used to snapshot ClientCAs at construction time, so a CA
// rotation was invisible to already-built listeners for the rest of the
// process's life — a client reissued under the new CA was permanently
// rejected until the server itself restarted. This builds the *tls.Config
// exactly ONCE and never reconstructs it, proving the SAME value both (a)
// accepts a client reissued under a newly-rotated CA, and (b) rejects a
// client cert that predates the rotation.
func TestServerConfig_HotRotatesCAWithoutRebuildingConfig(t *testing.T) {
	dir := t.TempDir()
	ca1 := newTestCA(t)
	ca1.writeTo(t, dir)
	ca1.issue(t, dir, "ledger", IdentityLedger, time.Hour)
	ca1.issue(t, dir, "gateway", IdentityGateway, time.Hour)

	const fastPoll = 20 * time.Millisecond
	ledgerSrc, err := newCertSource(filepath.Join(dir, "ledger.pem"), filepath.Join(dir, "ledger-key.pem"), filepath.Join(dir, "ca.pem"), nil, fastPoll)
	if err != nil {
		t.Fatalf("NewCertSource(ledger): %v", err)
	}
	defer ledgerSrc.Stop()
	gatewaySrc, err := newCertSource(filepath.Join(dir, "gateway.pem"), filepath.Join(dir, "gateway-key.pem"), filepath.Join(dir, "ca.pem"), nil, fastPoll)
	if err != nil {
		t.Fatalf("NewCertSource(gateway): %v", err)
	}
	defer gatewaySrc.Stop()

	// Built ONCE, before any rotation, and never reconstructed again.
	serverCfg := ServerConfig(ledgerSrc, []string{IdentityGateway})

	addr, result := listenTLS(t, serverCfg)
	if err := dial(addr, ClientConfig(gatewaySrc, IdentityLedger)); err != nil {
		t.Fatalf("pre-rotation dial failed: %v", err)
	}
	if err := result(); err != nil {
		t.Fatalf("pre-rotation server-side handshake failed: %v", err)
	}

	// A STATIC copy of the pre-rotation gateway cert/key, loaded once and
	// never refreshed — stands in for a leaked or simply un-reissued old
	// credential, the "old cert rejected" half of K9's proof.
	oldCert, err := tls.LoadX509KeyPair(filepath.Join(dir, "gateway.pem"), filepath.Join(dir, "gateway-key.pem"))
	if err != nil {
		t.Fatalf("load pre-rotation gateway cert: %v", err)
	}
	oldClientCfg := &tls.Config{
		MinVersion:           minTLSVersion,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &oldCert, nil },
		InsecureSkipVerify:   true, //nolint:gosec // test-only: identical rationale to ClientConfig above (no DNS SAN to verify)
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifyChainAgainst(cs, ledgerSrc.CAPool(), x509.ExtKeyUsageServerAuth)
		},
	}

	// Rotate: a brand new CA, ledger+gateway both reissued under it, IN
	// PLACE at the same file paths both CertSources are already watching.
	time.Sleep(1100 * time.Millisecond) // clear the mtime-granularity floor
	ca2 := newTestCA(t)
	ca2.writeTo(t, dir)
	ca2.issue(t, dir, "ledger", IdentityLedger, time.Hour)
	ca2.issue(t, dir, "gateway", IdentityGateway, time.Hour)

	// Both CertSources poll independently on their own timers, and each
	// reload() only updates cert+key+CAPool together once ALL THREE of
	// its OWN files read back consistently — but ledgerSrc's CAPool and
	// gatewaySrc's OWN presented leaf are two INDEPENDENT CertSources'
	// state, converging on separate poll cycles. Wait for both cheaply,
	// in-memory: not a real dial loop, which would otherwise open (and,
	// under t.Cleanup, only close at the very end of the test) one TCP
	// listener per retry and made this test flaky under full-suite
	// parallel load.
	newGatewayLeaf := loadTestCert(t, filepath.Join(dir, "gateway.pem"))
	waitUntil := func(desc string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(fastPoll)
		}
		t.Fatalf("%s: condition never became true within the deadline", desc)
	}
	// ledgerSrc's CAPool must trust the new leaf (this is what ServerConfig's
	// VerifyConnection checks against when verifying an incoming client).
	waitUntil("ledgerSrc CAPool trusts the rotated CA", func() bool {
		_, err := newGatewayLeaf.Verify(x509.VerifyOptions{Roots: ledgerSrc.CAPool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
		return err == nil
	})
	// gatewaySrc must actually be PRESENTING the new leaf itself — its own
	// CAPool trusting ca2 is a different, independently-converging fact
	// from whether its cert+key have finished reloading to match.
	waitUntil("gatewaySrc presents the rotated leaf", func() bool {
		presented, err := gatewaySrc.GetClientCertificate(nil)
		if err != nil || len(presented.Certificate) == 0 {
			return false
		}
		return bytes.Equal(presented.Certificate[0], newGatewayLeaf.Raw)
	})

	// Both sources have settled on the new CA — the same serverCfg value,
	// still never rebuilt, must now accept a client reissued under it.
	addr, result = listenTLS(t, serverCfg)
	if err := dial(addr, ClientConfig(gatewaySrc, IdentityLedger)); err != nil {
		t.Fatalf("post-rotation dial with a freshly-reissued client cert failed (ClientCAs frozen at construction time?): %v", err)
	}
	if err := result(); err != nil {
		t.Fatalf("post-rotation server-side handshake failed for a freshly-reissued client: %v", err)
	}

	// ...and the OLD (pre-rotation) client cert must now be rejected,
	// using that exact same serverCfg value.
	addr, result = listenTLS(t, serverCfg)
	if err := dial(addr, oldClientCfg); err == nil {
		t.Fatal("dial with a pre-rotation client cert unexpectedly succeeded after CA rotation")
	}
	if err := result(); err == nil {
		t.Fatal("server accepted a pre-rotation client cert after CA rotation — old cert was not rejected")
	}
}
