# 12 — Phase 2c: Resilience, Redis-Optional & Ops Tooling

> Prasyarat: baca [09-hardening-review.md](09-hardening-review.md) bagian B dan E. Task di sini membuat sistem tahan terhadap kegagalan dependency (Redis, RabbitMQ, Postgres) tanpa kehilangan uang, dan memberi tim operasional alat untuk merespons insiden.

## T1 — Redis opsional (K2): fallback in-memory untuk rate limit + scheduler lock

**Masalah**: `cmd/server/main.go` saat ini `os.Exit(1)` kalau Redis gagal connect — Redis diperlakukan sebagai hard dependency padahal pemakaiannya cuma rate-limit + scheduler distributed lock, keduanya punya fallback yang masuk akal untuk single-node.

### Langkah

1. **`internal/config/config.go`**: tambah `RedisConfig.Enabled bool`, env `REDIS_ENABLED` default `true`. **PENTING**: default tetap `true` (aman untuk deployment existing/multi-replica) — operator single-node yang mau hemat resource men-set eksplisit `REDIS_ENABLED=false`. Kalau `Enabled=false`, field `Addr` dkk boleh kosong (skip validasi required).
2. **`cmd/server/main.go`**: ganti blok
   ```go
   redisCache, err := cache.New(ctx, cfg.Redis)
   if err != nil { ... os.Exit(1) }
   ```
   menjadi kondisional:
   ```go
   var redisCache *cache.Cache // nil kalau disabled atau gagal connect
   if cfg.Redis.Enabled {
       redisCache, err = cache.New(ctx, cfg.Redis)
       if err != nil {
           log.Error("failed to connect to redis", "error", err)
           os.Exit(1) // Redis diaktifkan operator tapi gagal connect = config error, bukan graceful-degrade
       }
   } else {
       log.Warn("redis: disabled (REDIS_ENABLED=false) — rate limiting and scheduler lock running in-memory, single-instance only")
   }
   ```
   Catatan: kalau operator MENGAKTIFKAN Redis tapi ternyata down saat startup, tetap `os.Exit(1)` (fail fast, config error eksplisit) — bedanya dengan "opsional" adalah operator secara sadar memilih tidak pakai Redis sama sekali, bukan Redis-down-tapi-diharapkan-ada.
3. **`internal/handler/router.go`**: `NewRouter` sekarang harus menangani `deps.Cache` yang nilainya bisa nil-embedding. Cek bagaimana `limiter := cache.NewRedisRateLimiter(deps.Cache.Redis(), ...)` dipanggil — kalau `deps.Cache` nil, buat in-memory limiter sebagai gantinya:
   ```go
   var limiter cache.Limiter
   if deps.Cache != nil {
       limiter = cache.NewRedisRateLimiter(deps.Cache.Redis(), cache.RateConfig{Requests: 10, Per: time.Minute, Burst: 10})
   } else {
       limiter = cache.NewMemoryRateLimiter(cache.RateConfig{Requests: 10, Per: time.Minute, Burst: 10})
   }
   ```
