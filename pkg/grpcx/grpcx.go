// Package grpcx provides shared gRPC server and client plumbing.
package grpcx

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const dialTimeout = 5 * time.Second

// requestIDMetadataKey is the gRPC metadata key carrying the HTTP
// X-Request-Id equivalent across service boundaries (docs/plan/36 Task T3).
const requestIDMetadataKey = "x-request-id"

// NewServer creates a gRPC server with recovery, logging, and token auth.
func NewServer(logger *slog.Logger, token string, opts ...grpc.ServerOption) *grpc.Server {
	if logger == nil {
		logger = slog.Default()
	}
	interceptors := grpc.ChainUnaryInterceptor(
		recoveryInterceptor(logger),
		requestIDServerInterceptor(),
		loggingInterceptor(logger),
		authInterceptor(token),
	)
	opts = append([]grpc.ServerOption{interceptors}, opts...)
	server := grpc.NewServer(opts...)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	return server
}

// Dial connects to an internal gRPC service and attaches token auth.
func Dial(ctx context.Context, addr, token string) (*grpc.ClientConn, error) {
	return dial(ctx, addr, token)
}

// DialLazy creates a reconnecting client without requiring the remote service
// to be available during startup. RPC-level deadlines bound each call.
func DialLazy(ctx context.Context, addr, token string) (*grpc.ClientConn, error) {
	base := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true,
		}),
		grpc.WithChainUnaryInterceptor(clientAuthInterceptor(token), requestIDClientInterceptor()),
	}
	return grpc.DialContext(ctx, addr, base...) //nolint:staticcheck // Lazy reconnect behavior is intentional.
}

func dial(ctx context.Context, addr, token string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dialTimeout)
		defer cancel()
	}
	base := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck // Dial must honor the bounded connection timeout in this API.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true,
		}),
		grpc.WithChainUnaryInterceptor(clientAuthInterceptor(token), requestIDClientInterceptor()),
	}
	return grpc.DialContext(ctx, addr, append(base, opts...)...) //nolint:staticcheck // See WithBlock rationale above.
}

func recoveryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("grpc: handler panic", "method", info.FullMethod, "panic", recovered, "stack", string(debug.Stack()))
				resp = nil
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

func loggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		started := time.Now()
		resp, err := handler(ctx, req)
		logger.Info("grpc: request",
			"method", info.FullMethod,
			"request_id", middleware.RequestIDFromCtx(ctx),
			"duration", time.Since(started),
			"code", status.Code(err).String())
		return resp, err
	}
}

// requestIDServerInterceptor extracts the x-request-id metadata set by
// requestIDClientInterceptor (or a caller propagating it manually) into ctx
// under middleware.RequestIDKey — the same key HTTP middleware uses, so
// RequestIDFromCtx works identically on both transports. Runs BEFORE
// loggingInterceptor so every gRPC log line carries the field (docs/plan/36
// Task T3). A caller without an id (e.g. a background job with no request
// context) gets one generated here rather than logging an empty string.
func requestIDServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		id := ""
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if values := md.Get(requestIDMetadataKey); len(values) > 0 {
				id = values[0]
			}
		}
		if id == "" {
			id = uuid.New().String()
		}
		ctx = context.WithValue(ctx, middleware.RequestIDKey, id)
		return handler(ctx, req)
	}
}

// requestIDClientInterceptor mirrors clientAuthInterceptor's metadata-set
// pattern but for the request_id: propagates it onto the outgoing gRPC call
// whenever the caller's ctx carries one, leaving the metadata untouched
// otherwise (background callers with no request ctx get one assigned
// server-side by requestIDServerInterceptor instead).
func requestIDClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if id := middleware.RequestIDFromCtx(ctx); id != "" {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set(requestIDMetadataKey, id)
			ctx = metadata.NewOutgoingContext(ctx, md)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func authInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if token == "" {
			return handler(ctx, req)
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing authorization token")
		}
		values := md.Get("authorization")
		if len(values) != 1 || values[0] != "Bearer "+token {
			return nil, status.Error(codes.Unauthenticated, "invalid authorization token")
		}
		return handler(ctx, req)
	}
}

func clientAuthInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if token != "" {
			md, _ := metadata.FromOutgoingContext(ctx)
			md = md.Copy()
			md.Set("authorization", "Bearer "+token)
			ctx = metadata.NewOutgoingContext(ctx, md)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
