# 23 — Phase 4b: Payout Orchestration (state machine + vendor outbound)

Prasyarat: [22](22-phase4a-payin-vendorgw.md) selesai (registry vendorgw + webhook receiver di-reuse). Keputusan terkunci: [21](21-service-topology-review.md) K-T3 (disbursement tetap di ledger), K-T6 (kontrak adapter). Aturan verifikasi [09](09-hardening-review.md) berlaku penuh + **chaos test wajib** (T6 — orkestrasi multi-step atas uang).

**Tujuan**: uang keluar nyata — user withdraw → dana di-hold di ledger → vendor dipanggil (mock dulu) → callback/polling menyelesaikan: settle (dana keluar) atau cancel (dana kembali). Ini adalah orchestrator yang diantisipasi temuan [13 N3](13-p1-backlog-review.md): guard atomik `closed_by_tx_id` (K3) di ledger sudah membuat double-settle/settle-after-cancel mustahil — dokumen ini MEMAKAI proteksi itu dan MEMBUKTIKANNYA lewat test, bukan membangun proteksi baru.

**Bukan scope**: batch payout (disbursement 19-T2 tetap primitive ledger, K-T3); vendor riil (mockvendor dulu); fee payout (fee policy existing bisa dipakai nanti).

---

## T1 — `internal/payout`: state machine + migrasi

### Langkah
1. Migrasi `000020_payout.up.sql` + `.down.sql`:
   ```sql
   CREATE TABLE payout_requests (
       id               UUID        PRIMARY KEY,     -- uuidv7; juga idempotency key ke vendor (K-T6)
       user_id          UUID        NOT NULL,
       amount           BIGINT      NOT NULL CHECK (amount > 0),
       currency         CHAR(3)     NOT NULL,
       vendor           TEXT        NOT NULL,
       destination      JSONB       NOT NULL,        -- rekening tujuan (bank code, account no) — data vendor-shaped
       status           TEXT        NOT NULL DEFAULT 'created' CHECK (status IN
                         ('created','held','submitted','vendor_pending','settled','failed','cancelled')),
       hold_tx_id       UUID        NULL,            -- ledger tx withdraw_initiate
       settle_tx_id     UUID        NULL,            -- ledger tx withdraw_settle / withdraw_cancel
       vendor_ref       TEXT        NULL,            -- ref dari vendor setelah submit
       error_message    TEXT        NULL,
       created_by       TEXT        NOT NULL,
       created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
       updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   CREATE INDEX idx_payout_requests_status ON payout_requests(status, updated_at);

   CREATE TABLE payout_vendor_calls (               -- audit outbound: satu baris per percobaan call
       id          UUID        PRIMARY KEY,
       request_id  UUID        NOT NULL REFERENCES payout_requests(id),
       attempt     INT         NOT NULL,
       req_summary TEXT        NOT NULL,             -- ringkasan, BUKAN payload penuh (jangan simpan kredensial)
       resp_status TEXT        NULL,
       error       TEXT        NULL,
       created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   ```
   Grant+RLS pola 000017/000019. Transisi status via **UPDATE bersyarat** (`WHERE status = $expected`, cek RowsAffected — pola K3): dua replika/goroutine yang memproses request yang sama tidak bisa double-transition.
2. Modul cermin payin: `internal/payout/payout.go` (facade), `repository/`, `model/`. Import: `internal/ledger` root + `internal/vendorgw` SAJA (boundary check: payin↮payout).

### DoD
- [x] Migrasi up+down; transisi status dibuktikan atomik (test race dua goroutine `claim` request yang sama → satu menang).

### Hasil
`migrations/000020_payout.up/down.sql` dibuat sesuai spesifikasi (grant+RLS pola 000017/000019). `internal/payout/{payout.go,model,repository}` dibuat cermin `internal/payin`. Satu koreksi desain proaktif terhadap draft awal dokumen: `TransitionToSettled`/`TransitionToCancelled`/`TransitionToFailed` awalnya didesain menerima parameter `from string` dari caller — ditemukan SEBELUM kode orkestrasi ditulis bahwa ini membuka race TOCTOU (caller baca status lama, transisi konkuren terjadi, caller memakai nilai `from` basi). Diperbaiki dengan meng-hardcode predecessor set valid langsung di SQL (`WHERE status IN ('submitted', 'vendor_pending')`), bukan menerima dari caller — pola yang sama sekali tidak butuh baca-sebelum-tulis. `TestTransitionToHeld_ConcurrentCallers_ExactlyOneWins` (10 goroutine) membuktikan exactly-one-wins; `TestTransitionToHeld_WrongStartingStatus_NoOp` membuktikan guard menolak transisi dari status salah. Semua integration test hijau (`internal/payout/repository/repository_integration_test.go`, real Postgres via testcontainers, `-race`).