4. **`pkg/cache/rate_limiter.go`**: tambah `MemoryRateLimiter` yang mengimplementasi `Limiter` interface yang sama (`Allow(ctx, key) (allowed bool, remaining int, err error)`), pakai `sync.Mutex` + `map[string]*bucket` token-bucket sederhana (algoritma sama seperti Redis Lua script yang sudah ada — replikasi logikanya di Go, bukan hal baru, `RedisRateLimiter` yang sudah ada adalah referensi). Tambahkan goroutine pembersih (GC) periodik untuk entry kadaluarsa supaya map tidak bocor memori — pola yang sama seperti `MemoryLock` di `pkg/scheduler/scheduler.go` (`NewMemoryLock`, sudah ada, tiru pendekatannya).
5. **`pkg/middleware/rate_limit.go`**: `WithRateLimit`'s fail-open behavior (baris 19-25, comment "Fail-open (recommended), Redis down → traffic lewat") — SEKARANG dengan in-memory fallback dari langkah 3-4, error dari `limiter.Allow` seharusnya jarang terjadi (in-memory tidak pernah "down"). TAPI kalau memakai `RedisRateLimiter` dan Redis mendadak putus di tengah jalan (bukan saat startup), fail-open TETAP dipertahankan sebagai perilaku — ini keputusan sadar (availability > strict rate limiting), TULISKAN di komentar kenapa ini diterima: rate limit adalah pertahanan DoS/abuse, bukan mekanisme keuangan — fail-open di sini tidak menyebabkan money loss (beda dengan kalau ini adalah lock-related fail-open). Tidak perlu diubah, cukup perjelas komentarnya.
6. **`pkg/scheduler/scheduler.go`**: `LockProvider` + `MemoryLock`/`RedisLock` SUDAH ADA dan sudah dipakai dengan pola fallback yang benar di `internal/ledger/ledger.go` (`NewModule`, cek `redisClient != nil`). **Tidak ada perubahan diperlukan di scheduler** — hanya pastikan `cmd/server/main.go` meneruskan `nil` (bukan client Redis yang gagal init) kalau `cfg.Redis.Enabled == false`, yang sudah otomatis benar dengan langkah 2 (`redisCache` tetap nil kalau disabled, dan `redisCache.Redis()` dipanggil di `NewModule` — pastikan pemanggilan itu juga nil-guarded, cek `main.go`: `ledger.NewModule(db, mq, redisCache.Redis(), ...)` akan PANIC kalau `redisCache` nil karena `nil.Redis()` — method call pada nil pointer AMAN di Go SELAMA `Redis()` tidak dereference internal field yang nil sebelum dicek. Verifikasi implementasi `(*Cache).Redis()` — kalau cuma `return c.client`, dan `c` sendiri nil, ini PANIC saat method dipanggil pada nil receiver TERGANTUNG apakah method itu dereference `c` sebelum return. Untuk aman, ganti panggilan jadi:
   ```go
   var redisClient *redis.Client
   if redisCache != nil {
       redisClient = redisCache.Redis()
   }
   ledgerModule := ledger.NewModule(db, mq, redisClient, ...)
   ```
7. **`.env.example`**: dokumentasikan `REDIS_ENABLED=true` dengan catatan "set false untuk single-instance deployment tanpa Redis; rate limit & scheduler lock otomatis fallback in-memory".

### Test yang wajib ditulis
- `pkg/cache/rate_limiter_test.go`: `MemoryRateLimiter` — request melebihi burst → ditolak; window reset → diterima lagi; concurrent access aman (`-race`).
- `internal/handler/router_test.go`: `NewRouter` dengan `deps.Cache == nil` tidak panic, rate limit tetap berfungsi (in-memory).
- Integration/smoke: jalankan server dengan `REDIS_ENABLED=false`, Redis TIDAK dijalankan di docker-compose sama sekali → server start sukses, `/ready` tidak melaporkan redis unhealthy (redis skip dari readiness check kalau disabled — cek `internal/handler/health.go` `Ready` handler, sesuaikan supaya tidak mengecek Redis kalau disabled).

### DoD
- [ ] `make test` hijau.
- [ ] Server start sukses tanpa Redis sama sekali saat `REDIS_ENABLED=false`.
- [ ] Rate limit tetap berfungsi (in-memory) dalam mode itu.
- [ ] Scheduler verifier tetap jalan (memory lock, single-instance) dalam mode itu.
- [ ] Regression: `REDIS_ENABLED=true` (default) perilaku SAMA PERSIS seperti sebelumnya.

---

## T2 — Outbox: backoff, jangan increment retry_count di reaper

**Masalah** (09 §B5): `ReapStuck` (`internal/ledger/repository/outbox_event_repository.go:195-211`) meng-increment `retry_count` — broker down lama bisa membuat event `dead` tanpa pernah benar-benar dicoba publish sejumlah `max_retries`. Tidak ada backoff — cadence retry cuma tick 30 detik konstan (`defaultRetryInterval`).

### Langkah
1. **Migrasi baru** `migrations/000003_outbox_backoff.up.sql` / `.down.sql`: tambah kolom `outbox_events.next_attempt_at TIMESTAMPTZ NULL` (NULL = boleh dicoba kapan saja / segera). Index baru menggantikan `idx_outbox_retry`: `CREATE INDEX idx_outbox_retry ON outbox_events(next_attempt_at ASC NULLS FIRST) WHERE status='failed'` (drop index lama di up, atau `CREATE OR REPLACE`/`DROP INDEX IF EXISTS` lalu buat ulang).
2. **`repository/outbox_event_repository.go`**:
   - `MarkFailed`: tambah parameter/hitung `nextAttemptAt` = exponential backoff dengan jitter dari `retry_count` BARU (setelah increment): `base=30s, factor=2, cap=15m` → `delay = min(cap, base * 2^retryCount) + jitter(0, delay/2)`. Set `next_attempt_at = now() + delay` di UPDATE yang sama.
   - `ClaimFailedForRetry`: WHERE clause tambah `AND (next_attempt_at IS NULL OR next_attempt_at <= now())`.
   - `ReapStuck`: **HAPUS** `retry_count = retry_count + 1` dari statement UPDATE. Reaper hanya mengembalikan event ke `status='failed'` dengan `last_error='reaped: stuck in processing past deadline'` dan `next_attempt_at = now()` (boleh dicoba segera lagi) — TANPA menyentuh `retry_count`. Stuck event yang di-reap berkali-kali karena publish terus gagal TETAP akan mencapai `dead` lewat jalur normal (`MarkFailed` yang memang meng-increment saat publish benar-benar dicoba dan gagal), bukan lewat reaper yang cuma mendeteksi "proses sebelumnya crash sebelum sempat mencoba".
3. **`internal/ledger/worker/outbox_relay.go`**: tidak ada perubahan struktural — `ClaimFailedForRetry` yang sudah difilter `next_attempt_at` otomatis membuat `RetryInterval` tick (30s) hanya efektif men-trigger claim untuk event yang BENAR siap dicoba; event dengan backoff panjang tidak akan ke-claim walau tick jalan tiap 30s (query-nya yang menyaring, bukan intervalnya — ini benar dan tidak perlu diubah).

### Test yang wajib ditulis
- Unit test `MarkFailed`: `retry_count` 0→1, `next_attempt_at` di masa depan (>= now()+15s dengan base 30s minus jitter toleransi wajar).
- Unit test `ReapStuck`: `retry_count` TIDAK berubah, `next_attempt_at` di-reset ke sekitar now().
- Integration test: simulasikan broker down (matikan container rabbitmq) selama > `StuckAfter`, biarkan reaper jalan beberapa kali → assert event yang sama tidak mencapai `dead` hanya karena di-reap berkali-kali (retry_count tetap 0 selama belum pernah benar-benar dicoba publish).

### DoD
- [ ] Migrasi baru diverifikasi dengan `make migrate-up`/`migrate-down` roundtrip bersih.
- [ ] `make test`, integration test hijau.
- [ ] Reaper terbukti tidak mempercepat kematian event yang belum pernah dicoba publish.

---

## T3 — Admin tooling: replay outbox `dead`

**Masalah** (09 §B4): satu-satunya cara menghidupkan event `dead` adalah SQL manual.

### Langkah
1. **`repository/outbox_event_repository.go`**: tambah `ReplayDead(ctx, eventID uuid.UUID) error` — `UPDATE outbox_events SET status='failed', retry_count=0, next_attempt_at=now(), last_error=last_error || ' [replayed by admin]' WHERE id=$1 AND status='dead'`. Juga `ReplayAllDead(ctx, olderThan time.Time) (int, error)` untuk replay massal (dengan batas — misal max 100 sekali panggil, cegah replay storm).
2. **`internal/ledger/ledger.go`**: `Module` tambah method `ReplayDeadEvent(ctx, eventID uuid.UUID) error` dan `ReplayDeadEvents(ctx, olderThan time.Time) (int, error)`.
3. **`internal/ledger/transport/`**: tambah route KHUSUS ROUTER INTERNAL (dari [10 T1](10-phase2a-security-gating.md)) — `POST /admin/outbox/dead/{id}/replay` dan `POST /admin/outbox/dead/replay-all`, admin-gated (`isAdmin`) meski sudah di router internal (defense in depth, ini operasi yang jarang dan sensitif).

### Test yang wajib ditulis
- Unit + integration: event `dead` → replay → status `failed`, `retry_count=0`, ke-claim oleh relay berikutnya, sukses publish jika broker sudah pulih.

### DoD
- [ ] `make test`, integration test hijau.
- [ ] Endpoint replay hanya ada di router internal, admin-gated.

---

## T4 — Verifier: alert hook + runbook

**Masalah** (09 §B3): diskrepansi ledger hanya di-log + metric, tidak ada jalur alert.

### Langkah
1. **`internal/config/config.go`**: tambah `AlertWebhookURL string`, env `ALERT_WEBHOOK_URL` (opsional, kosong = tidak ada alert eksternal, tetap log+metric seperti sekarang — backward compatible).
2. **`internal/ledger/worker/verifier.go`**: tambah field `alertFn func(ctx context.Context, severity, message string) error` di `Verifier` struct, di-set dari `NewVerifier` (parameter baru, boleh nil = no-op). Panggil `alertFn` di titik yang sama dengan `logger.Error` untuk `checkTrialBalance` dan `checkProjectionAudit` (bukan untuk `checkOutboxLag` yang levelnya Warn, bukan P1 — opsional tambahkan juga kalau mau, tapi minimal dua yang pertama).
3. **`pkg/alerting/webhook.go`** (paket baru, generic reusable): `func NewWebhookAlerter(url string, httpClient *http.Client) func(ctx, severity, message string) error` — POST JSON `{"severity":..., "message":..., "service":"seev-ledger", "timestamp":...}` ke `url` dengan timeout pendek (5s) dan TIDAK boleh mem-block/retry (fire-and-forget dengan satu percobaan, log error kalau gagal kirim — alert yang gagal terkirim tidak boleh menghambat verifier terus berjalan). Kompatibel dengan Slack Incoming Webhook / generic HTTP endpoint (format payload sesederhana mungkin, dokumentasikan bisa di-depan-i n8n/Zapier/PagerDuty Events API kalau perlu format lain).
4. **`cmd/server/main.go`**: wire `cfg.AlertWebhookURL` ke `NewVerifier` kalau tidak kosong.
5. **Runbook** — tambah `docs/runbooks/ledger-integrity-alert.md`: langkah manual saat menerima alert "unbalanced transaction detected" — (1) jangan panik, sistem TIDAK auto-repair by design; (2) query `fn_verify_ledger_balance()` untuk detail transaksi; (3) cek `ledger_transactions` + entries terkait, cari tahu apakah ini bug processor atau data korup; (4) JANGAN pernah UPDATE/DELETE `ledger_entries` (append-only, trigger akan menolak) — koreksi selalu lewat reversal transaction baru via router internal `reversal` (admin-gated); (5) eskalasi ke engineer kalau root cause tidak jelas dalam 15 menit.

### Test yang wajib ditulis
- Unit test verifier: `alertFn` dipanggil sekali per diskrepansi ditemukan (mock function, assert call count + args).
- Unit test webhook alerter: request terkirim dengan payload benar; timeout/error tidak panic, cuma log.

### DoD
- [ ] `make test` hijau.
- [ ] Alert webhook opsional, backward compatible (kosong = perilaku lama).
- [ ] Runbook tersedia dan ditautkan dari `docs/plan/README.md`.

---

## T5 — OTel: wire exporter opsional (bukan hapus instrumentasi)

**Masalah** (09 §B6): span dibuat tapi `TracerProvider` tidak pernah diinstal — instrumentasi hidup tapi inert.

### Langkah
1. **`internal/config/config.go`**: tambah `OTLPEndpoint string`, env `OTEL_EXPORTER_OTLP_ENDPOINT` (opsional, kosong = no-op tracer seperti sekarang, TIDAK install provider apa pun — behavior default tidak berubah, cost tetap nol untuk deployment yang tidak butuh tracing).
2. **`cmd/server/main.go`**: kalau `cfg.OTLPEndpoint != ""`, install `TracerProvider` dengan OTLP gRPC/HTTP exporter (`go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`, tambah dependency ke `go.mod`), `sdktrace.NewTracerProvider` dengan `WithBatcher(exporter)`, `otel.SetTracerProvider(tp)`, dan `otel.SetTextMapPropagator(propagation.TraceContext{})`. Pastikan `tp.Shutdown(ctx)` dipanggil di cleanup (`main.go` cleanup func, tambahkan sebelum/sejalan dengan urutan existing).
3. Kalau `cfg.OTLPEndpoint == ""`: **jangan panggil `SetTracerProvider` sama sekali** — default global no-op tracer SDK OTel sudah otomatis aktif, span yang dibuat kode existing tetap valid secara sintaks tapi betul-betul zero overhead (ini alasan kenapa "hapus instrumentasi" BUKAN pilihan yang diambil — instrumentasi yang sudah ada berguna begitu diaktifkan, tidak masuk akal membongkarnya).
4. `.env.example`: dokumentasikan `OTEL_EXPORTER_OTLP_ENDPOINT` kosong secara default, contoh nilai untuk Jaeger/Tempo lokal (`http://localhost:4317`).

### Test yang wajib ditulis
- Test startup: `cfg.OTLPEndpoint=""` → tidak ada provider terpasang, tidak error, tidak panic (regression, harus sudah lolos existing test).
- Manual/smoke (tidak perlu automated): jalankan dengan Jaeger lokal via docker-compose (opsional, tambahkan service `jaeger` di `docker-compose.yml` sebagai profile terpisah `--profile observability`, JANGAN jalan default), verifikasi span posting transaction muncul di Jaeger UI.

### DoD
- [ ] `make test` hijau.
- [ ] Default behavior (`OTLPEndpoint` kosong) sama seperti sekarang — zero overhead, zero dependency baru diaktifkan.
- [ ] Saat diaktifkan, span benar-benar sampai ke backend tracing (dibuktikan manual).

---

## T6 — Perbaikan kecil

Kerjakan sekaligus, masing-masing kecil dan independen:

1. **`internal/ledger/repository/ledger_transaction_repository.go` `GetByID`** (baris ~284-319): ganti `uuid.MustParse` → `uuid.Parse` dengan error handling (`return model.LedgerTransaction{}, fmt.Errorf("scan transaction: invalid stored account id: %w", err)`) — defensif terhadap data korup, tidak boleh panic proses production karena satu baris rusak.
2. **`pkg/middleware/rate_limit.go`**: hapus blok kode yang dikomentari (baris ~54-109, in-memory fallback lama yang sudah digantikan implementasi resmi di T1) — dead code, bukan dokumentasi berguna.
3. **`pkg/middleware/rate_limit.go` `RateLimitByUser`** (baris 45-48): fungsi ini punya bug (`r.Context().Value("user_id").(string)` — key string polos bukan `middleware.UserIDKey`, unchecked type assertion bisa panic) dan TIDAK dipakai di router manapun saat ini. Perbaiki jadi konsisten dengan `currentUserID`/`UserIDFromCtx` yang dipakai transport ledger (`pkg/middleware/auth.go`) — pakai key context yang sama, cek `ok` sebelum assert, fallback ke `RateLimitByIP` kalau user ID tidak ada di context. Kalau nanti mau dipakai (rate limit per-user, bukan per-IP, untuk endpoint tertentu), sudah aman dipakai.
4. **`internal/handler/health.go`** (`Ready` handler): sesuaikan supaya tidak mengecek Redis kalau `cfg.Redis.Enabled == false` (terkait T1) — laporkan `"redis": "disabled"` bukan error di response readiness.

### DoD
- [ ] `make test` hijau untuk semuanya.
- [ ] Tidak ada regression pada test existing.

---

## T7 — Chaos test script

**Tujuan**: bukti empiris "no money lost" di bawah kegagalan nyata, bukan cuma klaim desain.

### Langkah
1. Buat `scripts/chaos-test.sh` (atau `docs/plan/testing/chaos-test.md` sebagai prosedur manual kalau scripting docker kill terlalu kompleks untuk environment CI) — skenario minimal:
   - **Skenario 1 — kill -9 mid-posting**: jalankan N `money_in`/`transfer_p2p` paralel (script `curl` loop di background), `kill -9` proses `seev-server` di tengah jalan, restart, ulang loop. Assert setelah semua selesai: `fn_verify_ledger_balance()` kosong, saldo akhir = jumlah net yang berhasil (compare dengan expected dari daftar request yang mendapat HTTP 2xx).
   - **Skenario 2 — broker down**: `docker stop seev-rabbitmq-1` selama 2 menit sambil traffic terus jalan (posting HARUS tetap sukses — outbox pattern memisahkan posting dari publish), lalu `docker start` — assert semua outbox event `pending`/`failed` (bukan `dead`, kalau dalam 2 menit belum sampai 5x percobaan dengan backoff dari T2) akhirnya `published`.
   - **Skenario 3 — Postgres restart**: `docker restart seev-postgres-1` di tengah traffic — assert request yang sedang berjalan gagal dengan error jelas (bukan hang selamanya, berkat timeout T5-11), request setelah Postgres pulih kembali sukses, tidak ada partial write (transaksi yang gagal tidak meninggalkan entries tanpa header atau sebaliknya — cek dengan query `ledger_transactions` yang `status='pending'` lebih lama dari wajar seharusnya nol setelah semua selesai, karena `execTransfer` selalu mark posted/failed di transaksi yang sama).
   - **Skenario 4 — Redis down** (relevan setelah T1): `docker stop seev-redis-1` (kalau `REDIS_ENABLED=true`) — assert server TETAP menerima traffic (rate limit fail-open, sudah desain existing), verifier tetap jalan pakai memory lock fallback SETELAH restart proses (catatan: kalau Redis mati saat proses SEDANG jalan dengan RedisLock aktif, verifier scheduler tidak otomatis pindah ke MemoryLock — itu hanya dipilih saat `NewModule` construction. Dokumentasikan ini sebagai limitasi yang diterima, bukan bug: restart proses adalah mitigasi untuk Redis yang mati lama).
2. Setiap skenario harus punya assertion otomatis (script query `fn_verify_ledger_balance`/`v_account_balance_audit` via `psql`/`docker exec`, bandingkan dengan expected) — bukan cuma observasi manual.

### DoD
- [x] Keempat skenario dijalankan minimal sekali, hasil didokumentasikan (bisa sebagai output log yang disimpan, atau ringkasan di PR/commit message saat mengerjakan task ini).
- [x] Tidak ada skenario yang menghasilkan `fn_verify_ledger_balance()` non-kosong.
- [x] Skenario 3 membuktikan tidak ada partial write.

### Hasil eksekusi (2026-07-11)

Dijalankan via `scripts/chaos-test.sh {1,2,3,4}` melawan Postgres/RabbitMQ/Redis asli (docker compose, dengan `postgres` host port di-remap sementara ke 5433 lalu dikembalikan ke 5432 setelah selesai — mesin dev punya Postgres native yang sudah memakai 5432). Setiap skenario mem-build ulang `cmd/server`, provision 2 akun user via SQL langsung, mendanai akun pengirim lewat `money_in` asli di internal router (bukan `UPDATE` mentah, supaya `v_account_balance_audit` tetap konsisten), lalu menjalankan skenarionya.

- **Skenario 1 (kill -9 mid-posting)**: 40 `transfer_p2p` paralel, `kill -9` di tengah batch. Semua 40 request in-flight gagal bersih (connection reset, HTTP code `000`), server di-restart, semua 40 di-retry dengan idempotency key yang sama → semua `201`. `fn_verify_ledger_balance()` = 0 baris, `v_account_balance_audit` konsisten, tidak ada `ledger_transactions` yang stuck `pending`.
- **Skenario 2 (broker down)**: `rabbitmq` di-stop, 10 posting saat broker mati — semua tetap `2xx` (outbox memisahkan posting dari publish). Broker di-restart, seluruh outbox event akhirnya `published` (tidak ada yang `dead`), ledger tetap seimbang.
- **Skenario 3 (Postgres restart mid-traffic)**: 20 request paralel, `docker restart` Postgres di tengah jalan. 10 request yang sempat masuk sebelum restart sukses (`201`), 10 yang kena saat Postgres down gagal cepat dengan `500` (bukan hang — dibuktikan tidak ada yang menyentuh client timeout 20s), posting setelah Postgres pulih sukses lagi (`201`). Tidak ada `ledger_transactions` stuck `pending` — bukti tidak ada partial write.
- **Skenario 4 (Redis down)**: `redis` di-stop, posting tetap `201` (rate limiter fail-open). Proses di-restart sambil Redis masih mati (memilih fallback in-memory saat construction) — posting tetap `201`. Ledger tetap seimbang dan konsisten.

Semua empat skenario: `fn_verify_ledger_balance()` kosong dan `v_account_balance_audit` konsisten di akhir setiap run.

---

## Urutan Pengerjaan
T1 duluan (paling berdampak pada cost/deployment). T2 → T3 (T3 butuh T2's `next_attempt_at` sudah ada agar replay konsisten). T4, T5, T6 independen. T7 dikerjakan PALING AKHIR — butuh T1, T2 sudah selesai supaya skenario 2 dan 4 punya perilaku final yang benar untuk diuji.

## Verifikasi Akhir Fase 2c
```bash
go build ./...
make lint
make test
go test -tags=integration -race ./...
```
Jalankan T7 chaos scripts secara manual dengan Docker aktif sebagai bukti akhir fase ini selesai.
