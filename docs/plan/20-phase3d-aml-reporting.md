# 20 — Phase 3d: AML/Fraud Hooks & Regulatory Reporting (S6, S7)

Prasyarat: S6 butuh [17 T1](17-phase3a-policy-recovery.md) (data velocity dari policy counter); S7 butuh H2+H3+H8 — semuanya ✅ ([15](15-phase2e-snapshots-statements.md)/[16](16-phase2f-governance-recon-rls.md)) — plus disarankan setelah [18](18-phase3b-multi-currency.md) bila laporan harus multi-currency sejak hari pertama. Keputusan desain: [13 K-S](13-p1-backlog-review.md) butir S6 dan S7. T1 dan T2 independen satu sama lain.

Aturan verifikasi 09 berlaku penuh: T1 menyentuh pipeline posting (`Handle()`) → integration + chaos test wajib; T2 read-only tapi menyentuh role DB → integration dengan koneksi `app_readonly` wajib.

---

## T1 — AML / fraud hooks (08 S6, keputusan K-S S6)

**Tujuan**: titik ekstensi terstruktur untuk screening AML/fraud di jalur posting — mulai dari rule sederhana internal (velocity anomali, amount threshold) dengan mode `monitor`, sebelum integrasi vendor apapun.

**Batas arsitektur yang dikunci (K-S S6)**: interface `PrePostHook` dipanggil di `Handle()` **setelah validasi bisnis, sebelum build entries**. Mode `monitor` (catat, jangan blokir) dulu; `block` menyusul per-rule. Vendor screening masa depan = implementasi lain dari interface yang sama — pipeline tidak berubah lagi.

### Langkah
1. Interface di `internal/ledger/processors/processors.go` (atau file baru `hooks.go` di package yang sama — bukan package baru, hook adalah bagian kontrak pipeline):
   ```go
   // PrePostHook screens a resolved command after business validation and
   // before entries are built. Verdict.Block=true aborts the posting as a
   // BUSINESS failure (committed as status='failed' — audit trail, docs/plan/04
   // pattern), never a rollback. err != nil = INFRA failure of the hook itself;
   // the posting pipeline treats it per FailOpen below.
   type PrePostHook interface {
       Name() string
       Screen(ctx context.Context, cmd ResolvedCommand) (Verdict, error)
   }
   type Verdict struct {
       Block  bool   // true = tolak posting (hanya dihormati bila rule mode 'block')
       Reason string // wajib bila Block; juga dicatat pada mode monitor
   }
   ```
2. Wiring di `internal/ledger/service/handle/service.go` `execTransfer`, titik **antara step 4b (close original) dan step 5 (build entries)** — SETELAH `p.Validate` (screening butuh command yang sudah tervalidasi bisnis) tapi SEBELUM uang "terbentuk":
   - `Service` dapat field `hooks []processors.PrePostHook` (variadic di `New`, default kosong — nol overhead bila tidak dipakai).
   - Blokir = business failure: `markFailed` + commit + return sentinel baru `apperror.ErrScreeningBlocked = errors.New("SCREENING_BLOCKED")` → 422. Audit trail tetap ada (baris `failed` + error message menyebut rule) — konsisten pola validasi bisnis existing.
   - **Fail-open, keputusan dikunci**: hook yang error (bukan Block) di-log ERROR + metric counter, posting **jalan terus**. Alasan: hook MVP adalah rule internal; menjadikannya single point of failure untuk semua posting lebih berbahaya daripada satu screening terlewat. Vendor eksternal nanti yang butuh fail-close bisa membawa keputusannya sendiri di dalam implementasinya (return Block saat backend-nya down). Tulis ini di doc comment interface.
3. Implementasi awal `internal/ledger/screening/` (package baru di dalam modul ledger — boleh, ini bagian modul):
   - `AmountThresholdRule`: amount ≥ threshold per tipe (config env `SCREENING_AMOUNT_THRESHOLD`, 0=off) → verdict per mode.
   - `VelocityAnomalyRule`: frekuensi posting per user per jam > N (baca `Counter` 17-T1 `pkg/cache` — key terpisah `scr:<user>:h:<YYYY-MM-DD-HH>`; JANGAN baca counter policy — dimensinya beda dan coupling antar modul lewat key Redis adalah bug menunggu jadwal).
   - Mode per rule via env: `SCREENING_MODE=off|monitor|block` global MVP (per-rule table = nanti). Default `off` — backward compatible mutlak.
