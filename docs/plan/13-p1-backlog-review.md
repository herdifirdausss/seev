# 13 — Backlog Review: Analisa & Keputusan Desain untuk Sisa 07/08

Tanggal audit: 2026-07-11 (setelah 10–12 selesai dan diverifikasi, termasuk chaos test T7).

Dokumen ini adalah **hasil analisa kode aktual** terhadap task yang masih tersisa di [07-phase-2-hardening.md](07-phase-2-hardening.md) (H1–H8) dan [08-phase-3-scale.md](08-phase-3-scale.md) (S1–S9). Perannya sama seperti [09-hardening-review.md](09-hardening-review.md) terhadap 10–12: mengunci keputusan desain supaya dokumen eksekusi ([14](14-phase2d-ledger-semantics-events.md), [15](15-phase2e-snapshots-statements.md), [16](16-phase2f-governance-recon-rls.md)) bisa dikerjakan tanpa re-litigasi, dengan tiga sudut yang diminta: **benar untuk skala jangka panjang, efisien di resource terbatas, dan secure**.

Baca dulu: [09 bagian "Pelajaran Terverifikasi"](09-hardening-review.md) — semua aturannya tetap berlaku (integration test wajib untuk SQL/locking; sqlmock tidak cukup).

---

## Temuan Baru (belum tercatat di 07/09)

Audit ini menemukan tiga hal yang **mengubah bentuk desain** task 07 — bukan sekadar konfirmasi bahwa task belum dikerjakan:

### N1. Race double-reversal (KRITIS — money creation)
`Reversal.Validate` membaca status transaksi original via `GetStatus` — SELECT biasa **tanpa `FOR UPDATE`** (`internal/ledger/processors/reversal.go:50`, `repository/ledger_transaction_repository.go:153-173`). Di bawah READ COMMITTED, dua reversal konkuren atas transaksi yang sama (idempotency key berbeda — misal dua admin panik menekan tombol bersamaan) bisa sama-sama melihat `posted`, sama-sama lolos Validate, dan sama-sama posting entries pembalik → **original ter-reverse dua kali, uang tercipta**. `UpdateStatus` yang menandai original `reversed` (`reversal.go:82`) tidak punya guard `WHERE status='posted'`, jadi tidak menutup race ini.

**Konsekuensi desain**: guard H7 tidak boleh berupa "cek status lalu update" — harus **satu UPDATE atomik bersyarat** yang gagal untuk pemenang kedua (lihat K3).

### N2. `external_ref` / metadata tidak pernah dipersist (memblokir H2)
07 Task H2 menulis "match ke `ledger_transactions` via `reference_id`/metadata" — tapi **tidak ada kolom untuk itu**. `ledger_transactions` (migrations/000001:65-78) tidak punya kolom metadata/reference; `Command.Metadata` (termasuk `external_ref` yang sudah di-allowlist di `transport/metadata.go`) hanya hidup di memori dan di payload `outbox_events` (transien, bukan surface query). `Command.ReferenceID` (UUID) dipakai reversal untuk menunjuk tx original, juga tidak disimpan. Rekonsiliasi eksternal **mustahil** tanpa perubahan skema dulu.

**Konsekuensi desain**: H2 mendapat prasyarat baru — persist `external_ref` + `gateway` di `ledger_transactions` (lihat K5). Ini juga dibutuhkan statement (H4) dan reporting (S7).

### N3. Lifecycle settle/cancel/release tidak diguard sama sekali (KRITIS di jalur internal)
`WithdrawSettle.Validate` / `WithdrawCancel.Validate` / `EscrowRelease.Validate` hanya menjalankan validator saldo (`withdraw_settle.go:47-52`, `withdraw_cancel.go:41-46`, `escrow_release.go:64-72`) — **tidak pernah memeriksa transaksi asal**. `ReferenceID` bahkan tidak diwajibkan untuk tipe-tipe ini. Proteksi yang ada hanya kebetulan: settle atas withdraw yang sudah cancel gagal **hanya kalau** saldo akun hold kebetulan tidak cukup. Karena akun hold user adalah satu akun agregat (bukan per-withdraw), settle atas withdraw yang sudah di-cancel bisa **mengkonsumsi dana hold milik withdraw lain** yang masih aktif → saldo hold "bocor" antar-operasi. Terjangkau hanya dari router internal (pasca 10-T1), tapi router internal dipanggil orchestrator yang bisa punya bug retry/urutan — justru skenario yang paling sering terjadi di praktik.