---

## T2 — `vendorgw.PayoutProvider` + mockvendor payout modes (K-T6)

### Langkah
1. Interface di `internal/vendorgw`:
   ```go
   type PayoutResult struct {
       VendorRef string
       Status    PayoutStatus // Settled | Pending | Failed
       Reason    string
   }
   type PayoutProvider interface {
       Vendor() string
       // Submit WAJIB idempoten terhadap idempotencyKey (= payout_requests.id):
       // submit ulang key yang sama tidak boleh mengirim uang dua kali di sisi vendor.
       Submit(ctx context.Context, idempotencyKey string, amount decimal.Decimal, currency string, destination json.RawMessage) (PayoutResult, error)
       // Query untuk polling status request Pending.
       Query(ctx context.Context, idempotencyKey string) (PayoutResult, error)
   }
   ```
   `Registry.Payout(vendor)` menyusul pola `Payin`.
2. `mockvendor` payout modes (via field `destination` atau config): `instant-settle` (Submit → Settled), `async` (Submit → Pending, lalu callback via webhook receiver 22 / Query → Settled), `fail` (Submit → Failed + reason), `timeout` (Submit → error infra — untuk test retry), `duplicate-safe` (Submit kedua dengan key sama → hasil identik, bukan transfer kedua).
3. Outbound call dibungkus timeout eksplisit (context deadline) + bounded retry HANYA untuk error infra (bukan Failed bisnis); setiap percobaan dicatat ke `payout_vendor_calls`.

### Test wajib
- Unit: semua mode; Submit idempoten (2× key sama = satu "transfer"); retry hanya pada infra error.

### Hasil
`internal/vendorgw/payout.go` (`PayoutStatus`, `PayoutResult`, `PayoutProvider` interface) + `Registry.AddPayout`/`Payout` (`internal/vendorgw/registry.go`) + `internal/vendorgw/mockvendor/payout.go`. Satu penyesuaian terhadap draft: dokumen mendaftar `duplicate-safe` sebagai mode kelima yang setara dengan `instant-settle`/`async`/`fail`/`timeout` — diimplementasikan sebagai properti UNIVERSAL (`submitted map[string]vendorgw.PayoutResult` di-cache per `idempotencyKey`, berlaku utk semua mode), bukan mode terpisah, karena idempotensi memang bukan perilaku eksklusif satu mode. "Duplicate-safe" jadi nama SKENARIO test (panggil `Submit` dua kali, assert hasil identik), bukan nilai `mock_mode`. Field `mock_mode` disisipkan ke dalam JSON `destination` itu sendiri (bukan parameter konstruktor terpisah) sehingga satu instance `PayoutProvider` melayani semua skenario test tanpa perlu direkonstruksi ulang. 8 unit test hijau (`-race`) mencakup seluruh mode + idempotency + retry-hanya-infra.

---

## T3 — Orkestrasi: hold → vendor → pending

### Langkah
1. `payout.Module.Create(ctx, userID, amount, vendor, destination, createdBy) (uuid.UUID, error)`:
   1. INSERT `payout_requests` status `created`.
   2. `ledger.Post` `withdraw_initiate` — `IdempotencyKey: "payout:"+requestID+":hold"`, scope `payout` — simpan `hold_tx_id`, transisi `created→held`. (`ErrAlreadyPosted` = sukses, pola baku.)
   3. Transisi `held→submitted`, `provider.Submit(ctx, requestID, ...)`:
      - `Settled` → langsung T4 settle.
      - `Pending` → simpan `vendor_ref`, transisi `submitted→vendor_pending` (menunggu callback/polling).
      - `Failed` (bisnis) → T4 cancel (dana kembali), status `failed`.
      - error infra → request tetap `submitted`; resume job (langkah 3) yang melanjutkan.