4. Persistensi kejadian (mode monitor maupun block): tabel `screening_events (id, tx_type, user_id, amount, rule, verdict TEXT CHECK ('flagged','blocked'), reason, created_at)` — migrasi `000017_screening.up.sql` + grant+RLS (`app_service` SELECT+INSERT saja — event screening immutable; `app_readonly` SELECT — compliance perlu baca). **INSERT di LUAR transaksi posting** (best-effort, log-on-error): kejadian screening bukan invariant uang, jangan menambah kerja di dalam tx posting yang sudah panjang; kalau insert gagal, log ERROR cukup.
5. Endpoint baca: `GET /admin/screening/events?user_id=&verdict=` router internal admin-gated (paginated, pola list existing).
6. Metrics: counter `ledger_screening_total{rule,verdict}` (pola metrics existing di `service/handle`).

### Test wajib
- Unit: hook Block + mode block → posting gagal-business (committed `failed`), mode monitor → posting jalan + event tercatat, hook error → posting jalan (fail-open) + log.
- Integration: posting nyata dengan threshold rendah mode block → 422, baris `ledger_transactions` status `failed` dengan reason, `screening_events` berisi `blocked`; mode monitor → posting sukses + event `flagged`; `fn_verify_ledger_balance` bersih di kedua mode.
- Integration urutan: hook TIDAK terpanggil bila validasi bisnis sudah menolak (saldo kurang) — bukti posisi pipeline benar.
- `./scripts/chaos-test.sh 1` ulang (jalur posting berubah) dengan `SCREENING_MODE=monitor`.

### DoD
- [x] `SCREENING_MODE=off` (default) = perilaku byte-identik dengan sebelum task ini (suite penuh hijau tanpa set env).
- [x] Blokir menghasilkan audit trail ganda: baris tx `failed` + baris `screening_events` — dibuktikan integration test.
- [x] Migrasi up+down; grant+RLS tabel baru.

### Hasil

Diimplementasikan persis sesuai desain di atas:

