package grpcx

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	pingv1 "github.com/herdifirdausss/seev/gen/ping/v1"
)

func grpcObservationCount(t *testing.T, method, code string) float64 {
	t.Helper()
	hist := grpcHandlingDuration.WithLabelValues(method, code).(prometheus.Metric)
	var m dto.Metric
	require.NoError(t, hist.Write(&m))
	return float64(m.GetHistogram().GetSampleCount())
}

// TestLoggingInterceptor_ObservesSuccessAndCode proves docs/plan/43 K5's
// grpc_server_handling_seconds is recorded once per unary call, labeled by
// the bounded {grpc_method, grpc_code} pair — not any per-caller value.
func TestLoggingInterceptor_ObservesSuccessAndCode(t *testing.T) {
	const method = "/seev.ping.v1.PingService/Ping"
	before := grpcObservationCount(t, method, "OK")

	conn, cleanup := startTestServer(t, "secret", &pingServer{})
	defer cleanup()
	_, err := pingv1.NewPingServiceClient(conn).Ping(context.Background(), &pingv1.PingRequest{})
	require.NoError(t, err)

	assert.Equal(t, before+1, grpcObservationCount(t, method, "OK"))
}

// TestLoggingInterceptor_ObservesNonOKCode proves a normal (non-panic)
// error return — authInterceptor rejecting a bad token — is observed with
// its real canonical code, not silently dropped. (A genuine panic is a
// separate, pre-existing case: recoveryInterceptor sits OUTERMOST in the
// chain — before loggingInterceptor — so a panic unwinds past
// loggingInterceptor's own post-handler code entirely, the same way it
// already skipped that interceptor's log line before this task; that gap
// is not something docs/plan/43 K5 asks this task to close.)
func TestLoggingInterceptor_ObservesNonOKCode(t *testing.T) {
	const method = "/seev.ping.v1.PingService/Ping"
	before := grpcObservationCount(t, method, "Unauthenticated")

	serverTLS, clientTLS := testMTLSPair(t)
	listener := bufconn.Listen(1024 * 1024)
	server, err := NewServer(slog.Default(), "correct", serverTLS)
	require.NoError(t, err)
	pingv1.RegisterPingServiceServer(server, &pingServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dial(ctx, "bufnet", "wrong", clientTLS, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	require.NoError(t, err)
	defer conn.Close()

	_, err = pingv1.NewPingServiceClient(conn).Ping(context.Background(), &pingv1.PingRequest{})
	require.Error(t, err)

	assert.Equal(t, before+1, grpcObservationCount(t, method, "Unauthenticated"))
}
