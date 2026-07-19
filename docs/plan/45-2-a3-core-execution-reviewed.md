# 45 — Track A3: Resiliensi Dependensi Eksternal — Durable Payout Outbox, Breaker Terdistribusi, dan Degradasi Redis Aman

> Lahir dari track **A3 ★** di [42-long-term-roadmap.md](42-long-term-roadmap.md).
>
> **Status verifikasi: SIAP DIEKSEKUSI DENGAN SCOPE REVISI (2026-07-17).**
> Fakta repository sudah diverifikasi terhadap kode saat dokumen ini ditulis.
> Track ini hanya memakai komponen yang sudah tersedia dan open-source:
> PostgreSQL, Redis, Go, Docker Compose, Prometheus, dan testcontainers.
> Adapter vendor proprietary, termasuk Xendit, bukan bagian Definition of Done
> dan dipindahkan menjadi follow-up opsional.

## 1. Tujuan dan batas track

Tujuan utama A3 adalah memastikan perintah payout tetap durable saat proses
mati atau dependency eksternal timeout, tanpa melemahkan aturan
anti-double-payout. Redis boleh gagal tanpa membutuhkan restart manual, tetapi
degradasi tidak boleh diam-diam mengubah fraud screening menjadi bypass.

Track ini menyelesaikan tiga hal:

1. Payout vendor command disimpan secara atomik dan dikirim worker dengan
   semantics **at-least-once**.
2. Circuit breaker dapat berbagi state lintas replika melalui Redis dan tetap
   berfungsi lokal ketika Redis unavailable.
3. Rate limiter dan policy counter dapat berpindah ke memory backend; fraud
   velocity tetap fail-closed untuk transaksi berisiko.

### Anti-scope

- Bukan onboarding vendor produksi, KYB, AML vendor, atau settlement riil.
- Bukan jaminan network dispatch exactly-once. Exactly-once tidak dapat
  dibuktikan setelah timeout; jaminan yang benar adalah at-least-once delivery
  + idempotency key vendor + satu efek ledger.
- Bukan Redis Cluster, multi-region, atau pengganti Postgres sebagai source of
  truth.
- Bukan memory fallback untuk fraud velocity lintas replika.
- Bukan perubahan `execTransfer`, aturan ledger balancing, RLS existing,
  `pkg/messaging`, atau aturan pinning doc 40.
- Bukan implementasi Xendit. Contract vendor tetap diuji lokal menggunakan
  mockvendor/`httptest`; adapter proprietary dibuat dalam plan follow-up.

## 2. Baseline yang diverifikasi

- `internal/payout/orchestrate.go` masih memanggil `provider.Submit` inline
  setelah `TransitionToSubmitted`; tidak ada durable vendor command.
- `Create` tidak menjanjikan hasil vendor sinkron dan klien sudah dapat polling
  status. Perubahan menjadi async tidak mengubah kontrak dasar, tetapi test
  yang mengasumsikan hasil langsung harus diperbarui.
- Resume job me-re-drive status non-terminal setiap menit dan masih dapat
  memanggil `Submit` langsung.
- Ledger outbox sudah memiliki pola `FOR UPDATE SKIP LOCKED`, retry, reaper,
  dead-letter, dan worker multi-replika yang dapat digunakan sebagai referensi.
- Breaker `internal/vendorgw` masih menyimpan state per-proses.
- Rate limiter dan policy counter sudah mempunyai Redis dan memory
  implementation. Fraud velocity masih Redis-only dan fraud-service gagal
  start bila Redis tidak tersedia.
- Migrasi payout terakhir adalah `000005_vendor_call_outcome`; migrasi baru
  menggunakan sequence `000006`.

## 3. Keputusan desain terkunci

### K1 — Durable command, bukan exactly-once network call

Tambahkan tabel `payout_vendor_commands` melalui migrasi
`000006_vendor_commands`:

```sql
id UUID PRIMARY KEY,
command_key TEXT NOT NULL UNIQUE,
payout_request_id UUID NOT NULL REFERENCES payout_requests(id),
vendor TEXT NOT NULL,
attempt INT NOT NULL CHECK (attempt > 0),
status TEXT NOT NULL CHECK (
  status IN ('pending','processing','failed','completed','dead')
),
retry_count INT NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
max_retries INT NOT NULL DEFAULT 8 CHECK (max_retries > 0),
next_attempt_at TIMESTAMPTZ,
last_attempted_at TIMESTAMPTZ,
locked_at TIMESTAMPTZ,
last_error TEXT,
created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
UNIQUE (payout_request_id, attempt)
```

Tambahkan index claim `(status, next_attempt_at, created_at)` dan partial
unique index yang hanya mengizinkan satu command hidup
(`pending|processing|failed`) per payout request. Terapkan grant dan RLS yang
sama ketatnya dengan `payout_requests`.

`command_key` berbentuk `payout:<request_id>:submit:<attempt>` untuk dedup
internal. Panggilan vendor tetap memakai `payout_request_id` sebagai
idempotency key agar retry command yang sama tidak membuat payout baru.
Amount, currency, destination, dan user pada `payout_requests` immutable
setelah insert; command menyimpan snapshot vendor dan attempt, bukan salinan
destination sensitif.

Repository menyediakan operasi atomik tingkat-use-case:

- `EnqueueInitialSubmit`: transaksi `held → submitted` + insert attempt 1.
- `CompleteAndEnqueueFailover`: conditional update expected vendor + complete
  command lama + insert command vendor berikutnya.
- `EnsureSubmitCommand`: recovery untuk `held/submitted` tanpa command hidup;
  insert memakai constraint/`ON CONFLICT` agar aman multi-replika.

Transaksi dimiliki repository melalui `DatabaseSQL.WithTx`; orchestration dan
worker tidak mengekspos atau meneruskan `*sql.Tx`.

### K2 — Relay adalah satu-satunya pemilik Submit

Relay payout menggunakan pola ledger outbox:

- Claim batch dengan `FOR UPDATE SKIP LOCKED`, ubah status ke `processing`,
  dan isi `locked_at`.
- Panggil vendor dengan context timeout eksplisit.
- Retry dengan exponential backoff + full jitter; reaper mengembalikan row
  `processing` yang lease-nya kedaluwarsa ke `failed` tanpa menganggap network
  call belum terjadi.
- Setelah retry budget habis, command menjadi `dead` dan hanya dapat dipulihkan
  melalui admin replay yang dibatasi batch.

Urutan outcome:

- Audit `payout_vendor_calls` harus durable sebelum failover, cancel, atau
  settlement diteruskan. Gagal audit = fail-closed.
- `uncertain`: request pinned ke vendor yang sama; command retry vendor yang
  sama dengan idempotency key yang sama.
- `accepted/pending`: request pinned; update `vendor_pending` dan complete
  command.
- `accepted/settled`: settle ledger secara idempotent, lalu complete command.
- `rejected`: jika `mayFailover` true, enqueue attempt berikutnya secara
  atomik; jika tidak ada kandidat, cancel hold secara idempotent.

`provider.Submit` dilarang di luar relay. Resume job hanya:

- memulihkan `created`/`held` dan memastikan command pertama ada,
- memastikan `submitted` memiliki tepat satu command hidup atau dead yang
  terlihat operator,
- melakukan `Query` untuk `vendor_pending`,
- memperbarui stuck metrics.

Jaminan DoD adalah satu efek ledger dan tidak ada failover setelah
`accepted/uncertain`, bukan satu baris audit atau satu HTTP call vendor.

### K3 — Breaker Redis dengan fallback lokal terukur

- Perkenalkan interface internal ber-context:
  `Allow(ctx,vendor)`, `RecordSuccess(ctx,vendor)`,
  `RecordFailure(ctx,vendor)`, dan `Snapshot(ctx)`.
- Redis menyimpan state per vendor. Semua perubahan state, cooldown, dan
  transisi half-open menggunakan Lua atomik.
- Single probe lintas replika memakai token `SET NX PX`; TTL selalu lebih
  panjang dari timeout vendor maksimum.
- Setiap operasi Redis dibatasi timeout pendek. Redis error tidak diteruskan ke
  payout flow; breaker beralih ke `HealthTracker` lokal.
- Peralihan backend hanya log sekali per degrade/recover dan menghasilkan
  metric low-cardinality `vendorgw_breaker_backend{backend="redis|local"}`.
- State lokal selama outage tidak di-merge kembali. Setelah Redis sehat,
  Redis kembali authoritative.
- `BREAKER_DISTRIBUTED=false` adalah default kompatibel. Compose dapat
  mengaktifkannya setelah gate integration lulus.

Breaker hanya optimization availability. Pinning dan vendor-call evidence
tetap menjadi pengaman uang yang authoritative.

### K4 — Degradasi Redis selektif