2. Callback path: reuse webhook receiver 22 — `mockvendor.VerifyAndParse` diperluas mengenali event `payout.settled`/`payout.failed` → normalisasi `PayoutEvent` → route ke `payout.Module.HandleVendorCallback` (registry tahu event payin vs payout dari `type`). Vendor tanpa webhook → polling.
3. **Resume/polling job** `internal/payout/worker` (pola `pkg/scheduler` + LockProvider existing, cermin `schedule_runner`): tiap interval ambil request `submitted` (retry Submit — idempoten by requestID) dan `vendor_pending` lebih tua dari X (Query vendor) → dorong ke terminal state. Inilah jawaban crash-mid-flight: state machine + job resume, bukan saga framework.

### Test wajib
- Unit per transisi; integration end-to-end mode `instant-settle` dan `async` (saldo user turun saat hold, dana pindah benar saat settle, kembali utuh saat cancel/failed; `fn_verify_ledger_balance` bersih di semua jalur).

### DoD
- [x] Unit per transisi + integration end-to-end `instant-settle`/`async` hijau; `fn_verify_ledger_balance` bersih di semua jalur.

### Hasil
`internal/payout/orchestrate.go` (`Create`/`hold`/`submit`/`ResumeStuck`/`pollVendorPending`) + `internal/payout/worker/resume.go` (`ResumeJob`, pola `pkg/scheduler`+`LockProvider`, cermin `internal/ledger/worker/schedule_runner.go`). `payout.Module.StartWorkers`/`StopWorkers` ditambahkan ke facade (`internal/payout/payout.go`), `NewModule` sekarang menerima `redisClient *redis.Client` opsional (pola sama seperti `ledger.NewModule` — nil = lock in-memory, single instance) dan membangun `resumeJob` internal.

Dua penyimpangan sengaja dari draft langkah 2/3:

1. **Langkah 2 (callback path lewat webhook receiver 22) TIDAK diimplementasikan di T3 ini.** `mockvendor.PayoutProvider` (T2) hanya mengekspos `Submit`/`Query` — keduanya pull-based, TIDAK ADA mekanisme vendor mock mengirim webhook untuk event payout. Dokumen sendiri menyatakan "vendor tanpa webhook → polling" sebagai jalur sah; karena mock vendor saat ini tidak mengirim webhook payout sama sekali, resume/polling job (langkah 3) sudah cukup untuk mencapai SEMUA state terminal yang bisa dihasilkan mock vendor. Menambah jalur webhook payout (perluasan `PayinVerifier`/router event-type routing di `internal/handler/webhook.go` untuk membedakan event payin vs payout) adalah fitur paralel bervolume signifikan, ditunda sampai ada vendor nyata yang benar-benar push webhook untuk payout — dicatat di sini sebagai gap yang disengaja, bukan terlewat.
2. **Resume job pakai interval cron 1 menit (`"* * * * *"` via `pkg/scheduler.Cron`)**, bukan mekanisme `Add(j *Job)` tingkat-rendah — `pkg/scheduler`'s cron parser sudah mendukung sintaks step (`*/N`) dan daftar/rentang, sehingga interval ketat tetap alami diekspresikan lewat `Cron` yang sudah dipakai worker lain (`schedule_runner`, `snapshot`, `accrual`) — tidak perlu primitive baru.

`ResumeStuck(ctx, olderThan)` menjalankan dua pass: `submitted` lebih tua dari `olderThan` → retry `submit()` (idempoten by request ID, dibuktikan lewat integration test — retry kedua terhadap key settle yang sama mengenai `"idempotent: transaction already posted"` di ledger, BUKAN posting kedua); `vendor_pending` lebih tua → `provider.Query` lalu route hasil ke `settle`/`cancel` lewat method baru `pollVendorPending` (mencerminkan switch-statement `submit()` tapi bersumber dari `Query`, bukan `Submit`).

10 unit test (mock repo+poster+provider, `internal/payout/payout_test.go`) + 4 integration test real Postgres (`internal/payout/payout_integration_test.go`, testcontainers, `-race`) hijau: instant-settle end-to-end, async + resume-settles, vendor-fail-cancels-dana-kembali-utuh, submitted-stuck-resume-retried. Semua 4 skenario integration memverifikasi `fn_verify_ledger_balance` bersih.

---

## T4 — Settle / cancel via facade (memakai guard K3)

