# 09 — Hardening Review: Temuan & Keputusan Desain

> Hasil review menyeluruh (2026-07-11) atas ledger MVP yang sudah jalan end-to-end, dari sudut: **resource terbatas, efisiensi, no-money-loss di bawah failure/chaos, dependency failure, security, locking/bottleneck, cost**. Dokumen ini adalah **referensi** — task implementasinya ada di [10](10-phase2a-security-gating.md), [11](11-phase2b-efficiency-locking.md), [12](12-phase2c-resilience-ops.md). Semua referensi `file:line` valid per commit saat dokumen ini ditulis; kalau kode sudah bergeser, cari nama fungsi/konstruk yang disebut.

## Keputusan Desain yang Dikunci (jangan re-litigasi)

| # | Keputusan | Alasan |
|---|-----------|--------|
| K1 | **Tipe transaksi sistem dipindah ke router internal terpisah** (`/internal/v1/ledger`, listener kedua yang bind ke `127.0.0.1`/network internal), BUKAN role-gating di router publik | Pilihan user. Isolasi network-level: endpoint yang bisa mencetak/memindahkan uang dari akun sistem tidak pernah ter-expose publik, apapun bug di lapisan auth. |
| K2 | **Deployment target: 1 node kecil, siap multi-replica.** Redis jadi OPSIONAL — rate limit & scheduler lock fallback in-memory bila Redis tidak dikonfigurasi; otomatis pakai Redis bila tersedia | Pilihan user. Cost minimum sekarang tanpa mengunci arsitektur; semua mekanisme tetap benar saat multi-replica (Redis diaktifkan). |
| K3 | **Fee dihitung server-side** dari fee policy (config), `fee_amount` dari client DITOLAK di router publik | Client-controlled fee = celah bisnis (set fee 0). |
| K4 | **Amount wajib integral dalam minor unit** sesuai exponent currency (IDR=0, USD=2) — fraksional ditolak di transport DAN validator | `IntPart()` men-truncate diam-diam = uang hilang/tercipta. |
| K5 | **Idempotency scope = userID dari JWT**, di-set server-side di transport | Key global lintas user = celah DoS/probing lintas tenant. |
| K6 | **Akun sistem (`allow_negative=true`) tidak ikut `FOR UPDATE`** — balance di-apply sebagai delta atomik `UPDATE ... SET balance = balance + $d ... RETURNING balance` di akhir transaksi; akun user tetap `FOR UPDATE` | Menghilangkan hot-row serialization tanpa mengorbankan overdraft check user. Lihat C13. |
| K7 | **Arsitektur inti TIDAK diubah** — sync double-entry posting + transactional outbox tetap; yang di-redesign hanya pipeline locking (K6) dan pemisahan API surface (K1) | Review menyimpulkan desain inti sound (lihat bagian E). |

---

## A. KRITIS — celah money loss / fraud

### A1. Tipe transaksi sistem callable oleh user biasa
`internal/ledger/transport/http.go:19-29` (`adminOnlyTypes`) hanya menggate 7 tipe (adjustment_*, freeze_*, reversal, chargeback). Semua tipe lain bisa dipanggil user ber-JWT biasa, termasuk:

- `money_in` — **user bisa mengkredit saldo sendiri** dari `settlement[gateway]` tanpa deposit nyata (processor `money_in.go:47` debit settlement, kredit user cash).
- `refund` — merchant.settle → user.cash.
- `withdraw_settle`, `withdraw_pending_settle`, `withdraw_cancel`, `withdraw_pending`, `withdraw_pending_cancel` — lifecycle yang seharusnya dipicu payment gateway/ops.
- `escrow_release`, `escrow_refund`, `fee_collect`.

**Fix (K1):** router internal terpisah. Tipe yang tetap publik: `transfer_p2p`, `transfer_pocket`, `withdraw_initiate`, `escrow_hold`. → Task di [10 T1](10-phase2a-security-gating.md).

### A2. `gateway`, `fee_amount`, `fee_gateway` dikontrol client
`metadata` diteruskan verbatim dari body ke processor (`transport/http.go:106`), lalu dibaca `processors.go:182` (`fee_amount`), `:196` (`fee_gateway`), `:210` (`requireGateway`). Akibat: user menentukan fee sendiri (termasuk 0), dan memilih akun settlement/fee mana yang terkena.

