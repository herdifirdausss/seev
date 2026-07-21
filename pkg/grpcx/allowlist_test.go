package grpcx

import (
	"context"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	pingv1 "github.com/herdifirdausss/seev/gen/ping/v1"
	"github.com/herdifirdausss/seev/pkg/tlsx"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// TestServerRejectsClientOutsideAllowlist proves docs/plan/49 K4 end-to-end
// through the real grpcx.NewServer/dial code path every cmd/*/main.go uses
// (not just pkg/tlsx's own lower-level TLS test): a client certificate that
// is validly signed by the trusted CA, but whose SPIFFE identity is NOT in
// the server's allowlist, must be rejected — exactly the scenario ledger's
// allowlist is meant to stop (e.g. a payout-service credential dialing
// fraud-service, which is outside fraud's {ledger,payin,payout} allowlist).
func TestServerRejectsClientOutsideAllowlist(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := issueTestCA(t, dir)
	const serverIdentity = "spiffe://seev/test-server"
	const allowedIdentity = "spiffe://seev/test-allowed-client"
	const outsiderIdentity = "spiffe://seev/test-outsider"
	issueTestLeaf(t, dir, "server", serverIdentity, caCert, caKey)
	issueTestLeaf(t, dir, "outsider", outsiderIdentity, caCert, caKey)

	serverSrc, err := tlsx.NewCertSource(filepath.Join(dir, "server.pem"), filepath.Join(dir, "server-key.pem"), filepath.Join(dir, "ca.pem"), nil)
	require.NoError(t, err)
	t.Cleanup(serverSrc.Stop)
	outsiderSrc, err := tlsx.NewCertSource(filepath.Join(dir, "outsider.pem"), filepath.Join(dir, "outsider-key.pem"), filepath.Join(dir, "ca.pem"), nil)
	require.NoError(t, err)
	t.Cleanup(outsiderSrc.Stop)

	// The allowlist deliberately does NOT include outsiderIdentity — this
	// is the real per-hop matrix (K4) enforced through the exact
	// NewServer/ClientConfig pair every service wires up.
	serverTLS := tlsx.ServerConfig(serverSrc, []string{allowedIdentity})
	outsiderClientTLS := tlsx.ClientConfig(outsiderSrc, serverIdentity)

	listener := bufconn.Listen(1024 * 1024)
	server, err := NewServer(slog.Default(), "token", serverTLS)
	require.NoError(t, err)
	pingv1.RegisterPingServiceServer(server, &pingServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	defer listener.Close()

	// dial() uses grpc.WithBlock(), so a handshake the server keeps
	// rejecting never completes — the rejection surfaces here as the
	// bounded dial itself failing, not as a later RPC error.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = dial(ctx, "bufnet", "token", outsiderClientTLS, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	require.Error(t, err, "a certificate outside the server's identity allowlist must be rejected")
}