**Konsekuensi desain**: H7 diperluas — `ReferenceID` jadi wajib untuk tipe lifecycle, dan guard-nya memakai mekanisme atomik yang sama dengan N1 (lihat K3).

---

## Kondisi Kode Saat Ini per Task (hasil audit, dengan file:line)

| Task | Yang sudah ada | Yang belum ada |
|------|----------------|----------------|
| H1 event contract | Payload dibangun ad-hoc `map[string]any` per processor, tanpa `schema_version` (contoh `money_in.go:106-125`); event type per-tipe (`"money_in.completed"`, `"withdraw.settled"`); kontrak at-least-once + dedup by `outbox_events.id` = AMQP `message_id` sudah benar tapi hanya terdokumentasi di 09 E2 | Package `internal/ledger/events`; struct payload versioned; konstanta event type; keseragaman 22 processor |
| H2 rekonsiliasi | `external_ref` di-allowlist di transport (`transport/metadata.go:23-27`) tapi dibuang (N2) | Persistensi korelasi; tabel `recon_batches`/`recon_items`; import CSV; matcher; akun suspense; alur resolusi |
| H3 snapshot | Verifier harian sudah ada (`worker/verifier.go`: trial balance, projection audit, outbox lag) tapi `v_account_balance_audit` hanya melihat 24 jam terakhir (migrations/000001:241) dan trial balance full-scan window; `pkg/scheduler` punya API `Cron(name, spec, fn)` + `LockProvider` siap pakai (scheduler.go:855, :809) | Tabel `account_balance_snapshots`; job harian; API `?as_of=`; pemanfaatan snapshot oleh verifier |
| H4 statement | `GET /accounts/{id}/entries` (keyset pagination) & `GET /accounts/{id}/balance` sudah ada (`transport/http.go:80-81`); `CanAccessAccount` untuk ownership (`ledger.go:237`) | Endpoint statement periode dengan opening/closing balance; format CSV |
| H5 maker-checker | `adjustment_credit/debit` sudah processor terdaftar (`processors.go:332-333`), admin-gated per-tipe di router internal (`transport/http.go:26-34,149`) | Tabel `pending_adjustments`; endpoint request/approve/reject; enforcement requester ≠ approver; audit trail |
| H6 semantic source/dest | Bug sort AccountIDs sudah diperbaiki (order-preserving `Deduplicate`); kolom diisi posisional `SafeIndex(cmd.AccountIDs, 0/1)` (`service/handle/service.go:238-239`) — kebetulan benar untuk mayoritas processor 2-kaki, tapi kontraknya implisit dan tidak diaudit | Kontrak eksplisit source/destination dari processor; audit 22 processor; test yang mengunci semantik |
| H7 lifecycle guard | Reversal punya cek status (`reversal.go:49-63`) tapi rentan race (N1); lifecycle lain tanpa guard apapun (N3) | Kolom `closed_by_tx_id` + guard atomik; wajib `ReferenceID`; tolak reversal-atas-reversal |
| H8 RLS | Desain lengkap di `docs/design/legacy-schemas/ledgernew.sql:648-760` (role `app_service`/`app_readonly`, ENABLE+FORCE RLS, grant minimal) — nama tabel lama (`balance_transactions`) | Port ke skema kanonik + tabel baru (outbox, snapshot, recon, pending_adjustments); pindah koneksi app ke `app_service`; test |

---

## Keputusan Desain yang Dikunci (jangan re-litigasi)

