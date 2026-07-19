# 06 — Phase 1c: Outbox Relay Worker + Job Verifikasi

Prasyarat: 04 & 05 selesai. Worker berjalan sebagai goroutine di dalam proses `cmd/server` (keputusan D9), dipicu `pkg/scheduler` dengan distributed lock Redis (aman untuk multi-replica di masa depan).

## Task 1c.1 — Outbox relay (`internal/ledger/worker/outbox_relay.go`)

Loop polling (interval 1s, configurable via env `OUTBOX_POLL_INTERVAL`):

1. **Claim batch** (transaksi DB singkat):
```sql
UPDATE outbox_events SET status='processing', last_attempted_at=now()
WHERE id IN (
  SELECT id FROM outbox_events WHERE status='pending'
  ORDER BY created_at ASC LIMIT 100
  FOR UPDATE SKIP LOCKED
) RETURNING id, aggregate_type, aggregate_id, event_type, payload, retry_count;
```
2. **Publish** tiap event ke RabbitMQ via `pkg/messaging` publisher — exchange topic `ledger.events`, routing key = `event_type` (mis. `ledger.transaction.posted`), message persistent, header `x-event-id` = outbox id (konsumen dedup pakai ini; delivery semantics = **at-least-once**, dokumentasikan di komentar).
3. **Mark hasil** per event:
   - sukses → `UPDATE ... SET status='published', published_at=now() WHERE id=$1`
   - gagal → `UPDATE ... SET status='failed', retry_count=retry_count+1, last_error=$2 WHERE id=$1` (trigger DB otomatis menjadikan `dead` saat retry habis).
4. **Retry pass** (tiap 30s): claim `status='failed' AND retry_count < max_retries` dengan query serupa (index `idx_outbox_retry` sudah ada).
5. **Stuck reaper** (tiap 5m): `status='processing' AND last_attempted_at < now() - INTERVAL '10 minutes'` → kembalikan ke `failed` + `retry_count+1` (worker crash sebelum mark).

Ketentuan:
- Semua loop menghormati `ctx.Done()`; `Stop()` menunggu batch berjalan selesai (graceful, sinkron dengan shutdown server).
- Distributed lock per loop-name via `pkg/scheduler` Redis lock, supaya kalau nanti ada 2 replica hanya satu yang polling.
- Metrics: `outbox_published_total`, `outbox_publish_failures_total`, gauge `outbox_pending` (query count tiap 15s), `outbox_dead_total`. Log WARN setiap event masuk `dead`.

Test:
- Unit: mock publisher — sukses, gagal→retry, gagal permanen→dead (verifikasi lewat status di sqlmock atau testcontainer).
- Integration (testcontainers postgres + rabbitmq): post transaksi via service → event sampai di queue; matikan konsumsi di tengah (simulasi crash antara claim dan mark) → jalankan reaper → event tetap terkirim (duplikat boleh, hilang tidak).

## Task 1c.2 — Job verifikasi integritas (`internal/ledger/worker/verifier.go`)

Dijadwalkan via `pkg/scheduler`:

1. **Trial balance per transaksi** — tiap jam: `SELECT * FROM fn_verify_ledger_balance(now()-'2 hours'::interval, now())`. Baris > 0 → log ERROR per baris + increment `ledger_verification_discrepancies_total{check="trial_balance"}`.
2. **Balance projection audit** — tiap hari 02:00 WIB: `SELECT * FROM v_account_balance_audit WHERE is_consistent = false`. Sama: log ERROR + metric `{check="projection"}`.
3. **Outbox lag** — tiap 5 menit: `SELECT count(*), min(created_at) FROM outbox_events WHERE status='pending'`; kalau event tertua > 5 menit → log WARN + metric.

Job TIDAK memperbaiki apa pun otomatis — hanya mendeteksi dan berteriak. (Auto-repair = keputusan manusia, Phase 2 rekonsiliasi.)

## Task 1c.3 — Lifecycle wiring

- `ledger.Module.StartWorkers(ctx)` menyalakan relay + verifier; dipanggil `cmd/server/main.go` setelah server siap.
- Urutan shutdown di cleanup callback `srv.Start`: **stop workers dulu**, baru close RabbitMQ → Redis → Postgres.
- Env baru didaftarkan di `internal/config` + `.env.example`: `OUTBOX_POLL_INTERVAL`, `OUTBOX_BATCH_SIZE`, `WORKER_ENABLED` (default true; false untuk menjalankan server tanpa worker saat debugging).

## Definition of Done 06

- [ ] Demo end-to-end: `docker compose up` → transfer via API → `rabbitmqadmin get queue=...` menunjukkan event diterima.
- [ ] Kill -9 proses server saat ada backlog outbox → restart → backlog terkirim semua (bukti at-least-once).
- [ ] Setelah test suite integration penuh: `fn_verify_ledger_balance` & audit view bersih, verifier log tanpa ERROR.
- [ ] `make lint`, `make test` hijau.
