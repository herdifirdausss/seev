# 19 — Phase 3c: Scheduled Posting, Batch Disbursement & Interest Accrual (S3, S8)

Prasyarat: 14–16 selesai (✅). Keputusan desain: [13 K-S](13-p1-backlog-review.md) butir S3 dan S8. Disarankan [17](17-phase3a-policy-recovery.md) T1 selesai duluan bila limit harus berlaku juga untuk eksekusi terjadwal (keputusan di T1 langkah 5 di bawah). Kerjakan **T1 → T2 → T3** (T2 memakai loop eksekusi T1; T3 independen tapi memakai pola job+key yang sama — kerjakan terakhir supaya polanya sudah teruji dua kali).

**Pola inti yang dikunci (K-S S3/S8, terbukti di 16-T1)**: eksekusi apapun yang otomatis/berulang = `ledger.Post` biasa dengan **idempotency key deterministik** — `sched:<id>:<run_date>`, `batch:<batch_id>:<item_no>`, `accrue:<account>:<date>`. Crash/retry di titik manapun aman karena posting engine sudah idempoten (chaos-tested). JANGAN membangun state machine eksekusi sendiri — state eksekusi = "apakah key ini sudah posted", ditanya ke ledger.

Aturan verifikasi 09 berlaku penuh: semua task menyentuh SQL + jalur posting → integration test wajib; T1/T2 endpoint baru → smoke test curl.

---

## T1 — Scheduled transactions (08 S3 butir 1)

**Tujuan**: transaksi berulang/tertunda (contoh: auto-debit langganan, transfer terjadwal) dieksekusi job harian tanpa mesin eksekusi baru.

### Langkah
1. Migrasi `000014_scheduled_transactions.up.sql` (+down):
   ```sql
   CREATE TABLE scheduled_transactions (
       id             UUID        PRIMARY KEY,
       user_id        UUID        NOT NULL,
       cmd_payload    JSONB       NOT NULL,      -- subset Command, pola persis pending_adjustments.cmd_payload (16-T1)
       schedule_kind  TEXT        NOT NULL CHECK (schedule_kind IN ('once','daily','monthly')),
       run_at_date    DATE        NOT NULL,      -- 'once': tanggal eksekusi; 'daily'/'monthly': tanggal mulai
       day_of_month   SMALLINT    NULL CHECK (day_of_month BETWEEN 1 AND 28), -- 'monthly' saja; 29-31 DITOLAK (hindari aturan akhir-bulan)
       status         TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','finished','failed')),
       last_run_date  DATE        NULL,
       last_error     TEXT        NULL,
       created_by     TEXT        NOT NULL,
       created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
       updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   CREATE INDEX idx_sched_tx_due ON scheduled_transactions (run_at_date) WHERE status = 'active';
   ```
   Grant+RLS di migrasi yang sama (pola 000010). Trigger `updated_at` pola `trg_accounts_ua`.
2. Validasi `cmd_payload` saat **create** (fail fast, pola 16-T1 langkah 3): type ∈ tipe yang boleh dijadwalkan — **HANYA `transfer_p2p` dan `transfer_pocket` untuk MVP** (tipe user-initiated murni; money_in/out terjadwal tidak masuk akal tanpa gateway event; adjustment tetap maker-checker). Amount integral positif, target jelas.
3. Service `internal/ledger/service/schedule/` (pola package `adjustments`): `Create`, `Pause`, `Resume`, `Cancel` (status `finished`), `List`, dan `RunDue(ctx, asOfDate) (executed, failed int, err error)`:
   - Query due: `status='active' AND run_at_date <= asOf AND (last_run_date IS NULL OR last_run_date < asOf)` + cek `schedule_kind` (daily: setiap hari; monthly: `day_of_month = extract(day from asOf)`; once: `run_at_date = asOf`).
   - Per baris due: `Post` dengan `IdempotencyKey: "sched:"+id+":"+asOf.Format("2006-01-02")`, `IdempotencyScope` = user_id (konsisten 10-T2). `ErrAlreadyPosted` diperlakukan sukses (job kemarin crash setelah post sebelum update `last_run_date` — inilah kenapa key deterministik).
   - Sukses → `last_run_date=asOf`; `once` → status `finished`. Gagal **business** (saldo kurang, akun suspended — `apperror` business sentinel) → catat `last_error`, `once` → `failed`, recurring → TETAP `active` (coba lagi jadwal berikutnya; auto-pause setelah N gagal beruntun = di luar scope, catat sebagai keputusan). Gagal infra → jangan sentuh baris (retry run berikutnya).
   - Eksekusi TIDAK melewati policy engine 17-T1 (`ledger.Post` langsung) — **keputusan**: limit velocity user berlaku saat CREATE jadwal dinilai ops, bukan per eksekusi; kalau produk butuh sebaliknya, panggil `policy.Check` di RunDue (tulis pilihan yang diambil di doc comment).
