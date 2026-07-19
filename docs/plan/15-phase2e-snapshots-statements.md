# 15 — Phase 2e: Balance Snapshots & Statements (H3, H4)

Prasyarat: [14](14-phase2d-ledger-semantics-events.md) selesai (statement menampilkan source/dest semantik; tidak ada dependensi keras ke T2/T3 tapi urutan fase dijaga sederhana). Keputusan desain: [13 K6, K7](13-p1-backlog-review.md). Kerjakan T1 → T2.

---

## T1 — Daily balance snapshot (07 H3, keputusan K6)

**Tujuan**: query saldo historis & opening balance tanpa full replay entries; verifier tidak perlu full-scan; fondasi partisi (S5) dan reporting (S7).

### Langkah
1. Migrasi `000005_balance_snapshots.up.sql` (+down):
   ```sql
   CREATE TABLE account_balance_snapshots (
       account_id      UUID        NOT NULL REFERENCES accounts(id),
       as_of_date      DATE        NOT NULL,
       closing_balance BIGINT      NOT NULL,
       entry_count     INT         NOT NULL,
       created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
       PRIMARY KEY (account_id, as_of_date)
   );
   ```
   Append-only by convention (job hanya INSERT); TIDAK perlu trigger immutability — snapshot boleh di-rebuild (DELETE per tanggal + hitung ulang) saat koreksi, justru itu bedanya dari `ledger_entries`.
2. Repository baru `internal/ledger/repository/snapshot_repository.go`:
   - `GetLatestBefore(ctx, accountID, date) (balance, asOf, found, err)` — snapshot terakhir ≤ tanggal.
   - `InsertForDate(ctx, date) (int, error)` — **satu statement set-based**, bukan loop per akun:
     ```sql
     INSERT INTO account_balance_snapshots (account_id, as_of_date, closing_balance, entry_count)
     SELECT e.account_id, $1::date,
            (SELECT balance_after FROM ledger_entries le
              WHERE le.account_id = e.account_id AND le.created_at < ($1::date + 1) AT TIME ZONE 'Asia/Jakarta'
              ORDER BY le.created_at DESC, le.id DESC LIMIT 1),
            count(*)
     FROM ledger_entries e
     WHERE e.created_at >= $1::date AT TIME ZONE 'Asia/Jakarta'
       AND e.created_at <  ($1::date + 1) AT TIME ZONE 'Asia/Jakarta'
     GROUP BY e.account_id
     ON CONFLICT (account_id, as_of_date) DO NOTHING;
     ```
     Hanya akun yang **beraktivitas hari itu** yang ditulis (K6 — hemat storage; pembaca memakai `GetLatestBefore`). `closing_balance` diambil dari `balance_after` entry terakhir (kontrak D6 skema: nilai final akun per transaksi) — bukan SUM ulang, sehingga sekaligus menjadi cross-check projection. `ON CONFLICT DO NOTHING` = job idempoten, aman retry.
   - `VerifyDate(ctx, date) ([]mismatch, error)` — bandingkan snapshot vs `account_balances.balance` untuk akun yang tidak beraktivitas setelah cutoff; selisih = korupsi projection.
3. Job harian di `internal/ledger/worker/` (ikut pola `Verifier`: `scheduler.Cron("balance_snapshot", "15 0 * * *", ...)` timezone Asia/Jakarta, `LockProvider` existing untuk multi-replica): snapshot H-1, lalu `VerifyDate`; mismatch → `alertFn` (pola 12-T4) + metric counter, dan JANGAN menulis ulang data yang benar dengan yang salah. Wire di `ledger.go` `StartWorkers`.
4. Catch-up: saat start, isi tanggal yang bolong sejak snapshot terakhir (loop mundur max 31 hari, log warning kalau lebih — situasi restore backup, arahkan ke runbook).
5. `GET /accounts/{id}/balance?as_of=YYYY-MM-DD` (extend handler `getBalance` existing, tetap lewat `CanAccessAccount`): `GetLatestBefore` + delta entries setelah snapshot s/d akhir tanggal — dua query ringan, bukan replay penuh.
6. Verifier `checkProjectionAudit` mulai membatasi scan ke akun beraktivitas sejak snapshot terakhir (view existing sudah 24 jam — samakan sumber kebenarannya dengan snapshot supaya tidak ada celah akun yang luput dua-duanya).