**Fix (K3):** metadata client di-allowlist (hanya key deskriptif, mis. `note`, `external_ref`); `gateway` divalidasi terhadap allowlist config; fee dihitung server-side dari fee policy. → [10 T3](10-phase2a-security-gating.md).

### A3. Amount fraksional diterima lalu di-truncate diam-diam
- Transport: `decimalFromString` = `decimal.NewFromString` (`transport/dto.go:108-111`) — `"100.75"` lolos.
- Validator: `PositiveAmountValidator` (`processors/validators.go:44-52`) hanya cek `> 0`.
- Repository: `newBalances[id].IntPart()` (`repository/account_balance_repository.go`) **men-truncate fraksi tanpa error** → Σdebit ≠ Σcredit versi tersimpan vs versi divalidasi, uang hilang/tercipta.

**Fix (K4):** tolak amount non-integral (dan non-integral terhadap exponent currency) di transport dan di validator (defense in depth). → [10 T4](10-phase2a-security-gating.md).

### A4. Tidak ada amount cap di manapun
`MaxAmountValidator` ada (`validators.go:66-74`) tapi **tidak diwire ke processor manapun**; komentar `processors.go:162,297` dan `transfer_p2p.go:22` menunda ke "API/policy layer" yang belum ada. → [10 T5](10-phase2a-security-gating.md).

### A5. Idempotency key global lintas user
Transport tidak pernah mengisi `IdempotencyScope` (`http.go:101` hanya set Key); unique index `uq_ltx_idempotency (idempotency_key, COALESCE(idempotency_scope,''))` (`migrations/000001_ledger_core.up.sql:81-82`). User A yang menebak/menduduki key user B membuat transaksi B gagal `ErrStillProcessing`/`ErrPreviousFailed`, atau mem-probe status transaksi orang lain.

**Fix (K5):** transport set `IdempotencyScope = userID` dari JWT. Router internal pakai scope nama service pemanggil. → [10 T2](10-phase2a-security-gating.md).

---

## B. TINGGI — resiliensi / chaos

### B1. Tidak ada timeout di level Postgres
`DSN()` (`internal/config/config.go:305-310`) polos — tidak ada `statement_timeout`, `lock_timeout`, `idle_in_transaction_session_timeout`; tidak ada context timeout per query di worker loops (ctx worker tanpa deadline). Chaos scenario: satu transaksi menggantung menahan row lock → antrian `FOR UPDATE` menumpuk → pool 25 koneksi habis → **seluruh API mati**, bukan cuma jalur yang bermasalah. → [11 T5](11-phase2b-efficiency-locking.md).

### B2. Rate limiter fail-open tanpa fallback saat Redis down
`WithRateLimit` (`pkg/middleware/rate_limit.go:12-39`): error dari limiter → request diteruskan. Fallback in-memory hanya ada di kode yang **dikomentari** (`:54-109`). Redis mati = tidak ada rate limit sama sekali. → [12 T1](12-phase2c-resilience-ops.md).

### B3. Verifier hanya log + metric
`worker/verifier.go`: temuan unbalanced tx / proyeksi inkonsisten → `logger.Error` + counter Prometheus, selesai. Tidak ada jalur alert (webhook/pager) dan tidak ada runbook. Diskrepansi ledger adalah insiden P1 — harus membangunkan orang. → [12 T4](12-phase2c-resilience-ops.md).

### B4. Outbox `dead` tanpa tooling replay
Satu-satunya cara menghidupkan event `dead` adalah SQL manual. → replay endpoint di router internal, [12 T3](12-phase2c-resilience-ops.md).

### B5. Reaper meng-increment `retry_count`
`ReapStuck` (`repository/outbox_event_repository.go:195-211`) set `status='failed', retry_count = retry_count + 1`. Broker down 1 jam → event di-claim, gagal publish (+1), di-reap (+1)… bisa mencapai `max_retries=5` dan **mati tanpa pernah benar-benar dicoba 5×**. Juga tidak ada kolom backoff — retry cadence hanya tick 30s global. → [12 T2](12-phase2c-resilience-ops.md).

### B6. OTel instrumented tapi provider tidak pernah diinstal
Span dibuat di `service/handle` dan `pkg/messaging` via `otel.Tracer(...)`, tapi tidak ada `SetTracerProvider`/exporter di manapun (grep kosong) → semua span no-op. Overhead kecil tanpa manfaat, dan menyesatkan pembaca kode. → [12 T5](12-phase2c-resilience-ops.md).

