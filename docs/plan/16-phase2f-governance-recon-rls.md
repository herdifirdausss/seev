# 16 — Phase 2f: Adjustment Governance, Reconciliation & RLS (H5, H2, H8)

Prasyarat: [14](14-phase2d-ledger-semantics-events.md) dan [15](15-phase2e-snapshots-statements.md) selesai. Keputusan desain: [13 K5, K8, K9](13-p1-backlog-review.md). Kerjakan **berurutan T1 → T2 → T3**: resolusi recon (T2) memakai maker-checker (T1); RLS (T3) harus mencakup semua tabel final sehingga paling akhir.

---

## T1 — Maker-checker untuk adjustment (07 H5, keputusan K8)

**Tujuan**: tidak ada satu orang pun yang bisa menggerakkan uang via adjustment sendirian.

### Langkah
1. Migrasi `000006_pending_adjustments.up.sql` (+down):
   ```sql
   CREATE TABLE pending_adjustments (
       id             UUID        PRIMARY KEY,
       requested_by   TEXT        NOT NULL,
       approved_by    TEXT        NULL,
       cmd_payload    JSONB       NOT NULL,
       reason         TEXT        NOT NULL,
       status         TEXT        NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','approved','rejected','executed','failed')),
       executed_tx_id UUID        NULL REFERENCES ledger_transactions(id),
       created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
       decided_at     TIMESTAMPTZ NULL,
       CHECK (approved_by IS NULL OR approved_by <> requested_by)
   );
   ```
   Constraint `approved_by <> requested_by` di DB — enforcement inti tidak boleh hanya di Go.
2. Repository + service kecil `internal/ledger/service/adjustments/` : `Create`, `Approve`, `Reject`, `List`. `Approve` = transisi status `pending→approved` via UPDATE bersyarat `WHERE status='pending'` (RowsAffected==1 — dua approver konkuren: satu menang; pola K3), lalu eksekusi `Post` dengan **idempotency key `adj:<pending_id>`** (retry approve tidak double-post — jaminan idempotensi existing), lalu tandai `executed` + `executed_tx_id`; kegagalan post → status `failed` + error tersimpan, BUKAN kembali ke pending (keputusan manusia baru untuk retry).
3. `cmd_payload` = subset field `Command` yang diizinkan (type ∈ {adjustment_credit, adjustment_debit}, amount, target user, metadata reason) — divalidasi saat `Create`, BUKAN saat approve saja (fail fast).
4. Endpoint router internal, admin-gated (pola `POST /admin/outbox/...` di `transport/http.go:91-92`):
   - `POST /admin/adjustments` → create pending (requester = JWT sub).
   - `POST /admin/adjustments/{id}/approve`, `POST /admin/adjustments/{id}/reject` (approver = JWT sub; sama dengan requester → 403 dengan pesan eksplisit).
   - `GET /admin/adjustments?status=` → list untuk tooling ops.
5. **Cabut akses langsung**: tambah `adjustment_credit`/`adjustment_debit` ke daftar tipe yang DITOLAK di `postTransaction` router internal (409/403 dengan petunjuk "use /admin/adjustments") — satu-satunya jalan adjustment adalah alur pending. `freeze_*`/`chargeback` tetap langsung tapi `reason` metadata jadi wajib via `ValidateCommand` masing-masing processor.
6. Event audit: approve/reject menghasilkan outbox event `ledger.adjustment.decided.v1` (requested_by, approved_by, decision, pending_id, executed_tx_id) — jejak governance ikut jalur event yang sama.

### Test wajib
- Unit: self-approve ditolak (Go) + integration: bypass Go langsung UPDATE → constraint DB menolak.
- Integration race: dua approve konkuren → tepat satu eksekusi, satu 409; retry approve setelah sukses → idempoten (tidak ada tx kedua di ledger).
- Integration: `POST /transactions` type adjustment di router internal → ditolak dengan petunjuk.

### DoD
- [x] Tidak ada jalur adjustment tanpa dua identitas berbeda yang tercatat.
- [x] Migrasi up+down teruji; smoke test alur penuh via curl (create→approve→cek saldo).