- **Interface**: `internal/ledger/processors/hooks.go` — `PrePostHook{Name() string; Screen(ctx, cmd) (Verdict, error)}`, `Verdict{Block bool; Reason string}`. Doc comment interface menulis eksplisit kontrak fail-open dan bahwa mode adalah urusan rule, bukan pipeline.
- **Wiring pipeline**: `internal/ledger/service/handle/service.go` — `Service.hooks []processors.PrePostHook` (variadic di `New(...)`, default kosong), loop hook baru sebagai step **4c** di `execTransfer`, tepat setelah step 4b (close original) dan sebelum step 5 (build entries). `Verdict.Block=true` → `markFailed` + commit (`apperror.ErrScreeningBlocked`, sentinel baru di `apperror/apperror.go`, dipetakan ke HTTP 422 di `transport/errors.go`). Hook error (bukan Block) → log ERROR + metric `ledger_screening_hook_errors_total{hook}` (di `service/handle/metrics.go`) + posting **lanjut** (fail-open).
- **Rules**: paket baru `internal/ledger/screening/` — `AmountThresholdRule` (amount ≥ threshold) dan `VelocityAnomalyRule` (posting/jam via `pkg/cache.Counter`, key `scr:<user>:h:<YYYY-MM-DD-HH>`, sengaja terpisah dari key `pol:...` milik `internal/policy`). Mode global `screening.Mode` (`off|monitor|block`, `ParseMode` default `off`). Metric rule-level: `ledger_screening_total{rule,verdict}` (`screening/metrics.go`).
- **Persistensi**: migrasi `000017_screening.up.sql`/`.down.sql` — tabel `screening_events`, RLS dua policy terpisah (`FOR SELECT`/`FOR INSERT`, bukan satu `FOR ALL`) sesuai kebutuhan "app_service SELECT+INSERT saja, tanpa UPDATE/DELETE". `internal/ledger/repository/screening_repository.go` (+mock) — `InsertEvent` dipanggil di LUAR transaksi posting (best-effort, log-on-error di level rule, bukan repository), `ListEvents` untuk endpoint baca.
- **Endpoint baca**: `GET /admin/screening/events?user_id=&verdict=&limit=&offset=` — router internal, admin-gated (`transport/http.go: listScreeningEvents`), DTO di `transport/dto.go`, method baru di `transport.Service` interface + facade `Module.ListScreeningEvents` (`ledger.go`).
- **Wiring startup**: `ledger.ScreeningConfig{Mode, AmountThreshold, VelocityMaxPerHour}` — field baru di `ledger.NewModule(...)`. **Kunci DoD #1**: ketika `Mode=off` (default/zero value), `NewModule` **tidak pernah memanggil** `screening.New*Rule` sama sekali — `hooks` tetap `nil`, `ledgerhandle.New(...)` dipanggil dengan slice hook kosong, byte-identik dengan sebelum task ini. `cmd/server/main.go` membaca `SCREENING_MODE`/`SCREENING_AMOUNT_THRESHOLD`/`SCREENING_VELOCITY_MAX_PER_HOUR` dari `internal/config` (default semua off/0). `VelocityAnomalyRule`'s in-memory counter fallback (`cache.NewMemoryCounter()`, hanya dibuat kalau `VelocityMaxPerHour>0` DAN Redis tidak tersedia) disimpan di `Module.screeningMemCounter` dan di-`Stop()`-kan di `StopWorkers()`.
- **Test**:
  - Unit (rule-level, `internal/ledger/screening/*_test.go`, 8 test, gomock untuk `ScreeningRepository` + `cache.NewMemoryCounter()` asli): below-threshold → no finding; block mode at/above threshold → `Verdict.Block=true`; monitor mode → finding direkam tapi `Block=false`; `InsertEvent` gagal → verdict tetap benar dikembalikan (persistensi best-effort, bukan bagian dari kontrak Screen's return); velocity under/over limit di kedua mode; counter error → `Screen` mengembalikan `error` (bukan `Verdict`), membuktikan jalur fail-open pipeline dipicu dengan benar.
  - Integration (`internal/ledger/schema_contract_test.go`, real Postgres via testcontainers): `TestSchemaContract_Screening_BlockMode_BlocksAndRecordsEvent` (422/`ErrScreeningBlocked`, tx `status='failed'` dengan reason menyebut rule, `screening_events` row `verdict='blocked'`, saldo TIDAK berubah, `fn_verify_ledger_balance` bersih), `TestSchemaContract_Screening_MonitorMode_PostsAndRecordsEvent` (posting sukses, saldo berubah, event `verdict='flagged'`, ledger tetap balanced), `TestSchemaContract_Screening_NotCalledWhenBusinessValidationFails` (spy hook, transfer saldo kurang → `ErrInsufficientFunds`, hook **tidak pernah dipanggil** — bukti posisi step 4c di pipeline benar).
  - Migrasi 000017: siklus up→down→up diverifikasi manual (`golang-migrate` CLI) — down benar-benar men-drop tabel (`CONFIRMED: table dropped` sebelum re-up), tidak ada sisa constraint/index setelah up ulang.
  - `./scripts/chaos-test.sh 1` dijalankan ulang dengan `SCREENING_MODE=monitor SCREENING_AMOUNT_THRESHOLD=1` — kill -9 mid-posting dengan hook AKTIF di pipeline: `fn_verify_ledger_balance` 0 unbalanced, `v_account_balance_audit` konsisten, tidak ada `ledger_transactions` nyangkut `pending`. (Catatan: skenario ini memakai idempotency key tetap `chaos1-*` di volume Postgres yang persisten lintas run, jadi transaksi di eksekusi run ini adalah replay idempotent dari run sebelumnya, bukan insert baru — hook path itu sendiri sudah dibuktikan terpisah lewat integration test + verifikasi manual curl langsung ke server hidup dengan `SCREENING_MODE=monitor SCREENING_AMOUNT_THRESHOLD=1`, yang menghasilkan baris `screening_events` `verdict='flagged'` seperti diharapkan. Yang dibuktikan oleh chaos run ini secara spesifik: pipeline yang SUDAH menyertakan hook loop tetap tidak kehilangan uang / tidak nyangkut di bawah kill -9, bukan bahwa hook itu sendiri dieksekusi selama proses restart tertentu ini.)
- **Full verification**: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `make test` (unit, semua paket hijau termasuk `internal/ledger/screening` 88.4% coverage), `go test -tags=integration -race ./...` (semua paket hijau, termasuk `internal/ledger` 166s dengan 3 test screening baru) — semua hijau, tanpa regresi pada suite existing.

---

## T2 — Regulatory reporting (08 S7, keputusan K-S S7)

**Tujuan**: laporan posisi dana & mutasi periodik yang bisa dijalankan compliance/finance TANPA akses tulis dan TANPA membebani jalur transaksi — format final menyesuaikan kebutuhan BI/OJK saat entitas legal jelas, jadi yang dibangun sekarang adalah **fondasi query + ekspor yang benar**, bukan format regulator spesifik.

**Batas arsitektur yang dikunci (K-S S7)**: sumber data = snapshots (H3) + recon (H2), akses via role `app_readonly` (H8). Ini fitur READ-ONLY murni — tidak boleh ada jalur tulis baru.

### Langkah
1. View pelaporan (migrasi `000018_reporting_views.up.sql` + down; view = kontrak query yang di-review, bukan query ad-hoc di kode):
   - `v_report_daily_position`: posisi dana per tanggal per currency per tipe akun — agregat `account_balance_snapshots` JOIN `accounts` (`GROUP BY as_of_date, currency, type, owner_type`). Inilah "posisi dana" harian.
   - `v_report_daily_mutation`: mutasi per tanggal per tipe transaksi per currency — agregat `ledger_transactions` posted per hari (`count`, `sum(amount)`), memakai timezone Asia/Jakarta konsisten (hati-hati pelajaran bug `::date` vs `::timestamptz::date` di 16-T2 — tulis ekspresi konversi timezone eksplisit `(created_at AT TIME ZONE 'Asia/Jakarta')::date`).
   - `v_report_recon_summary`: per batch: gateway, report_date, counts per match_status, jumlah item ter-resolve — agregat `recon_batches`+`recon_items`.
   - `GRANT SELECT ON` ketiga view `TO app_readonly, app_service` di migrasi yang sama. View atas tabel ber-RLS: pemilik view adalah owner schema → **cek perilaku RLS pada view** (view berjalan dengan hak pemilik view secara default; Postgres 15+ mendukung `security_invoker=true`). **Keputusan**: set `security_invoker = false` (default) DITERIMA di sini — justru inilah mekanisme yang membuat `app_readonly` bisa melihat agregat `pending_adjustments`-adjacent data TANPA akses tabel mentah; tapi pastikan view TIDAK mengekspos kolom sensitif (`cmd_payload`, payload outbox). Review kolom per view di PR.
2. Ekspor CSV: endpoint `GET /admin/reports/{position|mutation|recon}?from=&to=&format=csv|json` di router **internal** admin-gated — streaming CSV pola statement 15-T2 (`csv.NewWriter` langsung ke response, tanpa buffer penuh). Query via koneksi pool aplikasi biasa (`app_service` — yang penting VIEW-nya yang jadi kontrak; koneksi `app_readonly` adalah untuk tool BI eksternal langsung ke DB, bukan untuk endpoint aplikasi).
3. Runbook `docs/runbooks/regulatory-reporting.md`: cara tool eksternal connect sebagai `app_readonly` (DSN, grant yang dipunya, tabel yang TIDAK bisa dibaca dan kenapa), jadwal yang disarankan (setelah snapshot 00:15), dan catatan bahwa format BI/OJK final akan jadi view/endpoint tambahan di atas fondasi yang sama.
4. TIDAK ada job/scheduler baru — laporan ditarik on-demand; kalau nanti butuh pengiriman terjadwal, itu konsumen dari endpoint ini (atau query view langsung), bukan fitur modul ledger.

### Test wajib
- Integration: seed aktivitas nyata (posting + snapshot job + recon batch dari test 15/16 helpers) → ketiga view mengembalikan agregat yang cocok dengan penjumlahan manual per currency/tipe.
- Integration role: koneksi `app_readonly` (helper `setupAppServiceTestDB` 16-T3) bisa `SELECT` ketiga view; tetap DITOLAK `SELECT pending_adjustments`/`outbox_events` mentah.
- Integration timezone: transaksi jam 00:30 WIB (17:30 UTC hari sebelumnya) masuk tanggal WIB yang benar di `v_report_daily_mutation` — regression guard untuk pelajaran 16-T2.
- Smoke test curl: ekspor CSV dua format, header kolom benar.

### DoD
- [x] Tidak ada statement INSERT/UPDATE/DELETE baru di seluruh diff task ini (read-only murni — `grep` diff).
- [x] Ketiga view tidak mengekspos `cmd_payload`/payload outbox (review kolom tercatat di PR).
- [x] Migrasi up+down; runbook ada.

### Hasil

Diimplementasikan persis sesuai desain di atas:

- **Views**: `migrations/000018_reporting_views.up.sql`/`.down.sql` — `v_report_daily_position` (agregat `account_balance_snapshots` JOIN `accounts`, GROUP BY `as_of_date, currency, type, owner_type`), `v_report_daily_mutation` (agregat `ledger_transactions` WHERE `status='posted'`, GROUP BY `(created_at AT TIME ZONE 'Asia/Jakarta')::date, type, currency` — konversi timezone eksplisit, bukan `::date` polos, sesuai pelajaran 16-T2), `v_report_recon_summary` (satu baris per batch: gateway, report_date, count per `match_status` via `FILTER`, `resolved_count`). `security_invoker` dibiarkan default (`false`) sesuai keputusan K-S S7 — didokumentasikan di header komentar migrasi bahwa ini AMAN di sini karena `app_readonly` sudah punya grant SELECT langsung ke semua tabel sumber (bukan mekanisme bypass RLS ke tabel yang sebelumnya tidak bisa dibaca). `GRANT SELECT` ke `app_readonly, app_service` di migrasi yang sama. **Review kolom** (dicatat di sini, bukan hanya di PR): tidak satu pun dari tiga view membaca `outbox_events.payload`, `pending_adjustments.cmd_payload`, atau `scheduled_transactions.cmd_payload` — ketiganya sama sekali tidak disentuh oleh view manapun.
- **Repository**: `internal/ledger/repository/reporting_repository.go` (+mock) — `ReportingRepository{DailyPosition, DailyMutation, ReconSummary}`, murni `SELECT` (`grep INSERT\|UPDATE\|DELETE` atas file ini + migrasi 000018 = nol hasil, memenuhi DoD #1). Model baru `internal/ledger/model/reporting.go` (`ReportDailyPosition`, `ReportDailyMutation`, `ReportReconSummary`).
- **Endpoint**: `GET /admin/reports/{position|mutation|recon}?from=&to=&format=csv|json` — router internal, admin-gated (`transport/http.go: getReport` + tiga `write*CSV` streaming helper, pola persis `writeStatementCSV` 15-T2 — `csv.NewWriter` langsung ke response, tanpa buffer penuh). Query lewat `app_service` pool biasa (bukan `app_readonly` — sesuai keputusan, `app_readonly` untuk tool BI eksternal). Date range dibatasi `maxReportDays=366` (guard ukuran box, bukan aturan bisnis). DTO di `transport/dto.go`, method baru di `transport.Service` + facade `Module.GetDailyPositionReport/GetDailyMutationReport/GetReconSummaryReport` (`ledger.go`).
- **Runbook**: `docs/runbooks/regulatory-reporting.md` — dua jalur baca (endpoint app sendiri vs koneksi `app_readonly` langsung), tabel yang BISA/TIDAK BISA dibaca `app_readonly` dan kenapa (termasuk catatan bahwa `scheduled_transactions` ternyata SUDAH bisa dibaca `app_readonly` sejak migrasi 000014/docs-plan-19 — temuan saat menulis test, bukan perubahan task ini), jadwal disarankan (setelah snapshot 00:15), catatan format BI/OJK final adalah fondasi tambahan di atas ini, TIDAK ada job/scheduler baru.
- **Tidak ada job/scheduler baru** — dikonfirmasi: tidak ada file baru di `internal/ledger/worker/` untuk task ini, laporan murni on-demand.
- **Test**:
  - Integration (`internal/ledger/schema_contract_test.go`, real Postgres): `TestSchemaContract_Reporting_DailyPositionMatchesManualAggregate`, `TestSchemaContract_Reporting_DailyMutationMatchesManualAggregate`, `TestSchemaContract_Reporting_ReconSummaryMatchesManualAggregate` — masing-masing membandingkan hasil view terhadap agregat manual SQL langsung atas tabel sumber, cocok persis.
  - Integration role: `TestSchemaContract_Reporting_AppReadonlyCanSelectViews` (koneksi `app_readonly` asli via `setupAppServiceTestDB` 16-T3 bisa SELECT ketiga view), `TestSchemaContract_Reporting_AppReadonlyBlockedFromPayloadTables` (tetap DITOLAK `outbox_events`/`pending_adjustments` — **catatan penting**: draft awal test ini juga menyertakan `scheduled_transactions` di daftar blokir berdasarkan asumsi analogi dengan dua tabel lain, tapi integration test-nya sendiri GAGAL dan mengungkap bahwa `app_readonly` SUDAH punya grant SELECT ke `scheduled_transactions` sejak migrasi 000014 (docs/plan/19 Task T1, mendahului task ini) — test dan runbook diperbaiki untuk mencerminkan perilaku aktual, bukan asumsi; task ini TIDAK mengubah grant tersebut ke arah manapun).
  - Integration timezone: `TestSchemaContract_Reporting_TimezoneRegressionGuard` — transaksi di-seed pada 17:30 UTC (00:30 WIB keesokan harinya) via `seedPostedTransaction` (helper baru, insert langsung `ledger_transactions` tanpa entries — cukup untuk `v_report_daily_mutation` yang hanya baca tabel itu), dibuktikan masuk `report_date` WIB yang benar, bukan tanggal UTC.
  - Migrasi 000018: siklus up→down→up diverifikasi manual (`golang-migrate` CLI) — down menghapus ketiga view (`\dv` hanya menyisakan `v_account_balance_audit` sebelum re-up), up ulang mengembalikan ketiganya bersih.
  - Smoke test curl (server hidup, `docker-compose` Postgres di-remap sementara ke port 5433 lalu dikembalikan ke 5432 — workaround native-Postgres-vs-Docker yang sudah baku di sesi ini): `GET /admin/reports/position|mutation` format `json` DAN `csv` — kolom CSV benar (header + `Content-Disposition`/`Content-Type` sesuai), data cocok dengan aktivitas nyata yang di-posting; `GET /admin/reports/bogus` → 400; non-admin token → 403; `GET /admin/screening/events` (T1) juga diverifikasi ulang di server hidup yang sama — event `flagged` dari sesi sebelumnya masih terbaca lewat endpoint baru.
- **Full verification**: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `make test` (semua paket hijau), `go test -tags=integration -race ./...` (semua paket hijau — satu kegagalan `TestPolicy_Engine_CacheExpiresAndRefetchesFromRealDB` di `internal/policy` terkonfirmasi flaky-di-bawah-beban-paralel-bukan-regresi, lolos bersih saat dijalankan sendirian; paket ini tidak disentuh sama sekali oleh docs/plan/20).

**Catatan tooling sesi ini**: `go generate`/`mockgen` sempat mengalami outage sementara pada classifier keamanan tool-calling selama beberapa menit di tengah task ini — `internal/ledger/transport/service_mock.go` untuk sementara ditambal manual (3 method baru, mengikuti pola persis method mockgen lain di file yang sama) agar build/vet tetap hijau sebelum `mockgen` berhasil dijalankan ulang untuk regenerasi kanonik penuh (dikonfirmasi identik secara fungsional, `go build`/`go vet` bersih setelah keduanya).

---

## Verifikasi akhir

```bash
go build ./... && go vet ./... && go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all      # T1 mengubah pipeline posting
```
Smoke test curl endpoint baru. Migrasi 000017–000018 up+down teruji. Setelah selesai: DoD + "Hasil" di dokumen ini, status di [README.md](README.md), supersede note S6/S7 di [08](08-phase-3-scale.md).

### Hasil verifikasi akhir

- `go build ./...` + `go build -tags=integration ./...` + `go vet ./...` + `go vet -tags=integration ./...` — bersih total.
- `make test` (unit, seluruh repo) — semua paket hijau, termasuk `internal/ledger/screening` (88.4% coverage, 8 test baru) dan `internal/ledger/transport` (32.3% coverage).
- `go test -tags=integration -race ./...` (seluruh repo, real Postgres via testcontainers) — semua paket hijau, termasuk `internal/ledger` (196s, 9 test screening+reporting baru di antaranya). Satu kegagalan intermiten `TestPolicy_Engine_CacheExpiresAndRefetchesFromRealDB` (paket `internal/policy`, dari docs/plan/17, tidak disentuh task ini) muncul dua kali saat dijalankan sebagai bagian suite penuh di bawah beban paralel testcontainers yang berat, tapi lolos bersih dua kali saat dijalankan sendirian — dikonfirmasi flaky-di-bawah-beban, bukan regresi dari docs/plan/20.
- `./scripts/chaos-test.sh all` — jalur posting berubah (hook loop step 4c di T1), jadi wajib diulang penuh. **Percobaan pertama scenario 2 (broker down) gagal** ("outbox did not fully drain: pending/failed=2 dead=0") — diinvestigasi: dua baris `outbox_events` nyangkut status `processing`, bukan hasil dari 10 posting scenario 2 sendiri (yang semuanya lolos), melainkan sisa dari `kill -9` scenario 1 sebelumnya di sesi chaos yang sama — volume Docker dev (`seev_postgres_data`) sudah dipakai berulang-ulang sepanjang sesi kerja panjang ini (banyak run manual/debug sebelumnya), dan skrip chaos memang sengaja TIDAK mereset volume antar-run (lihat komentar header skrip). Baris `processing` yang tersisa dari kill -9 SEHARUSNYA di-reclaim oleh `ReapStuck` (`outbox_event_repository.go`) setelah melewati ambang staleness, tapi jendela tunggu scenario 2 lebih pendek dari itu. **Diverifikasi bukan regresi**: `docker compose down -v` (reset volume dev bersih — bukan data produksi/pekerjaan pengguna, murni state uji chaos sesi ini) lalu `./scripts/chaos-test.sh all` diulang dari nol → **ke-4 skenario lolos bersih**, termasuk scenario 2 ("all outbox events reached 'published' after the broker recovered (none dead)"). `internal/ledger/worker/outbox_relay.go`/`outbox_event_repository.go` sama sekali tidak disentuh oleh docs/plan/20.
- Smoke test manual (server hidup, workaround remap port 5432↔5433 dikembalikan setelah selesai): endpoint T1 (`GET /admin/screening/events`) dan T2 (`GET /admin/reports/{position,mutation,recon}` format `json`+`csv`) — lihat detail di masing-masing "Hasil" T1/T2 di atas.
- Migrasi 000017 dan 000018: masing-masing diverifikasi up→down→up secara terpisah (kontainer Postgres throwaway per migrasi) — lihat detail di "Hasil" T1/T2.
- `docker-compose.yml` dikembalikan ke port asli (`5432:5432`) setelah setiap sesi remap sementara — dikonfirmasi `git diff` nol perbedaan terhadap kondisi sebelum verifikasi.
