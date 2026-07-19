# 45 — Track A3: Resiliensi Dependensi Eksternal — Outbox Perintah Vendor, Breaker Terdistribusi, Hot-Swap Redis, Adapter Xendit

> Lahir dari track **A3 ★** di [42-long-term-roadmap.md](42-long-term-roadmap.md).
>
> **Status verifikasi: SIAP DIEKSEKUSI (2026-07-17).** Semua fakta kode di
> dokumen ini (path, line reference, nama identifier, sequence migrasi) sudah
> diverifikasi langsung terhadap repo pada tanggal tersebut. Fakta EKSTERNAL
> (bentuk API Xendit, perilaku versi go-redis) sengaja TIDAK ditulis detail —
> eksekutor wajib memverifikasinya saat eksekusi (lihat §6). Line reference
> bergeser seiring kode berubah; verifikasi dengan grep, jangan percaya angka
> baris secara buta.

## 1. Trigger dan tujuan

Bukti trigger (pola doc 42 §2 poin 1, jalur trigger belajar):

- **[40](40-phase7e-vendor-resilience.md) selesai** — breaker per-proses,
  routing multi-kandidat, aturan failover berbasis bukti, dan chaos scenario 8
  (vendor failover end-to-end) semuanya hijau; terverifikasi ulang dalam
  `make verify-full` penuh 2026-07-17 (GATE 3 doc 43).
- **Keputusan sadar 2026-07-17**: user memilih mengaktifkan A3 sebagai track
  ketiga (setelah A1 selesai via [43](43-a1-observability.md), A2 ditulis via
  [44](44-a2-ci-pipeline.md)), dengan dua keputusan desain diambil eksplisit
  lewat sesi tanya-jawab: vendor sandbox = **Xendit** (K4), semantics
  kematian Redis = **hot-swap selektif** (K3).

Tujuan bisnis (dari track A3): uang riil bergerak lewat vendor riil; vendor
atau Redis mati tidak boleh menghilangkan uang dan tidak boleh membutuhkan
restart manual. Empat hutang terdokumentasi dilunasi track ini:

| Hutang | Sumber | Dilunasi oleh |
|---|---|---|
| "Real vendor adapter to replace internal/vendorgw/mockvendor" | PROJECT_GUIDE.md future work | T4 |
| "Transactional payout outbox for vendor commands" | PROJECT_GUIDE.md future work | T1 |
| "A distributed (Redis-backed) circuit breaker" | PROJECT_GUIDE.md deferral doc 36/40 | T2 |
| Semantics kematian Redis (no hot-swap; fraud velocity Redis-only) | limitasi doc 12 §T1, doc 34 §gap-4 | T3 |

## 2. Fakta repo saat dokumen ditulis

Semua diverifikasi 2026-07-17. Ini baseline yang diubah task-task di §5.

**Jalur dispatch vendor payout (target T1):**

- Vendor `Submit` dipanggil INLINE di `internal/payout/orchestrate.go` (fungsi
  `submit()`, panggilan `provider.Submit` di sekitar baris 233), di LUAR
  transaksi DB apa pun: `TransitionToSubmitted` (statement sendiri) → network
  call telanjang → `recordVendorCall` (statement sendiri) → breaker
  `RecordFailure`/`RecordSuccess` → transisi terminal (`SetError` /
  `TransitionToVendorPending` / `settle`). Tidak ada command row durable,
  tidak ada relay.
- Failover loop (`maxFailoverAttempts = 20`), klasifikasi outcome
  (`classifySubmitOutcome`: `callErr != nil → uncertain`; `PayoutFailed` tanpa
  error → `rejected`; selainnya `accepted`), dan aturan pinning (`mayFailover`:
  ada call `accepted` ATAU `uncertain` = PINNED ke vendor itu selamanya)
  semuanya berjalan sinkron di request path.