### Hasil
Implementasi selesai dan terverifikasi penuh (build/vet/unit/integration/smoke):
- `migrations/000006_pending_adjustments.{up,down}.sql` — tabel `pending_adjustments` + `CHECK (approved_by IS NULL OR approved_by <> requested_by)`. Up→down→up dites dengan `migrate` CLI terhadap Postgres asli, bersih.
- `internal/ledger/service/adjustments/` — `Service.{Create,Approve,Reject,Get,List}`. `Approve` melakukan self-check sebelum tulis apapun, `MarkApproved` atomik (`WHERE status='pending'`), posting via idempotency key `adj:<id>`, lalu `MarkExecuted`+outbox event dalam satu transaksi. 8 unit test (mocked) + 5 integration test (Postgres asli) — semua pass.
- `internal/ledger/transport/http.go` — 5 endpoint `/admin/adjustments*` di router internal (admin-gated), `adjustment_credit`/`adjustment_debit` dicabut dari `adminOnlyTypes` dan masuk `directPostBlockedTypes` (403 di `postTransaction` bahkan untuk admin).
- `internal/ledger/events/events.go` — event `ledger.adjustment.decided.v1`.
- Integration test baru di `internal/ledger/schema_contract_test.go`: `TestSchemaContract_PendingAdjustment_DBConstraint_RejectsSelfApprove` (bypass Go, raw SQL UPDATE ditolak constraint DB), `_ConcurrentApprove_ExactlyOneWins` (8 approver konkuren, tepat 1 menang, sisanya `ErrAdjustmentAlreadyDecided`), `_RetryApprove_NoDoublePost` (retry setelah sukses → 409, tetap 1 baris `ledger_transactions`), `_FullFlow_MovesBalance`, `_Reject_NoMoneyMoves`.
- Smoke test manual via curl terhadap stack Docker penuh (postgres+redis+rabbitmq, port Postgres diremap sementara ke 5433 karena ada Postgres native di 5432 mesin dev, dikembalikan setelah selesai): create (admin) → self-approve ditolak 403 `SELF_APPROVAL` → approve oleh identitas berbeda sukses 200 → saldo akun target naik sesuai amount → retry-approve oleh approver sama ditolak 409 `ADJUSTMENT_ALREADY_DECIDED` → saldo TIDAK berubah lagi (tidak ada double-post) → direct `POST /transactions` dengan `adjustment_credit` di router internal ditolak 403 dengan petunjuk `/admin/adjustments` → `fn_verify_ledger_balance` kosong (tidak ada transaksi timpang) setelah seluruh alur.
- `go build ./...`, `go vet -tags=integration ./...`, `go test -race ./...`, `go test -tags=integration -race ./...` — semua hijau.

---

## T2 — Rekonsiliasi eksternal (07 H2, temuan N2, keputusan K5)

**Tujuan**: selisih antara laporan settlement gateway dan ledger ketahuan harian, terparkir jelas, dan resolusinya ter-governance.

### Langkah
1. **Prasyarat skema (N2)** — migrasi `000007_tx_correlation.up.sql` (+down):
   ```sql
   ALTER TABLE ledger_transactions
     ADD COLUMN external_ref TEXT NULL,
     ADD COLUMN gateway      TEXT NULL;
   CREATE INDEX idx_ltx_external_ref ON ledger_transactions (gateway, external_ref)
     WHERE external_ref IS NOT NULL;
   ```
   Isi dari service saat posting: `external_ref` dari metadata tervalidasi (max 128 char — validasi di transport, bukan silently truncate), `gateway` dari metadata (sudah ter-allowlist `constant.ValidGateways`). Kolom informatif — sama seperti source/dest, TIDAK ada backfill data lama; catat cutoff.