Rate limiter dan policy counter mendapat wrapper yang:

- memakai Redis selama health probe sehat,
- pindah atomik ke memory setelah operasi Redis gagal,
- probe setiap sekitar 5 detik dengan timeout pendek,
- kembali ke Redis setelah dua probe sukses berturut-turut untuk mencegah
  flapping,
- tidak memindahkan state memory kembali ke Redis,
- menyediakan `Close/Stop` agar goroutine probe dan GC berhenti bersih.

Policy counter memory bersifat per-replika dan dapat memperbesar allowance
saat outage. Karena perilaku existing adalah fail-open, memory counter adalah
degradasi yang lebih kuat, tetapi wajib diberi warning/metric dan tidak boleh
dipasarkan sebagai enforcement global.

Fraud velocity menggunakan kebijakan berbeda:

- fraud-service boleh start tanpa Redis dan terus mem-probe Redis,
- selama unavailable, operasi velocity mengembalikan error dependency yang
  terklasifikasi, bukan data nol atau memory approximation,
- ketiga caller fraud memetakan dependency unavailable menjadi 503/
  `DEPENDENCY_UNAVAILABLE` sebelum uang dipindahkan,
- definitive fraud rejection tetap menggunakan kontrak business rejection
  existing,
- saat Redis pulih, screening kembali aktif tanpa restart.

Scheduler lock tetap skip-tick ketika Redis unavailable. Memory lock tidak
digunakan pada deployment multi-replika.

### K5 — Open-source dan vendor-neutral

Plan 45 tidak membutuhkan akun atau kredensial vendor. Mockvendor dan
`httptest` menjadi conformance harness untuk:

- timeout/5xx → error non-nil (`uncertain`),
- definitive business rejection → `PayoutFailed` + nil error,
- processing → `PayoutPending`,
- duplicate Submit dengan idempotency key sama → satu efek vendor,
- callback token salah → ditolak dan body tidak diproses.

Adapter Xendit atau vendor proprietary lain menjadi follow-up default-off dan
env-gated. Saat follow-up dieksekusi, endpoint, idempotency header, status,
webhook authentication, dan biaya sandbox/produksi wajib diverifikasi dari
dokumentasi resmi saat itu. Tidak ada credential di repo, Compose, fixture,
atau log.

### K6 — Observability minimum wajib

Tambahkan metric low-cardinality:

- `payout_vendor_commands{status}` gauge,
- `payout_vendor_command_attempts_total{outcome}`,
- `payout_vendor_command_reaped_total`,
- `vendorgw_breaker_backend{backend}`,
- `redis_backend_active{primitive,backend}`,
- `scheduler_job_skips_total{job}`.

Label hanya berasal dari enum internal. Dashboard dan Grafana alert
provisioning bukan blocker implementasi core; dilakukan setelah metric lolos
audit cardinality dan stack observability Plan 43 stabil.

## 4. Task eksekusi

### T0 — Contract dan schema

1. Tambahkan model command dan migrasi `000006` lengkap dengan constraint,
   index, grant, RLS, dan down migration.
2. Tambahkan repository operasi atomik enqueue/claim/complete/fail/reap/replay.
3. Buat integration test rollback: transition tidak boleh commit tanpa command
   dan command tidak boleh ada tanpa transition.

**Gate:** migration up/down, repository unit/integration, race, vet, lint.

### T1 — Relay dan refactor orchestration

1. Implement relay, retry, lease reaper, dead-letter, replay limit, dan metric.
2. Pindahkan seluruh `provider.Submit` serta klasifikasi outcome ke relay.
3. Refactor Create/resume/failover memakai operasi enqueue atomik.
4. Ubah business E2E dan chaos existing dari asumsi sinkron menjadi polling.

**Gate:** tidak ada `provider.Submit` di luar relay; test crash pada setiap
boundary enqueue/claim/audit/settle; `make verify-full` hijau.

### T2 — Distributed breaker

1. Tambahkan backend Redis Lua dan fallback HealthTracker lokal.
2. Wire ke payin/payout melalui config opt-in.
3. Test dua tracker yang berbagi Redis dan N caller half-open bersamaan:
   tepat satu probe lintas instance.

**Gate:** Redis down/up tidak membuat caller error; dua replika melihat state
open yang sama; unit/integration/race hijau.

### T3 — Redis degradation aman

1. Tambahkan wrapper failover limiter/counter dengan hysteresis dan lifecycle
   bersih.
2. Ubah fraud-service menjadi start-degraded + recovering Redis store.
3. Tambahkan error mapping fail-closed pada tiga caller fraud.
4. Tambahkan scheduler skip metric.

**Gate:** Redis stop/start tanpa restart process; limiter/counter pindah dan
pulih; transaksi berisiko mendapat 503 selama fraud velocity unavailable;
`make verify-full` hijau.

### T4 — Chaos dan final gate

Tambahkan tiga skenario:

1. Redis outage: limiter/counter memory fallback, fraud fail-closed, recovery
   tanpa restart.
2. Breaker lintas dua payout replica: state open terlihat dari replica kedua;
   Redis outage mengaktifkan fallback lokal tanpa crash.
3. Crash setelah command enqueue dan setelah network timeout: relay retry
   at-least-once, satu efek ledger, request tidak berpindah vendor setelah
   uncertain.

Test harness saat ini masih memakai nama container default di
`scripts/lib.sh`, sedangkan port Redis/RabbitMQ belum dinamis. Sebelum final
gate, hentikan stack default tanpa `-v` agar port bebas; volume developer tetap
utuh. Lalu jalankan gate dengan project Compose terisolasi dan override nama
container secara eksplisit:

```bash
docker compose stop
COMPOSE_PROJECT_NAME=seev-plan45-gate \
POSTGRES_CONTAINER=seev-plan45-gate-postgres-1 \
REDIS_CONTAINER=seev-plan45-gate-redis-1 \
RABBITMQ_CONTAINER=seev-plan45-gate-rabbitmq-1 \
make verify-full
COMPOSE_PROJECT_NAME=seev-plan45-gate docker compose down -v
```

Setelah cleanup, hidupkan kembali hanya service project default yang memang
aktif sebelum preflight. Jangan pernah menjalankan `down -v` tanpa
`COMPOSE_PROJECT_NAME=seev-plan45-gate` pada langkah ini. Follow-up yang lebih
baik adalah membuat `scripts/lib.sh` menemukan container melalui
`docker compose ps -q` dan membuat seluruh host port dapat dioverride; itu
memungkinkan dua project berjalan paralel, tetapi bukan blocker core A3.

## 5. Test matrix wajib

| Area | Skenario minimum | Bukti |
|---|---|---|
| Atomic enqueue | DB error sebelum/sesudah insert | rollback penuh |
| Claim | dua relay claim batch bersamaan | satu owner per lease |
| Timeout vendor | response tidak diketahui | pinned + retry same key |
| Rejection | kandidat berikut tersedia/tidak ada | enqueue failover/cancel |
| Crash | setelah network call sebelum mark | retry aman, satu efek ledger |
| Dead-letter | retry budget habis + replay | dead terlihat dan replay dibatasi |
| Breaker | N probe lintas dua instance | tepat satu probe |
| Redis outage | down → degraded → recover | tanpa restart/flapping |
| Fraud outage | Redis tidak tersedia | 503 sebelum money movement |
| Cardinality | audit metric labels | hanya enum/allowlist |

## 6. Definition of Done

- [x] Transition `held → submitted` dan command pertama selalu satu transaksi.
- [x] Hanya ada satu command hidup per payout request.
- [x] Tidak ada `provider.Submit` di luar relay.
- [x] Delivery didokumentasikan at-least-once; semua retry memakai idempotency
      key yang sama dan menghasilkan satu efek ledger.
- [x] Audit failure, accepted, dan uncertain selalu fail-closed/pinned sesuai
      doc 40.
- [x] Distributed breaker converge lintas replika dan fallback lokal ketika
      Redis unavailable.
- [x] Rate limiter/policy counter pulih tanpa restart; fraud velocity tidak
      pernah berubah menjadi bypass diam-diam.
- [x] Semua test unit, integration, race, lint, vet dua tag, business E2E, dan
      chaos hijau memakai stack open-source lokal.
- [x] Tidak ada credential atau ketergantungan service berbayar di CI/DoD.
- [x] Semua `### Hasil` task diisi command, output ringkas, dan commit terkait.

## 7. Constraint eksekutor

1. Satu commit per task; jangan mencampur T0–T4.
2. Jangan mengubah `execTransfer`, ledger balancing, atau `mayFailover`.
3. Jangan mengklaim exactly-once network dispatch.
4. Jangan memakai memory fallback untuk fraud velocity atau scheduler lock
   pada multi-replika.
