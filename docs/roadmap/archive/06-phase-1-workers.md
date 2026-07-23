# 06 — Phase 1c: Outbox Relay Worker and Verification Jobs

Prerequisite: 04 and 05 are complete. The workers run as goroutines inside the
`cmd/server` process (decision D9), scheduled by `pkg/scheduler` with a Redis
distributed lock so the design is safe for future multi-replica deployments.

## Task 1c.1 — Outbox relay (`internal/ledger/worker/outbox_relay.go`)

Polling loop, with a one-second default interval configurable through
`OUTBOX_POLL_INTERVAL`:

1. **Claim a batch** in a short database transaction:

```sql
UPDATE outbox_events SET status='processing', last_attempted_at=now()
WHERE id IN (
  SELECT id FROM outbox_events WHERE status='pending'
  ORDER BY created_at ASC LIMIT 100
  FOR UPDATE SKIP LOCKED
) RETURNING id, aggregate_type, aggregate_id, event_type, payload, retry_count;
```

2. **Publish** each event through the `pkg/messaging` publisher. Use the
   `ledger.events` topic exchange, `event_type` as the routing key, persistent
   messages, and `x-event-id` as the outbox ID. Consumers deduplicate by this
   ID; delivery is **at least once**. Document that guarantee in the code.
3. **Record each result:**
   - success →
     `UPDATE ... SET status='published', published_at=now() WHERE id=$1`
   - failure →
     `UPDATE ... SET status='failed', retry_count=retry_count+1, last_error=$2 WHERE id=$1`.
     The database trigger changes the row to `dead` when retries are exhausted.
4. **Retry pass** every 30 seconds: claim `status='failed' AND retry_count < max_retries` using a similar query. The `idx_outbox_retry` index already supports it.
5. **Stuck reaper** every five minutes: rows with
   `status='processing' AND last_attempted_at < now() - INTERVAL '10 minutes'`
   are returned to `failed` with `retry_count+1`, covering a worker crash before
   the result was recorded.

Requirements:

- Every loop honors `ctx.Done()`. `Stop()` waits for the current batch to
  finish and shuts down gracefully with the server.
- Acquire a `pkg/scheduler` Redis lock per loop name. If two replicas run in
  the future, only one may poll each loop.
- Metrics: `outbox_published_total`, `outbox_publish_failures_total`, an
  `outbox_pending` gauge refreshed every 15 seconds, and `outbox_dead_total`.
  Log a warning whenever an event becomes `dead`.

Tests:

- Unit tests with a mocked publisher: success, retryable failure, and permanent
  failure leading to `dead` (verify with sqlmock or a test container).
- Integration test with PostgreSQL and RabbitMQ test containers: post a
  transaction, observe the event in the queue, interrupt consumption between
  claim and mark to simulate a crash, run the reaper, and confirm that the
  event is eventually delivered. A duplicate is acceptable; a lost event is
  not.

## Task 1c.2 — Ledger-integrity verifier (`internal/ledger/worker/verifier.go`)

Schedule these jobs through `pkg/scheduler`:

1. **Trial balance per transaction** — every hour run
   `SELECT * FROM fn_verify_ledger_balance(now()-'2 hours'::interval, now())`.
   For every returned row, log an error and increment
   `ledger_verification_discrepancies_total{check="trial_balance"}`.
2. **Balance-projection audit** — every day at 02:00 WIB run
   `SELECT * FROM v_account_balance_audit WHERE is_consistent = false`.
   Apply the same error log and increment
   `ledger_verification_discrepancies_total{check="projection"}`.
3. **Outbox lag** — every five minutes run
   `SELECT count(*), min(created_at) FROM outbox_events WHERE status='pending'`.
   If the oldest event is more than five minutes old, log a warning and emit a
   metric.

The job must not repair anything automatically; it only detects and reports
problems. Automatic repair is a human decision for the Phase 2 reconciliation
work.

## Task 1c.3 — Lifecycle wiring

- `ledger.Module.StartWorkers(ctx)` starts the relay and verifier. Call it from
  `cmd/server/main.go` after the server is ready.
- In the `srv.Start` cleanup callback, stop workers first, then close RabbitMQ,
  Redis, and PostgreSQL.
- Register `OUTBOX_POLL_INTERVAL`, `OUTBOX_BATCH_SIZE`, and `WORKER_ENABLED` in
  `internal/config` and `.env.example`. The default for `WORKER_ENABLED` is
  true; set it to false when running the server without workers for debugging.

## Definition of done for 06

- [ ] End-to-end demo: `docker compose up` → transfer through the API →
      `rabbitmqadmin get queue=...` shows the event was received.
- [ ] Kill the server process while an outbox backlog exists, restart it, and
      confirm that the entire backlog is delivered (at-least-once evidence).
- [ ] After the full integration suite, `fn_verify_ledger_balance` and the
      audit view are clean and verifier logs contain no errors.
- [ ] `make lint` and `make test` pass.