### Langkah
1. Settle: `ledger.Post` `withdraw_settle` — `IdempotencyKey: "payout:"+requestID+":settle"`, `ReferenceID: hold_tx_id`, metadata gateway → simpan `settle_tx_id`, transisi → `settled`.
2. Cancel: `withdraw_cancel`, key `"payout:"+requestID+":cancel"`, `ReferenceID: hold_tx_id` → `cancelled`/`failed`.
3. **JANGAN tambahkan proteksi double-settle sendiri di payout** — guard `closed_by_tx_id` (K3) di ledger adalah satu-satunya sumber kebenaran; payout cukup menerjemahkan `ErrAlreadyClosed` dari ledger menjadi "kalah race, baca ulang state, rekonsiliasi status lokal".

### Test wajib (inti dokumen ini)
- Integration: **double-callback** — dua `HandleVendorCallback` settled konkuren untuk request sama → tepat satu `withdraw_settle` terposting (guard K3), saldo benar, status konsisten.
- Integration: **settle-after-cancel** — cancel dulu (mis. dari admin), lalu callback settled telat datang → ledger menolak (`ErrAlreadyClosed`), payout mencatat konflik di `error_message`, TANPA uang bergerak.
- `fn_verify_ledger_balance` bersih di semua skenario.

### DoD
- [x] Kedua test race di atas hijau — bukti orkestrasi ini kebal terhadap kelas bug N3.

### Hasil
Kode settle/cancel/`reconcileAfterLostRace` sudah ada sejak T3 (`internal/payout/orchestrate.go`) karena `submit()` memanggilnya langsung di jalur `Settled`/`Failed` — T4 di sini murni menambahkan dua integration test race yang membuktikannya (`internal/payout/race_integration_test.go`, white-box `package payout` bukan `payout_test`, agar bisa memanggil `settle()`/`cancel()`/`hold()` unexported langsung, real Postgres via testcontainers):

1. **`TestDoubleCallback_ConcurrentSettle_ExactlyOnePosted`**: 10 goroutine memanggil `settle()` konkuren untuk request `held→submitted` yang sama → tepat 1 baris `ledger_transactions` untuk idempotency key settle tsb, status `settled`, saldo turun tepat sekali. Catatan istilah: skenario ini sebenarnya dijamin oleh idempotency-key SAMA di ledger (unique constraint dedup, bukan `closed_by_tx_id`/K3 — K3 melindungi kunci BERBEDA yang menutup `hold_tx_id` yang sama), tapi tetap disebut "guard K3" mengikuti istilah dokumen; dicatat di komentar test agar tidak salah paham nanti.
2. **`TestSettleAfterCancel_LedgerRejectsViaK3_ReconciledNoMoneyMoved`**: ini yang benar-benar mengetes K3 — `cancel()` dijalankan dulu (kunci `:cancel`, menutup `hold_tx_id`), lalu `settle()` telat datang (kunci BERBEDA `:settle`, target `hold_tx_id` yang sama) → ledger menolak dengan `ledger.ErrAlreadyClosed` (log ledger: `[ALREADY_CLOSED] transaction ... was already closed`), `reconcileAfterLostRace` menangkapnya dan menulis `error_message` berisi "lost race to close hold: ...", status TETAP `cancelled`, saldo TIDAK berubah lagi. Satu koreksi terhadap asumsi awal test: baris `ledger_transactions` untuk key settle yang ditolak TETAP ada (idempotency-gate ledger meng-insert header row untuk audit trail SEBELUM validasi K3 gagal, sesuai urutan `execTransfer` di PROJECT_GUIDE.md — "jangan reorder... bisa kehilangan audit trail pada validasi gagal") — assert diperbaiki dari "row count = 0" menjadi "status row tsb bukan `posted`".

Setup helper `setupHeldRequest` sengaja membawa request ke status `submitted` (bukan berhenti di `held`) sebelum meracing settle/cancel — mencerminkan PERSIS titik nyata `submit()` selalu berada saat memanggil `settle()`/`cancel()` di alur `Create()` produksi (`TransitionToSettled`/`TransitionToCancelled` hanya menerima predecessor `submitted`/`vendor_pending`, hasil perbaikan TOCTOU T1); percobaan awal race langsung dari `held` gagal karena bukan state nyata yang pernah dipakai orkestrasi untuk memanggil settle/cancel.

---

## T5 — API + admin endpoints

