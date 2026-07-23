# 43 — Track A1: Production-Grade Observability

This execution plan implements Track A1 from [plan 42](../42-long-term-roadmap.md). The trigger is a conscious learning decision after plans 36–41, with request-ID correlation, Prometheus metrics on all six services, optional OpenTelemetry, and operational runbooks already available.

## Scope and locked decisions

- Use only open-source local components: Prometheus, Grafana, Loki, Tempo, and Promtail.
- Run them in a separate `observability` Compose profile; never combine the profile with the full testcontainers suite on the 4 GB development machine.
- Replace Jaeger with Tempo while keeping OTLP gRPC on 4317.
- Centralize tracing setup in `pkg/tracing`; an empty endpoint remains a no-op and exporter failures are non-fatal.
- Add automatic HTTP (`otelhttp`) and gRPC (`otelgrpc`) spans. AMQP tracing and correlation from plan 36 are already complete and must not be changed.
- Add RED metrics with route-pattern labels only. Never label metrics with user IDs, account IDs, idempotency keys, or raw paths.
- Add business gauges for vendor breaker state and stuck payouts.
- Define SLOs for posting availability, webhook-to-post latency, and notification lag. Use multi-window burn-rate alerts, and attach a repository runbook to every alert.
- Parse JSON logs with Promtail and link request IDs and trace IDs between Loki and Tempo without creating high-cardinality labels.
- No database migrations are part of this track.

## T1 — Observability Compose profile

Add pinned Prometheus, Grafana, Loki, Tempo, and Promtail services plus provisioning files under `deploy/observability`. Configure six application scrape targets, local retention, persistent named volumes, three Grafana data sources, and file-based dashboard/alert provisioning. Remove Jaeger from Compose.

**Tests:** Compose config with and without the profile, six Prometheus targets up, three Grafana data sources healthy, and logs queryable in Loki.

Status: complete.

## T2 — Shared tracing and automatic instrumentation

Move duplicated command-level tracing setup into `pkg/tracing.Setup`. Wire all six services with correct service names, add request-ID-aware `WithTracing` middleware after `WithRequestID`, and register `otelgrpc` server/client handlers in `pkg/grpcx`.

**Tests:** in-memory span naming, existing gRPC tests, no-op behavior with no endpoint, and a full trace from gateway through gRPC and AMQP notification consumption.

Status: complete.

## T3 — RED metrics and business gauges

Add HTTP request duration metrics with route patterns and an `unmatched` fallback, gRPC handling duration metrics, breaker-state gauges, and stuck-payout gauges refreshed by the existing resume worker data path. Audit every new label for cardinality.

**Tests:** route-pattern and 404 metrics, breaker transitions, resume-worker gauge updates, and `/metrics` checks on all services.

Status: complete.

## T4 — Dashboards as code

Provision six service dashboards and a money dashboard. Include request rate, errors, latency, runtime health, service-specific metrics, ledger posting and verifier status, outbox lag, stuck payouts, and breaker state. Link panels to operational runbooks.

**Tests:** restart Grafana and confirm all seven dashboards reappear without manual UI configuration and show data during the business journey.

Status: complete.

## T5 — SLO recording rules and alerts

Add Prometheus recording rules for posting availability, webhook latency, and notification lag, with fast and slow burn-rate windows. Add Grafana unified alerts for SLO burn, verifier discrepancies, outbox lag, stuck payouts, open breakers, and webhook latency. Every alert must include a runbook annotation.

**Tests:** `promtool check rules`, plus synthetic alert-fire and alert-resolve checks for a verifier discrepancy and one operational SLO.

Status: complete.

## T6 — Centralized correlated logs

Set JSON logging for the six application services. Configure Promtail Docker discovery and Grafana derived fields for request ID and trace ID. Keep host-run script logs local; they are outside this track.

**Tests:** run the business journey, query one request ID across at least three services in Loki, and follow the link to its Tempo trace.

Status: complete.

## Constraints

Do not reorder ledger posting, expose metrics publicly, change AMQP tracing, add internal imports to `pkg`, introduce high-cardinality labels, or run full observability and testcontainers concurrently. Preserve existing ports and use new ports only for observability services.

## Final gate

The track is complete when the full repository gate passes from a clean volume, all provisioning is reproducible from a clone, the plan index marks 43 done, and plan 42 marks A1 complete.
