package grpcx

import (
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	pingv1 "github.com/herdifirdausss/seev/gen/ping/v1"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type pingServer struct {
	pingv1.UnimplementedPingServiceServer
	panicOnce     atomic.Bool
	lastRequestID atomic.Value
}

func (s *pingServer) Ping(ctx context.Context, _ *pingv1.PingRequest) (*pingv1.PingResponse, error) {
	if s.panicOnce.CompareAndSwap(true, false) {
		panic("boom")
	}
	s.lastRequestID.Store(middleware.RequestIDFromCtx(ctx))
	return &pingv1.PingResponse{}, nil
}

func startTestServer(t *testing.T, token string, implementation pingv1.PingServiceServer) (*grpc.ClientConn, func()) {
	t.Helper()
	serverTLS, clientTLS := testMTLSPair(t)
	listener := bufconn.Listen(1024 * 1024)
	server, err := NewServer(slog.Default(), token, serverTLS)
	require.NoError(t, err)
	pingv1.RegisterPingServiceServer(server, implementation)
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	conn, err := dial(ctx, "bufnet", token, clientTLS, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	cancel()
	require.NoError(t, err)
	return conn, func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
}

func TestTokenAuthAndHealth(t *testing.T) {
	conn, cleanup := startTestServer(t, "secret", &pingServer{})
	defer cleanup()

	_, err := pingv1.NewPingServiceClient(conn).Ping(context.Background(), &pingv1.PingRequest{})
	require.NoError(t, err)
	response, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, response.Status)
}

func TestWrongTokenIsRejected(t *testing.T) {
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
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

// TestNewServer_EmptyTokenFailsFast proves docs/roadmap/archive/49 K5: a gRPC server
// must never boot able to accept every call unauthenticated, which is what
// happened before this task when INTERNAL_GRPC_TOKEN was empty.
func TestNewServer_EmptyTokenFailsFast(t *testing.T) {
	serverTLS, _ := testMTLSPair(t)
	_, err := NewServer(slog.Default(), "", serverTLS)
	require.Error(t, err)
}

// TestNewServer_NilTLSConfigFailsFast proves the mTLS requirement itself
// can't be silently skipped by omission either.
func TestNewServer_NilTLSConfigFailsFast(t *testing.T) {
	_, err := NewServer(slog.Default(), "token", nil)
	require.Error(t, err)
}

// TestRequestIDPropagatesClientToServer proves docs/roadmap/archive/36 Task T3: a
// request_id present on the client's ctx is injected as x-request-id
// metadata by requestIDClientInterceptor and extracted back into the
// server-side ctx by requestIDServerInterceptor under the same
// middleware.RequestIDKey used by HTTP handlers.
func TestRequestIDPropagatesClientToServer(t *testing.T) {
	implementation := &pingServer{}
	conn, cleanup := startTestServer(t, "irrelevant-token", implementation)
	defer cleanup()

	ctx := context.WithValue(context.Background(), middleware.RequestIDKey, "trace-abc-123")
	_, err := pingv1.NewPingServiceClient(conn).Ping(ctx, &pingv1.PingRequest{})
	require.NoError(t, err)
	assert.Equal(t, "trace-abc-123", implementation.lastRequestID.Load())
}

// TestRequestIDGeneratedWhenAbsent proves a caller with no request_id in ctx
// (e.g. a background worker) still gets one assigned server-side, so gRPC
// logs never show an empty request_id field.
func TestRequestIDGeneratedWhenAbsent(t *testing.T) {
	implementation := &pingServer{}
	conn, cleanup := startTestServer(t, "irrelevant-token", implementation)
	defer cleanup()

	_, err := pingv1.NewPingServiceClient(conn).Ping(context.Background(), &pingv1.PingRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, implementation.lastRequestID.Load())
}

func TestPanicReturnsInternalAndServerStaysAlive(t *testing.T) {
	implementation := &pingServer{}
	implementation.panicOnce.Store(true)
	conn, cleanup := startTestServer(t, "irrelevant-token", implementation)
	defer cleanup()
	client := pingv1.NewPingServiceClient(conn)

	_, err := client.Ping(context.Background(), &pingv1.PingRequest{})
	require.Equal(t, codes.Internal, status.Code(err))
	_, err = client.Ping(context.Background(), &pingv1.PingRequest{})
	require.NoError(t, err)
}
