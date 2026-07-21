package tlsx

import (
	"context"
	"crypto/tls"
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
	// this particular listener (docs/plan/49 K4: identity allowlist, not
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
