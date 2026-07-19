# 01 — Arsitektur Target & Keputusan yang Dikunci

## Prinsip Modular Monolith

Satu binary, satu database, tapi kode terorganisir sebagai modul-modul yang boundary-nya ditegakkan:

```
cmd/
  server/main.go            # satu-satunya entrypoint aplikasi; hanya wiring, <150 baris
internal/
  config/                   # env config (sudah ada)
  server/                   # HTTP server + graceful shutdown (sudah ada)
  handler/                  # router + composition root HTTP (sudah ada, akan diisi)
  ledger/                   # ← MODUL 1 (fokus saat ini)
    ledger.go               # public API modul: interface + constructor (satu-satunya pintu masuk)
    constant/  apperror/  model/  processors/  repository/
    service/                # posting engine, account provisioning
    transport/              # HTTP handlers milik modul ledger
    worker/                 # outbox relay, verification jobs
  <modul-berikutnya>/       # auth/, payment/, notification/ — pola yang sama, NANTI
pkg/                        # library generik bebas domain (database, cache, messaging,
                            # middleware, logger, response, scheduler, generalutil, generalerror)
migrations/                 # golang-migrate, bernomor, up/down berpasangan
docs/plan/                  # dokumen ini
docs/design/legacy-schemas/ # arsip skema lama (referensi, tidak dieksekusi)
```

### Aturan boundary (ditegakkan manual + review)
1. Modul lain **hanya boleh** import `internal/ledger` (root package: interface publik + tipe DTO). Dilarang import `internal/ledger/repository`, `internal/ledger/processors`, dst dari luar modul ledger. (Pengecualian tunggal: `internal/<mod>/events` — kontrak payload event, lihat 14 T3.)
2. Komunikasi antar modul: (a) pemanggilan method sinkron via interface publik modul, atau (b) event async via outbox → RabbitMQ. Tidak ada query lintas tabel milik modul lain.
3. `pkg/` tidak boleh import `internal/` (arah dependensi satu arah: `cmd` → `internal` → `pkg`).
4. Setiap modul memiliki tabel-tabelnya sendiri. Prefix tidak wajib untuk ledger (tabelnya sudah jelas), modul baru berikutnya pakai prefix (mis. `auth_users`).

> **Update 2026-07-12**: aturan 1–3 kini ditegakkan otomatis oleh `boundary_test.go` (jalan via `make test`), bukan hanya review manual. Peta jangka panjang modul ↔ calon service (payin, payout, vendorgw, fraud, admin, user-facing) + keputusan terkuncinya ada di [21-service-topology-review.md](21-service-topology-review.md) — `<modul-berikutnya>` pada pohon di atas kini punya nama dan urutan konkret di sana.

## Keputusan yang Dikunci (JANGAN diperdebatkan ulang saat implementasi)