- `Create` SUDAH menelan submit error ("initial submit failed, resume job will
  retry") — kontrak API tidak pernah menjanjikan hasil vendor sinkron; klien
  yang benar melakukan polling status. Full-async via outbox adalah kelanjutan
  alami, bukan perubahan kontrak fundamental.
- Resume job (`internal/payout/worker/resume.go`): cron `"* * * * *"` (tiap
  menit), `WithJobTimeout(30s)`, re-drive semua status non-terminal
  (`created`→hold+submit; `held`/`submitted`→submit; `vendor_pending`→poll).
- Migrasi payout berikutnya: `migrations/payout/000006_*` (terakhir
  `000005_vendor_call_outcome`).

**Referensi outbox yang di-reuse (cetak biru T1):** ledger outbox —
`internal/ledger/worker/outbox_relay.go` (empat loop: poll `ClaimPending`,
retry `ClaimFailedForRetry`, reaper `ReapStuck`, gauge; claim pakai
`FOR UPDATE SKIP LOCKED`; eksplisit aman multi-replika) +
`internal/ledger/repository/outbox_event_repository.go` (`InsertEvents(ctx,
tx, ...)` menerima `*sql.Tx` — dipanggil DALAM transaksi yang sama dengan
posting, `internal/ledger/service/handle/service.go` langkah "11. OUTBOX
(same transaction)") + status `pending|failed|dead` + admin replay.

**Breaker (`internal/vendorgw/breaker.go`, target T2):** state per-vendor
`{state, consecutiveFailures, openedAt, lastProbeAt}` dijaga mutex per-vendor.
Jaminan "exactly one probe" saat half-open ditegakkan MURNI oleh mutex: caller
pertama yang menang lock setelah cooldown lewat men-transisi open→half_open
dan mendapat `true`; sisanya melihat half_open dan mendapat `false`. Limitasi
per-proses terdokumentasi eksplisit di doc comment ("a documented, ACCEPTED
limitation... slower convergence than a shared (e.g. Redis-backed) breaker
would give") dan di deferral doc 36. Interface publik: `Allow(vendor) bool`,
`RecordSuccess(vendor)`, `RecordFailure(vendor)`, `Snapshot() []VendorHealth`.
Preseden operasi Redis atomik di repo: `pkg/scheduler`'s `RedisLock`
(`SetNX` + Lua compare-and-delete unlock).

**Lima konsumen Redis + failure semantics hari ini (target T3):**

| # | Konsumen | Service | Redis mati mid-flight | Memory twin? |
|---|---|---|---|---|
| 1 | Rate limiter (`pkg/cache/rate_limiter.go`) | ledger public router | fail OPEN (request lolos) | ADA (`MemoryRateLimiter`) |
| 2 | Policy velocity counter (`pkg/cache/counter.go`) | ledger (`internal/policy`) | fail OPEN + alert | ADA (`MemoryCounter`) |
| 3 | Scheduler lock (`pkg/scheduler` `RedisLock`) | ledger (5 job) + payout (resume) | job SKIP tick (tidak jalan unguarded) | ADA (`MemoryLock`) |
| 4 | **Fraud velocity** (`internal/fraud/velocity_store.go`, Redis **DB 1**) | fraud | Screen error → SEMUA caller fail open (screening jadi no-op diam-diam); velocity consumer nack/requeue | **TIDAK ADA** |
| 5 | `pkg/cache` Cacher | (semua) | — | — (tidak ada data aplikasi di-cache di produksi; peran nyata = health probe + penyedia `*redis.Client`) |

- Backend semua konsumen dipilih SAAT KONSTRUKSI — tidak ada hot-swap di
  manapun (limitasi doc 12 §T1: "restart proses adalah mitigasi").
- fraud-service HARDCODE Redis wajib saat boot (`cmd/fraud-service/main.go`
  set `cfg.Redis.Enabled = true; cfg.Redis.DB = 1`, mengabaikan
  `REDIS_ENABLED`) — Redis down saat start = service menolak start (carve-out
  sengaja, doc 34 §gap-4).
- Idempotency = Postgres (unique `idempotency_key`), BUKAN Redis — tidak
  tersentuh track ini.

**Chaos suite:** 8 scenario; scenario 4 satu-satunya yang mematikan Redis dan
hanya membuktikan fail-open + restart-dengan-`REDIS_ENABLED=false`, BUKAN
hot-swap. Scenario 8 (vendor failover) asersinya sinkron — berubah karena T1.
Dispatch: `case "${1:-}"` + blok `all)` di ujung `scripts/chaos-test.sh`;
scenario baru = fungsi `scenario_N()` + case arm + baris di `all)`.

**Registrasi vendor + config:** composition root
(`cmd/payout-service/main.go` `registry.AddPayout(mockvendor...)`,
`cmd/payin-service/main.go` `registry.AddPayin(mockvendor...)`) di belakang
flag `VendorConfig` (`VENDOR_MOCKVENDOR_ENABLED/SECRET`,
`MOCKVENDOR2_ENABLED/SECRET`); validasi enabled-but-empty-secret fatal.
Interface adapter: `vendorgw.PayoutProvider` (`Vendor()`, `Submit(ctx,
idempotencyKey, amount, currency, destination) (PayoutResult, error)`,
`Query(...)`) dan `vendorgw.PayinVerifier` (`Vendor()`,
`VerifyAndParse(headers, rawBody) (*PayinEvent, error)`). Payin TIDAK pernah
memanggil vendor outbound (webhook-receipt only) — adapter payin hanya
verifier, tidak tersentuh outbox T1.

## 3. Anti-scope

Disalin dari track A3 doc 42 + turunan dokumen ini:

- Bukan onboarding vendor produksi ber-KYB — sandbox only.
- Screening AML vendor ada di track A4, bukan di sini.
- Bukan Redis multi-region/cluster — satu Redis Compose seperti sekarang.
- Adapter Xendit TIDAK pernah masuk jalur CI/`verify-full` — mockvendor tetap
  vendor untuk semua gate; sandbox riil diverifikasi terpisah (K4).
- TIDAK ada kredensial Xendit di repo/compose/`.env.example` (placeholder
  kosong + komentar cara mendapatkannya saja).
- TIDAK mengubah aturan anti-double-payout berbasis bukti doc 40
  (`payout_vendor_calls` + `mayFailover`) — hanya MEMINDAHKAN tempat
  eksekusinya.
- TIDAK mengubah `execTransfer` ledger (PROJECT_GUIDE.md hard rule #5), skema tabel
  existing, RLS, atau `pkg/messaging`.

## 4. Keputusan desain terkunci

### K1 — Outbox perintah vendor payout: full async, relay memiliki dispatch

Tabel baru `payout_vendor_commands` (migrasi `migrations/payout/000006_vendor_commands`):

```sql
id UUID PK, payout_request_id UUID NOT NULL REFERENCES payout_requests(id),
vendor TEXT NOT NULL, attempt INT NOT NULL DEFAULT 1,
status TEXT NOT NULL DEFAULT 'pending'
  CHECK (status IN ('pending','dispatched','failed','dead')),
next_retry_at TIMESTAMPTZ, retry_count INT NOT NULL DEFAULT 0,
last_error TEXT, created_at/updated_at TIMESTAMPTZ
-- + index (status, next_retry_at); RLS pola payout_requests existing
```

- `InsertCommand(ctx, tx, ...)` dipanggil DALAM TRANSAKSI YANG SAMA dengan
  `TransitionToSubmitted` — menggantikan `provider.Submit` inline. Ini persis
  pola `OutboxRepository.InsertEvents(ctx, tx, ...)` ledger.
- Relay worker baru (`internal/payout/worker/`, di-clone dari pola
  `internal/ledger/worker/outbox_relay.go`): claim `FOR UPDATE SKIP LOCKED`,
  poll interval ~1s, backoff eksponensial + jitter untuk retry, `dead` setelah
  max retry, reaper untuk `dispatched` yang macet, aman multi-replika.
- SEMUA logika yang hari ini inline di `submit()` PINDAH ke relay dispatch:
  `provider.Submit` → `classifySubmitOutcome` → `recordVendorCall` → breaker
  `Record*` → transisi terminal. Failover: outcome `rejected` + `mayFailover`
  masih true → resolve kandidat berikutnya (exclusion list) → `SetVendor` →
  enqueue command baru `attempt+1`; cap `maxFailoverAttempts` existing.
- **Invarian yang TIDAK berubah**: idempotency Submit by
  `payout_requests.id` (vendor dipanggil dengan key yang sama berapa kali pun
  command di-retry); pinning setelah `accepted`/`uncertain` (`mayFailover`
  tidak disentuh); `payout_vendor_calls` tetap satu-satunya sumber bukti;
  gagal tulis `recordVendorCall` = berhenti tanpa transisi state (perilaku
  existing).
- **Perubahan perilaku yang DIAKUI eksplisit**: `POST /api/v1/payout` kembali
  setelah hold + enqueue — `status="submitted"` kini berarti "perintah
  terkirim durable ke antrean", hasil vendor menyusul (relay poll ~1s).
  `error_message` hasil vendor tidak lagi ada di response Create. Asersi
  business-e2e dan chaos 5/8 yang mengasumsi hasil vendor sinkron WAJIB
  diupdate (test churn yang diharapkan dan dicatat di Hasil — bukan regresi;
  pola polling `wait_for_payout_status` yang sudah ada tetap bekerja).
- **Batas kepemilikan relay vs resume job** (wajib ditulis di kode):
  - Relay MEMILIKI: dispatch command `pending`/`failed`, retry backoff,
    failover, transisi `submitted → vendor_pending|settled|failed-side`.
  - Resume job MEMILIKI: re-drive `created` (hold ulang + enqueue command
    pertama kalau belum ada), re-drive `held` tanpa command hidup (enqueue),
    polling `vendor_pending` (`Query` + klasifikasi existing), gauge stuck.
  - Resume job BERHENTI memanggil `submit()`/`provider.Submit` langsung —
    satu-satunya jalan ke vendor untuk Submit adalah relay. `RunNow` (dipakai
    chaos) menjalankan satu pass relay + satu pass resume.

### K2 — Breaker terdistribusi: Redis-backed, fallback lokal per-proses

- State per-vendor pindah ke Redis: hash `vendorgw:breaker:<vendor>` berisi
  `state`, `failures`, `opened_at`; probe token TERPISAH
  `vendorgw:breaker:<vendor>:probe` (SETNX + TTL = jaminan single-probe
  LINTAS REPLIKA — preseden `scheduler.RedisLock`).
- SEMUA transisi via Lua script atomik (record-failure yang increment +
  open-kalau-threshold; allow yang cek cooldown + CAS open→half_open +
  klaim probe token; record-success yang reset). Tidak ada
  read-modify-write non-atomik dari Go.
- Interface `Allow`/`RecordSuccess`/`RecordFailure`/`Snapshot` TIDAK berubah —
  call site (payout submit path → relay pasca-T1, routing payin/payout, admin
  health) tidak tersentuh.
- **Fallback saat Redis unreachable**: degrade otomatis per-panggilan ke state
  in-memory lokal (implementasi `HealthTracker` existing dipertahankan sebagai
  fallback tertanam), log WARN sekali per transisi degrade/recover (bukan per
  call), metric sumber state. Saat Redis pulih: kembali membaca/menulis Redis;
  state lokal selama outage TIDAK di-merge (diterima — konsisten sifat breaker
  sebagai availability optimization, doc 40).
- Config: `BREAKER_FAILURE_THRESHOLD`/`BREAKER_COOLDOWN` existing tetap;
  `BREAKER_DISTRIBUTED` baru (default `true` saat Redis tersedia, `false`
  memaksa perilaku lama). fraud-service tidak memakai breaker (tidak ada
  vendor call) — hanya payin/payout yang tersentuh.
- Metric: `vendorgw_breaker_state{vendor}` tetap; tambah
  `vendorgw_breaker_backend{backend="redis|local"}` gauge (atau label —
  eksekutor pilih satu, dokumentasikan; jaga cardinality: 2 nilai).

### K3 — Hot-swap selektif Redis (keputusan user 2026-07-17)

Prinsip: hot-swap HANYA untuk primitif yang aman di-fallback per-replika;
yang tidak aman tetap degrade eksplisit + alert. Tiga primitif di-hot-swap:

1. **Rate limiter** (Redis↔Memory) — wrapper failover baru di `pkg/cache`:
   health-probe periodik (interval ~5s) memutuskan backend aktif; saat Redis
   down → `MemoryRateLimiter` existing mengambil alih TANPA restart; saat
   Redis pulih → kembali. Divergensi bucket saat window transisi DITERIMA
   (limiter bukan kontrol finansial — konsisten doc 12).
2. **Policy velocity counter** (Redis↔Memory) — wrapper yang sama atas
   `Counter`; `MemoryCounter` existing sebagai twin. Fail-open existing di
   `internal/policy` TETAP sebagai lapisan terakhir.
3. **Fraud velocity** — `MemoryVelocityStore` BARU di `internal/fraud`
   (twin `RedisVelocityStore`: dedup event-id + counter per-jam + TTL, semua
   in-memory dengan GC; per-replika = approximation, terdokumentasi LEBIH
   LEMAH dari Redis tapi BUKAN no-op seperti hari ini) + wrapper failover.
   fraud-service BERHENTI hardcode Redis wajib: Redis down saat boot → start
   degraded (memory store) + log ERROR + alert, bukan menolak start.

Yang TIDAK di-hot-swap (keputusan eksplisit, bagian dari kontrak K3):

- **Scheduler lock**: skip-tick tetap (job idempotent; skip sudah graceful;
  memory lock di multi-replika = job jalan ganda di semua replika — lebih
  buruk dari skip). Ditambah metric `scheduler_job_skips_total{job}` counter +
  alert saat skip beruntun > N tick (K7).
- **Breaker**: fallback-nya state lokal (K2), bukan wrapper generik ini.

Semantik pemulihan: semua wrapper kembali ke Redis otomatis saat health-probe
hijau; nilai yang terkumpul di memory selama outage TIDAK di-migrate balik
(counter/limiter/velocity semuanya windowed + availability-oriented; jendela
divergensi ≈ durasi outage + 1 window, didokumentasikan di doc comment).

### K4 — Adapter Xendit: config-gated, di luar jalur CI selamanya

- Package baru `internal/vendorgw/xendit/`: `PayoutProvider` (Disbursement
  API; idempotency via external-id = `payout_requests.id` — kontrak SAMA
  dengan mockvendor) + `PayinVerifier` (verifikasi callback-token header
  webhook Xendit → `vendorgw.PayinEvent`).
- Pemetaan klasifikasi WAJIB mengikuti kontrak vendorgw: HTTP 5xx/timeout/
  transport error → return `error` non-nil (= `uncertain`, breaker trip);
  respons sukses dengan status gagal-bisnis definitif → `PayoutResult{Status:
  PayoutFailed}` + nil error (= `rejected`, boleh failover); status
  antara/processing → `PayoutPending`.
- Config (`internal/config` `VendorConfig`, pola mockvendor):
  `VENDOR_XENDIT_ENABLED` (default **false**), `VENDOR_XENDIT_SECRET_KEY`,
  `VENDOR_XENDIT_WEBHOOK_TOKEN`, `VENDOR_XENDIT_BASE_URL` (default URL API
  publik Xendit — eksekutor verifikasi). Validasi enabled-but-empty fatal
  (pola existing). Registrasi di kedua composition root di belakang flag.
- Verifikasi = integration test env-gated: `XENDIT_SANDBOX_TEST=1` +
  kredensial nyata di env; tanpa itu `t.Skip`. TIDAK pernah jalan di
  `make test`, CI, atau `verify-full`. Satu disbursement sandbox end-to-end
  (Submit → poll Query sampai terminal) dijalankan manual saat eksekusi dan
  hasilnya dicatat di Hasil T4.
- **Eksekutor WAJIB memverifikasi bentuk API Xendit TERKINI saat eksekusi**:
  endpoint create-disbursement, field idempotency/external-id, enumerasi
  status dan pemetaannya ke `PayoutStatus`, skema + header verifikasi webhook,
  format error 4xx/5xx. Dokumen ini sengaja hanya menulis KONTRAK yang harus
  dipenuhi, bukan detail API — fakta eksternal bergerak (pelajaran doc 43).
  User perlu mendaftar akun sandbox Xendit sendiri untuk mendapat kredensial.

### K5 — Chaos scenario baru + update scenario existing

- **Scenario 9 — Redis mati: hot-swap tanpa restart** (bukti K3): traffic
  normal → `docker stop` Redis → rate-limited route tetap jalan (limiter
  memory), posting dengan policy limit tetap benar (counter memory), fraud
  Screen TETAP menghitung velocity (memory twin — dibedakan eksplisit dari
  fail-open no-op hari ini: asersi velocity count naik saat Redis mati) →
  `docker start` Redis → asersi primitif kembali ke Redis, SEMUA TANPA
  restart proses. Ledger balanced di akhir.
- **Scenario 10 — Breaker terdistribusi lintas replika** (bukti K2): start
  payout-service KEDUA di port alternatif (env port override lib.sh existing)
  → force-fail vendor → trip breaker via replika A → asersi replika B
  melaporkan vendor `open` dari Redis TANPA pernah mengalami kegagalan lokal
  (admin health kedua replika) → Redis `docker stop` → kedua replika degrade
  ke state lokal (metric backend) tanpa crash.
- **Scenario 11 — Durabilitas command outbox** (bukti K1, perluasan kill
  matrix scenario 5): crash payout-service (kill -9) SETELAH command
  ter-enqueue SEBELUM relay dispatch → restart → relay dispatch TEPAT SEKALI
  (satu `payout_vendor_calls` row, satu settle, saldo benar).
- Update existing: scenario 4 ditambah asersi hot-swap (menggantikan
  narasi restart-only untuk primitif yang kini di-hot-swap; jalur restart
  `REDIS_ENABLED=false` tetap diuji untuk scheduler lock); scenario 8
  disesuaikan asersi async (polling status menggantikan asersi respons
  sinkron; substansi failover/pinning TIDAK berubah).
- Dispatch: `scenario_9/10/11()` + case arm + baris `all)`. Fault injection
  pakai helper lib.sh existing; perbaikan lifecycle di lib.sh, bukan
  duplikasi per-scenario (aturan PROJECT_GUIDE.md).

### K6 — Yang TIDAK berubah

`execTransfer` ledger; aturan pinning + bukti anti-double-payout doc 40;
mockvendor/mockvendor2 sebagai vendor semua gate; alur payin (tidak ada
outbound, tidak kena outbox); skema tabel existing + RLS; `pkg/messaging`;
boundary map (`internal/vendorgw` tetap shared library payin+payout saja).

### K7 — Observability wajib untuk semua yang baru (nyambung doc 43)

- Metric baru: `payout_vendor_commands_pending`/`_dead` gauge (pola
  `ledger_outbox_pending`/`_dead_total`), `vendorgw_breaker_backend`,
  counter transisi hot-swap per primitif, `scheduler_job_skips_total{job}`.
  Semua low-cardinality (label vendor/job/backend dari allowlist internal,
  tidak pernah dari input request).
- Dashboard `money-flow.json`: row baru "Payout command outbox" (pending,
  dead, dispatch rate) + panel backend breaker/hot-swap.
- Alert Grafana baru (pola `seev-op-*` doc 43 T5, annotation `runbook` wajib):
  command `dead` > 0 (critical → `docs/runbooks/reconciliation.md`), hot-swap
  aktif berkepanjangan > 10 menit (warning), skip-tick scheduler beruntun
  (warning). Recording/alert rule di file provisioning existing.

## 5. Task eksekusi

Urutan sengaja: T1 dulu (memindahkan call site breaker ke relay), T2 breaker
(di call site barunya), T3 hot-swap (butuh fallback breaker T2), T4 adapter
(independen, paling aman terakhir sebelum chaos), T5 chaos+observability.
Setiap task diakhiri bagian `### Hasil` diisi bukti nyata (pola doc 43).
Satu commit per task.

### T1 — Outbox perintah vendor (K1)

**Langkah**

1. Migrasi `migrations/payout/000006_vendor_commands.{up,down}.sql` — tabel
   `payout_vendor_commands` sesuai K1 + RLS pola `payout_requests`.
2. Repository command (`internal/payout/repository/`): `InsertCommand(ctx,
   tx, ...)`, `ClaimPending`, `ClaimFailedForRetry`, `MarkDispatched`,
   `MarkFailed` (backoff + dead), `ReapStuck`, `CountByStatuses` — pola
   `outbox_event_repository.go` ledger; regenerate mock.
3. Relay worker `internal/payout/worker/` — pola `OutboxRelay` ledger; logika
   dispatch = pindahan `submit()` (klasifikasi, recordVendorCall, breaker,
   failover-enqueue, transisi terminal).
4. Refactor `orchestrate.go`: `Create` → hold → (dalam tx TransitionToSubmitted)
   InsertCommand; `submit()` inline dihapus/dirampingkan; resume job berhenti
   memanggil Submit langsung (batas kepemilikan K1); `RunNow` chaos = pass
   relay + pass resume.
5. Update asersi `scripts/business-e2e.sh` + chaos scenario 5/8 ke pola
   polling (perubahan perilaku K1 yang diakui).

**Test wajib**

- Unit: klasifikasi outcome di relay (uncertain/rejected/accepted → state +
  breaker + pinning benar), failover enqueue attempt+1 dengan exclusion,
  idempotency dispatch (command diproses dua kali → vendor tetap dipanggil
  dengan key sama → satu efek), command dead setelah max retry.
- Integration (tag `integration`): insert command + crash sebelum dispatch →
  relay pass berikutnya dispatch tepat sekali.
- `make verify-full` HIJAU dari volume bersih — **GATE 1** (perubahan
  perilaku terbesar track ini; jangan lanjut T2 sebelum hijau).

**DoD**: tidak ada `provider.Submit` di luar relay; payout end-to-end tetap
settle lewat jalur async; semua invarian doc 40 terbukti tidak berubah.

### Hasil

> Diisi saat T1 selesai.

### T2 — Breaker terdistribusi (K2)

**Langkah**

1. Implementasi state Redis + Lua scripts (record-failure/allow-probe/
   record-success) + probe token SETNX TTL di `internal/vendorgw`.
2. Fallback lokal tertanam + log transisi degrade/recover + metric backend.
3. Config `BREAKER_DISTRIBUTED` + wiring composition root payin/payout.
4. Script debug dua-replika (pola `make chaos-debug`): start dua
   payout-service, trip via A, baca health B.

**Test wajib**

- Unit (miniredis atau testcontainers-redis): state machine penuh lintas dua
  instance HealthTracker yang berbagi Redis; single-probe under concurrency
  (N goroutine `Allow` bersamaan saat cooldown lewat → tepat SATU true);
  Redis down mid-call → degrade lokal tanpa error ke caller; pulih → kembali.
- Manual dua-replika: bukti replika B melihat open tanpa gagal lokal (dicatat
  di Hasil).
- `make test` + vet dua tag hijau.

**DoD**: breaker converge lintas replika via Redis; Redis mati tidak pernah
membuat vendor call gagal karena breaker error (degrade transparan).

### Hasil

> Diisi saat T2 selesai.

### T3 — Hot-swap selektif (K3)

**Langkah**

1. Wrapper failover `pkg/cache` (health-probe periodik, backend aktif atomik)
   untuk `Limiter` dan `Counter`; wiring ledger router + policy engine.
2. `MemoryVelocityStore` + wrapper di `internal/fraud`; fraud-service boot
   degraded (hapus hardcode wajib-Redis) + alert.
3. Metric `scheduler_job_skips_total` + counter transisi hot-swap (K7
   sebagian; alert/dashboard lengkapnya di T5).

**Test wajib**

- Unit: transisi Redis→memory→Redis bolak-balik untuk ketiga primitif
  (termasuk velocity dedup + counter tetap benar di memory); fraud-service
  boot tanpa Redis → degraded, Screen tetap menghitung.
- Integration: matikan Redis container di tengah test → limiter/counter/
  velocity pindah memory tanpa error request.
- `make verify-full` HIJAU dari volume bersih — **GATE 2**.

**DoD**: tidak ada primitif hot-swap yang butuh restart saat Redis mati/pulih;
fraud-service bisa hidup tanpa Redis (degraded, ber-alert, BUKAN no-op).

### Hasil

> Diisi saat T3 selesai.

### T4 — Adapter Xendit (K4)

**Langkah**

1. **Verifikasi fakta eksternal dulu** (wajib, sebelum menulis kode): bentuk
   API disbursement Xendit terkini, mekanisme idempotency, enumerasi status,
   skema webhook + verifikasi token. Catat temuan di Hasil.
2. `internal/vendorgw/xendit/`: `PayoutProvider` + `PayinVerifier` sesuai
   kontrak K4; tidak ada logika bisnis payout/payin di adapter.
3. Config + validasi + registrasi behind flag; `.env.example` placeholder
   kosong + komentar cara daftar sandbox.
4. Integration test env-gated (`XENDIT_SANDBOX_TEST=1`); verifikasi manual
   satu disbursement sandbox end-to-end.

**Test wajib**

- Unit (httptest server memalsukan respons Xendit): pemetaan status →
  `PayoutStatus`, klasifikasi 5xx/timeout → error (uncertain) vs business
  fail → `PayoutFailed`+nil, verifikasi webhook token benar/salah.
- Sandbox nyata: satu Submit + Query sampai terminal (env-gated, manual).
- `make test` hijau TANPA kredensial (semua sandbox test ter-skip);
  `docker compose config` valid tanpa env Xendit.

**DoD**: `VENDOR_XENDIT_ENABLED=false` (default) = zero perubahan perilaku;
enabled + kredensial valid = disbursement sandbox nyata jalan lewat pipeline
outbox yang sama dengan mockvendor.

### Hasil

> Diisi saat T4 selesai.

### T5 — Chaos, observability, dan dokumentasi (K5/K7)

**Langkah**

1. Scenario 9/10/11 baru + update scenario 4/8 sesuai K5.
2. Metric/panel/alert K7 lengkap (dashboard money-flow, alert provisioning,
   annotation runbook); satu kalimat sumber alert baru di runbook terkait
   (pola doc 43 T5 — jangan tulis ulang runbook).
3. Update PROJECT_GUIDE.md: future-work list (hapus tiga butir yang lunas: real
   vendor adapter, transactional payout outbox, distributed breaker), runbook
   "fraud-service down" (fail-open kini ditambah memory velocity twin),
   runbook "payout-service down" (relay). README root bagian vendor/Redis.

**Test wajib**

- `./scripts/chaos-test.sh all` hijau (11 scenario) dari volume bersih.
- Alert baru terbukti firing + resolve sekali secara sintetis (pola doc 43
  T5: kondisi nyata, bukan threshold diturunkan permanen).
- `make verify-full` HIJAU dari volume bersih — **GATE 3/final**.

**DoD**: semua perbaikan track punya chaos scenario yang membuktikannya;
observability paritas dengan doc 43; dokumentasi hutang ter-update.

### Hasil

> Diisi saat T5 selesai.

## 6. Constraint eksekutor

1. Boleh breakdown task jadi sub-langkah; DILARANG mengubah K1–K7 tanpa
   kembali ke user.
2. Do-not-touch: `execTransfer` (PROJECT_GUIDE.md hard rule #5); `mayFailover` +
   aturan bukti doc 40; lifecycle `scripts/lib.sh` (perbaikan DI lib.sh,
   bukan duplikasi per-script); RLS; `pkg/messaging`.
3. Fakta eksternal WAJIB diverifikasi saat eksekusi, jangan percaya memori
   model: bentuk API Xendit (T4 langkah 1), fitur Lua/versi go-redis yang
   dipakai (T2), perilaku miniredis vs Redis nyata untuk Lua (kalau miniredis
   tidak mendukung script yang dipakai → testcontainers-redis).
4. Kredensial (Xendit atau apapun) TIDAK PERNAH masuk repo, compose, log,
   atau test fixture — env lokal saja; log adapter tidak boleh memuat body
   request/respons mentah (pola `req_summary` existing).
5. Setiap gate wajib `docker compose down -v` dulu (gotcha #1 PROJECT_GUIDE.md);
   `make verify-full` adalah bentuk gate kanonik.
6. Jangan menurunkan test/threshold untuk membuat gate hijau; override
   sintetis sementara wajib dikembalikan dan dibuktikan lewat diff
   (constraint #9 doc 43).
7. Metric/label baru wajib low-cardinality (nilai dari allowlist internal,
   tidak pernah dari input request) — audit upper bound series sebelum merge
   (pola K5 doc 43).
8. Jika implementasi butuh file/perilaku di luar task ini: berhenti, update
   dokumen ini dulu — jangan improvisasi lintas boundary.

## 7. Definition of Done global

- [ ] `make lint`, `make test`, vet dua tag, dan `make verify-full` hijau
      dari volume bersih di ketiga gate.
- [ ] Chaos 11 scenario hijau; scenario 9/10/11 membuktikan hot-swap,
      breaker terdistribusi, dan durabilitas outbox secara nyata.
- [ ] Tidak ada `provider.Submit` di luar relay; invarian anti-double-payout
      doc 40 terbukti tak berubah (asersi settle-tepat-sekali di chaos).
- [ ] fraud-service hidup tanpa Redis (degraded + alert), semua primitif
      hot-swap pulih tanpa restart.
- [ ] Satu disbursement sandbox Xendit nyata terbukti end-to-end (manual,
      env-gated); default build/CI tidak pernah menyentuh Xendit.
- [ ] Observability paritas doc 43: metric/panel/alert baru ter-provision,
      alert terbukti firing+resolve.
- [ ] PROJECT_GUIDE.md future-work list dan runbook ter-update; tidak ada kredensial
      di repo.

## 8. Penutup setelah GATE 3

- [ ] Isi semua `### Hasil` dengan bukti command + output ringkas.
- [ ] Update baris plan 45 di [README](README.md) menjadi `✅ done`.
- [ ] Update status A3 di [42](42-long-term-roadmap.md) menjadi selesai via 45.
- [ ] Catat: butir vendor AML/sanctions sengaja TIDAK dikerjakan di sini —
      diteruskan ke track A4 sesuai anti-scope.