2. Migrasi `000008_recon.up.sql` (+down): tabel `recon_batches` dan `recon_items` persis skema di K5 (match_status CHECK: `matched|missing_internal|missing_external|amount_mismatch`; `resolved_by_adjustment_id UUID NULL REFERENCES pending_adjustments(id)`). Seed akun suspense per gateway (`system_qualifier='suspense:<gw>'`, `allow_negative=true`) ikut pola `000002_seed_system_accounts`.
3. Import: `POST /admin/recon/batches` (router internal, admin-gated) — multipart CSV kolom `external_ref,amount,settled_at`; parsing `encoding/csv` streaming, amount integral minor-unit (tolak desimal — konsisten 10-T4), max **50.000 baris** per batch (di atas itu: pecah file; batas eksplisit di error). Simpan batch+items dalam satu tx DB.
4. Matcher = fungsi sinkron dipanggil setelah import selesai (bukan worker baru — K5): satu query set-based UPDATE `recon_items` JOIN `ledger_transactions` ON (gateway, external_ref) → `matched` bila amount sama, `amount_mismatch` bila beda; sisanya `missing_internal`. Lalu satu query kebalikan: tx money_in/money_out ber-external_ref pada `report_date` yang tidak ada di batch → INSERT items `missing_external`. Set-based, dua statement — bukan loop per-row.
5. Laporan: `GET /admin/recon/batches/{id}` → ringkasan count per match_status + daftar item non-matched (paginated).
6. Resolusi: endpoint `POST /admin/recon/items/{id}/resolve` membuat **pending adjustment** (T1) dengan `reason` otomatis menunjuk `recon_item` + suspense account sebagai lawan entri; simpan `resolved_by_adjustment_id`. TIDAK ada auto-resolve — uang tidak bergerak tanpa approve manusia kedua (K5).
7. Runbook `docs/runbooks/reconciliation.md`: arti tiap match_status, siapa investigasi apa, kapan eskalasi (pola runbook 12-T4).

### Test wajib
- Integration end-to-end: posting money_in ber-external_ref via router internal → import CSV berisi ref itu (match), ref asing (missing_internal), ref sama amount beda (amount_mismatch), dan tanpa satu tx yang ada (missing_external) → keempat status benar.
- Integration resolusi: resolve → pending adjustment tercipta → approve oleh identitas kedua → saldo suspense bergerak, `fn_verify_ledger_balance()` kosong.
- Unit: CSV malformed / amount desimal / >50k baris → 400 dengan pesan jelas.

### DoD
- [x] Alur harian lengkap terbukti di stack Docker: posting → import → laporan → resolve → approve.
- [x] `missing_external` tidak pernah menggerakkan uang otomatis.