### K1. Urutan pengerjaan: 14 → 15 → 16, dan alasannya
- **[14] H6 → H7 → H1** dulu: H6 (semantik source/dest) dibutuhkan payload event H1; H7 menutup dua lubang money-safety (N1, N3) — prioritas tertinggi di antara semua sisa backlog; H1 mengunci kontrak event **sebelum** ada konsumen eksternal (mengubah kontrak setelah ada konsumen = breaking change yang mahal — inilah alasan jangka-panjangnya).
- **[15] H3 → H4**: H4 butuh opening balance dari snapshot H3. H3 juga menurunkan biaya verifier (projection audit tidak perlu full-scan akun lama) — relevan untuk box kecil.
- **[16] H5 → H2 → H8**: resolusi selisih rekonsiliasi (H2) memakai `adjustment_*` yang harus lewat maker-checker (H5) — H5 duluan supaya jalur resolusi lahir sudah ter-governance. H8 (RLS) paling akhir karena kebijakannya harus mencakup SEMUA tabel final — mengerjakannya sebelum tabel recon/snapshot/pending ada berarti migrasi RLS dua kali.
- **S-track (08) tetap di belakang H-track** — lihat bagian S di bawah.

### K2. H6 — `ResolveAccounts` mengembalikan struct, bukan slice polos
```go
type ResolvedAccounts struct {
    Ordered     []uuid.UUID // urutan posisional existing — BuildEntries per-index tetap jalan
    Source      uuid.UUID   // akun asal dana (uuid.Nil bila tidak applicable, mis. adjustment_credit)
    Destination uuid.UUID   // akun tujuan dana (uuid.Nil bila tidak applicable)
}
```
- Perubahan mekanis di 22 processor + `service.go` + mocks — besar tapi dangkal; alternatif "method baru di interface" tetap menyentuh 22 file tanpa keuntungan, dan alternatif "dokumentasikan konvensi posisional" ditolak karena kontrak implisit sudah terbukti rapuh (bug sort AccountIDs di 09 E4 lahir dari kontrak implisit yang sama).
- `Source`/`Destination` harus anggota `Ordered` (di-assert di service, bukan trust). Kolom `ledger_transactions.source/destination_account_id` diisi dari sini, bukan `SafeIndex` lagi.
- **Tidak ada migrasi data lama** — kolom informatif, kebenaran tetap di entries (keputusan 07 H6 dipertahankan). Catat cutoff date di komentar migrasi.

### K3. H7 — satu mekanisme guard lifecycle: `closed_by_tx_id` + UPDATE bersyarat
Tambah ke `ledger_transactions`:
```sql
closed_by_tx_id UUID NULL UNIQUE REFERENCES ledger_transactions(id),
closed_reason   TEXT NULL CHECK (closed_reason IN ('reversed','settled','cancelled','released','refunded')),
CHECK ((closed_by_tx_id IS NULL) = (closed_reason IS NULL))
```
- Guard = **satu statement**: `UPDATE ledger_transactions SET closed_by_tx_id=$new, closed_reason=$r, status=CASE WHEN $r='reversed' THEN 'reversed' ELSE status END, updated_at=now() WHERE id=$ref AND closed_by_tx_id IS NULL` → cek `RowsAffected == 1`. Pemenang kedua dari race konkuren dapat 0 rows → tolak. Ini menutup N1 (double-reversal), N3 (settle-after-cancel, double-settle), dan "reversal atas transaksi yang sudah settle" — **satu mekanisme untuk semua**, bukan state machine per-tipe.
- `ReferenceID` jadi **wajib** untuk `withdraw_settle`, `withdraw_cancel`, `withdraw_pending_settle`, `withdraw_pending_cancel`, `escrow_release`, `escrow_refund`, `reversal` — divalidasi di `ValidateCommand` (pre-DB). Validate juga mengecek: tipe tx asal cocok (settle hanya atas `withdraw_initiate`, dst.), `amount` = amount asal (**full-amount only untuk MVP** — partial settle butuh sub-ledger holds, itu scope S-track; tolak dengan error jelas), dan tolak reversal atas tx bertipe `reversal`.
- `status='reversed'` existing dipertahankan (di-set bersamaan) — konsumen `GetStatus` tidak berubah.
- **Kenapa bukan tabel `holds` terpisah**: benar secara teori untuk partial settle, tapi menambah tabel + join di jalur panas untuk kebutuhan yang belum ada. Kolom nullable di tabel existing = nol biaya untuk transaksi biasa, dan tidak menghalangi migrasi ke sub-ledger nanti (kolom tinggal jadi denormalisasi).