### B7. `uuid.MustParse` di jalur scan
`ledger_transaction_repository.go:284-319` (`GetByID`) — `MustParse` panic bila data tidak valid; defensif seharusnya `uuid.Parse` + error. → [12 T6](12-phase2c-resilience-ops.md).

---

## C. EFISIENSI / BOTTLENECK (resource terbatas)

### C13. Hot-row: akun sistem ikut `SELECT ... FOR UPDATE` *(temuan paling berdampak throughput)*
`LockBalances` (`repository/account_balance_repository.go:75`, `FOR UPDATE OF ab`) mengunci **semua** akun yang terlibat — termasuk `settlement[gateway]` dan `fee[gateway]`. Akibat: setiap `money_in` via gateway yang sama ter-serialize sepanjang seluruh pipeline (validasi → build entries → insert → update). Di box kecil ini adalah ceiling throughput; klasik "hot account problem" (lihat referensi TigerBeetle di bawah).

**Fix (K6):** pisahkan dua kelas akun di pipeline:
- **Akun user** (`allow_negative=false`): tetap `FOR UPDATE` — pre-read balance diperlukan untuk overdraft check yang konsisten.
- **Akun sistem** (`allow_negative=true`): TIDAK di-lock, TIDAK di-pre-read. Balance di-apply di langkah update sebagai delta atomik: `UPDATE account_balances SET balance = balance + $delta WHERE account_id = $1 RETURNING balance`. Tidak butuh floor check (boleh negatif); `balance_after` untuk `ledger_entries` diambil dari `RETURNING`. Row lock hanya dipegang selama satu statement, bukan sepanjang transaksi validasi.

→ [11 T1](11-phase2b-efficiency-locking.md). **Wajib diverifikasi dengan integration test nyata (Docker), bukan sqlmock** — lihat "Pelajaran Terverifikasi" di bawah.

### C14. `InsertEntries` per-entry round trip
`repository/ledger_entry_repository.go:50-70` — loop `ExecContext` per entry di dalam transaksi yang memegang lock → memperpanjang lock hold. Pola batch multi-row sudah ada di `InsertEvents` outbox (`outbox_event_repository.go:89-119`) — tiru. → [11 T2](11-phase2b-efficiency-locking.md).

### C15. ResolveAccounts 3–4 query pool per posting
`money_in.go`/`money_out.go` dkk.: `GetAccountID` + `GetSystemAccountID` + `GetAccountCurrency` sebelum transaksi. System account ID **immutable pasca-seed** → cache in-process selamanya; (userID,type)→(accountID,currency) → cache TTL dengan invalidasi saat provisioning. → [11 T3](11-phase2b-efficiency-locking.md).

### C16. UUIDv4 di tabel insert-heavy
PK acak menyebar di seluruh btree → page split, cache miss, WAL besar. `google/uuid` ≥1.6 punya `uuid.NewV7()` (time-ordered, RFC 9562). Ganti pembuatan ID untuk `ledger_transactions`, `ledger_entries`, `outbox_events`. → [11 T4](11-phase2b-efficiency-locking.md).

### C17. Redis wajib padahal pemakaian minim
Dipakai hanya untuk rate limit + scheduler lock. Di single node keduanya bisa in-memory (`MemoryLock` sudah ada di `pkg/scheduler`). → Redis opsional (K2), [12 T1](12-phase2c-resilience-ops.md).

### C18. Pool default 25/25 terlalu besar untuk box kecil
`config.go:145-148`. Postgres se-mesin ikut berebut memori/CPU. Turunkan default `MaxOpenConns` ke 10 (override via env tetap ada), dokumentasikan sizing. → [11 T5](11-phase2b-efficiency-locking.md).

### C19. Gauge loop 2 query terpisah tiap 15s
`worker/outbox_relay.go:184-208` — `CountByStatus` dipanggil 2× (pending, dead). Gabungkan jadi satu `GROUP BY status`. Minor. → [11 T6](11-phase2b-efficiency-locking.md).

---

## D. SECURITY lain