### Hasil
Implementasi selesai dan terverifikasi penuh (build/vet/unit/integration/smoke):
- `migrations/000007_tx_correlation.{up,down}.sql` — kolom `external_ref`/`gateway` di `ledger_transactions` + partial index `(gateway, external_ref)`. Diisi otomatis di `internal/ledger/service/handle/service.go`'s `execTransfer` dari `cmd.Metadata` tervalidasi (`external_ref` max 128 char — `transport/metadata.go`).
- `migrations/000008_recon.{up,down}.sql` — tabel `recon_batches`/`recon_items` (skema K5 persis, plus `UNIQUE(batch_id, external_ref)`), akun sistem `type='suspense'` baru (ditambahkan ke `accounts_type_check`) diseed per gateway dengan `system_qualifier='suspense:<gateway>'`, `allow_negative=true`.
- **Desain penting yang tidak eksplisit di dokumen asli**: resolusi discrepancy butuh cara untuk menggerakkan `suspense[gateway]` — bukan akun user seperti `adjustment_credit/debit` (T1) yang selalu memakai `user.cash`. Ditambahkan dua processor baru `adjustment_suspense_credit`/`adjustment_suspense_debit` (`system.adjustment` ↔ `system.suspense[gateway]`, gateway dari metadata) yang didaftarkan ke `adjustments.Service.allowedTypes` (T1) — jadi resolusi recon lewat jalur maker-checker YANG SAMA, tanpa tabel/endpoint governance kedua. `POST /admin/adjustments` sekarang menerima `user_id` KOSONG untuk kedua tipe suspense ini (memakai `metadata.gateway` sebagai gantinya); `POST /admin/recon/items/{id}/resolve` selalu memakai salah satu dari kedua tipe ini.
- `internal/ledger/repository/recon_repository.go` — `RunMatcher` dua-statement set-based (UPDATE JOIN untuk matched/amount_mismatch, INSERT SELECT NOT EXISTS untuk missing_external); `InsertItems` di-chunk 2000 baris/statement (batas parameter bind Postgres, bukan 1 statement untuk 50rb baris).
- `internal/ledger/service/recon/recon.go` — `ImportBatch` (validasi gateway/duplikat ref/integral amount/>50k baris, satu transaksi DB: create batch+insert items+run matcher+mark completed), `GetBatchReport`, `ResolveItem` (buat pending adjustment via `adjustments.Service.Create` — TIDAK PERNAH menggerakkan uang sendiri; guard atomik `MarkItemResolved` sebagai backstop race, sama pola K3).
- `internal/ledger/transport/http.go` — 3 endpoint `/admin/recon/*` di router internal (admin-gated), parser CSV streaming (`parseReconCSV`) dengan urutan kolom fleksibel, cap 50.000 baris saat streaming (bukan hanya di service layer).
- **Bug ditemukan & diperbaiki via integration test** (bukan lolos ke smoke test): `RunMatcher`'s perbandingan `lt.created_at::date = $3` salah — Postgres membandingkan representasi TEKS parameter langsung ke `::date` tanpa konversi timezone lebih dulu (terbukti: `'2026-07-12 00:30+07'::date` = `2026-07-12`, padahal `'2026-07-12 00:30+07'::timestamptz::date` = `2026-07-11`, tanggal UTC yang benar). Diperbaiki jadi `$3::timestamptz::date`. Ini persis alasan kenapa `docs/plan/09` mewajibkan integration test dengan Postgres asli untuk setiap perubahan locking/SQL — sqlmock/unit test tidak akan pernah menangkap bug semantik SQL seperti ini.
- **Bug kedua ditemukan & diperbaiki via smoke test**: `pkg/middleware.RequireJSON()` menolak SEMUA body non-`application/json` di POST/PUT/PATCH, termasuk `multipart/form-data` — yang berarti endpoint upload CSV baru tidak akan pernah bisa dipanggil di production. Diperbaiki agar `multipart/form-data` juga diterima, dengan unit test baru.
- Unit test: 11 di `service/recon/recon_test.go` (validasi ImportBatch, ResolveItem termasuk race-loser orphan case), 8 di `transport/recon_csv_test.go` (CSV malformed/kolom hilang/amount desimal/>50k baris), 2 baru di `transport/http_test.go` (`adjustment_suspense_credit/debit` diblokir dari POST langsung).
- Integration test baru di `internal/ledger/schema_contract_test.go`: `TestSchemaContract_Recon_Matcher_AllFourStatuses` (posting nyata via posting engine + import CSV nyata, keempat match_status diverifikasi termasuk `amount_mismatch` menyimpan amount dari REPORT bukan ledger, dan `missing_external` menyimpan amount dari LEDGER bukan report), `TestSchemaContract_Recon_ResolveItem_CreatesAdjustment_ApproveMovesBalance` (Create tidak pernah gerak uang, Approve oleh identitas berbeda baru menggerakkan saldo suspense, `fn_verify_ledger_balance` kosong), `TestSchemaContract_Recon_DBConstraint_UniqueExternalRefPerBatch`.
- Runbook: [docs/runbooks/reconciliation.md](../runbooks/reconciliation.md) — arti tiap match_status, siapa investigasi apa (termasuk peringatan false-positive `missing_external` karena settlement lag), kapan eskalasi.
- Smoke test manual via curl terhadap stack Docker penuh: posting 2 transaksi money_in nyata (gateway=bca, external_ref berbeda) → import CSV 3 baris (1 match, 1 mismatch, 1 orphan tanpa tx internal) → laporan menunjukkan counts benar → resolve item missing_internal (buat pending adjustment, saldo suspense TETAP 0) → approve oleh identitas kedua (saldo suspense naik sesuai amount) → item menunjukkan `resolved_by_adjustment_id` terisi → `fn_verify_ledger_balance` kosong.
- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `go test -race ./...`, `go test -tags=integration -race ./...` — semua hijau. Migrasi 000007+000008 up→down→up diuji bersih.