### K4. H1 — satu event generik versioned, bukan 22 skema per-tipe
- Package baru **`internal/ledger/events`** — **satu-satunya subpackage `internal/ledger` yang boleh diimport modul lain** (tulis eksplisit di PROJECT_GUIDE.md). Isinya hanya tipe payload + konstanta — tidak boleh import subpackage ledger lain (no repository/processors), supaya konsumen tidak menyeret dependensi.
- Event type dikonsolidasi: **`ledger.transaction.posted.v1`** (+ `ledger.transaction.reversed.v1`) dengan field `transaction_type` di payload — konsumen filter by field, bukan by 22 routing key. Alasan jangka-panjang: versioning 2 skema jauh lebih murah daripada 22; menambah tipe transaksi baru (S8 `interest_accrue` dst.) tidak menambah skema event.
- Payload: `schema_version`, `tx_id`, `transaction_type`, `amount` (string minor-unit), `currency`, `source_account_id`/`destination_account_id` (dari K2, nullable), `entries` ringkas `[{account_id, direction, amount}]`, `occurred_at`, `metadata_ref` (external_ref bila ada, lihat K5).
- Kontrak delivery ditulis di doc yang sama: **at-least-once, konsumen WAJIB dedup by AMQP `message_id`** (= `outbox_events.id`) — formalisasi 09 E2.
- Event type lama (`money_in.completed` dst.) langsung diganti — **boleh breaking sekarang** karena belum ada konsumen; justru itu alasan H1 dikerjakan sebelum konsumen lahir.

### K5. H2 — persist korelasi dulu, baru rekonsiliasi
1. Migrasi: `ledger_transactions` + `external_ref TEXT NULL`, `gateway TEXT NULL`; partial index `(gateway, external_ref) WHERE external_ref IS NOT NULL`. Diisi service dari metadata tervalidasi (`external_ref` max 128 char; `gateway` sudah ter-allowlist `constant.ValidGateways`).
2. Tabel `recon_batches` (id, gateway, report_date, source_filename, row_count, status, created_by, created_at) + `recon_items` (id, batch_id FK, external_ref, amount BIGINT, raw JSONB, match_status CHECK IN ('matched','missing_internal','missing_external','amount_mismatch'), matched_tx_id UUID NULL, resolved_by_adjustment_id UUID NULL).
3. Import CSV via **router internal, admin-gated** (pola `POST /admin/outbox/...` di `transport/http.go:91-92`): `POST /admin/recon/batches` (multipart CSV, kolom: external_ref, amount, settled_at). Matcher = fungsi sinkron per-batch (bukan worker baru — batch settlement harian ukurannya ribuan row, satu query JOIN cukup; jangan tambah goroutine/worker untuk ini di box kecil).
4. Akun suspense: seed `system_qualifier='suspense:<gateway>'` per gateway (pola seed 000002), `allow_negative=true`. Selisih diparkir via `adjustment_*` yang **wajib** lewat maker-checker H5 dengan `reason` menunjuk `recon_items.id`.
5. `missing_external` TIDAK auto-resolve — hanya laporan; uang tidak bergerak tanpa keputusan manusia (maker-checker).

