package tlsx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCA is a minimal, test-local mini-CA — deliberately independent of
// cmd/certgen (which this package must not import, and which exercises
// its own issuance logic in its own tests) so pkg/tlsx's tests only ever
// depend on stdlib crypto/x509.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          randomSerialForTest(t),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) writeTo(t *testing.T, dir string) {
	t.Helper()
	writeTestPEM(t, filepath.Join(dir, "ca.pem"), "CERTIFICATE", ca.cert.Raw)
}

// issue writes <name>.pem/<name>-key.pem into dir, carrying identity as
// the certificate's sole URI SAN — identical shape to what cmd/certgen
// produces, so pkg/tlsx's tests exercise the real consumer contract.
func (ca *testCA) issue(t *testing.T, dir, name, identity string, ttl time.Duration) {
	t.Helper()
	uri, err := url.Parse(identity)
	if err != nil {
		t.Fatalf("parse identity: %v", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          randomSerialForTest(t),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(ttl),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		URIs:                  []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	writeTestPEM(t, filepath.Join(dir, name+".pem"), "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	writeTestPEM(t, filepath.Join(dir, name+"-key.pem"), "EC PRIVATE KEY", keyDER)
}

func writeTestPEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

// loadTestCert reads and parses a single PEM-encoded certificate from
// path — used where a test needs the parsed *x509.Certificate itself
// (e.g. to Verify it against a CertSource's live CAPool), not just a
// tls.Certificate for a handshake.
func loadTestCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatalf("%s contains no PEM block", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cert
}

func randomSerialForTest(t *testing.T) *big.Int {
	t.Helper()
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatalf("random serial: %v", err)
	}
	return serial
}