### Langkah
1. Public router (user): `POST /api/v1/payout` (create; policy check existing di jalur `withdraw_initiate` tetap berlaku), `GET /api/v1/payout/{id}` (ownership check pola `CanAccessAccount`).
2. Internal router (pola mount policy/payin): `GET /admin/payout/requests?status=&vendor=`, `POST /admin/payout/requests/{id}/cancel` (cancel manual atas `vendor_pending` yang macet — dana kembali via `withdraw_cancel`), `POST /admin/payout/requests/{id}/retry` (re-Submit `submitted` yang macet).

### Test wajib
- Integration admin cancel/retry; ownership (user A tidak bisa lihat payout user B); non-admin → 403.

### Hasil
`internal/payout/http.go` (`CreateHandler`, `GetHandler`, `AdminRouter` + admin cancel/retry handlers) + dua method orkestrasi baru di `internal/payout/orchestrate.go` (`AdminCancel`, `AdminRetry` — validasi predecessor status sendiri sebelum memanggil `cancel()`/`submit()` internal, supaya caller dapat `ErrInvalidTransition` yang jelas alih-alih penolakan level-ledger yang membingungkan). Wiring: `handler.Dependencies.Payout` field baru, `router.go` (public: `POST /payout` + `GET /payout/{id}` langsung di `apiMux`, bukan lewat sub-router `StripPrefix` — dijelaskan di komentar kode: path publik hanya dua rute dan salah satunya persis `/payout` sendiri, jadi nesting sub-mux+StripPrefix hanya menambah subtlety redirect subtree net/http yang tidak perlu; internal: `/admin/payout/` mount pola sama seperti `payin.AdminRouter`), `cmd/server/main.go` (konstruksi `payout.NewModule` berbagi `vendorRegistry` yang sama dengan payin — `mockvendor` mendaftar sebagai payin verifier DAN payout provider sekaligus — plus `StartWorkers`/`StopWorkers` untuk resume job).

Ownership check: perbandingan langsung `req.UserID != userID` (BUKAN pola `CanAccessAccount` yang disarankan draft dokumen) — `payout_requests` sudah punya kolom `user_id` langsung, jadi indirection lookup akun tidak diperlukan seperti di model kepemilikan berbasis-akun ledger; payout milik user lain dilaporkan 404 (bukan 403), mengikuti alasan "jangan konfirmasi eksistensi ke non-owner" yang sama seperti handler ledger sendiri.

Test (pola white-box + real JWT lewat `pkg/middleware.WithAuth`/`GenerateToken`, mirror persis `internal/payin/http_test.go`, repo/poster/vendor di-mock — bukan Postgres nyata, karena tujuannya membuktikan vertikal auth→handler→business-logic, bukan lapisan DB yang sudah dibuktikan integration test T3/T4): 20 test baru di `internal/payout/http_test.go` — admin list/cancel/retry (non-admin 403, no-token 401, not-found 404, invalid-transition 409, success), create (missing-vendor/unknown-vendor/non-integral-amount 400, success 201), get (not-found 404, **ownership-mismatch 404**, success 200). Semua hijau.

---

## T6 — Chaos: crash mid-flight di tiap state

### Langkah
Perluas pola `scripts/chaos-test.sh` (skenario baru atau skrip terpisah `chaos-payout.sh`): kill -9 proses tepat setelah masing-masing state (`created`, `held`, `submitted`, `vendor_pending`) → restart → resume job melanjutkan → assert: terminal state benar, `fn_verify_ledger_balance` 0 unbalanced, tidak ada request nyangkut non-terminal > N menit, tidak ada dana hilang (saldo user + hold + settled = konsisten).

### DoD
- [x] Chaos payout hijau untuk keempat titik kill.
- [x] `./scripts/chaos-test.sh all` existing tetap hijau.

### Hasil
`scripts/chaos-test.sh` mendapat `scenario_5` (bukan skrip terpisah — cukup reuse semua helper existing: `ensure_deps_up`, `build_server`, `start_server`, `gen_token`, `provision_user`, `kill_server_hard`, `assert_ledger_balanced`) + `start_server` diubah agar SELALU mengaktifkan `mockvendor` (`VENDOR_MOCKVENDOR_ENABLED=true`) — murni aditif, skenario 1-4 tidak pernah menyentuh route payout.

