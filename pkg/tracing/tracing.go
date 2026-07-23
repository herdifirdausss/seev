// Package tracing installs the OpenTelemetry TracerProvider shared by all
// six services (docs/roadmap/archive/43 Task T2, decision K3) — a single implementation
// replacing what used to be a duplicated setupTracing in cmd/gateway and
// cmd/ledger-service.
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// Config controls OpenTelemetry trace export (docs/roadmap/archive/43 K3).
type Config struct {
	// ServiceName identifies the resource in trace backends — one of
	// gateway, auth-service, ledger-service, payin-service, payout-service,
	// fraud-service. Each cmd/* main passes its own literal name.
	ServiceName string
	// Endpoint is the OTLP gRPC collector address (e.g. "tempo:4317").
	// Empty installs no provider at all — every otel.Tracer(...).Start
	// call already in the codebase then runs against the SDK's global
	// no-op tracer, which is genuinely zero-cost.
	Endpoint string
	// SampleRatio is the fraction of traces sampled, applied via
	// ParentBased(TraceIDRatioBased(...)) — a sampled parent always keeps
	// its children sampled, an unsampled parent always drops them. Must be
	// in [0, 1].
	SampleRatio float64
	// Insecure selects a plaintext (non-TLS) OTLP gRPC connection — true
	// for every environment this repo targets (a local Tempo on the
	// private Compose network, docs/roadmap/archive/43 K1/K2).
	Insecure bool
}

// Setup installs a real OTel TracerProvider only when cfg.Endpoint is
// non-empty. Returns a shutdown func to call during cleanup — always
// non-nil and always safe to call, even when no provider was installed (a
// no-op then). A setup failure is returned to the caller, which by
// convention (see every cmd/*/main.go call site) treats it as non-fatal:
// tracing is pure observability, never load-bearing for moving money.
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	noop := func(context.Context) error { return nil }
	if cfg.Endpoint == "" {
		return noop, nil
	}
	if cfg.SampleRatio < 0 || cfg.SampleRatio > 1 {
		return noop, fmt.Errorf("tracing: sample ratio must be in [0,1], got %v", cfg.SampleRatio)
	}

	exporterOpts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		exporterOpts = append(exporterOpts, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, exporterOpts...)
	if err != nil {
		return noop, fmt.Errorf("tracing: create otlp exporter: %w", err)
	}

	resource, err := sdkresource.New(ctx, sdkresource.WithAttributes(semconv.ServiceName(cfg.ServiceName)))
	if err != nil {
		return noop, fmt.Errorf("tracing: build resource: %w", err)
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return provider.Shutdown, nil
}