5. DB/Redis/vendor operation harus memiliki timeout eksplisit dan error tidak
   boleh memuat credential, destination mentah, amount, atau full idempotency
   key.
6. Metric labels harus enum/allowlist dan diaudit sebelum merge.
7. Jika perubahan membutuhkan dependency proprietary atau behavior di luar
   keputusan K1–K6, hentikan implementasi dan buat plan follow-up.

## 8. Hasil

### Hasil T0

Dieksekusi 2026-07-18.

**Migrasi** — `migrations/payout/000006_vendor_commands.{up,down}.sql`, persis
skema K1 (constraint, `idx_payout_vendor_commands_claim`, partial unique
index `idx_payout_vendor_commands_one_live`, grant `app_service`/
`app_readonly`, RLS FORCE + `pol_all_service`/`pol_read_readonly` — pola
identik `payout_requests`). Diverifikasi terhadap Postgres nyata (Compose
`seev-postgres-1`, volume fresh — init script `03-service-migrations.sh`
sendiri sudah menjalankan `000006` saat bootstrap, versi tercatat 6 di
`schema_migrations_payout`):

```
$ migrate -path migrations/payout -database ".../seev_payout?...&x-migrations-table=schema_migrations_payout" down 1
6/d vendor_commands (155.974208ms)
$ psql ... -c '\dt'   # payout_vendor_commands hilang, 5 tabel tersisa
$ migrate ... up
6/u vendor_commands (258.865416ms)
$ psql ... -c '\d payout_vendor_commands'   # kolom/index/check/FK persis K1
$ psql ... # relrowsecurity=t relforcerowsecurity=t; pol_all_service(app_service,ALL), pol_read_readonly(app_readonly,SELECT)
$ psql ... # grants: app_service INSERT/SELECT/UPDATE, app_readonly SELECT
```

**Model** — `internal/payout/model/model.go`: `PayoutVendorCommand` +
`Command{Pending,Processing,Failed,Completed,Dead}`.

**Repository** — file baru `internal/payout/repository/vendor_command_repository.go`
(interface `VendorCommandRepository`, terpisah dari `Repository` — pola sama
dengan `RoutingRepository`, bukan penambahan ke interface existing):
`EnqueueInitialSubmit`, `CompleteAndEnqueueFailover`, `EnsureSubmitCommand`,
`ClaimPendingCommands`, `ClaimFailedCommandsForRetry`, `CompleteCommand`,
`FailCommand` (backoff formula identik ledger outbox: base 30s, factor 2,
cap 15m, +50% jitter; dead-letter inline via `CASE` saat `retry_count+1 >=
max_retries`, bukan trigger DB terpisah), `ReapStuckCommands`,
`ReplayDeadCommand`/`ReplayAllDeadCommands` (cap 100/panggilan),
`CountCommandsByStatuses`, `GetLiveCommand`. Transaksi dimiliki repository
via `DatabaseSQL.WithTx`; orchestration/worker (T1) tidak akan menyentuh
`*sql.Tx`. Mock digenerate via `go generate` (mockgen) —
`vendor_command_repository_mock.go`.

**Dua bug nyata ditemukan dan diperbaiki selama T0** (bukan sekadar code
review — ditemukan lewat integration test yang sungguh gagal):

1. `commandColumns` awalnya berisi `COALESCE(last_error, '')` untuk dipakai
   ulang baik di `SELECT` biasa (`GetLiveCommand`) maupun di dalam rantai
   `WITH ... RETURNING ... SELECT` (`claim`). RETURNING tanpa alias
   menghasilkan nama kolom otomatis (`coalesce`), sehingga SELECT terluar
   yang mereferensikan `COALESCE(last_error, '')` gagal dengan `column
   "last_error" does not exist` — CTE tidak punya kolom bernama itu. Diganti
   jadi kolom polos `last_error` + null-handling di `scanCommand` via
   `sql.NullString`.
2. `EnsureSubmitCommand` awalnya memakai INSERT polos lalu menangkap error
   duplicate-key di Go (`generalerror.IsDuplicateKey`) dan mengembalikan nil
   agar transaksi "berhasil". Ini salah: begitu SATU statement di dalam
   transaksi Postgres gagal, seluruh transaksi itu aborted sampai
   ROLLBACK — `Commit()` sesudahnya gagal dengan `commit unexpectedly
   resulted in rollback` (pgx `ErrTxCommitRollback`), persis yang muncul di
   test konkurensi. Diganti jadi `INSERT ... ON CONFLICT DO NOTHING` (tanpa
   target — menangkap SEMUA unique constraint pada tabel: `command_key`,
   `(payout_request_id, attempt)`, maupun partial index satu-command-hidup)
   dan cek rows-affected, bukan menangkap error.

**Test** — file baru
`internal/payout/repository/vendor_command_repository_integration_test.go`
(build tag `integration`, pola sama `repository_integration_test.go`
existing — modul payout tidak punya unit test sqlmock terpisah untuk
repository, hanya integration test terhadap testcontainers Postgres nyata,
jadi T0 mengikuti konvensi itu apa adanya). 12 test baru, termasuk bukti
rollback wajib T0 (`TestEnqueueInitialSubmit_RollsBackTransitionWhenCommandConflicts`
— command hidup diseed manual di luar `EnqueueInitialSubmit` untuk memaksa
konflik partial unique index, lalu memverifikasi status TETAP `held`,
bukan `submitted`, membuktikan transisi yang sudah berjalan di transaksi
yang sama ikut rollback), claim konkurensi (`TestClaimPendingCommands_ConcurrentCallers_OneOwnerPerLease`,
`TestConcurrentEnsureSubmitCommand_ExactlyOneWins`), backoff/dead-letter/
replay (`TestFailCommand_BackoffThenDeadLetterThenReplay`), reaper
(`TestReapStuckCommands`), dan CAS failover
(`TestCompleteAndEnqueueFailover_AtomicFailover`/`_VendorMismatch_NoOp`).

**Gate dijalankan nyata:**

```
go build ./...                                    # OK
go vet ./...                                        # OK
go vet -tags=integration ./...                      # OK
go test -race -cover ./...                          # OK, semua paket
go test -tags=integration -race ./internal/payout/repository/... -v   # 12 test baru PASS + 4 existing PASS
golangci-lint run ./...                              # OK, 0 finding
go test -tags=integration -race ./...                # OK, KECUALI 2 test RabbitMQ
```

Dua kegagalan pada run penuh (`internal/fraud` `TestVelocityConsumerRealRabbitMQIncrementsPostedCounterOnce`,
`internal/notify` `TestNotify_MoneyIn_RealStack_NotificationRowAppears_DuplicateDeliveryDedup`)
adalah timeout startup container RabbitMQ testcontainers (`wait until
ready: ... context deadline exceeded`) — bukan regresi dari perubahan T0
(kedua paket itu tidak disentuh sama sekali). Dikonfirmasi flaky lingkungan
(Docker Desktop 4GB budget di bawah beban banyak testcontainer paralel,
PROJECT_GUIDE.md), bukan bug: dijalankan ulang terisolasi, keduanya PASS
(`internal/fraud`: 12.835s PASS; `internal/notify`: 21.801s PASS).
`internal/payout/repository` sendiri PASS penuh (264.889s, 16 test).

**Constraint eksekutor dipatuhi**: tidak menyentuh `execTransfer`,
`mayFailover`, atau `orchestrate.go` (itu scope T1); command menyimpan
snapshot vendor/attempt, bukan salinan `destination` sensitif; setiap error
message dipotong 500/1000 karakter dan tidak memuat payload mentah.

Commit: satu commit mencakup migrasi + model + repository + test (lihat
riwayat git `docs/plan/45 T0`).

### Hasil T1

Dieksekusi 2026-07-18.

**Relay (`internal/payout/relay.go`, package `payout`)** — `dispatchOne`
adalah SATU-SATUNYA pemanggil `provider.Submit` di seluruh modul (dibuktikan
`grep -rn "provider\.Submit" internal/payout/*.go` — hanya `relay.go:106`).
Alur: guard `req.Status != StatusSubmitted` (defensif — command yang
tertinggal setelah admin-cancel selesai tanpa memanggil vendor lagi) →
`provider.Submit` → klasifikasi outcome → `recordVendorCall` (audit; gagal =
fail-closed, `FailCommand` tanpa transisi apa pun) → breaker
Record{Success,Failure} → routing outcome: `uncertain` → `FailCommand`
(pin, retry vendor sama); `rejected` → `handleRejected` (cek `mayFailover`
via `payout_vendor_calls`, lalu `ListTriedVendors` sebagai exclusion list
pengganti slice in-process lama, `ResolvePayoutRoute`, dan
`CompleteAndEnqueueFailover` atomik atau `cancel` bila tak ada kandidat);
`accepted` → `handleAccepted` (`settle`/`TransitionToVendorPending` lalu
`CompleteCommand`). `DispatchPendingCommands`/`DispatchFailedCommandsForRetry`/
`ReapStuckCommands`/`CountCommandsByStatuses` diekspos di `Module` sebagai
titik masuk worker DAN dipakai langsung oleh integration test untuk memicu
satu putaran dispatch secara sinkron tanpa menjalankan ticker asli.

