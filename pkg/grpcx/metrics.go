package grpcx

// Package-level metric, registered once regardless of how many times
// NewServer is called (docs/plan/43 K5).

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// grpcHandlingDuration observes one duration per unary RPC in
// loggingInterceptor — deliberately not a second, separate interceptor, so
// there is exactly one place computing RPC duration (docs/plan/43 K5: "jangan
// duplikasi observasi dengan stats handler" — the otelgrpc stats handler
// wired in NewServer only contributes TRACE spans here, no MeterProvider is
// configured, so there is no competing metrics source to duplicate against
// regardless). grpc_method and grpc_code are both bounded (finite RPC
// surface across six services; ~17 canonical gRPC status codes) — safe
// cardinality.
var grpcHandlingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "grpc_server_handling_seconds",
	Help:    "gRPC server unary call duration in seconds, by method/code.",
	Buckets: prometheus.DefBuckets,
}, []string{"grpc_method", "grpc_code"})
