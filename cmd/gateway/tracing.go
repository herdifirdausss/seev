package main

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"

	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// setupTracing installs a real OTel TracerProvider only when endpoint is
// non-empty (docs/plan/12 Task T5). The span-creation code in
// internal/ledger/service/handle already calls otel.Tracer(...).Start(...)
// unconditionally — when no provider is installed here, those calls hit the
// SDK's global no-op tracer, which is genuinely zero-cost (no allocation
// beyond what's already inherent to the call, no export, no background
// goroutine). That's the whole reason this is additive wiring, not a
// rewrite of the existing instrumentation: the instrumentation was already
// correct, it just had nothing to report to.
//
// Returns a shutdown func to call during cleanup — always non-nil and
// always safe to call, even when no provider was installed (a no-op then).
func setupTracing(ctx context.Context, endpoint string) (shutdown func(context.Context) error, err error) {
	noop := func(context.Context) error { return nil }
	if endpoint == "" {
		return noop, nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		// Plaintext gRPC — matches the local Jaeger/Tempo dev workflow this
		// is meant for (docs/plan/12 Task T5 example: http://localhost:4317).
		// A production collector reachable only over TLS needs a different
		// endpoint/credentials setup; not needed for this MVP's scope.
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return noop, fmt.Errorf("tracing: create otlp exporter: %w", err)
	}

	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(semconv.ServiceName("seev")),
	)
	if err != nil {
		return noop, fmt.Errorf("tracing: build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp.Shutdown, nil
}
