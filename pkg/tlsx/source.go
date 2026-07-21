package tlsx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// defaultPollInterval bounds how stale a hot-reloaded cert can be after
// cmd/certgen rewrites it on disk (docs/plan/49 K2/K9 — rotation is
// "regenerate + hot-reload", not a process restart).
const defaultPollInterval = 5 * time.Second

// CertSource loads one service's own leaf certificate/key and the CA pool
// used to verify peers, and keeps both fresh by polling file mtimes —
// deliberately not fsnotify (docs/plan/49 K2: no new dependency for this).
// A single CertSource is shared by every tls.Config a process builds
// (pkg/grpcx AND every internal HTTP server/client), so a rotation is
// picked up everywhere in the process at once.
type CertSource struct {
	certFile, keyFile, caFile string
	logger                    *slog.Logger
	pollInterval              time.Duration

	mu       sync.RWMutex
	cert     *tls.Certificate
	caPool   *x509.CertPool
	identity string // this process's own identity, parsed from cert once at load

	certMod, keyMod, caMod time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewCertSource loads certFile/keyFile/caFile once (failing loudly if any
// is missing or invalid — a service must never boot believing it's
// running mTLS when it isn't) and starts the background reload loop.
func NewCertSource(certFile, keyFile, caFile string, logger *slog.Logger) (*CertSource, error) {
	return newCertSource(certFile, keyFile, caFile, logger, defaultPollInterval)
}

// LoadFromDir applies this repo's one directory convention (cmd/certgen's
// output layout, docs/plan/49 K3): <dir>/<service>.pem,
// <dir>/<service>-key.pem, and a CA shared by every service at
// <dir>/ca.pem. Every process loads exactly its own identity this way —
// there is deliberately no per-file env var, so compose/scripts/lib.sh
// only ever need to mount one directory.
func LoadFromDir(dir, service string, logger *slog.Logger) (*CertSource, error) {
	return NewCertSource(
		filepath.Join(dir, service+".pem"),
		filepath.Join(dir, service+"-key.pem"),
		filepath.Join(dir, "ca.pem"),
		logger,
	)
}

// newCertSource is NewCertSource's real constructor, parameterized on
// poll interval — unexported, for this package's own tests to get a fast
// deterministic reload cadence without mutating pollInterval after the
// background goroutine (started here) has already begun reading it.
func newCertSource(certFile, keyFile, caFile string, logger *slog.Logger, pollInterval time.Duration) (*CertSource, error) {
	if logger == nil {
		logger = slog.Default()
	}
	s := &CertSource{
		certFile:     certFile,
		keyFile:      keyFile,
		caFile:       caFile,
		logger:       logger,
		pollInterval: pollInterval,
	}
	if err := s.reload(); err != nil {
		return nil, fmt.Errorf("tlsx: initial cert load: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go s.pollLoop(ctx)
	return s, nil
}

// Stop halts the background reload loop. Call on shutdown.
func (s *CertSource) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// Identity returns this process's own SPIFFE-style URI SAN, parsed from
// the leaf certificate at last successful load.
func (s *CertSource) Identity() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.identity
}

// GetCertificate implements the server-side tls.Config.GetCertificate
// hook — called fresh on every handshake, so a reload is picked up by the
// very next connection without restarting the listener.
func (s *CertSource) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cert, nil
}

// GetClientCertificate implements the client-side
// tls.Config.GetClientCertificate hook — same hot-reload guarantee as
// GetCertificate, for the dial path.
func (s *CertSource) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cert, nil
}

// CAPool returns the current CA pool used to verify peers — also
// hot-reloaded, so a CA rotation (docs/plan/49 T6 drill) doesn't need a
// tls.Config rebuild either, only a fresh handshake.
func (s *CertSource) CAPool() *x509.CertPool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.caPool
}

func (s *CertSource) pollLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reloadIfChanged()
		}
	}
}

// reloadIfChanged only re-parses when at least one file's mtime moved —
// cmd/certgen rewrites cert+key+ca together, but a stat-based check keeps
// the common case (nothing changed) cheap and avoids logging noise on
// every single poll tick.
func (s *CertSource) reloadIfChanged() {
	certMod, keyMod, caMod, err := s.statAll()
	if err != nil {
		s.logger.Warn("tlsx: stat cert files for reload check failed, keeping current cert", "error", err)
		return
	}
	s.mu.RLock()
	changed := !certMod.Equal(s.certMod) || !keyMod.Equal(s.keyMod) || !caMod.Equal(s.caMod)
	s.mu.RUnlock()
	if !changed {
		return
	}
	if err := s.reload(); err != nil {
		s.logger.Error("tlsx: cert reload failed, keeping previous cert in use", "error", err)
		return
	}
	s.logger.Info("tlsx: certificate reloaded", "identity", s.Identity())
}

func (s *CertSource) statAll() (certMod, keyMod, caMod time.Time, err error) {
	certInfo, err := os.Stat(s.certFile)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("stat cert: %w", err)
	}
	keyInfo, err := os.Stat(s.keyFile)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("stat key: %w", err)
	}
	caInfo, err := os.Stat(s.caFile)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("stat ca: %w", err)
	}
	return certInfo.ModTime(), keyInfo.ModTime(), caInfo.ModTime(), nil
}

func (s *CertSource) reload() error {
	certMod, keyMod, caMod, err := s.statAll()
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
	if err != nil {
		return fmt.Errorf("load keypair: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse leaf certificate: %w", err)
	}
	id, err := identityOf(leaf)
	if err != nil {
		return fmt.Errorf("leaf certificate identity: %w", err)
	}
	caPEM, err := os.ReadFile(s.caFile)
	if err != nil {
		return fmt.Errorf("read ca file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("ca file %q contains no usable certificates", s.caFile)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cert = &cert
	s.caPool = pool
	s.identity = id
	s.certMod, s.keyMod, s.caMod = certMod, keyMod, caMod
	return nil
}

// identityOf extracts the single SPIFFE-style URI SAN a cmd/certgen leaf
// carries. A cert with zero or more than one URI SAN is rejected outright
// — this repo's identity model is exactly one identity per certificate,
// never a set (docs/plan/49 K3/K4).
func identityOf(cert *x509.Certificate) (string, error) {
	if len(cert.URIs) != 1 {
		return "", fmt.Errorf("expected exactly 1 URI SAN, got %d", len(cert.URIs))
	}
	return cert.URIs[0].String(), nil
}