4. Job harian: `internal/ledger/worker/schedule_runner.go` pola persis `worker/snapshot.go` (scheduler.Cron + LockProvider, `"30 0 * * *"` Asia/Jakarta — SETELAH snapshot 00:15 supaya snapshot hari kemarin sudah beres), wiring di `ledger.NewModule`/`StartWorkers` (pola `snapshotJob`).
5. Endpoint: router **publik** `POST /schedules`, `GET /schedules`, `POST /schedules/{id}/pause|resume|cancel` — user mengelola jadwalnya sendiri (`CanAccess` by user_id, pola `getBalance`); create memakai payload user sendiri (`UserID` dari JWT, bukan body — pola postTransaction).

### Test wajib
- Unit service: due-selection per schedule_kind (table-driven, termasuk monthly di tanggal bukan day_of_month → tidak due), business-fail sekali → recurring tetap active.
- Integration: create daily → `RunDue(hari1)` → posted; `RunDue(hari1)` kedua → idempoten (tidak dobel, `countLedgerTransactions(key)==1`); `RunDue(hari2)` → posting kedua.
- Integration crash-window: post sukses lalu simulasi gagal update `last_run_date` (panggil Post manual dengan key yang sama baru RunDue) → RunDue memperlakukan `ErrAlreadyPosted` sebagai sukses dan MENULIS `last_run_date`.
- Smoke test curl: create schedule → jalankan runner (trigger manual via method, atau tunggu — sediakan endpoint internal `POST /admin/schedules/run?date=` admin-gated untuk ops/testing).

### DoD
- [x] Tidak ada state machine eksekusi baru — bukti: satu-satunya penanda "sudah jalan" adalah idempotency key ledger + `last_run_date` informatif.
- [x] Migrasi up+down; grant+RLS tabel baru.

### Hasil

Dibangun sesuai spesifikasi Langkah 1-5, tanpa penyimpangan.

**Langkah 1 — migrasi**: `migrations/000014_scheduled_transactions.up.sql`/`.down.sql`, kolom persis spek, index `idx_sched_tx_due` (partial, `WHERE status='active'`) + `idx_sched_tx_user`, trigger `trg_scheduled_tx_ua`, grant+RLS pola 000010.

**Langkah 2 — validasi create**: `internal/ledger/service/schedule/schedule.go` `Create()` menolak type di luar `transfer_p2p`/`transfer_pocket`, amount non-integer/non-positif, `schedule_kind` di luar `once`/`daily`/`monthly`, `day_of_month` di luar 1-28 atau hadir pada kind selain monthly, self-transfer, dan pocket_code kosong untuk transfer_pocket.

