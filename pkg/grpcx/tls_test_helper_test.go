package grpcx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/herdifirdausss/seev/pkg/tlsx"
)

// testMTLSPair builds a self-contained CA plus one "client" and one
// "server" leaf certificate (arbitrary SPIFFE-style identities, since this
// package's own tests only care that mTLS is REQUIRED and enforced, not
// about this repo's real service identity set — that's pkg/tlsx's own
// test responsibility) and returns ready-to-use server/client tls.Config
// pairs, mirroring exactly what cmd/*/main.go builds via
// tlsx.LoadFromDir + tlsx.ServerConfig/ClientConfig.
func testMTLSPair(t *testing.T) (serverTLS, clientTLS *tls.Config) {
	t.Helper()
	const clientIdentity = "spiffe://seev/test-client"
	const serverIdentity = "spiffe://seev/test-server"

	dir := t.TempDir()
	caCert, caKey := issueTestCA(t, dir)
	issueTestLeaf(t, dir, "client", clientIdentity, caCert, caKey)
	issueTestLeaf(t, dir, "server", serverIdentity, caCert, caKey)

	serverSrc, err := tlsx.NewCertSource(filepath.Join(dir, "server.pem"), filepath.Join(dir, "server-key.pem"), filepath.Join(dir, "ca.pem"), nil)
	if err != nil {
		t.Fatalf("load server cert source: %v", err)
	}
	t.Cleanup(serverSrc.Stop)
	clientSrc, err := tlsx.NewCertSource(filepath.Join(dir, "client.pem"), filepath.Join(dir, "client-key.pem"), filepath.Join(dir, "ca.pem"), nil)
	if err != nil {
		t.Fatalf("load client cert source: %v", err)
	}
	t.Cleanup(clientSrc.Stop)

	serverTLS = tlsx.ServerConfig(serverSrc, []string{clientIdentity})
	clientTLS = tlsx.ClientConfig(clientSrc, serverIdentity)
	return serverTLS, clientTLS
}

func issueTestCA(t *testing.T, dir string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          testSerial(t),
		Subject:               pkix.Name{CommonName: "grpcx test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
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
	writeTestPEMFile(t, filepath.Join(dir, "ca.pem"), "CERTIFICATE", der)
	return cert, key
}

func issueTestLeaf(t *testing.T, dir, name, identity string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) {
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
		SerialNumber:          testSerial(t),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		URIs:                  []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	writeTestPEMFile(t, filepath.Join(dir, name+".pem"), "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	writeTestPEMFile(t, filepath.Join(dir, name+"-key.pem"), "EC PRIVATE KEY", keyDER)
}

func writeTestPEMFile(t *testing.T, path, blockType string, der []byte) {
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

func testSerial(t *testing.T) *big.Int {
	t.Helper()
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatalf("random serial: %v", err)
	}
	return serial
}