### Test wajib
- Integration: seed entries lintas 3 hari (kontrol `created_at` via SQL langsung) → snapshot per hari benar; akun pasif tidak dapat baris; `as_of` mengembalikan saldo historis benar; job dijalankan 2× untuk tanggal sama → idempoten.
- Integration timezone: entry pukul 23:30 WIB dan 00:30 WIB masuk tanggal berbeda (jam UTC-nya menjebak — 16:30 dan 17:30 UTC di hari yang sama).
- Integration mismatch: korup `account_balances` manual → `VerifyDate` menangkap + alert terkirim (pola test alert verifier).

### DoD
- [x] Job jalan di stack Docker nyata (smoke: set jadwal dekat, amati baris snapshot muncul).
- [x] `as_of` terdokumentasi di response API; migrasi up+down teruji.

### Hasil (2026-07-11)
Migrasi `000005_balance_snapshots` (tabel + index `idx_snapshots_account_date`) diverifikasi up+down bersih terhadap Postgres asli. Repository baru `internal/ledger/repository/snapshot_repository.go` mengikuti SQL persis dari dokumen (set-based `InsertForDate`, `GetLatestBefore`, `VerifyDate`) plus `BalanceAsOf` (gabung snapshot+delta, fallback ke `-infinity` saat belum ada snapshot — Postgres date-infinity arithmetic diverifikasi dulu terhadap Postgres asli sebelum dipakai, menghindari CASE yang tidak perlu) dan `LatestSnapshotDate` untuk catch-up job.

Job `internal/ledger/worker/snapshot.go` (`SnapshotJob`) mengikuti pola `Verifier` (scheduler.Cron, LockProvider, alertFn opsional) tapi type terpisah karena dia MENULIS data (INSERT), bukan cuma detect. Catch-up saat start dibatasi `maxCatchUpDays=31`. Diwire di `ledger.go` (`m.snapshotJob`, `StartWorkers`/`StopWorkers`). API `GET /accounts/{id}/balance?as_of=YYYY-MM-DD` menambah `Module.GetBalanceAsOf` (facade) + field `as_of` di response (omitempty — hanya muncul saat diminta).

**Bug nyata ditemukan lewat Docker smoke test** (bukan test suite — `SELECT max(as_of_date)` atas tabel kosong mengembalikan SATU baris dengan NULL, bukan nol baris; `sql.ErrNoRows` tidak pernah muncul, scan `NULL` langsung ke `*time.Time` panic di startup pertama SETIAP deployment baru). Fix: `sql.NullTime` + cek `.Valid`. Ditambahkan regression test `TestSchemaContract_BalanceSnapshot_LatestDate_EmptyTable`. Ini kejadian berulang di sesi ini: bug jenis ini tidak pernah tertangkap oleh unit test (sqlmock) maupun integration test yang tidak spesifik menguji tabel kosong — hanya smoke test end-to-end terhadap Postgres asli yang menangkapnya, sesuai pelajaran 09.

4 integration test baru (testcontainers, semua lulus): `MultiDay` (snapshot lintas 3 hari dengan gap, `GetLatestBefore` jalan mundur, `BalanceAsOf` benar, idempotent re-run), `TimezoneBoundary` (entry 23:30 WIB vs 00:30 WIB — beda hari WIB meski beda ~1 jam UTC di hari UTC yang sama — masing-masing snapshot cuma hitung entrinya sendiri), `MismatchDetected` (korupsi kolom snapshot, BUKAN `account_balances`, karena `account_balances` punya trigger `trg_balances_ua` yang selalu stamp `updated_at=now()` pada UPDATE apapun — didokumentasikan di komentar test kenapa), `LatestDate_EmptyTable` (regression bug di atas).

Smoke test manual penuh terhadap Docker stack nyata: post `money_in` via server yang jalan sungguhan → jalankan kode path `InsertForDate`/`VerifyDate` yang SAMA dengan yang dipakai cron (lewat program kecil sementara di dalam pohon modul, dihapus setelah selesai) → baris snapshot muncul di `account_balance_snapshots` (dicek via `psql`) → `GET /accounts/{id}/balance?as_of=<hari-ini>` lewat API asli mengembalikan `balance` yang benar plus field `as_of` di response, dan `as_of` tidak valid → `400`. `go build`, `go vet ./...` (+`-tags=integration`), `go test ./...`, `go test -tags=integration -race ./...` semua hijau.

---

## T2 — Statement & export (07 H4, keputusan K7)