**Langkah 3 — service**: `Service.Create/Pause/Resume/Cancel/List/RunDue` lengkap. `Pause/Resume/Cancel` memverifikasi kepemilikan (`GetByID` lalu bandingkan `UserID`) sebelum UPDATE atomik `WHERE status=<from>` (pola K3 `MarkApproved`). `RunDue` mengklasifikasi error dari `Poster.Handle` via `errors.As(*apperror.LedgerError)` — konvensi "Business sentinels vs Structural sentinels" yang SUDAH ada di `apperror` package, bukan allowlist kode error baru. Kebijakan: eksekusi TIDAK melewati `policy.Engine` (keputusan diambil sesuai opsi doc — limit velocity dinilai saat create, bukan per-eksekusi; ditulis di doc comment `Service.RunDue`... — sebenarnya ditulis di ledger.go dan schedule.go's package doc).

**Langkah 4 — job harian**: `internal/ledger/worker/schedule_runner.go` (`ScheduleRunnerJob`), cron `"30 0 * * *"` Asia/Jakarta (setelah snapshot 00:15), pola `scheduler.Cron` + `LockProvider` sama persis `SnapshotJob`. TIDAK ada catatan day-by-day catch-up loop (beda sengaja dari `SnapshotJob`) — didokumentasikan di doc comment kenapa: schedule bukan rekaman historis yang harus direkonstruksi persis, `ListDue`'s query sendiri sudah "self-catching-up" untuk hari berikutnya.

**Langkah 5 — endpoint**: `POST /schedules`, `GET /schedules`, `POST /schedules/{id}/pause|resume|cancel` di kedua router (bukan admin-gated — user mengelola jadwal miliknya sendiri, ownership dicek di service layer); `POST /admin/schedules/run?date=` internal-router-only, admin-gated, membungkus `ScheduleRunnerJob.RunNow`.

**Test wajib — semua terpenuhi**:
- Unit (`internal/ledger/service/schedule/schedule_test.go`, 17 test): validasi Create (7 kasus reject + 2 kasus sukses), business-fail recurring tetap active vs once jadi failed, infra-fail baris tidak tersentuh, sukses menulis `last_run_date` + `finished` untuk once, `ErrAlreadyPosted` diperlakukan sukses, ownership check Pause.
- Integration (`internal/ledger/schema_contract_test.go`): `TestSchemaContract_Schedule_DailyRunDue_IdempotentAcrossDays` (create daily → RunDue hari1 posted → RunDue hari1 lagi idempoten → RunDue hari2 posting kedua), `TestSchemaContract_Schedule_CrashWindow_AlreadyPostedTreatedAsSuccess` (post manual dengan key yang sama duluan → RunDue menulis last_run_date meski Handle mengembalikan ErrAlreadyPosted), `TestSchemaContract_Schedule_ListDue_PerScheduleKind` (8 kasus table-driven langsung terhadap SQL `ListDue`: once hari-ini/masa-lalu/masa-depan, daily sudah-jalan/belum, monthly cocok/tidak-cocok day_of_month, paused).
- Smoke test manual end-to-end via HTTP (server nyata + docker-compose, pola sama seperti smoke test 18): provisioning 2 user IDR → fund user A → create daily schedule → `POST /admin/schedules/run` (admin JWT) → `executed=1` → saldo A turun/B naik benar → re-run hari sama → `executed=0` (idempoten) → pause → resume → cancel (`status=finished`) — semua berhasil.

**Verifikasi penuh**: `go build ./...` bersih, `go vet ./...` bersih (termasuk `-tags=integration`), `go test -race -count=1 ./...` semua PASS, `go test -tags=integration -race -count=1 ./internal/ledger/...` semua PASS. Migrasi 000014 diverifikasi up→down→up (struktur tabel + trigger + RLS policy identik).

---

## T2 — Batch disbursement (08 S3 butir 2)

**Tujuan**: satu manifest (CSV) → banyak `Post` dengan progress + resume. Contoh: payroll/refund massal.

### Langkah
1. Migrasi `000015_disbursement.up.sql` (+down): `disbursement_batches (id, source_filename, row_count, status CHECK ('processing','completed','completed_with_errors'), created_by, created_at)` + `disbursement_items (id, batch_id FK, item_no INT, user_id UUID, amount BIGINT, note TEXT, status CHECK ('pending','posted','failed'), error TEXT, posted_tx_id UUID NULL REFERENCES ledger_transactions(id), UNIQUE(batch_id, item_no))`. Grant+RLS.
2. Import CSV multipart `POST /admin/disbursements` di router **internal** admin-gated — pola persis recon import 16-T2 (`parseReconCSV` streaming, cap 50.000 baris, kolom `user_id,amount,note`), simpan batch+items `pending` satu transaksi DB.
3. Eksekusi sinkron setelah import (batch harian ribuan baris — konsisten keputusan K5 "jangan tambah worker"): loop per item `Post` dengan key `batch:<batch_id>:<item_no>`, scope NULL (internal). Tipe transaksi: `money_in`? **Bukan** — disbursement dana platform ke user = tipe `disbursement` BARU (processor baru: `system.settlement[gateway]` atau akun sumber khusus → `user.cash`; keputusan akun sumber ditentukan saat implementasi berdasar kebutuhan finance, default `settlement[platform]`). Registry pattern membuat ini murah (pola 16-T2 suspense processors). Internal-router-only.
4. Resume: `POST /admin/disbursements/{id}/resume` — proses ulang item `pending`/`failed`; item `failed` karena business error butuh flag `?retry_failed=true` eksplisit. Idempotency key deterministik menjamin item `posted` yang ke-retry tidak dobel.
5. Laporan: `GET /admin/disbursements/{id}` — counts per status + item gagal (paginated, pola recon report).
6. Timeout HTTP: 50rb Post sinkron bisa > 30s (middleware `WithTimeout(30s)` di router internal!). **Keputusan**: eksekusi dipecah — import menyimpan items lalu return `202`; eksekusi via `POST /admin/disbursements/{id}/run` yang memproses **maksimal N item per panggilan** (N=500, konstanta) dan return progress; ops/script memanggil berulang sampai selesai. Sederhana, resumable by design, tanpa worker baru.

### Test wajib
- Integration: import 10 item → run (2 panggilan N=6) → semua posted, saldo benar, `countLedgerTransactions` per key = 1.
- Integration resume: matikan proses di tengah (item 5 posted, sisanya pending) → resume → item 1-5 tidak dobel, 6-10 posted.
- Integration: item gagal business (user tanpa akun) → status failed + error tersimpan, batch `completed_with_errors`, item lain tetap jalan.
- Smoke test curl end-to-end.

### DoD
- [x] Resume terbukti idempoten di integration test.
- [x] Tidak ada goroutine/worker baru untuk eksekusi.

### Hasil

Dibangun sesuai spesifikasi Langkah 1-6, dengan satu konsolidasi desain dicatat di bawah.

**Langkah 1 — migrasi**: `migrations/000015_disbursement.up.sql`/`.down.sql`, tabel `disbursement_batches`+`disbursement_items` persis skema di spek, index `idx_disbursement_items_batch_status` (drives Run's item-selection query). Akun sumber `settlement[platform]` di-seed PER CURRENCY aktif (IDR+USD, ID `...027`/`...028`, `allow_negative=true`) — bukan hanya IDR, konsisten pola multi-currency 18-T2.

**Langkah 2 — import CSV**: `POST /admin/disbursements`, internal-router-only, admin-gated, pola persis `parseReconCSV` (16-T2): kolom `user_id,amount,note` (note opsional), cap 50.000 baris, disimpan `pending` dalam SATU transaksi DB (`DisbursementRepository.CreateBatchWithItems`).

**Langkah 3 — processor baru `disbursement`**: `internal/ledger/processors/disbursement.go` — `settlement[platform][currency] → user.cash`, resolve currency dari user dulu (pola 18-T2, bukan tipe `money_in`, sesuai keputusan eksplisit di spek). Internal-router-only.

**Langkah 4 — resume**: TIDAK ada endpoint `/resume` terpisah — **keputusan konsolidasi** (dicatat eksplisit karena spek awalnya menyebut dua endpoint berbeda di langkah 4 vs langkah 6): "resume" dan "run" adalah SATU mekanisme yang sama. Memanggil `POST /admin/disbursements/{id}/run` lagi setelah sebagian batch selesai SECARA OTOMATIS hanya memilih item yang masih `pending`(+`failed` bila `?retry_failed=true`) — `ListItemsToProcess` tidak pernah memilih ulang item `posted`. Menambah endpoint `/resume` terpisah hanya akan menduplikasi logika yang identik.

**Langkah 5 — laporan**: `GET /admin/disbursements/{id}?status=&limit=&offset=` — header batch + count per status + item terpaginasi, pola persis recon report (16-T2).

**Langkah 6 — eksekusi dipecah, N per panggilan**: `Run()` memproses maksimal `maxItemsPerRun` (default 500, production) item per panggilan — TIDAK sinkron penuh saat import (import hanya `INSERT ... pending`, return 201). `maxItemsPerRun` dibuat overridable via `disbursement.WithMaxItemsPerRun(n)` (pola `policy.Engine.WithCacheTTL`, 17-T1) supaya integration test bisa membuktikan pagination lintas panggilan tanpa mengimpor ratusan baris.

**Catatan desain (perbedaan dari 19-T1 yang disengaja)**: Berbeda dari `schedule.RunDue` (T1), `disbursement.Run` TIDAK membedakan business-failure vs infra-failure — SEMUA error dari `Poster.Handle` menandai item `failed` + menyimpan pesan error, titik. Alasan: item disbursement adalah aksi SEKALI JALAN (bukan berulang seperti schedule) — operator memutuskan retry secara eksplisit lewat `?retry_failed=true`, tidak ada "coba lagi jadwal berikutnya" otomatis yang perlu dibedakan dari kegagalan permanen.

**Test wajib — semua terpenuhi**:
- Unit (`internal/ledger/service/disbursement/disbursement_test.go`, 11 test): validasi Import (5 kasus reject + 1 sukses), Run sukses-semua → `completed`, Run business-failure → `completed_with_errors`, Run belum-selesai → `Done=false`, batch tidak ditemukan → error, `WithMaxItemsPerRun` override teruji.
- Integration (`internal/ledger/schema_contract_test.go`): `TestSchemaContract_Disbursement_ImportThenRun_AllPostedAcrossMultipleCalls` (import 10 → run dengan `maxPerRun=6` → panggilan 1 memproses 6 `Done=false`, panggilan 2 memproses 4 sisanya `Done=true`, `countLedgerTransactions` per key = 1, batch `completed`), `TestSchemaContract_Disbursement_Resume_NoDoublePost` (item 5 di-post manual duluan lalu ditandai `posted` — simulasi crash — Run berikutnya HANYA memproses 9 item sisanya, item 5 tidak pernah dipilih ulang, tidak ada transaksi dobel), `TestSchemaContract_Disbursement_BusinessFailure_OtherItemsStillProcess` (1 dari 3 item target user tanpa akun cash → gagal + error tersimpan, 2 item lain tetap posted, batch `completed_with_errors`).
- Unit source/destination audit (`resolved_accounts_test.go`, pola 14-T1): `TestResolvedAccounts_Disbursement`.
- Smoke test manual end-to-end via HTTP (server nyata + docker-compose, pola sama seperti smoke test 18/19-T1): provisioning 3 user IDR → `POST /admin/disbursements` (multipart CSV 3 baris) → `POST /admin/disbursements/{id}/run` → `processed=3,posted=3,done=true` → `GET /admin/disbursements/{id}` menunjukkan 3 item `posted` dengan `posted_tx_id` nyata → saldo user benar → re-run → `processed=0,done=true` (idempoten) — semua berhasil.

**Verifikasi penuh**: `go build ./...` bersih, `go vet ./...` bersih (termasuk `-tags=integration`), `go test -race -count=1 ./...` semua PASS, `go test -tags=integration -race -count=1 ./internal/ledger/...` semua PASS. Migrasi 000015 diverifikasi up→down→up (settlement[platform] IDR+USD dan disbursement_batches/items dikonfirmasi hilang lalu kembali identik). Chaos scenario 1 (kill -9 mid-posting) dijalankan ulang setelah 27 processor terdaftar — PASS, nol transaksi unbalanced.

---

## T3 — Interest accrual (08 S8)

**Tujuan**: akrual bunga harian untuk akun produk saving — job menghitung, posting `interest_accrue`, kapitalisasi periodik.

### Langkah
1. Penanda akun saving: MVP TANPA tabel produk — akun `type='pocket'` dengan `pocket_code` berprefiks `sv-` **DITOLAK** (magic string). Keputusan: tabel `savings_config (account_id UUID PK REFERENCES accounts(id), annual_rate_bps INT NOT NULL CHECK (annual_rate_bps BETWEEN 0 AND 2000), enabled BOOLEAN DEFAULT true, created_at)` — migrasi `000016_savings.up.sql` + grant+RLS. Ops mendaftarkan akun yang berbunga secara eksplisit.
2. Processor baru `interest_accrue`: `system.interest_expense` (akun sistem baru, type `interest_expense` ke CHECK + seed per currency, `allow_negative=true`) → akun saving user. Metadata wajib `accrual_date`, `rate_bps`. Internal-router-only + TIDAK bisa direct-post publik.
3. Perhitungan (keputusan dikunci): bunga harian = `floor(balance_akhir_hari_kemarin × rate_bps / 10000 / 365)` minor unit — pakai **snapshot H3** (`account_balance_snapshots`, 15-T1) sebagai basis `balance_akhir_hari` (inilah nilai prasyarat H3), BUKAN saldo live (race dengan transaksi hari ini). Hasil 0 (saldo kecil) → tidak posting (jangan bikin transaksi 0 — `chk amount > 0` menolak juga).
4. Job harian `worker/accrual.go` (pola snapshot.go), `"45 0 * * *"` Asia/Jakarta — SETELAH snapshot 00:15 (dependensi data) dan schedule-runner 00:30. Per akun enabled: hitung → `Post` key `accrue:<account_id>:<date>` → `ErrAlreadyPosted` = sukses (idempoten lintas restart/replica; lock scheduler sudah mencegah dua replica, key mencegah dobel kalau lock gagal — defense in depth).
5. Kapitalisasi: TIDAK ada langkah terpisah — akrual langsung ke saldo akun saving setiap hari (simple interest harian, compound alami karena snapshot hari berikutnya sudah memuat bunga kemarin). Tulis di doc comment bahwa ini keputusan produk MVP; akrual-ke-akun-penampung + kapitalisasi bulanan = perubahan kecil nanti (ganti akun tujuan + job bulanan).
6. Endpoint admin: `PUT /admin/savings/{account_id}` (set rate/enabled), `GET /admin/savings` — internal, admin-gated.

### Test wajib
- Unit: rumus akrual table-driven (pembulatan floor, rate 0, saldo 0, saldo kecil → 0 → skip).
- Integration: seed saldo via posting → snapshot hari H → accrual H+1 → saldo naik benar, key `accrue:` unik; jalankan job dua kali → idempoten; verifier bersih.
- Integration: akun `enabled=false` → tidak diakrual.

### DoD
- [x] Basis perhitungan = snapshot (bukan saldo live) — assertion eksplisit di test (ubah saldo live setelah snapshot → akrual tetap dari snapshot).
- [x] Migrasi up+down; akun sistem baru ter-seed per currency yang aktif.

### Hasil

Dibangun sesuai spesifikasi Langkah 1-6, tanpa penyimpangan.

**Langkah 1 — savings_config**: `migrations/000016_savings.up.sql`/`.down.sql` — tabel `savings_config(account_id PK, annual_rate_bps, enabled, ...)`, TIDAK ada magic pocket_code prefix. Menambah type `interest_expense` ke `accounts_type_check` (pola penambahan `fx_conversion` di 000013), seed per currency aktif (IDR+USD, ID `...029`/`...030`, `allow_negative=true`).

**Langkah 2 — processor `interest_accrue`**: `internal/ledger/processors/interest_accrue.go` — `interest_expense[currency] → savings account`. Target akun dibaca dari metadata `"account_id"` (bukan `cmd.UserID`+type, karena target bisa akun pocket manapun yang terdaftar di `savings_config`, bukan hanya cash) — pola persis `escrow_release`'s `merchant_account_id`. Metadata wajib `account_id`, `accrual_date`, `rate_bps` (rate_bps audit trail saja, tidak pernah dipakai aritmetika di processor — jumlah sudah dihitung `accrual.Service` sebelum Command dibuat). Internal-router-only.

**Langkah 3 — perhitungan**: `accrual.DailyInterest(balance, rateBps) = floor(balance × rateBps / 10000 / 365)`, basis SELALU `SnapshotRepository.BalanceAsOf` (pola persis `Module.GetBalanceAsOf`/`Statement` dari 15-T1) — TIDAK PERNAH saldo live. Hasil non-positif (termasuk pembulatan ke 0) → `skipped++`, tidak posting.

**Langkah 4 — job harian**: `internal/ledger/worker/accrual.go` (`AccrualJob`), cron `"45 0 * * *"` Asia/Jakarta (setelah snapshot 00:15 dan schedule-runner 00:30), pola `scheduler.Cron`+`LockProvider` persis `SnapshotJob`/`ScheduleRunnerJob`. Key `accrue:<account_id>:<date>`; `ErrAlreadyPosted` diperlakukan sukses (idempoten lintas restart, defense-in-depth di atas scheduler lock).

**Langkah 5 — kapitalisasi**: TIDAK ada langkah kapitalisasi terpisah — bunga masuk langsung ke saldo akun setiap hari, dicatat eksplisit di doc comment processor bahwa ini keputusan MVP dan migrasi ke akrual-ke-penampung + kapitalisasi bulanan adalah perubahan kecil nanti (ganti akun tujuan + tambah job bulanan), bukan redesign.

**Langkah 6 — endpoint admin**: `PUT /admin/savings/{account_id}` (set rate+enabled, upsert), `GET /admin/savings` (list semua config, termasuk yang disabled) — internal-router-only, admin-gated.

**Catatan desain**: `accrual.Service.RunDue` mengembalikan `(accrued, skipped int)` TANPA error keras per-akun — satu akun gagal (lookup snapshot error, post error) tidak boleh menghentikan akun lain dalam batch harian yang sama (dicatat di doc comment method), konsisten semangat resiliensi dokumen 19 secara umum (disbursement's per-item isolation, schedule's per-row isolation).

**Test wajib — semua terpenuhi**:
- Unit (`internal/ledger/service/accrual/accrual_test.go`, table-driven 6 kasus): floor rounding, rate 0, saldo 0, saldo negatif, saldo kecil → 0, rate maksimum 20%.
- Unit source/destination audit (`resolved_accounts_test.go`, pola 14-T1): `TestResolvedAccounts_InterestAccrue`.
- Integration (`internal/ledger/schema_contract_test.go`): `TestSchemaContract_Accrual_BasicFlow_IdempotentAcrossRuns` (fund → snapshot → accrue → saldo naik benar dengan key unik → run dua kali → idempoten → `fn_verify_ledger_balance` bersih), `TestSchemaContract_Accrual_BasisIsSnapshotNotLiveBalance` (bukti eksplisit DoD: saldo live diubah besar-besaran SETELAH snapshot → akrual tetap memakai saldo snapshot yang lama, bukan saldo live baru), `TestSchemaContract_Accrual_DisabledAccount_NotAccrued` (akun `enabled=false` tidak pernah masuk `ListEnabled`, nol akrual, saldo tidak berubah).
- Smoke test manual end-to-end via HTTP + script langsung (server nyata + docker-compose, pola sama seperti smoke test 18/19-T1/19-T2): provisioning user → fund 1.000.000 IDR → `PUT /admin/savings/{account_id}` (rate 500 bps) → `GET /admin/savings` menunjukkan config tersimpan → jalankan `accrual.Service.RunDue` langsung (belum ada endpoint HTTP trigger untuk T3, sesuai spek yang hanya meminta endpoint config) → saldo API `GET /accounts/{id}/balance` menunjukkan `1000136` (1.000.000 + 136 bunga harian 5% tahunan) — cocok persis dengan formula.

**Verifikasi penuh**: `go build ./...` bersih, `go vet ./...` bersih (termasuk `-tags=integration`), `go test -race -count=1 ./...` semua PASS, `go test -tags=integration -race -count=1 ./internal/ledger/...` semua PASS. Migrasi 000016 diverifikasi up→down→up (termasuk constraint `accounts_type_check` terbukti aktif kembali setelah down — INSERT `interest_expense` ditolak). Chaos scenario 1 (kill -9 mid-posting) dijalankan ulang setelah 28 processor terdaftar — PASS, nol transaksi unbalanced.

---

## Verifikasi akhir

```bash
go build ./... && go vet ./... && go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all
```
Smoke test curl untuk semua endpoint baru. Migrasi 000014–000016 up+down teruji. Setelah selesai: DoD + "Hasil" di dokumen ini, status di [README.md](README.md), supersede note S3/S8 di [08](08-phase-3-scale.md).

### Hasil verifikasi akhir (semua langkah di atas dijalankan ✅)

- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...` — bersih di seluruh project (bukan hanya `internal/ledger`).
- `make test` (`go test -race -cover ./...`) — semua package PASS.
- `go test -tags=integration -race -count=1 ./...` (seluruh project) — semua package PASS, termasuk `internal/policy` yang sebelumnya sempat flake sekali di sesi 18 (lolos bersih di run ini).
- `./scripts/chaos-test.sh all` (ke-4 skenario: kill -9 mid-posting, broker down, Postgres restart mid-traffic, Redis down) — semua PASS, nol transaksi unbalanced, nol saldo inkonsisten, di atas registry 28 processor (13 baru sejak dokumen 17: fx_out/fx_in/disbursement/interest_accrue, plus scheduled transactions memakai processor existing transfer_p2p/transfer_pocket).
- Migrasi 000014 (`scheduled_transactions`), 000015 (`disbursement`), 000016 (`savings`) — masing-masing diverifikasi up→down→up terhadap container Postgres sekali pakai secara terpisah per task; down 000015/000016 dibuktikan benar-benar mengembalikan CHECK constraint lama (`fx_conversion`-only tanpa `interest_expense`) dan menghapus akun sistem terkait.
- Smoke test curl end-to-end untuk SEMUA endpoint baru (server nyata + docker-compose, port Postgres di-remap sementara ke 5433 karena native Postgres di mesin dev menempati 5432 — direvert setelah selesai):
  - **Schedules**: create → list → `POST /admin/schedules/run` (admin JWT) → `executed=1` → re-run idempoten (`executed=0`) → pause → resume → cancel (`status=finished`).
  - **Disbursements**: import CSV multipart 3 baris → `POST /admin/disbursements/{id}/run` → `processed=3,posted=3,done=true` → report menunjukkan `posted_tx_id` nyata → re-run idempoten (`processed=0`).
  - **Savings/accrual**: fund akun → `PUT /admin/savings/{account_id}` → `GET /admin/savings` menunjukkan config tersimpan → jalankan `accrual.Service.RunDue` (belum ada trigger HTTP untuk T3, sesuai spek) → saldo API menunjukkan `1000136` (1.000.000 + bunga 5% tahunan harian = 136), cocok persis formula.
- Ditemukan (dan diselesaikan) satu observasi tooling saat smoke test: `POST`/`PUT` tanpa body masih wajib header `Content-Type: application/json` (middleware `RequireJSON` global) — bukan bug baru, perilaku existing dari 10-T3, hanya perlu diketahui saat curl testing endpoint admin yang tidak membaca body (`run`, `pause`, `resume`, `cancel`).

**Status: docs/plan/19 (Phase 3c — Scheduled Posting, Batch Disbursement & Interest Accrual) SELESAI — T1, T2, T3 semua ✅.**