| # | Temuan | Lokasi | Fix |
|---|--------|--------|-----|
| D1 | JWT tanpa `nbf`/`iss`/`aud`; `JWTConfig.Issuer` tidak pernah dipakai. (Catatan: verifikasi HMAC sudah benar & kebal alg-confusion — header alg diabaikan, selalu HS256 server-side, `hmac.Equal` constant-time) | `pkg/middleware/auth.go:55-90` | [10 T6](10-phase2a-security-gating.md) |
| D2 | `/metrics` tanpa auth, di luar chain middleware | `internal/handler/router.go:31-37` | [10 T6] — bind ke listener internal (K1) |
| D3 | HSTS hanya saat `r.TLS != nil` — di belakang reverse proxy tidak pernah terkirim | `pkg/middleware/security.go:35` | [10 T6] — trust-proxy config (`X-Forwarded-Proto`) |
| D4 | Metadata body tanpa batas key/size sendiri (body total 1 MiB sudah dibatasi via `response.Decode`) | `pkg/response/response.go:110` | [10 T3] — allowlist + max size |
| D5 | RLS masih deferred (keputusan D11 lama) | `migrations/000001:244-245` | Tetap di backlog [07 Task H8](07-phase-2-hardening.md) — jangan hilang |

---

## E. Yang SUDAH BENAR — dipertahankan sebagai kontrak

Ini bukan area kerja; ditulis supaya implementor **tidak merusaknya**:

1. **Idempotency gate menangani ambiguous commit.** Retry setelah commit yang statusnya tidak jelas → duplicate key → `handleDuplicate` → `ErrAlreadyPosted` → sukses idempoten (`service/handle/service.go`). Ini pola yang sama dengan yang dipublikasikan Stripe (lihat referensi).
2. **Outbox pattern lengkap**: event ditulis satu transaksi DB dengan posting; relay claim pakai `FOR UPDATE SKIP LOCKED`; publish menunggu broker confirm (`pkg/messaging/publisher.go:163-195`); reaper untuk stuck. Delivery = **at-least-once** → konsumen WAJIB dedup by event id (`outbox_events.id` = AMQP `message_id`). Kontrak ini harus masuk dokumentasi event ([07 Task H1](07-phase-2-hardening.md)).
3. **Guard DB**: `trg_entries_immutable` (append-only), `chk_balance_floor` + `allow_negative`, unique idempotency, dead-letter trigger.
4. **Lock ordering deterministik** via `ORDER BY account_id` di SQL `LockBalances` — bukan di slice Go (AccountIDs positional, JANGAN di-sort; bug ini pernah terjadi dan sudah diperbaiki).
5. **Graceful shutdown ordering** benar: drain HTTP → stop workers → close MQ → Redis → PG (`cmd/server/main.go:78-95`).

---

## Pelajaran Terverifikasi (2026-07-11) — WAJIB dibaca implementor

Tiga bug nyata lolos dari seluruh unit test (sqlmock) dan baru tertangkap saat integration test dengan Postgres asli:
1. `FOR UPDATE OF <nama tabel>` vs alias — Postgres menuntut alias bila FROM memakai alias.
2. Placeholder di dalam multi-branch `CASE` butuh cast `::bigint` eksplisit per-placeholder.
3. Sort `AccountIDs` menukar arah debit/kredit tergantung byte order UUID.

**Konsekuensi**: setiap task di 10–12 yang menyentuh SQL mentah atau urutan/locking WAJIB diverifikasi dengan `go test -tags=integration -race ./...` (Docker) DAN smoke test manual via HTTP sebelum dianggap selesai. Unit test hijau saja TIDAK cukup.

## Referensi

- PostgreSQL docs — [Explicit Locking](https://www.postgresql.org/docs/current/explicit-locking.html), [Client Connection Defaults: statement_timeout/lock_timeout](https://www.postgresql.org/docs/current/runtime-config-client.html)
- Brandur Leach (Stripe) — [Implementing Stripe-like Idempotency Keys in Postgres](https://brandur.org/idempotency-keys)
- TigerBeetle — [design docs: hot account / contention](https://docs.tigerbeetle.com/) (kenapa akun sistem panas adalah bottleneck utama ledger)
- AWS Builders' Library — [Timeouts, retries, and backoff with jitter](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
- RFC 9562 — UUIDv7 (time-ordered UUID untuk index locality)
- Chris Richardson — [Transactional Outbox pattern](https://microservices.io/patterns/data/transactional-outbox.html)