---

## T3 — RLS & DB roles (07 H8, keputusan K9) — PALING AKHIR

**Tujuan**: defense-in-depth — kredensial bocor/readonly tidak bisa menulis; grant minimal eksplisit per tabel.

### Langkah
1. Migrasi `000009_rls_roles.up.sql` (+down) — port dari `docs/design/legacy-schemas/ledgernew.sql:648-760` dengan penyesuaian: `balance_transactions` → `ledger_transactions`; tambahkan SEMUA tabel yang kini ada (`outbox_events`, `account_balance_snapshots`, `recon_*`, `pending_adjustments`). Idempotent guard `IF NOT EXISTS` untuk CREATE ROLE (pola legacy sudah benar).
2. Grant `app_service`: per tabel eksplisit — SELECT/INSERT semua; UPDATE hanya di tabel yang memang di-UPDATE aplikasi (`account_balances`, `ledger_transactions`, `outbox_events`, `accounts`, `pending_adjustments`, `recon_items`); **TIDAK ADA UPDATE/DELETE pada `ledger_entries` dan `account_balance_snapshots`** — lapisan kedua di bawah trigger immutability. TIDAK ADA DELETE di tabel manapun kecuali yang terbukti dibutuhkan test suite (audit saat pengerjaan; ekspektasi: tidak ada).
3. Grant `app_readonly`: SELECT semua KECUALI `outbox_events` dan `pending_adjustments` (payload command internal); kalau reporting butuh status adjustment, buat view tanpa `cmd_payload`.
4. `ENABLE ROW LEVEL SECURITY` + `FORCE` di semua tabel; policy tahap ini `USING (true)` untuk kedua role (K9 — nilai sekarang ada di grant minimal + FORCE; policy per-tenant menyusul hanya bila multi-tenant nyata).
5. Deployment: buat user login `seev_app` dengan `GRANT app_service`, pindahkan `POSTGRES_USER` (tidak ada perubahan kode Go); migrasi tetap dijalankan role pemilik skema (bukan `app_service` — pisahkan identitas DDL dari DML, tulis di `.env.example` dan README deployment).
6. Update `docker-compose.yml`/`.env.example` supaya dev stack memakai `seev_app` juga — **jangan** biarkan dev berjalan sebagai owner sementara prod sebagai app_service (perbedaan environment = bug grant baru ketahuan di prod).

### Test wajib
- Integration suite penuh dijalankan dengan koneksi `app_service` → hijau (bukti grant cukup; ini test paling penting task ini).
- Integration negatif: sebagai `app_service`, `UPDATE ledger_entries` → ditolak permission (bukan cuma trigger); sebagai `app_readonly`, INSERT apapun → ditolak; `SELECT outbox_events` → ditolak.
- `./scripts/chaos-test.sh all` dengan role baru.

### DoD
- [x] Aplikasi (dev & CI) berjalan sebagai `app_service`, bukan owner.
- [x] Matriks grant per tabel terdokumentasi di komentar migrasi.
- [x] Migrasi down mengembalikan bersih (drop policy, disable RLS, revoke, drop role bila tidak dipakai).