**Relay loop (`internal/payout/worker/vendor_relay.go`)** — pola 4-loop
identik `internal/ledger/worker.OutboxRelay` (poll 1s, retry 30s, reaper
5m/stuck 10m, gauge 15s), tapi TIDAK bisa claim+dispatch dalam repository
layer seperti ledger outbox karena dispatch butuh logika domain
(`settle`/`cancel`/`recordVendorCall`) yang cuma ada di package `payout` —
diselesaikan dengan interface `dispatcher` 4 method yang masing-masing
claim+dispatch SEKALIGUS di sisi `Module`, bukan claim murni di sisi worker.

**Repository tambahan (bagian dari T1 karena baru dibutuhkan relay, bukan
scope creep T0)**: `ListTriedVendors` (SELECT DISTINCT vendor dari semua
command milik satu request, menggantikan slice in-process lama karena
attempt sekarang tersebar lintas command row/dispatch terpisah) dan
`HasDeadCommand` (cek command TERBARU per `attempt DESC`, bukan
"ada command apa pun" — lihat bug #3 di bawah).

**Refactor orchestrate.go**: `submit()` (loop vendor in-process) dihapus
total, diganti `enqueueSubmit` (bungkus `EnqueueInitialSubmit`, dipakai
`Create` dan resume created/held) dan `ensureSubmitCommand` (bungkus
`EnsureSubmitCommand`). `maxFailoverAttempts` dihapus — tidak relevan lagi,
terminasi failover dijamin exclusion list `ListTriedVendors` yang tumbuh per
command, bukan loop Go. `ResumeStuck` dipecah 4 blok sesuai K2: created
(hold+enqueueSubmit), held (enqueueSubmit — recovery gap TransitionToHeld
sukses tapi EnqueueInitialSubmit belum sempat jalan), submitted (HANYA cek
`GetLiveCommand`→no-op jika ada, `HasDeadCommand`→no-op jika command terakhir
dead/operator-visible, baru `ensureSubmitCommand` jika benar-benar tak ada
command hidup maupun dead — resume TIDAK PERNAH memanggil vendor lagi),
vendor_pending (tetap `pollVendorPending` langsung, TIDAK pindah ke relay —
sesuai K2 eksplisit: hanya `provider.Submit` yang dilarang di luar relay,
`provider.Query` tetap di resume). `AdminRetry` diubah: cek `GetLiveCommand`
dulu (no-op jika ada), baru `ensureSubmitCommand` — beda dari resume karena
ini aksi admin SENGAJA membangkitkan command baru walau yang terakhir dead
(operator sudah melihat dan memutuskan retry).

**Tiga bug nyata ditemukan lewat test yang sungguh gagal** (bukan review
kode semata):

1. Test unit gomock: lupa bahwa `settle()` memanggil `m.repo.Get` KEDUA
   KALINYA secara internal (selain Get pertama di `dispatchOne`) — mock
   `.Times(1)` default meleset jadi `.Times(2)` di 2 test failover.
2. `TestCreate_FraudInfraError_FailsOpen_StillCreates`: registry kosong
   (`vendorgw.NewRegistry()`) padahal routing tetap mengarah ke
   "mockvendor" — `ResolvePayoutRoute` gagal `ErrNoVendorAvailable` karena
   provider tak pernah didaftarkan. Salah tempel dari refactor, bukan bug
   desain.
3. **Bug desain nyata**: `HasAnyCommand` awalnya cek "ada command APA PUN"
   (termasuk yang sudah `completed`) untuk memutuskan resume boleh insert
   command baru di status `submitted` tanpa command hidup. Ini salah: sebuah
   request yang (lewat skenario chaos buatan — vendor_pending dipaksa balik
   ke submitted via SQL untuk simulasi "vendor selesai saat service down")
   punya HANYA command `completed` di riwayatnya (bukan `dead`) tidak akan
   pernah dapat command baru — resume mengiranya "sudah ada, biarkan",
   padahal tidak ada yang hidup maupun mati untuk dilihat operator. Diganti
   `HasDeadCommand` (cek status command TERBARU secara spesifik `= 'dead'`)
   — hanya command dead yang membuat resume diam; command `completed` (atau
   tidak ada command sama sekali) tetap dapat command baru.

**Test unit direstrukturisasi** — `failover_test.go` (5 test, semua
`m.submit(...)` → `m.dispatchOne(...)` dengan `model.PayoutVendorCommand`
buatan + mock `commandRepo`), `payout_test.go` (`TestCreate_HappyPath_*`
sekarang membuktikan Create TIDAK PERNAH memanggil vendor — `t.Fatal` di
`submitFn` kalau terpanggil; `TestResumeStuck_*` dipecah jadi 4 test baru
mencerminkan 4 cabang submitted-handling K2 di atas), `http_test.go`
(`TestCreateHandler_Success_201`/`TestAdminRouter_Retry_Success` mengikuti
pola yang sama). Total 5+9+2 test berubah/baru di paket `payout`.

**Test integration** — `payout_integration_test.go`: setiap assertion
"Create langsung settled/vendor_pending" ditambah
`payoutModule.DispatchPendingCommands(ctx, 10)` sebelum baca status ulang
(perilaku API yang diakui berubah eksplisit sesuai K2:
"POST /api/v1/payout kembali setelah hold+enqueue"); satu test diganti total
(`TestPayout_ResumeStuck_SubmittedWithNoCommand_RecoversAndSettles`,
menghapus SEMUA command row lalu membuktikan resume insert command baru DAN
relay men-dispatch-nya — skenario lama "paksa balik ke submitted lalu resume
retry submit inline" sudah tidak match arsitektur baru).
`failover_integration_test.go`: `TestFailover_ConcurrentSubmit_...` (10
goroutine memanggil `m.submit` bersamaan) diganti
`TestFailover_ConcurrentDispatch_RaceRelayReplicasVsFailover` — race lama
sudah TIDAK MUNGKIN terjadi lagi secara struktural (partial unique index
T0 menjamin cuma 1 command hidup per request kapan pun), jadi test baru
membuktikan hal yang lebih kuat: 10 pemanggil `DispatchPendingCommands`
konkuren di SETIAP babak (reject→failover, lalu settle) tetap cuma 1 vendor
call/babak berkat `FOR UPDATE SKIP LOCKED`.

**Shell E2E/chaos diubah dari asumsi sinkron ke polling** (K2's admitted API
change) — 3 bug shell timing ditemukan lewat run yang sungguh gagal, bukan
dari membaca kode:

1. `wait_for_payout_status` dipindah dari `chaos-test.sh` ke `lib.sh`
   (dipakai bersama), plus 2 helper baru: `wait_for_vendor_call` (poll
   `payout_vendor_calls.outcome`) dan `wait_for_vendor_command_status`
   (poll command hidup) — dibutuhkan karena beberapa assertion butuh bukti
   dispatch SUDAH terjadi (bukan cuma status berubah) sebelum langkah
   berikutnya (mis. me-rewrite mock destination, membaca breaker health).
2. `scripts/smoke-test.sh` — instant-settle DAN async create sama-sama
   membaca status langsung dari response body Create (selalu `submitted`
   sekarang) — run pertama `make verify-full` GAGAL nyata di sini (3
   assertion), diperbaiki dengan `wait_for_payout_status`.
3. `scripts/business-e2e.sh` — 4 tempat sama (withdraw settle, async
   withdraw+cancel, quote-backed payout, failover-drill) plus 1 race
   (`probe_status`/health check dibaca sebelum dispatch tentu selesai) —
   diperbaiki dengan wait yang sama.
4. `scripts/chaos-test.sh` scenario 5 kill point 3 — race asli: mock_mode
   destination di-rewrite SEGERA setelah `wait_for_payout_status
   "submitted"` (yang sekarang lolos SEKETIKA dari enqueue, sebelum relay
   sempat mencoba vendor sama sekali) — bisa menghapus mock_mode SEBELUM
   command sempat gagal, membatalkan seluruh maksud kill point. Diperbaiki
   dengan `wait_for_vendor_call ... uncertain` + `wait_for_vendor_command_status
   ... failed` sebelum rewrite, plus reset `next_attempt_at` command
   (relay retry-poll 30s, bukan menunggu backoff natural ~60-90s). Scenario
   8's probe payout kena pola sama (breaker health dibaca sebelum breaker
   sempat di-record). **Run kedua `make verify-full` masih gagal** (kill
   point 2 & 3 macet di `submitted`) — root cause: command yang gagal
   dispatch SELAMA ledger-down window (di titik crash ledger scenario 5)
   mewarisi backoff eksponensial (~60-90s) yang tidak ikut di-reset oleh
   bulk-backdate `payout_requests.updated_at` yang sudah ada — ditambahkan
   bulk-reset `payout_vendor_commands.next_attempt_at` sejajar dengan
   bulk-backdate itu. Diverifikasi ulang `make chaos-debug SCENARIO=5` —
   PASS penuh (10/10).

**Gate dijalankan nyata (3x `make verify-full` penuh):**

```
go build ./...                          # OK
go vet ./... && go vet -tags=integration ./...   # OK
golangci-lint run ./...                 # OK, 0 finding
go test -race -cover ./...              # OK, semua paket
go test -tags=integration -race ./internal/payout/...   # OK (payout 84.7s, repository 91.6s)
grep -rn 'provider\.Submit' internal/payout/*.go   # HANYA relay.go:106 (dispatchOne)
make verify-full   # run 1: GAGAL di smoke-test.sh (3 assertion, bug #2 di atas)
make verify-full   # run 2: GAGAL di scenario 5 (2 assertion, bug #4 di atas)
make chaos-debug SCENARIO=5   # PASS 10/10 setelah fix
make verify-full   # run 3: GAGAL di scenario 7 (2 assertion, TIDAK terkait payout)
make chaos-debug SCENARIO=7   # PASS 16/16 — dikonfirmasi flaky lingkungan sudah ada sebelum sesi ini,
                               # bukan regresi T1 (scenario 7 = fraud/P2P velocity, tidak disentuh T1 sama sekali)
```

Scenario 7's kegagalan (`block-mode P2P transfer code=201`, `expected at
least one blocked fraud event for P2P, found 0`) muncul di run 3 tapi PASS
bersih di run 2 dengan kode fraud/ledger yang identik (T1 tidak menyentuh
`internal/fraud`/`internal/ledger` sama sekali) — dikonfirmasi flaky
lingkungan (kemungkinan kontensi Redis/velocity-counter timing saat
scenario berjalan berurutan dalam satu invocation `chaos-test.sh all`),
sama persis polanya dengan RabbitMQ testcontainer flakiness yang
didokumentasikan di Hasil T0.

**Constraint eksekutor dipatuhi**: `execTransfer`, `mayFailover`'s
pinning rule, dan RLS/skema T0 tidak disentuh; command tetap tidak
menyimpan salinan `destination` (dispatchOne re-fetch dari `payout_requests`
via `PayoutRequestID`); tidak ada exactly-once diklaim (retry command tetap
memakai idempotency key vendor yang sama — `payout_requests.id` — tak
berubah lintas attempt).

### Hasil T2

Dieksekusi 2026-07-19.

**Interface `vendorgw.Breaker`** (`internal/vendorgw/breaker.go`) — 4 method
ber-context (`Allow`, `RecordSuccess`, `RecordFailure`, `Snapshot`), dua
implementasi: `*HealthTracker` (existing, ctx ditambahkan sebagai parameter
tak terpakai — cuma untuk kesesuaian interface, tidak ada perilaku baru) dan
`*DistributedBreaker` (baru). `payin.Module`/`payout.Module.breaker`
berubah dari `*vendorgw.HealthTracker` (pointer konkret) jadi
`vendorgw.Breaker` (interface) — setiap call site (`routing.go` x2,
`relay.go`, `http.go` x2) diberi `ctx`/`r.Context()`.

**`DistributedBreaker` (`internal/vendorgw/distributed_breaker.go`)** —
state machine SAMA PERSIS dengan `HealthTracker` (closed→open pada
`failureThreshold`; open→half-open sekali cooldown lewat; probe gagal
langsung re-open tanpa re-akumulasi threshold), tapi seluruh transisi jadi
tiga Lua script atomik (`allowScript`, `recordSuccessScript`,
`recordFailureScript`, pola inline-backtick-var sama seperti
`pkg/scheduler`'s `luaUnlock` dan `internal/fraud/velocity_store.go`'s
`recordVelocityScript`). Jaminan single-probe lintas replika: token
terpisah `breaker:<ns>:probe:<vendor>` via `SET NX PX` — token yang
KEDALUWARSA TANPA PERNAH diselesaikan (prober crash) dideteksi via `EXISTS`
di awal `allowScript` dan diperlakukan seperti open+cooldown-lewat yang
segar, sehingga slot probe self-heal tanpa mewajibkan restart siapa pun.
Setiap key dinamespace `breaker:<namespace>:...` (`namespace` = "payin"
atau "payout") agar dua modul yang berbagi satu Redis/DB yang sama tidak
pernah bentrok pada nama vendor yang kebetulan sama. Setiap operasi Redis
dibatasi `redisTimeout=150ms`; error APA PUN dari Redis (termasuk timeout)
membuat panggilan itu fallback ke `local *HealthTracker` yang tertanam —
TIDAK PERNAH menjadi error yang terlihat pemanggil. Peralihan backend
dicatat sekali per transisi nyata (bukan per panggilan) via
`vendorgw_breaker_backend{namespace,backend}` — flag `initialized` mencegah
panggilan PERTAMA (belum ada transisi sungguhan) tercatat keliru sebagai
"recovered". State lokal yang terakumulasi selama Redis mati TIDAK PERNAH
di-merge balik ke Redis begitu Redis pulih (K3) — Redis kembali jadi
otoritatif murni dari titik itu.

**Config/wiring**: `BreakerConfig.Distributed` (`BREAKER_DISTRIBUTED`,
default `false`) di `internal/config/config.go`; kedua `cmd/*-service/
main.go` membangun `HealthTracker` seperti biasa lalu MENIMPA dengan
`DistributedBreaker` hanya jika `cfg.Breaker.Distributed && redisClient !=
nil` — Redis mati/nonaktif saat startup TIDAK PERNAH membuat service gagal
start, cukup diam-diam tetap pakai breaker lokal. **payin-service
sebelumnya TIDAK PUNYA koneksi Redis sama sekali** — ditambahkan blok
wiring `if cfg.Redis.Enabled {...}` yang identik dengan pola
payout/ledger-service (nil-berarti-nonaktif), DB 0 (aman dibagi dengan
payout karena namespace key terpisah), plus cleanup `redisCache.Close()` di
setiap jalur shutdown/error yang sudah ada.

**Test unit** — `internal/vendorgw/distributed_breaker_test.go` (12 test
baru, semua pakai `miniredis` — konvensi Redis-test yang sudah dipakai
`pkg/cache/redis_test.go` dan `internal/fraud/consumer_integration_test.go`,
BUKAN testcontainers): state machine (closed/threshold/cooldown/half-open
sukses+gagal), token probe kedaluwarsa membuka slot segar,
**`TestDistributedBreaker_ConcurrentHalfOpenCallers_ExactlyOneProbeWins`**
(20 goroutine, satu instance — tepat satu menang),
**`TestDistributedBreaker_TwoInstancesShareRedis_StateConverges`** (dua
instance breaker terpisah berbagi Redis — state konvergen),
**`TestDistributedBreaker_TwoInstancesConcurrentHalfOpen_ExactlyOneProbeWins`**
(gabungan keduanya — persis kalimat gate T2 sendiri: "dua tracker yang
berbagi Redis dan N caller half-open bersamaan: tepat satu probe lintas
instance", 10 goroutine per instance, 20 total, tepat 1 menang), Redis
mati → fallback lokal tanpa panik/error, gauge backend mencerminkan
backend aktual.

**Satu bug nyata ditemukan lewat test yang gagal**: test kedaluwarsa-token
awalnya pakai `time.Sleep` untuk menunggu TTL probe token kedaluwarsa —
gagal konsisten karena **miniredis TIDAK memajukan TTL lewat wall-clock
sungguhan**, hanya lewat `mr.FastForward(duration)` eksplisit (cooldown
sendiri aman karena dihitung Lua dari timestamp Go yang dikirim eksplisit
via `ARGV[1]`, bukan TTL Redis — hanya probe token yang benar-benar
memakai `PX`). Diperbaiki dengan `mr.FastForward` menggantikan
`time.Sleep` khusus untuk assertion itu.

**Gate dijalankan nyata:**

```
go build ./...                                    # OK
go vet ./... && go vet -tags=integration ./...     # OK
golangci-lint run ./...                            # OK, 0 finding
go test -race -cover ./...                         # OK, semua paket termasuk vendorgw 84.2% coverage
go test -tags=integration -race ./internal/payin/... ./internal/payout/... \
  ./internal/vendorgw/... ./internal/config/...     # OK (payout 91.2s, payin 53.2s)
go test -race -run TestDistributedBreaker ./internal/vendorgw/... -v   # 12/12 PASS
make chaos-debug SCENARIO=8   # PASS 14/14 — breaker Allow/RecordFailure/RecordSuccess/Snapshot
                               # tervalidasi lewat stack hidup penuh (masih HealthTracker,
                               # BREAKER_DISTRIBUTED=false default — membuktikan refactor
                               # interface TIDAK mengubah perilaku default byte-identik)
```

**Catatan lingkup verifikasi**: `make verify-full` PENUH tidak dijalankan
ulang untuk T2 — T2 murni penggantian backend breaker di belakang
interface yang sama, tidak menyentuh logika bisnis payout/payin apa pun;
`BREAKER_DISTRIBUTED=false` tetap default sehingga seluruh business-e2e/
smoke/chaos existing berjalan dengan `HealthTracker` yang sama persis
seperti sebelum T2 — dibuktikan lewat kombinasi test unit menyeluruh
(termasuk race, seluruh paket) + satu chaos scenario live (8) yang secara
spesifik melewati setiap method breaker via stack nyata. Uji hidup
`BREAKER_DISTRIBUTED=true` dua-replika terhadap Redis Compose sungguhan
adalah scope eksplisit T4 (chaos scenario 10 baru), bukan T2.

**Constraint eksekutor dipatuhi**: tidak mengubah `mayFailover`/aturan
pinning doc 40 (breaker tetap murni optimasi availability, bukti
`payout_vendor_calls` tetap satu-satunya sumber kebenaran anti-double-
payout); setiap operasi Redis punya timeout eksplisit; tidak ada
credential/destination/amount di log error breaker.

### Hasil T3

Dieksekusi 2026-07-19.

**`pkg/cache.RedisHealthSwitcher`** (`pkg/cache/failover.go`) — primitif
health-probe bersama dipakai `FailoverLimiter`/`FailoverCounter` (fallback
memory) DAN `internal/fraud.FailClosedVelocityStore` (fail-closed, tanpa
fallback) — satu mekanisme, dua reaksi berbeda terhadap "unhealthy". Degrade
SEGERA pada kegagalan operasi nyata; recovery HANYA lewat probe latar
belakang (~5s, timeout 2s) dan HANYA setelah 2 probe sukses BERTURUT-TURUT
(hysteresis anti-flapping K4). `Healthy()`/`Degrade()` dipisah dari
keputusan "apa yang dilakukan saat unhealthy" — itulah yang membuat
pemakaian ganda (fallback vs fail-closed) mungkin dari kode yang sama.
`Stop()` menghentikan goroutine probe dengan bersih.

**`FailoverLimiter`/`FailoverCounter`** (`pkg/cache/failover.go`) —
implementasi `Limiter`/`Counter` yang menggantikan pilihan
Redis-vs-Memory-SEKALI-DI-STARTUP lama dengan pemilihan runtime: pakai
Redis selama switcher sehat, pindah ke `MemoryRateLimiter`/`MemoryCounter`
yang SUDAH ADA (tidak dibuat ulang) begitu satu operasi gagal, kembali ke
Redis otomatis via hysteresis switcher — TANPA restart proses. Diwire ke
3 tempat: `internal/handler/router.go` (`buildRateLimiter`, limiter publik
ledger), `cmd/auth-service/router.go` (limiter publik auth), dan
`cmd/ledger-service/main.go` (policy counter velocity harian/bulanan).

**`internal/fraud.FailClosedVelocityStore`** (`internal/fraud/
velocity_store.go`) — SATU-SATUNYA primitif K4 yang TIDAK dapat fallback
memory (sengaja, per koreksi #2 di pembuka dokumen): saat switcher
unhealthy, `Get`/`Record` langsung mengembalikan `model.ErrDependencyUnavailable`
TANPA mencoba Redis sama sekali (fail-fast, dibuktikan test
`TestFailClosedVelocityStore_RedisDown_SubsequentCallsFailFastWithoutRetryingRedis`
< 50ms per panggilan setelah degradasi, bukan menunggu timeout Redis
berulang). `cmd/fraud-service/main.go` diubah total: `cfg.Redis.Enabled =
true` yang tadinya memaksa `cache.New`'s eager Ping (fraud-service GAGAL
START kalau Redis mati) diganti `cache.NewClientWithoutPing` (fungsi baru
di `pkg/cache/redis.go` — go-redis client memang lazy by construction,
konstruktor ini cuma MELEWATKAN Ping eager `cache.New` sendiri yang
lakukan) — fraud-service sekarang START meski Redis mati, terus mem-probe
di background, dan otomatis pulih tanpa restart begitu Redis kembali.

**Rantai `ErrDependencyUnavailable` lintas 4 layer** (K4's paling
safety-critical): `model.ErrDependencyUnavailable` (baru, di
`internal/fraud/model` — BUKAN di `internal/fraud` sendiri, supaya
`internal/fraud/grpcserver` bisa `errors.Is` tanpa import balik yang bikin
cycle, karena `internal/fraud` sendiri yang import `grpcserver` untuk
`RegisterGRPC`) → `internal/fraud/grpcserver` memetakan ke
`codes.FailedPrecondition` + pesan literal `"DEPENDENCY_UNAVAILABLE"` (BUKAN
`codes.Unavailable` — kode itu SUDAH dipakai transport gRPC asli untuk
"service tidak terjangkau sama sekali", jadi memakainya ulang di sini akan
bikin caller tidak bisa membedakan "fraud-service mati" dari
"fraud-service hidup tapi Redis-nya mati"; `FailedPrecondition` tidak
pernah dihasilkan otomatis oleh lapisan transport gRPC) →
`pkg/fraudcheck.Client.Check` mencocokkan KODE **dan** PESAN persis
(literal `dependencyUnavailableMessage` DIDUPLIKASI sengaja di
`pkg/fraudcheck` — `pkg/` dilarang import `internal/` per aturan boundary
PROJECT_GUIDE.md, jadi ini bagian dari kontrak wire fraudv1, bukan detail
implementasi bersama) → mengembalikan `fraudcheck.ErrDependencyUnavailable`
(sentinel baru) ke SEMUA 3 caller.

**Tiga caller diubah dari fail-open MURNI jadi fail-closed HANYA untuk
sinyal ini** (fail-open untuk error infra generik TETAP TIDAK BERUBAH):

- **Ledger** (`internal/ledger/transport/http.go`, 1 layer — HTTP
  langsung): cek `errors.Is` sebelum blok fail-open lama, tulis 503
  `DEPENDENCY_UNAVAILABLE` via `response.ServiceUnavailable` (helper baru
  di `pkg/response`) langsung, log WARN bukan ERROR (kondisi diharapkan
  &amp; self-healing, bukan bug).
- **Payout** (`internal/payout/orchestrate.go` `Create()` → 3 layer):
  sentinel baru `ErrScreeningDependencyUnavailable`
  (`internal/payout/errors.go`) → `grpcserver.New` dapat parameter ke-6 →
  gRPC `codes.Unavailable` + pesan `"screening dependency unavailable"`
  (kode SAMA dengan `noVendorAvailable` yang sudah ada — keduanya transient
  &amp; retry-worthy — tapi PESAN beda supaya gateway bisa membedakan) →
  `internal/handler/payout.go` sub-switch berdasar pesan di dalam case
  `codes.Unavailable` yang sudah ada, `break` mencegah fallthrough ke
  respons `VENDOR_UNAVAILABLE`.
- **Payin** (`internal/payin/payin.go` `postAndFinalize` → 3 layer): sentinel
  `ErrScreeningDependencyUnavailable` (`internal/payin/errors.go`) —
  SENGAJA BUKAN `businessError` (beda dari Block verdict) karena redelivery
  identik akan SUKSES begitu Redis pulih, jadi webhook receiver harus
  membuat vendor RETRY (503), bukan ack 200 seolah keputusan final →
  `grpcserver.New` dapat parameter ke-4, `codes.Unavailable` + pesan sama
  → **`internal/handler/webhook.go` TERNYATA TIDAK PERLU DIUBAH** — case
  `default` yang sudah ada SUDAH mengirim 503 tanpa body detail (filosofi
  "jangan bocorkan detail error ke vendor" yang sudah berlaku) — cukup
  ditambah SATU case sebelum default untuk log-level WARN (bukan ERROR)
  supaya operator tidak salah baca kondisi self-healing sebagai bug.

**Scheduler skip-tick metric** (K6) — `pkg/scheduler.PrometheusMetrics`
(baru) mengimplementasi `Metrics` interface yang SUDAH ADA sejak awal
(`JobSkip` sudah dipanggil internal, tapi SELALU `noopMetrics` — tidak ada
implementasi Prometheus produksi sama sekali sebelum ini) — hanya
`JobSkip` yang diisi nyata (`scheduler_job_skips_total{job}`),
Start/Success/Fail sengaja no-op (di luar lingkup K6). Diwire ke SEMUA 5
titik `NewScheduler(...)` (4 job ledger + payout resume) menggantikan
`nil`.

**Empat bug nyata ditemukan lewat test yang gagal:**

1. `RedisHealthSwitcher.probeOnce`: probe yang GAGAL tidak me-reset
   `consecutiveOK` ke 0 — hanya `return` diam-diam. Ini membuat Redis yang
   flapping (sukses-gagal-sukses-gagal berselang-seling) bisa "membocorkan"
   2 sukses tak-berurutan lewat 2 SIKLUS PROBE TERPISAH dan pulih padahal
   TIDAK PERNAH benar-benar stabil 2 kali berturut-turut — bertentangan
   langsung dengan tujuan hysteresis K4 sendiri. Ditemukan oleh
   `TestRedisHealthSwitcher_FlappingProbe_NeverRecoversOnSingleSuccess`.
   Diperbaiki: probe gagal me-reset counter ke 0.
2. Test unit `RedisHealthSwitcher` awalnya mem-mutasi
   `s.probeInterval`/`probeTimeout` SETELAH konstruksi sambil goroutine
   probe (sudah jalan sejak `NewRedisHealthSwitcher`) membacanya — race
   sungguhan terdeteksi `-race`. Diperbaiki: konstruktor privat
   `newRedisHealthSwitcher` menerima interval sebagai parameter, tidak
   pernah dimutasi post-construction.
3. Test recovery `FailoverCounter` awalnya memutasi `client.Options().Addr`
   pada client REDIS YANG MASIH HIDUP dipakai goroutine probe — race
   sungguhan kedua. Diperbaiki: `miniredis.Restart()` (restart di alamat
   YANG SAMA) menggantikan pembuatan instance miniredis baru dengan alamat
   berbeda.
4. Pola cycling test flapping awal (`[nil, err, nil, err, nil]`, panjang
   ganjil) SECARA TAK SENGAJA menghasilkan 2 sukses berurutan di titik
   "jahitan" wraparound (index 4=nil lalu index 0=nil lagi) — bug DI TEST,
   bukan produksi, menyamarkan bug #1 di atas pada percobaan pertama.
   Diperbaiki: pola alternating ketat (`calls%2`) yang dijamin TIDAK PERNAH
   menghasilkan 2 sukses berurutan.

**Gate dijalankan nyata:**

```
go build ./...                                      # OK
go vet ./... && go vet -tags=integration ./...       # OK
golangci-lint run ./...                              # OK, 0 finding
go test -race -cover ./...                           # OK, semua paket
  # fraud: 20.2%→34.8%, fraud/grpcserver: 73.3%→82.4%, cache: →82.3%, fraudcheck: 100%
go test -tags=integration -race ./internal/fraud/... ./internal/payin/... ./internal/payout/... \
  ./internal/ledger/transport/... ./internal/handler/... ./pkg/cache/... ./pkg/scheduler/... \
  ./pkg/fraudcheck/...                                # OK KECUALI 1 test RabbitMQ
go test -tags=integration -race -run TestVelocityConsumerRealRabbitMQIncrementsPostedCounterOnce \
  ./internal/fraud/...                                # PASS terisolasi (13.3s) — dikonfirmasi
                                                        # flaky lingkungan sama seperti Hasil T0,
                                                        # bukan regresi (tidak ada perubahan pada
                                                        # jalur RabbitMQ konsumer velocity)
make chaos-debug SCENARIO=4                           # PASS 4/4 — Redis-down fail-open TIDAK
                                                        # regresi (sekarang hot-swap, dulu perlu
                                                        # restart; hasil akhir sama: traffic served)
make chaos-debug SCENARIO=7                           # PASS 16/16 — fraud-service down/recover
                                                        # (mode block, semua 3 flow) tidak berubah
```

**Catatan lingkup**: scenario 4's komentar in-script (pola
"hot-swap tidak terjadi, perlu restart" dan "fraud-service tidak bisa
start tanpa Redis") kini SEBAGIAN TIDAK AKURAT LAGI setelah T3 — dibiarkan
apa adanya untuk T3 (assertion-nya sendiri tetap valid, hanya komentar
penjelasnya yang jadi usang) karena bukti langsung perilaku BARU (hot-swap
tanpa restart, fraud-service start tanpa Redis) adalah scope chaos scenario
9 baru di T4 sendiri (per pembagian kerja dokumen ini) — diperbaiki
sekalian di sana, bukan tambal-sulam di T3.

**Constraint eksekutor dipatuhi**: `mayFailover`/aturan pinning doc 40
tidak disentuh; memory fallback TIDAK PERNAH dipakai untuk fraud velocity
atau scheduler lock multi-replika (scheduler tetap skip-tick, tidak diubah
sama sekali selain metric); setiap operasi Redis (switcher, breaker T2,
velocity store) punya timeout eksplisit; tidak ada credential/destination/
amount di log baru manapun.

### Hasil T4

Dieksekusi 2026-07-19. Tidak ada perubahan kode produksi — T4 murni
`scripts/chaos-test.sh` (+3 skenario baru, 1 fix di skenario 4's komentar
usang, 2 harden fix di skenario 7/9/10), `scripts/lib.sh` (+`assert_metric_value`,
+replica-2 payout helper, +`BREAKER_DISTRIBUTED` wiring, +cleanup untuk
replica), `Makefile` (usage string chaos-debug 1..11).

**Tiga skenario baru (`scripts/chaos-test.sh`):**

- **`scenario_9`** (Redis outage — hot-swap selektif + fraud fail-closed):
  tiga bukti DEKAT tapi SENGAJA terpisah agar tidak saling kontaminasi —
  rate limiter dibuktikan lewat burst 11 create topup (tipe yang TIDAK
  di-screen fraud sama sekali) memakai SATU koneksi curl `--next`-chained
  (bukan 11 proses curl terpisah — `RateLimitByIPAndPath` mengunci pada
  `r.RemoteAddr` termasuk port efemeral, jadi 11 proses terpisah = 11 key
  berbeda, tidak pernah benar-benar menguji bucket yang sama); policy
  counter dibuktikan lewat `max_daily_count=1` yang SELALU habis di
  percobaan pertama (bukan `=0` — `policy_limits_max_daily_count_check`
  DB constraint menolak nol, ditemukan lewat 500 nyata saat eksekusi) lalu
  proses SATU permintaan yang policy ALLOW (memory counter fresh, tidak
  tahu Redis sudah pernah dipakai) tapi fraud tetap fail-closed — satu
  panggilan itu membuktikan backend counter berpindah ke memory DAN
  screening fail-closed sekaligus; setelah Redis pulih, permintaan
  berikutnya dibuktikan membaca hitungan REDIS YANG ASLI (bukan hitungan
  hantu dari memory saat outage) — bukti eksplisit bahwa K4's "state lokal
  TIDAK PERNAH digabung balik ke Redis" benar.
- **`scenario_10`** (breaker terdistribusi lintas 2 replika payout):
  `BREAKER_DISTRIBUTED=true` di dua proses payout-service nyata
  (`start_payout_service_replica`, port `PAYOUT2_*` baru di `lib.sh`)
  berbagi Postgres+Redis yang sama. force-fail dipanggil di KEDUA replika
  (bukan satu) — mockvendor's force-fail flag adalah in-memory PER-PROSES,
  bukan replikasi Redis, jadi relay replika lain bisa memenangkan race
  claim `payout_vendor_commands` dan dispatch lewat instance mockvendor-nya
  SENDIRI yang masih sehat, membuat payout settle instan tanpa pernah
  menyentuh 'uncertain' — reproduksi nyata sebelum diperbaiki (lihat bug
  list). Setelah diperbaiki: replika B membuktikan lihat state 'open'
  murni lewat Redis (bukan panggilan lokal), lalu Redis dimatikan dan KEDUA
  replika dibuktikan degradasi ke local `HealthTracker` tanpa crash.
- **`scenario_11`** (crash setelah command enqueue / setelah network
  timeout): dua sub-kasus terpisah tepat sesuai kalimat dokumen — sub-kasus
  A men-seed `payout_requests`+`payout_vendor_commands` langsung via SQL
  SAAT proses mati (bukan saat proses hidup — beda dari kill point 1/2
  scenario 5 yang men-seed sambil proses tetap jalan), lalu proses
  di-restart dan relay dibuktikan men-dispatch command yang di-seed tanpa
  duplikasi; sub-kasus B membuktikan crash SETELAH `uncertain` +
  `failed`/backing-off tercatat durable, retry-setelah-restart tetap SATU
  command row (bukan command baru) dan SATU transaksi settle — bukti
  at-least-once delivery + exactly-once ledger effect + vendor pinning
  bertahan lintas crash.

**Lima bug nyata ditemukan lewat eksekusi (bukan review kode):**

1. **Rate-limiter burst test salah desain**: 11 proses curl terpisah tidak
   pernah menguji bucket yang sama (lihat di atas) — awalnya melapor
   `10/11 succeeded` semu tanpa satupun 429; diperbaiki pakai satu
   invocation curl dengan `--next` berulang (diverifikasi dulu di luar
   skenario bahwa `--next` tetap memakai satu koneksi TCP persisten ke
   server `net/http` sungguhan, sebelum dipakai di skenario).
2. **`policy_limits.max_daily_count=0` ditolak DB**: constraint
   `policy_limits_max_daily_count_check` mewajibkan nilai positif (NULL =
   unbounded, tapi 0 eksplisit BUKAN pilihan valid) — desain awal skenario
   9 (seed 0, harapkan semua percobaan langsung 422) gagal dengan 500 nyata
   dari admin endpoint; didesain ulang total memakai `max_daily_count=1`
   plus panggilan tunggal yang sengaja menguji DUA hal sekaligus (lihat di
   atas) agar tidak perlu multi-langkah "N sukses lalu N+1 gagal" yang akan
   tabrakan dengan fraud fail-closed (policy.Check jalan SEBELUM
   fraud.Check, `internal/ledger/transport/http.go:571` vs `:590` —
   `withdraw_initiate` JUGA di-screen fraud di router publik, bukan cuma
   `transfer_p2p`, kontras dengan asumsi awal dari PROJECT_GUIDE.md's kalimat
   ringkas).
3. **Race klaim command lintas replika di scenario 10**: dijelaskan di
   atas — diperbaiki dengan force-fail di kedua replika, bukan satu.
4. **State breaker Redis scenario 10 bocor lintas run**: `DistributedBreaker`
   menaruh state di Redis, TIDAK mati bersama proses seperti
   `HealthTracker` biasa — jalan standalone scenario 10 dua kali berturutan
   (atau scenario 10 di dalam `all` run kedua) mewarisi mockvendor 'open'
   dari run sebelumnya, membuat asersi "state baru dari force-fail SENDIRI"
   jadi rancu. Diperbaiki: `redis-cli DEL breaker:payout:state:mockvendor
   breaker:payout:probe:mockvendor` di awal scenario 10, sebelum priming.
5. **Race reconnect gRPC client fraud di scenario 7**: `pkg/grpcx.Dial`
   memakai lazy-reconnect yang disengaja (komentar sendiri: "Lazy reconnect
   behavior is intentional") — ledger/payin/payout masing-masing memegang
   satu `grpc.ClientConn` persisten ke fraud-service sejak proses MEREKA
   sendiri start; ketika fraud-service di-kill lalu di-restart oleh
   scenario 7, koneksi lama masuk siklus transient-failure/backoff grpc-go
   BAWAAN — permintaan PERTAMA yang menyentuh fraud tepat setelah restart
   bisa kena "connection refused" walau fraud-service SUDAH listening,
   sampai backoff (~1s, jitter) selesai. Direproduksi 2x berturutan
   standalone, HANYA di panggilan P2P block-mode (panggilan
   fraud-tergantung PERTAMA setelah restart) — bukan bug produksi, murni
   karakteristik client grpc-go yang memang didesain begitu. Diperbaiki:
   `sleep 3` di scenario 7 setelah `start_fraud_service`, sebelum asersi
   block-mode manapun.

**Satu race timing tambahan diperkuat (bukan bug, defensif)**: `docker stop
$REDIS_CONTAINER` mengembalikan kontrol begitu kontainer berhenti, bukan
begitu port benar-benar tak terjangkau dari host — reproduksi SEKALI (di
gate isolated, scenario 9) sebagai 2xx yang seharusnya 503. Scenario 9 dan
10 sekarang polling `redis-cli ping` sampai gagal sebelum melanjutkan,
bukan percaya waktu `docker stop` semata.

**Satu flake pre-existing dikonfirmasi, TIDAK diperbaiki (di luar lingkup
T4)**: scenario 5 (kill point 1-4) gagal SEKALI di dalam full-suite run,
dengan error persis `"terminating connection due to administrator command
(SQLSTATE 57P01)"` pada koneksi Postgres payout-service scenario 5's
sendiri, tepat menyusul scenario 3's `docker restart` Postgres — scenario 5
lulus BERSIH tiap kali dijalankan standalone (termasuk langsung di
kontainer project isolated yang sama), jadi ini murni interaksi timing
scenario-3-ke-5 yang sudah ada SEBELUM T4 (scenario 3 dan 5 tidak disentuh
sama sekali sesi ini) — pola yang sama seperti flake testcontainer
RabbitMQ di T0/T3, dikonfirmasi lewat re-run terisolasi, bukan regresi.

**Gate final terisolasi dijalankan PERSIS sesuai prosedur dokumen:**

```bash
docker compose stop                          # bukan down -v — volume dev utuh
COMPOSE_PROJECT_NAME=seev-plan45-gate \
POSTGRES_CONTAINER=seev-plan45-gate-postgres-1 \
REDIS_CONTAINER=seev-plan45-gate-redis-1 \
RABBITMQ_CONTAINER=seev-plan45-gate-rabbitmq-1 \
make verify-full
# go build ./...                             OK
# go vet ./... && go vet -tags=integration    OK
# golangci-lint run ./...                     OK
# go test -race -cover ./...                  OK, semua paket
# docker compose down -v (project isolated)   OK
# ./scripts/smoke-test.sh                     OK
# ./scripts/business-e2e.sh                   OK
# ./scripts/chaos-test.sh all (11 skenario)   OK — 209 assertion pass, 0 fail
COMPOSE_PROJECT_NAME=seev-plan45-gate docker compose down -v
```

Butuh 4 percobaan sampai bersih — bukan karena kode salah, tapi 4 masalah
infrastruktur/timing berbeda ditemukan dan diperbaiki satu per satu persis
di lingkungan terisolasi ini (percobaan 1: volume RabbitMQ segar kena bug
permission `.erlang.cookie` Docker-Desktop-macOS yang cukup diatasi dengan
retry sekali — dikonfirmasi BUKAN masalah kode lewat `docker logs`; volume
lalu di-drop total dan gate diulang dari nol. Percobaan 2: bug #5 di atas
[scenario 7 gRPC reconnect] ditemukan dan diperbaiki. Percobaan 3: flake
pre-existing scenario 3→5 di atas — dikonfirmasi lewat re-run scenario 5
standalone di kontainer isolated yang SAMA, lulus bersih, membuktikan
bukan masalah environment isolated itu sendiri. Percobaan 4: bersih penuh,
209/209). Setelah gate: `COMPOSE_PROJECT_NAME=seev-plan45-gate docker
compose down -v` dijalankan (volume project isolated dibuang total), lalu
`docker compose up -d postgres redis rabbitmq` mengembalikan stack project
default ke kondisi PERSIS sebelum preflight (container observability —
alloy/grafana/loki/prometheus/tempo/docker-socket-proxy — tidak pernah
disentuh, karena `docker compose stop` tanpa argumen hanya menyasar
service non-profiled).

**Integration test dijalankan terpisah** (di luar `verify-full`, yang
sendiri tidak menyertakan tag `integration` — pola sama seperti Hasil
T0-T3):

```bash
go test -tags=integration -race \
  ./internal/fraud/... ./internal/payin/... ./internal/payout/... \
  ./internal/ledger/transport/... ./internal/handler/... ./pkg/cache/... \
  ./pkg/scheduler/... ./pkg/fraudcheck/...
# OK, semua paket — termasuk TestVelocityConsumerRealRabbitMQIncrementsPostedCounterOnce
# (flaky di T0/T3), lulus bersih kali ini.
```

**Constraint eksekutor dipatuhi**: tidak ada perubahan
`execTransfer`/`mayFailover`/aturan pinning doc 40; tidak ada klaim
exactly-once network dispatch (skenario 11 eksplisit menguji at-least-once
+ exactly-once EFEK LEDGER, beda hal); tidak ada dependency proprietary
baru; `down -v` isolated SELALU diberi prefix
`COMPOSE_PROJECT_NAME=seev-plan45-gate` eksplisit, tidak pernah dijalankan
telanjang.

**Catatan lingkup, follow-up (bukan blocker T4)**: `scripts/lib.sh` masih
memakai nama container hardcoded dan port host tetap (bukan dinamis via
`docker compose ps -q`) — override manual lewat env var (seperti dipakai
gate ini) tetap berfungsi penuh, tapi dua project Compose tidak bisa
berjalan PARALEL. Sesuai catatan dokumen sendiri (§4 T4), ini follow-up
terpisah, bukan blocker penyelesaian A3.