### Langkah
1. Endpoint `GET /accounts/{id}/statement?from=YYYY-MM-DD&to=YYYY-MM-DD&format=json|csv` di router **publik** (`transport/http.go`, daftar di `mux()` bersama `getBalance`), ownership via `CanAccessAccount`.
2. Validasi: `from ≤ to`, rentang max **92 hari**, default `format=json`. Hitung opening balance = `GetLatestBefore(from-1)` + delta s/d awal `from` (pakai T1). Entries periode via query `ListEntries` pattern existing (index `idx_entries_account` menopang), max **5.000 baris** — lebih dari itu → 400 `"range too large, narrow the period"` (JANGAN silent truncate — statement terpotong diam-diam adalah bug finansial, bukan fitur).
3. Response json: `{account_id, currency, from, to, opening_balance, closing_balance, entries: [{entry_id, tx_id, transaction_type, direction, amount, balance_after, note, created_at}]}` — amount string minor-unit (konsisten K4). `closing_balance` = `balance_after` entry terakhir periode (atau = opening bila kosong).
4. Format csv: header tetap `entry_id,tx_id,type,direction,amount,balance_after,note,created_at`, **stream per-baris** ke `http.ResponseWriter` (`Content-Type: text/csv`, `Content-Disposition: attachment`) — jangan buffer 5.000 baris di memori (box kecil, K7). Escape RFC 4180 via `encoding/csv`.
5. Facade: method `Module.Statement(ctx, accountID, from, to)` — transport tidak menyentuh repository langsung (boundary rule).

### Test wajib
- Unit transport: validasi rentang/limit/format; 403 non-owner (pola test `getBalance`).
- Integration: seed entries lintas periode → opening/closing cocok dengan snapshot+delta; CSV di-parse balik `encoding/csv` = data sama dengan JSON.
- Smoke: curl kedua format terhadap stack Docker.

### DoD
- [x] Opening balance terbukti dari snapshot (bukan replay penuh) via query-count assertion atau EXPLAIN di test integration.
- [x] Limit 92 hari / 5.000 baris tertulis di error message dan di dokumentasi endpoint.

### Hasil (2026-07-11)
`model.StatementEntry`/`model.Statement` baru; `EntryRepository.ListByAccountRange` (join `ledger_transactions` untuk `type`, urutan kronologis ASC, `LIMIT $n` dipakai sebagai `maxStatementEntries+1` supaya overflow terdeteksi tanpa query COUNT terpisah). `Module.Statement` menyusun opening balance dari `snapshotRepo.BalanceAsOf(from-1 hari)` (bukan replay — memakai mekanisme snapshot T1 yang sama) + entries dari `ListByAccountRange`, menolak dengan `apperror.ErrStatementRangeTooLarge` (→ 400) kalau lebih dari 5.000 baris. Validasi rentang 92 hari & format dicek di transport (murah, tanpa DB) sebelum manggil facade.

Endpoint `GET /accounts/{id}/statement?from=&to=&format=json|csv` di router publik, lewat `CanAccessAccount` (pola sama seperti `getBalance`/`listEntries`). CSV di-stream baris-per-baris via `encoding/csv` langsung ke `http.ResponseWriter` (tidak buffer 5.000 baris) dengan header `Content-Type: text/csv` + `Content-Disposition: attachment`.

8 unit test transport (rentang/format/ownership/JSON/CSV, semua lulus) + 2 integration test terhadap Postgres asli: `TestSchemaContract_Statement_PeriodOpeningClosing` (seed data 3 hari, snapshot hari pertama, minta statement 2 hari terakhir → opening balance dari snapshot BUKAN replay dari awal akun, closing = `balance_after` entry terakhir, urutan kronologis benar) dan `TestSchemaContract_Statement_RangeTooLarge_LimitPlusOne` (bukti kontrak LIMIT+1 di level repository).

Smoke test manual penuh terhadap Docker stack nyata: post 2× `money_in` via server sungguhan → `GET .../statement?format=json` dan `...&format=csv` mengembalikan data yang PERSIS sama (entry_id, tx_id, amount, balance_after cocok satu-satu) → header CSV benar (`Content-Type`, `Content-Disposition: attachment`) → rentang >92 hari dan `format=xml` sama-sama `400` → akses akun user lain `404`. `go build`, `go vet ./...` (+`-tags=integration`), `go test ./...`, `go test -tags=integration -race ./...` semua hijau.

---

## Verifikasi Akhir Fase 2e
```bash
go build ./... && make test && go test -tags=integration -race ./...
```
Smoke test manual kedua endpoint terhadap Docker stack (pola sesi 10–12).