| # | Keputusan | Alasan |
|---|-----------|--------|
| D1 | **Skema DB mengikuti bentuk yang dipakai kode Go** (`ledger_transactions`, TEXT codes `'debit'/'cash'/'active'`), bukan desain `ledgernew.sql` (SMALLINT lookup FK). Guard bagus dari `ledgernew.sql` di-port. | Kode + posting engine + 22 processor + test sudah ada dan sudah melalui iterasi review. Mengadaptasi semuanya ke skema lain jauh lebih berisiko daripada menulis DDL yang cocok. Normalisasi lookup table adalah optimasi storage yang bisa dilakukan belakangan tanpa mengubah semantik. |
| D2 | **Nominal = BIGINT minor units** di DB, `decimal.Decimal` di Go. Validator wajib menolak amount non-integer. MVP hanya `IDR` (minor_unit 0). | Aritmetika eksak, index kecil, tidak ada isu presisi NUMERIC. |
| D3 | **Currency disimpan di `accounts`**, bukan `account_balances`. Query `LockBalances` diubah SELECT `a.currency` (satu baris berubah). | Menghilangkan duplikasi dua sumber kebenaran currency. |
| D4 | **Idempotency**: `UNIQUE INDEX (idempotency_key, COALESCE(idempotency_scope,''))` pada `ledger_transactions`. | Postgres menganggap NULL distinct di UNIQUE biasa — dua request scope NULL akan lolos dua-duanya tanpa COALESCE. |
| D5 | **System accounts pakai kolom `system_qualifier TEXT`** di `accounts` (mis. settlement per gateway `'bca'`, fee `'platform'`, escrow per currency `'IDR'`). `GetSystemAccountID(type, qualifier)` lookup ke kolom ini. | Processor sudah memanggil `GetSystemAccountID(ctx, type, gateway)`. Menumpang di `pocket_code` itu hack; kolom eksplisit lebih jelas. |
| D6 | **`balance_after` di `ledger_entries` = saldo final akun setelah seluruh transaksi** (bukan per-entry running balance). Konsekuensi: JANGAN port constraint `chk_balance_math` per-entry dari `ledgernew.sql`. | Kode (`applyEntries` + `InsertEntries`) menulis nilai final yang sama untuk semua entry akun tsb dalam satu tx. Mengubah ini menyentuh jantung posting engine — tidak sepadan untuk MVP. Integritas dijaga oleh `validateBalanced` + fungsi verifikasi. |
| D7 | **Outbox lifecycle penuh** (`pending/processing/published/failed/dead` + `retry_count/max_retries` + trigger auto-dead) di-port dari `ledgernew.sql`, karena kolom insert kode hanya 6 kolom → sisanya DEFAULT. | Worker relay butuh state machine ini; kode insert tidak perlu berubah. |
| D8 | **Publikasi event**: RabbitMQ via `pkg/messaging` (topic exchange `ledger.events`, routing key = `event_type`). | Infra sudah ada lengkap dengan DLQ. |
| D9 | **Scheduler**: pindahkan `cmd/scheduler/scheduler_final.go` → `pkg/scheduler` sebagai library; worker outbox & verification jalan **di dalam proses `cmd/server`** (goroutine) untuk MVP, dengan distributed lock Redis dari scheduler agar aman saat nanti multi-replica. | Modular monolith: satu proses dulu. Binary worker terpisah adalah keputusan deployment, bukan keputusan kode — mudah dipisah nanti karena worker sudah berupa package sendiri. |
| D10 | **API style**: satu endpoint generik `POST /api/v1/ledger/transactions` dengan `type` di body (memetakan langsung ke processor registry) + endpoint read. Tipe admin (`adjustment_*`, `freeze_*`, `reversal`) hanya untuk role `admin`. | Registry pattern sudah ada; satu endpoint per tipe = 22 handler boilerplate. |
| D11 | **RLS ditunda ke Phase 2.** MVP cukup: role DB aplikasi non-superuser + REVOKE CREATE ON SCHEMA public. | RLS dari `ledgernew.sql` menambah kompleksitas setup lokal; nilai keamanannya baru relevan saat ada koneksi readonly/analytics. |
| D12 | **Auth user management BUKAN scope ledger.** MVP pakai JWT middleware yang ada; `user_id` diambil dari claim. Modul `auth` = modul berikutnya setelah ledger MVP. | Fokus. |

## Konvensi Kode (mengikuti yang sudah ada di repo)

- Logging: `log/slog`, logger di-inject, request-scoped via middleware.
- Error: sentinel di `apperror`, wrap dengan `%w`, mapping ke HTTP status di transport layer (400 validation / 402 insufficient / 404 not found / 409 conflict-idempotency / 422 business / 500 internal).
- Mock: `//go:generate mockgen` (go.uber.org/mock), file `*_mock.go` sepaket dengan interface.
- Test: unit test sepaket; integration test pakai `testcontainers-go` (dependency sudah ada) dengan build tag `//go:build integration`.
- SQL: raw SQL di repository, parameterized (`$1`), tidak ada ORM.
- Router: `net/http` Go 1.22 pattern (`mux.HandleFunc("POST /path", ...)`), tidak ada router pihak ketiga.

## Observability Target (MVP)

- Metrics Prometheus (dependency sudah ada): `ledger_transactions_total{type,status}`, `ledger_post_duration_seconds`, `outbox_pending_gauge`, `outbox_publish_failures_total`, `ledger_verification_discrepancies_total` (alert kalau > 0). Endpoint `GET /metrics`.
- Tracing OTel (dependency sudah ada): span per `Handle()` + per publish. Cukup instrumentasi manual sederhana, exporter dikonfigurasi via env.