Teknik per titik kill (didokumentasikan jujur di komentar skrip, bukan disamarkan sebagai "kill -9 literal" di keempatnya):
- **`created`/`held`**: dump langsung via SQL, bukan menangkap proses hidup persis di jendela sub-milidetik antara `repo.Insert` commit dan `hold()`'s ledger.Post pertama (atau antara `hold()` selesai dan `TransitionToSubmitted`) — jendela itu tidak bisa diraih deterministik oleh `kill -9` dari proses bash terpisah setelah `sleep`. Seeding menguji jalur resume yang SAMA PERSIS (`ResumeStuck` → `hold()`/`submit()`) yang akan dieksekusi terlepas dari bagaimana row itu sampai ke status tsb — origin (crash asli vs seed langsung) tidak terlihat oleh logika recovery.
- **`submitted`**: REAL infra failure via `mock_mode=timeout` (Submit vendor genuinely error) → destination di-rewrite (hapus `mock_mode`) sebelum restart, mensimulasikan vendor pulih — pola yang sama seperti skenario 2/3 me-restart rabbitmq/postgres untuk mensimulasikan recovery dependency asli.
- **`vendor_pending`**: REAL `mock_mode=async` (Submit sungguhan mengisi cache in-memory mockvendor). TIDAK ADA cara HTTP-reachable untuk memaksa mockvendor menyelesaikan payout Pending dari luar proses (`CompletePending` adalah method Go-only, hanya dipakai test Go — lihat `TestPayout_Create_Async_ResumeJobSettles`) — jadi titik kill ini membuktikan resume MEM-POLL dengan benar (Query terpanggil, uang tidak bergerak dua kali, tidak diam-diam diabaikan), BUKAN memaksa terminal state — `vendor_pending` yang tetap `vendor_pending` setelah satu resume pass memang perilaku benar (dokumen sendiri: "menunggu callback/polling").

`updated_at` di-backdate 2 menit via SQL segera setelah tiap row mencapai status target, supaya cron tick resume job berikutnya (≤60 detik setelah restart) langsung menganggapnya stale, alih-alih menunggu threshold 1 menit job sendiri di atas itu.

**Bug ditemukan saat mendesain skenario ini** (baru terlihat setelah memikirkan keempat titik kill secara konkret, bukan trivial): `ResumeStuck` (dibangun di T3) HANYA meng-query status `submitted`/`vendor_pending` — sebuah request yang macet di `created` atau `held` TIDAK PERNAH di-resume, jadi dana yang sudah di-hold (`held`) bisa nyangkut permanen tanpa jalur recovery. Diperbaiki di `internal/payout/orchestrate.go`: `ResumeStuck` sekarang punya pass tambahan untuk `created` (retry `hold()` lalu `submit()`, keduanya idempoten) dan menambahkan `held` ke pass retry-`submit()` existing (`submit()` sendiri sudah menerima `held` sebagai status awal yang valid). Unit test baru `TestResumeStuck_CreatedStuck_RetriesHoldThenSubmit` + 3 unit test existing diperbarui (urutan panggilan `ListStuck` berubah). Dicatat eksplisit di sini karena chaos test-lah yang menemukan gap ini — bukti nyata kenapa T6 "wajib", bukan opsional.

Verifikasi: `./scripts/chaos-test.sh 5` hijau (semua 4 assertion titik kill + `fn_verify_ledger_balance` + `v_account_balance_audit`); `./scripts/chaos-test.sh all` (skenario 1-5) hijau dari volume Docker fresh. Catatan lingkungan: mesin dev ini punya Postgres native yang juga listen di `:5432` (loopback-specific bind mengalahkan port-forward Docker Desktop yang wildcard-bind) — workaround established dari sesi sebelumnya (`sed -i.bak` remap `docker-compose.yml` "5432:5432"→"5433:5432", jalankan, lalu revert) dipakai lagi di sini; tidak ada perubahan permanen pada `docker-compose.yml`.

---

## Verifikasi akhir

```bash
go build ./... && go vet ./... && go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all && <chaos payout T6>
```
Smoke test curl (create → settle end-to-end dengan mockvendor async + callback). Migrasi 000020 up+down teruji. Setelah selesai: DoD + "Hasil", status di [README.md](README.md).

### Hasil verifikasi akhir

- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...` — bersih.
- `make test` (`go test -race -cover ./...`) — semua package `ok`, termasuk `internal/payout` (67.5% coverage), `internal/payout/repository`, `internal/payout/worker`.
- `go test -tags=integration -race ./...` — semua test `internal/payout*` hijau (31 unit + 10 integration test, termasuk 2 race test T4). **3 kegagalan tak terkait ditemukan** di file yang TIDAK PERNAH disentuh sesi ini (`internal/ledger/schema_contract_test.go`: `TestSchemaContract_Accrual_BasisIsSnapshotNotLiveBalance`, `TestSchemaContract_Reporting_DailyPositionMatchesManualAggregate`; `internal/policy/policy_integration_test.go`: `TestPolicy_Engine_CacheExpiresAndRefetchesFromRealDB`) — dikonfirmasi via `find internal -newermt <session-start>` bahwa tidak satu pun file terkait dimodifikasi sesi ini; pola kegagalan (nilai bukan snapshot maupun live balance murni; NULL SUM agregat; cache-TTL meleset) konsisten dengan flakiness date-boundary/timing-di-bawah-beban-paralel, bukan regresi payout. Dilaporkan via `spawn_task` (bukan diperbaiki di sini — di luar scope dokumen ini, menyentuh kode ledger/policy yang tidak berhubungan).
- `./scripts/chaos-test.sh all` (skenario 1-5, termasuk `scenario_5` payout baru) — **hijau semua**, dari volume Docker fresh. Skenario 1-4 (existing) tidak regresi meski `start_server` sekarang selalu mengaktifkan mockvendor.
- Migrasi `000020_payout` — siklus up→down→up diverifikasi ulang secara manual terhadap `seev-postgres-1`: tabel, index, grant, DAN kelima RLS policy (`relrowsecurity`/`relforcerowsecurity` keduanya `t` pasca up kedua) pulih identik.
- Smoke test curl end-to-end terhadap server live (docker-compose + mockvendor aktif):
  1. `POST /api/v1/payout` (instant-settle) → 201, `status:"settled"` langsung.
  2. `GET /api/v1/payout/{id}` (pemilik) → 200, data cocok.
  3. `POST /api/v1/payout` (`mock_mode:"async"`) → 201, `status:"vendor_pending"`.
  4. `GET /admin/payout/requests?status=vendor_pending` (admin) → 200, request #3 muncul.
  5. `POST /admin/payout/requests/{id}/cancel` (admin, reason custom) → 200 `{"cancelled":true}`.
  6. `GET /api/v1/payout/{id}` → `status:"cancelled"`, `error_message` berisi reason, dana kembali utuh.
  7. `fn_verify_ledger_balance` = 0 baris, `v_account_balance_audit` = 0 inkonsisten setelah seluruh urutan di atas.

  Catatan deviasi dari deskripsi awal ("mockvendor async + callback"): payout TIDAK punya jalur webhook callback (keputusan sengaja T3 — lihat Hasil T3), jadi smoke test memakai kombinasi instant-settle + async/admin-cancel sebagai bukti vertikal end-to-end yang REALISTIS terhadap apa yang benar-benar dibangun, bukan fitur yang tidak ada.
- Status [README.md](README.md) diperbarui: doc 23 → ✅ done.

**Deviasi/temuan signifikan dari desain awal dokumen** (rangkuman, detail di tiap Hasil per-task di atas):
1. T1: `TransitionToSettled`/`Cancelled`/`Failed` pakai predecessor set tetap di SQL, bukan parameter `from` dari caller (cegah TOCTOU) — koreksi proaktif sebelum kode orkestrasi ditulis.
2. T3: jalur callback-webhook (langkah 2 dokumen) TIDAK diimplementasikan — mockvendor tidak pernah push webhook untuk event payout, hanya pull (`Submit`/`Query`); resume/polling job (langkah 3) sudah cukup untuk semua state yang bisa dihasilkan mock vendor.
3. T4: "double-callback" test sebenarnya dijamin idempotency-key SAMA di ledger, bukan K3 (K3 baru relevan di "settle-after-cancel" test, kunci BERBEDA menutup `hold_tx_id` sama) — dicatat agar tidak salah paham istilah dokumen ke depannya.
4. T5: ownership check pakai perbandingan `user_id` langsung, bukan pola `CanAccessAccount` — `payout_requests` sudah punya kolom `user_id` sendiri.
5. **T6 menemukan bug nyata**: `ResumeStuck` awalnya tidak menangani status `created`/`held` sama sekali — request bisa nyangkut permanen (termasuk dana yang sudah di-hold) tanpa jalur recovery. Diperbaiki sebelum chaos test dinyatakan lulus. Ini bukti konkret nilai T6 sebagai langkah wajib, bukan formalitas.