### K6. H3 — snapshot incremental, timezone-pinned, cross-checked
- Tabel: `account_balance_snapshots (account_id UUID REFERENCES accounts(id), as_of_date DATE, closing_balance BIGINT NOT NULL, entry_count INT NOT NULL, created_at TIMESTAMPTZ DEFAULT now(), PRIMARY KEY (account_id, as_of_date))`.
- Job harian 00:15 **Asia/Jakarta** via `pkg/scheduler.Cron` + `LockProvider` existing (aman multi-replica, konsisten keputusan 09 K2 Redis-opsional). Hitungan **incremental**: snapshot kemarin + agregat entries hari itu (satu query GROUP BY per hari, index `idx_entries_account` sudah menopang); akun tanpa aktivitas hari itu TIDAK ditulis ulang (baca "snapshot terakhir ≤ tanggal" saat query — hemat storage, penting saat jutaan akun pasif).
- Cross-check wajib di job yang sama: `closing_balance` vs `balance_after` entry terakhir akun hari itu — selisih = alert via `alerting.AlertFunc` existing (pola verifier 12-T4) + JANGAN tulis snapshot yang salah.
- `GET /accounts/{id}/balance?as_of=YYYY-MM-DD` = snapshot terakhir ≤ tanggal + delta entries setelahnya s/d cutoff — bukan full replay.
- Ini juga **prasyarat efisiensi S5** (partisi): query saldo tidak menyentuh partisi lama.

### K7. H4 — statement di router publik, dibatasi ketat
`GET /accounts/{id}/statement?from=&to=&format=json|csv` di router **publik** dengan `CanAccessAccount` (pola `getBalance`). Batas wajib: rentang max 92 hari, max 5.000 entri per response (di atas itu → 400 dengan pesan minta rentang lebih sempit), format CSV di-stream (`text/csv`, tidak buffer penuh di memori). Opening balance dari K6. Rate-limit publik existing sudah melindungi endpoint ini.

### K8. H5 — maker-checker minimal-tapi-tegas
- Tabel `pending_adjustments (id UUID PK, requested_by TEXT NOT NULL, approved_by TEXT NULL, cmd_payload JSONB NOT NULL, reason TEXT NOT NULL, status TEXT CHECK IN ('pending','approved','rejected','executed','failed'), created_at, decided_at, executed_tx_id UUID NULL)`.
- Endpoint di router internal, admin-gated: `POST /admin/adjustments` (buat pending — TIDAK memanggil `Post`), `POST /admin/adjustments/{id}/approve`, `POST /admin/adjustments/{id}/reject`. **Enforcement inti: `approved_by != requested_by`** dari JWT sub — ditolak 403 kalau sama; dua identitas tercatat permanen.
- Eksekusi saat approve = `m.Post` biasa dengan **idempotency key deterministik `adj:<pending_id>`** — approve yang di-retry tidak double-post (memakai jaminan idempotensi yang sudah teruji chaos-test).
- `adjustment_credit/debit` **dicabut dari akses langsung** router internal (hapus dari tipe yang bisa di-POST langsung; hanya via alur pending). `freeze_*`/`chargeback` tetap langsung (compliance mendesak) tapi `reason` di metadata jadi wajib via `ValidateCommand`.
- Approval TIDAK pakai tabel antrian generik/workflow engine — dua endpoint + satu tabel cukup; kebutuhan multi-step approval belum ada (kalau nanti ada, tabel ini tinggal ditambah kolom step).

### K9. H8 — RLS sebagai defense-in-depth, bukan tenant isolation (dulu)
- Port `ledgernew.sql:648-760` ke migrasi baru dengan nama tabel kanonik + tabel baru (snapshot, recon, pending_adjustments). Role: `app_service` (CRUD minimal per tabel — tetap TIDAK dapat UPDATE/DELETE `ledger_entries`, selaras trigger immutability), `app_readonly` (SELECT semua KECUALI `outbox_events` dan `pending_adjustments.cmd_payload` — via view kalau perlu kolom lain).
- Policy tahap ini sederhana: `app_service` melihat semua baris (`USING (true)`) — nilai RLS-nya adalah **ENABLE+FORCE + grant minimal** (kredensial readonly yang bocor tidak bisa menulis; superuser/owner accident tertahan FORCE). Policy per-tenant menyusul HANYA kalau multi-tenant jadi nyata — jangan bayar kompleksitas `current_setting('app.user_id')` per-query sekarang.
- Koneksi aplikasi pindah ke `app_service` via `POSTGRES_USER` (tidak ada perubahan kode Go). Integration test suite dijalankan dengan role ini untuk membuktikan grant cukup.