### Hasil
Implementasi selesai dan terverifikasi penuh (build/vet/unit/integration/chaos):
- `migrations/000009_rls_roles.{up,down}.sql` — role `app_service`/`app_readonly` (idempotent guard), grant per tabel eksplisit untuk SEMUA 9 tabel final (diaudit langsung terhadap kode Go — nol `DELETE` di seluruh `internal/ledger`, jadi tidak ada tabel yang dapat grant DELETE): `accounts`/`account_balances`/`ledger_transactions`/`outbox_events`/`pending_adjustments`/`recon_batches`/`recon_items` dapat SELECT+INSERT+UPDATE; `ledger_entries`/`account_balance_snapshots` HANYA SELECT+INSERT (lapisan kedua di bawah trigger immutability). `app_readonly` dapat SELECT semua KECUALI `outbox_events` dan `pending_adjustments`. ENABLE+FORCE RLS di semua 9 tabel, policy `USING(true)` per role (K9 — nilai di grant minimal + FORCE, bukan tenant isolation).
- **Deployment (docs/plan/16 K9 step 5-6)**: identitas DDL (migrasi) dan DML (aplikasi) dipisah — `.env.example` sekarang punya `POSTGRES_USER`/`PASSWORD` (role `app_service` restricted, dipakai aplikasi) terpisah dari `POSTGRES_MIGRATE_USER`/`PASSWORD` (schema owner, dipakai HANYA oleh `make migrate-up`/`migrate-down`). `docker-compose.yml` membuat role login `seev_app` otomatis saat container pertama boot via `scripts/postgres-init/01-create-app-role.sh` — dev stack SENGAJA disamakan dengan produksi (bukan owner) supaya grant yang kurang ketahuan di mesin developer, bukan pertama kali di prod. `make grant-app-role` (baru) menjalankan `GRANT app_service TO $(POSTGRES_USER);` — satu kali per environment setelah migrate-up pertama membuat role `app_service`.
- **Bug ditemukan & diperbaiki saat mengerjakan task ini**: `pkg/middleware.RequireJSON()` menolak SEMUA body non-JSON termasuk `multipart/form-data` — baru ketahuan tidak berhubungan langsung dengan T3, tapi diperbaiki dalam rentang kerja yang sama (lihat Hasil T2 di atas untuk detail; disebut lagi di sini karena juga terverifikasi ulang lewat chaos test di bawah).
- Integration test baru di `internal/ledger/schema_contract_test.go` (helper `setupAppServiceTestDB` — provisioning 2 role login throwaway per test, satu digrant `app_service`, satu `app_readonly`): `TestSchemaContract_AppServiceRole_FullFlowSucceeds` (money_in → transfer_p2p → adjustment create+approve → recon import+resolve+approve, SEMUANYA lewat koneksi `app_service` murni — ini test paling penting task ini, per plan sendiri), `TestSchemaContract_AppServiceRole_CannotUpdateLedgerEntries`, `TestSchemaContract_AppReadonlyRole_CannotWrite`, `TestSchemaContract_AppReadonlyRole_CannotSeeOutboxOrAdjustments`, `TestSchemaContract_AppReadonlyRole_CanReadEverythingElse` (bukti grant readonly tidak kebablasan sempit).
- Verifikasi manual via psql (positive+negative) terhadap container throwaway sebelum menulis Go test: `app_service` bisa INSERT/UPDATE/SELECT `accounts`, DITOLAK UPDATE `ledger_entries`; `app_readonly` bisa SELECT `accounts`, DITOLAK INSERT dan DITOLAK SELECT `outbox_events`; superuser tetap bypass RLS (perilaku standar Postgres, FORCE hanya mengikat non-superuser termasuk table owner).
- `./scripts/chaos-test.sh all` dijalankan PENUH dengan server berjalan sebagai `seev_app`/`app_service` (bukan owner) — keempat skenario (kill -9 mid-posting, broker down, Postgres restart mid-traffic, Redis down) PASS, termasuk retry 40 request konkuren setelah kill -9 dan `fn_verify_ledger_balance`/`v_account_balance_audit` bersih setiap kali. Script `chaos-test.sh` diupdate (`ensure_app_role` idempotent + `start_server` connect sebagai `seev_app`) supaya benar-benar menguji role baru, bukan owner.
- Migrasi 000009 up→down→up diuji bersih di container terpisah.
- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `go test -race ./...`, `go test -tags=integration -race ./...` — semua hijau.

---

## Verifikasi Akhir Fase 2f
```bash
go build ./... && make test && go test -tags=integration -race ./...
./scripts/chaos-test.sh all
```
Smoke test manual: alur maker-checker penuh dan alur recon penuh via curl terhadap stack Docker. Setelah fase ini, seluruh backlog 07 (H1–H8) selesai; 08 S-track menyusul per keputusan [13 K-S](13-p1-backlog-review.md).
