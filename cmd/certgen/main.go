// certgen is this repo's mini-CA (docs/plan/49 K3): it issues the CA and
// per-service leaf certificates that pkg/tlsx loads for mutual TLS between
// services. Identity is a SPIFFE-style URI SAN ("spiffe://seev/<service>"),
// never a Common Name. This is a development/local tool, not a production
// CA — no revocation, no HSM-backed key, short TTLs by design so operators
// exercise rotation routinely instead of trusting a cert for months.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/herdifirdausss/seev/pkg/tlsx"
)

// caTTL/leafTTL match docs/plan/49 K3 exactly: short leaf lifetime so
// rotation is a routine, well-exercised operation rather than a rare
// break-glass procedure.
const (
	caTTL   = 30 * 24 * time.Hour
	leafTTL = 72 * time.Hour
)

// knownServices maps certgen's --service flag values to the SPIFFE
// identity pkg/tlsx expects — the closed set from pkg/tlsx.Identity*
// constants (docs/plan/49 K3/K4). Adding a service here is the ONE place
// certgen needs to change; pkg/tlsx's allowlists are a separate, per-
// listener decision (K4) that a new identity existing here does not by
// itself grant any access to.
var knownServices = map[string]string{
	"gateway":      tlsx.IdentityGateway,
	"auth":         tlsx.IdentityAuth,
	"ledger":       tlsx.IdentityLedger,
	"payin":        tlsx.IdentityPayin,
	"payout":       tlsx.IdentityPayout,
	"fraud":        tlsx.IdentityFraud,
	"admin-bff":    tlsx.IdentityAdminBFF,
	"assurance":    tlsx.IdentityAssurance,
	"dev-operator": tlsx.IdentityDevOperator,
	"prometheus":   tlsx.IdentityPrometheus,
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init-ca":
		err = cmdInitCA(os.Args[2:])
	case "issue":
		err = cmdIssue(os.Args[2:])
	case "rotate":
		err = cmdRotate(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "certgen:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  certgen init-ca --out <dir>
  certgen issue --service <name> --out <dir> [--force]
  certgen rotate --out <dir>

Known --service values:`)
	for name := range knownServices {
		fmt.Fprintln(os.Stderr, "  -", name)
	}
}

func cmdInitCA(args []string) error {
	fs := flag.NewFlagSet("init-ca", flag.ExitOnError)
	out := fs.String("out", "deploy/certs", "output directory")
	force := fs.Bool("force", false, "overwrite an existing CA")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*force {
		if _, err := os.Stat(filepath.Join(*out, "ca.pem")); err == nil {
			fmt.Println("certgen: CA already exists, skipping (use --force to regenerate)")
			return nil
		}
	}
	return generateCA(*out)
}

func cmdIssue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ExitOnError)
	service := fs.String("service", "", "service identity to issue a leaf certificate for")
	out := fs.String("out", "deploy/certs", "output directory (must already contain ca.pem/ca-key.pem)")
	force := fs.Bool("force", false, "reissue even if a non-expiring leaf already exists")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *service == "" {
		return errors.New("--service is required")
	}
	identity, ok := knownServices[*service]
	if !ok {
		return fmt.Errorf("unknown --service %q (see certgen usage for the known list)", *service)
	}
	if !*force && leafFresh(*out, *service) {
		fmt.Printf("certgen: %s leaf still fresh, skipping (use --force to reissue)\n", *service)
		return nil
	}
	ca, caKey, err := loadCA(*out)
	if err != nil {
		return fmt.Errorf("load CA (run init-ca first): %w", err)
	}
	return issueLeaf(*out, *service, identity, ca, caKey)
}

// cmdRotate is the T6 drill operation, not part of routine `make certs`:
// it regenerates the CA itself and reissues every known-service leaf
// against the new CA, so certificates signed by the OLD CA stop verifying
// the moment pkg/tlsx's poll-reload picks up the new ca.pem (docs/plan/49
// K9 — this is what "cert lama ditolak setelah rotate" actually means).
func cmdRotate(args []string) error {
	fs := flag.NewFlagSet("rotate", flag.ExitOnError)
	out := fs.String("out", "deploy/certs", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := generateCA(*out); err != nil {
		return fmt.Errorf("regenerate CA: %w", err)
	}
	ca, caKey, err := loadCA(*out)
	if err != nil {
		return fmt.Errorf("reload freshly-rotated CA: %w", err)
	}
	for service, identity := range knownServices {
		if err := issueLeaf(*out, service, identity, ca, caKey); err != nil {
			return fmt.Errorf("reissue %s: %w", service, err)
		}
	}
	fmt.Println("certgen: CA rotated, all known-service leaves reissued")
	return nil
}

func generateCA(out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "seev dev CA"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(caTTL),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}
	if err := writePEM(filepath.Join(out, "ca.pem"), "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	if err := writePEM(filepath.Join(out, "ca-key.pem"), "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return err
	}
	fmt.Printf("certgen: CA written to %s (expires %s)\n", out, template.NotAfter.Format(time.RFC3339))
	return nil
}

func loadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		return nil, nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("ca.pem contains no PEM block")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca.pem: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("ca-key.pem contains no PEM block")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca-key.pem: %w", err)
	}
	return cert, key, nil
}

func issueLeaf(out, service, identity string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) error {
	uri, err := url.Parse(identity)
	if err != nil {
		return fmt.Errorf("parse identity %q: %w", identity, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate %s key: %w", service, err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: service},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(leafTTL),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		URIs:                  []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create %s certificate: %w", service, err)
	}
	if err := writePEM(filepath.Join(out, service+".pem"), "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal %s key: %w", service, err)
	}
	if err := writePEM(filepath.Join(out, service+"-key.pem"), "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return err
	}
	fmt.Printf("certgen: issued %s (%s), expires %s\n", service, identity, template.NotAfter.Format(time.RFC3339))
	return nil
}

// leafFresh reports whether a service's existing leaf is both present and
// not within its final quarter of validity — `make certs`'s idempotent
// "regenerate bila absen/kedaluwarsa" behavior (docs/plan/49 K3), so a
// routine `make certs` run never thrashes certs that are still good.
func leafFresh(dir, service string) bool {
	certPEM, err := os.ReadFile(filepath.Join(dir, service+".pem"))
	if err != nil {
		return false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	remaining := time.Until(cert.NotAfter)
	return remaining > leafTTL/4
}

func writePEM(path, blockType string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial, nil
}
