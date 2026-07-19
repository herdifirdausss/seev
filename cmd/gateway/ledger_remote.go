package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/herdifirdausss/seev/pkg/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func newLedgerProxy(rawURL string, log *slog.Logger) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid LEDGER_USER_API_URL %q", rawURL)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	// [docs/plan/43 Task T6] Without this, the proxy's outbound request to
	// ledger-service carries none of gateway's own span context (a raw
	// httputil.ReverseProxy only forwards whatever headers the ORIGINAL
	// client request already had — it never injects a traceparent
	// reflecting gateway's own server-side span, which lives only in the
	// Go context, not on the incoming request's headers). Found live:
	// tracing a real public transfer_p2p request end to end showed
	// ledger-service and fraud-service sharing one trace_id while
	// gateway-service had a DIFFERENT one — ledger's ParentBased sampler
	// saw no valid parent and started a brand-new root trace instead of
	// continuing gateway's. otelhttp.NewTransport wraps the outbound
	// RoundTrip to inject a correct traceparent header from the request's
	// current span before it leaves gateway.
	proxy.Transport = otelhttp.NewTransport(http.DefaultTransport)
	// Belt-and-braces on top of WithRequestID already setting r.Header
	// (docs/plan/36 Task T2): explicitly re-assert X-Request-Id from ctx on
	// the outgoing request so it survives even if middleware ordering
	// changes later. Wraps the existing Director (host/path rewriting)
	// rather than switching to Rewrite, which would silently disable it.
	defaultDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		defaultDirector(req)
		if id := middleware.RequestIDFromCtx(req.Context()); id != "" {
			req.Header.Set("X-Request-Id", id)
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Error("ledger proxy unavailable", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "ledger service unavailable"})
	}
	return proxy, nil
}

func ledgerReady(client healthpb.HealthClient) func(context.Context) error {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
		if err != nil {
			return err
		}
		if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			return fmt.Errorf("status %s", response.GetStatus())
		}
		return nil
	}
}