### K-S. Keputusan S-track (08) — dikunci sekarang, dokumen detail ditulis saat dijadwalkan
- **S1 (limits/velocity)**: modul `internal/policy` dievaluasi di transport SEBELUM `ledger.Post`; counter Redis sliding-window ATAU in-memory bila `REDIS_ENABLED=false` (pola persis 12-T1 `MemoryRateLimiter`); konfigurasi limit di tabel, di-cache in-process dengan TTL. Ledger module tidak tahu-menahu. Prasyarat: tidak ada.
- **S2 (multi-currency)**: tabel `currencies (code, minor_unit)` jadi rujukan `IntegralAmountValidator` (exponent per currency, bukan asumsi 0-desimal IDR); `GetSystemAccountID` mulai filter currency (TODO 05-1b.1); FX = orchestration dua transaksi via akun `fx_conversion` per pasangan — **bukan** fitur ledger. Prasyarat: H1 (payload event membawa currency dengan benar).
- **S3 (scheduled/batch)**: `scheduled_transactions` + job `pkg/scheduler`; idempotency key deterministik `sched:<id>:<run_date>` — pola sudah terbukti di K8. Batch disbursement = loop `Post` dengan manifest + resume by idempotency; tidak butuh mesin baru.
- **S4 (hot account lanjutan)**: TETAP ditunda sampai ada bukti metrics lock-wait pada `UPDATE balance = balance + delta` (11-T1 sudah menghilangkan bottleneck utama). Jangan sub-shard tanpa pengukuran.
- **S5 (partisi/archival)**: ikuti panduan 6-fase `ledgernew.sql` bagian PARTITIONING (dual-write → backfill → rename). Prasyarat keras: H3 (K6). Baru relevan saat `ledger_entries` > ~50 juta row — ukur dulu.
- **S6 (AML hooks)**: interface `PrePostHook` di `Handle()` setelah validasi bisnis sebelum build entries; mode `monitor` dulu, `block` menyusul. Prasyarat: S1 (velocity data).
- **S7 (regulatory reporting)**: read-only atas snapshot (H3) + recon (H2) via role `app_readonly` (H8) — ketiganya prasyarat keras; jangan mulai sebelum itu.
- **S8 (interest accrual)**: tipe transaksi + processor baru (registry pattern membuatnya murah) + job harian; idempotency `accrue:<account>:<date>`.
- **S9 (rebuild & DR drill)**: script `scripts/rebuild-projection.sh` — truncate `account_balances` → replay agregat dari `ledger_entries` → verifier bersih; drill restore di staging dengan RTO tercatat. Murah dan berharga — boleh dikerjakan kapan saja, tidak ada prasyarat; kandidat quick-win setelah 14.

---

## Yang TIDAK berubah (kontrak dari 09 tetap berlaku)

Semua butir 09 bagian E dipertahankan: idempotency gate, outbox at-least-once, guard DB immutability, lock ordering via `ORDER BY account_id` di SQL, graceful shutdown ordering. Tambahan dari 10–12 yang kini juga berstatus kontrak: split lock user-vs-system (11-T1 — **jangan** tambah validator yang membaca `Balance` akun sistem), fee server-side + metadata allowlist (10-T3), idempotency scope per-user (10-T2), outbox backoff + replay (12-T2/T3).

## Verifikasi (berlaku untuk semua task 14–16)

```bash
go build ./...
make test                              # unit, -race
go test -tags=integration -race ./...  # WAJIB untuk semua task yang menyentuh SQL/locking/migrasi
./scripts/chaos-test.sh all            # jalankan ulang setelah 14 selesai (guard H7 mengubah jalur posting)
```
Task yang menyentuh migrasi wajib menguji `up` DAN `down`. Task dengan endpoint baru wajib smoke test manual via curl terhadap stack Docker (pola sesi 10–12).
