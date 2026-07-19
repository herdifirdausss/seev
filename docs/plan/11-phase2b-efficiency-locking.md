# 11 — Phase 2b: Efficiency & Locking Redesign

> Prasyarat: baca [09-hardening-review.md](09-hardening-review.md) bagian C. **Setiap task di dokumen ini menyentuh SQL mentah, locking, atau posting pipeline — WAJIB diverifikasi dengan `go test -tags=integration -race ./...` (Docker aktif) DAN smoke test manual sebelum ditandai selesai.** Unit test (sqlmock) hijau TIDAK CUKUP — tiga bug nyata (alias FOR UPDATE, CASE placeholder cast, AccountIDs sort order) lolos sqlmock di sesi sebelumnya dan hanya tertangkap oleh Postgres asli. Lihat `internal/ledger/schema_contract_test.go` sebagai pola test yang harus diperluas.

## T1 — Pisahkan strategi lock: akun user (FOR UPDATE) vs akun sistem (delta apply)

**Masalah** (09 §C13): `LockBalances` (`internal/ledger/repository/account_balance_repository.go:48-95`) mengunci SEMUA akun yang terlibat transaksi, termasuk akun sistem (`settlement[gateway]`, `fee[gateway]`) yang `allow_negative=true`. Setiap `money_in`/`money_out` lewat gateway yang sama ter-serialize total sepanjang seluruh pipeline validasi.

**Desain baru (K6)**:
- Akun **user** (`allow_negative=false`): tetap `SELECT ... FOR UPDATE` — perlu pre-read balance untuk overdraft check yang benar (`chk_balance_floor` di DB adalah safety net terakhir, bukan mekanisme utama — race harus dicegah oleh lock, bukan cuma ditangkap constraint setelah kejadian).
- Akun **sistem** (`allow_negative=true`): TIDAK di-lock, TIDAK di-pre-read balance. Delta di-apply sebagai satu statement atomik di akhir: `UPDATE account_balances SET balance = balance + $delta, updated_at = now() WHERE account_id = $1 RETURNING balance`. Karena `allow_negative=true`, tidak perlu floor check di aplikasi (DB constraint `chk_balance_floor` otomatis skip cek untuk baris ini). `balance_after` untuk `ledger_entries` diambil dari `RETURNING balance` — bukan dihitung di Go seperti sekarang.

### Langkah

1. **`internal/ledger/model/account_balance.go`**: tambah field `AllowNegative bool`.
2. **`internal/ledger/repository/account_balance_repository.go`**:
   - `LockBalances`: SELECT tambah kolom `ab.allow_negative`; **filter WHERE**: query ini sekarang HANYA untuk akun yang perlu dikunci. Pemanggil (Service, lihat langkah 4) harus memisahkan `accountIDs` jadi dua slice SEBELUM memanggil repository — `LockBalances` sendiri tidak berubah signature/perilaku untuk daftar yang diberikan (ia mengunci semua yang diminta), tapi Service hanya mengirim subset akun user.
     - Alternatif lebih aman (pilih ini): `LockBalances` tetap generik (mengunci apa pun yang diberi), TAPI hapus tanggung jawab "tahu mana yang perlu dikunci" dari situ — itu keputusan Service, bukan repository. Repository tidak perlu tahu soal `allow_negative` untuk fungsi ini sama sekali kalau Service sudah memfilter sebelumnya. **Namun** Service butuh tahu `allow_negative` sebelum memutuskan filter — jadi tambahkan method baru:
       ```go
       // GetAccountFlags returns allow_negative for the given accounts, read
       // outside any lock (these flags are immutable post-provisioning — see
       // docs/plan/01, system accounts are seeded once and never toggled).
       GetAccountFlags(ctx context.Context, tx *sql.Tx, ids []uuid.UUID) (map[uuid.UUID]bool, error)
       ```
       Implementasi: `SELECT account_id, allow_negative FROM account_balances WHERE account_id IN (...)` tanpa `FOR UPDATE` (baca biasa, boleh di luar lock karena nilainya immutable per desain — didokumentasikan sebagai invariant, BUKAN di-enforce oleh trigger DB untuk task ini; kalau mau enforce, tambahkan trigger `BEFORE UPDATE ON account_balances WHEN OLD.allow_negative IS DISTINCT FROM NEW.allow_negative` yang menolak — opsional, tambahkan kalau waktu cukup, tandai TODO kalau tidak).
   - Tambah method baru:
     ```go
     // ApplySystemDeltas atomically applies signed deltas to unlocked
     // (allow_negative=true) accounts and returns each account's balance
     // AFTER the delta, for use as ledger_entries.balance_after. Must be
     // called from within the posting transaction, after InsertEntries'
     // caller has computed deltas — see Service.execTransfer.
     ApplySystemDeltas(ctx context.Context, tx *sql.Tx, deltas map[uuid.UUID]decimal.Decimal) (map[uuid.UUID]decimal.Decimal, error)
     ```
     Implementasi: loop per akun (jumlah akun sistem per transaksi selalu kecil, 1-2, jadi round-trip per akun disini TIDAK jadi bottleneck — beda dengan `InsertEntries` di T2 yang per-baris untuk N entries besar) — `UPDATE account_balances SET balance = balance + $1::bigint, updated_at = now() WHERE account_id = $2 RETURNING balance`. Kumpulkan hasil `RETURNING` ke map. Kalau mau batch, bisa pakai pola `CASE` yang sama seperti `UpdateBalances` tapi dengan `+` bukan replace — opsional optimisasi, tidak wajib untuk DoD task ini (loop kecil dulu, ukur nanti).
   - `UpdateBalances` (existing, dipakai untuk akun USER yang di-lock) — TIDAK berubah, tetap replace-by-value seperti sekarang (karena Service sudah punya balance akurat dari `LockBalances` + `applyEntries`).
3. **`internal/ledger/service/handle/service.go`** (`execTransfer`, langkah 2-9 di komentar existing):
   - Langkah **2 (LOCK ACCOUNTS)**: sebelum lock, panggil `GetAccountFlags` untuk `cmd.AccountIDs`, pisahkan jadi `userAccountIDs` (allow_negative=false) dan `systemAccountIDs` (allow_negative=true). Panggil `LockBalances(ctx, tx, userAccountIDs)` — HANYA untuk akun user.
   - Langkah **3 (STRUCTURAL VALIDATION)**: `validateAccounts` sekarang hanya menerima balances akun user dari LockBalances — untuk akun sistem, validasi status/currency perlu jalur berbeda (baca ringan tanpa lock, mis. lewat `GetAccountFlags` yang diperluas untuk juga mengembalikan currency+status, atau query terpisah `GetAccountsMeta`). Sesuaikan `validateAccounts` untuk menerima dua sumber: `balances map[uuid.UUID]model.AccountBalance` (user, dari lock) + `systemMeta map[uuid.UUID]model.AccountMeta` (sistem, dari baca ringan) — gabungkan validasi status/currency untuk kedua kelompok, TAPI overdraft check (`balance < amount`) HANYA berlaku untuk kelompok user.
   - Langkah **4 (BUSINESS VALIDATION / processor.Validate)**: processor seperti `SufficientFundsValidator` mengasumsikan `balances map[uuid.UUID]model.AccountBalance` berisi SEMUA `cmd.AccountIDs`. Ini PENTING: `SufficientFundsValidator{AccountID: cmd.AccountIDs[0]}` hanya pernah menunjuk akun SOURCE yang SELALU akun user untuk semua processor yang ada (cek `processors/*.go` — source selalu `user.cash`/`user.pocket`, tidak pernah akun sistem). Jadi `balances` yang dilihat processor tetap bisa berupa map gabungan (user account balances dari lock + system account balances "virtual" dengan `Balance` diisi placeholder/tidak dipakai) — TAPI validator TIDAK BOLEH memanggil `SufficientFundsValidator` pada akun sistem (sudah tidak pernah terjadi di kode saat ini, verifikasi ulang dengan grep `SufficientFundsValidator{AccountID: cmd.AccountIDs[1]}` atau `[2]` — HARUS NOL hasil, karena AccountIDs[1]/[2] selalu destination/fee yang sistem atau user penerima, bukan pernah dicek sufficient-funds). Kalau grep menemukan ada, itu bug yang harus diperbaiki dulu sebelum lanjut (source selalu index yang benar per processor's own ResolveAccounts order — cross-check dengan T1 fix di sesi sebelumnya soal AccountIDs ordering).
   - Langkah **5 (BUILD ENTRIES)**: tidak berubah — processor tetap mengembalikan `[]EntryInstruction` yang sama seperti sekarang, terlepas dari akun user/sistem.
   - Langkah **6 (VALIDATE BALANCED)**: tidak berubah.
   - Langkah **7 (COMPUTE NEW BALANCES)**: `applyEntries` sekarang HANYA dipakai untuk akun user (yang datanya ada dari lock). Untuk akun sistem, hitung delta per akun dari `entries` (`Σcredit - Σdebit` untuk akun itu) — fungsi baru `computeSystemDeltas(entries []EntryInstruction, systemAccountIDs []uuid.UUID) map[uuid.UUID]decimal.Decimal`.
   - Langkah **8 (INSERT LEDGER ENTRIES)**: **urutan berubah** — `ApplySystemDeltas` harus dipanggil SEBELUM `InsertEntries`, karena `InsertEntries` butuh `balance_after` final untuk SEMUA akun (user dari `applyEntries`, sistem dari `ApplySystemDeltas`'s `RETURNING`). Gabungkan kedua map balance (`newBalances` user + hasil `ApplySystemDeltas`) jadi satu map sebelum dilempar ke `InsertEntries`.
   - Langkah **9 (UPDATE BALANCE PROJECTIONS)**: `UpdateBalances` sekarang HANYA dipanggil dengan `newBalances` akun USER (subset). Akun sistem sudah ter-update oleh `ApplySystemDeltas` di langkah 8.
   - **Urutan final yang benar**: idempotency gate → lock akun user → structural validation (user+sistem) → business validation → build entries → validate balanced → compute user balances (applyEntries) → **apply system deltas (dapat balance_after sistem)** → insert entries (pakai balance gabungan) → update balance projections (user saja) → mark posted → outbox. **Tulis urutan lengkap ini di komentar function**, gantikan komentar existing yang menyebut urutan lama — dan JANGAN reorder lagi tanpa membaca dokumen ini (mengulang aturan PROJECT_GUIDE.md soal jangan reorder `execTransfer` tanpa paham kenapa).
4. **Race yang harus dicegah**: dua transaksi `money_in` konkuren lewat gateway yang sama TIDAK BOLEH lost-update pada `account_balances.balance` milik akun settlement — ini kenapa `ApplySystemDeltas` HARUS pakai `balance = balance + $delta` (atomic increment), BUKAN read-modify-write dua langkah. Postgres menjamin atomicity untuk single-row `UPDATE ... SET col = col + $x` tanpa butuh row lock eksplisit sebelumnya (MVCC + row-level write lock implisit saat UPDATE). Ini sudah otomatis benar SELAMA implementasi memakai `balance + $delta` langsung di SQL, bukan `SELECT` lalu `UPDATE` dua statement terpisah.

### Test yang wajib ditulis
- **Integration test (Docker, WAJIB)**: perluas `internal/ledger/schema_contract_test.go` — jalankan N goroutine `money_in` konkuren (mis. 50 goroutine, gateway sama, amount berbeda) → setelah semua selesai, saldo `settlement[gateway]` harus sama dengan `-Σamount` (settlement dikreditkan negatif net karena dia debit source), dan `fn_verify_ledger_balance` harus kosong (tidak ada tx unbalanced). Test ini adalah bukti utama bahwa delta-apply tidak lost-update di bawah concurrency tinggi.
- Unit test (mock repo) untuk `execTransfer` dengan skenario 1 akun user + 1 akun sistem + 1 akun fee (3-entry) — pastikan `LockBalances` dipanggil HANYA dengan akun user, `ApplySystemDeltas` dipanggil dengan akun sistem+fee, urutan pemanggilan benar (mock `.InOrder()` kalau library mock mendukung).
- Regression: semua 22 processor existing test tetap hijau tanpa modifikasi (perubahan ada di Service, bukan di processor interface).

### DoD
- [ ] `go build ./...`, `make test` hijau.
- [ ] `go test -tags=integration -race ./...` hijau, termasuk test concurrency baru di atas.
- [ ] Smoke test manual: `money_in` berulang cepat (script loop curl) ke gateway yang sama dari BEBERAPA proses/goroutine paralel tidak menunjukkan lock wait time signifikan dibanding sebelum perubahan (bandingkan `pg_stat_activity` wait_event sebelum/sesudah kalau memungkinkan — opsional tapi bagus untuk laporan).
- [ ] `fn_verify_ledger_balance()` dan `fn_verify_account_balance()` tetap 0 diskrepansi setelah seluruh test suite.

---

## T2 — Batch `InsertEntries`

**Masalah** (09 §C14): `internal/ledger/repository/ledger_entry_repository.go:50-70` — loop `ExecContext` per entry, memperpanjang lock hold pada akun user yang masih dikunci sepanjang transaksi (setelah T1, lock hold akun user makin penting untuk diperpendek karena T1 sudah mempersingkat sisi akun sistem — sisi user harus ikut diperpendek).

### Langkah
1. Tiru pola `InsertEvents` di `internal/ledger/repository/outbox_event_repository.go:89-119` (multi-row INSERT dengan placeholder dinamis, capped batch size — untuk entries per transaksi biasanya kecil, 2-3 baris, jadi cap tidak kritis tapi tetap tambahkan `maxEntriesBatch = 50` sebagai safety yang konsisten dengan pola outbox).
2. `InsertEntries` baru: bangun satu statement `INSERT INTO ledger_entries (id, transaction_id, account_id, direction, amount, balance_after, note, created_at) VALUES ($1,$2,...,now()), ($8,$9,...,now()), ...` dengan `args` yang di-generate dari loop `entries`.
3. Pastikan urutan generate args konsisten dengan urutan `entries` (tidak perlu sort — insert tidak butuh urutan spesifik, unlike `UpdateBalances` yang butuh `SortedDecimalKeys` untuk determinisme antar-transaksi paralel/deadlock avoidance; INSERT baris baru tidak berkontensi dengan baris lain jadi urutan sembarang aman).

### Test yang wajib ditulis
- Unit test repository (sqlmock oke untuk ini karena hanya menguji SQL statement generation, bukan semantik Postgres-specific): 1 entry → 1 row VALUES; 3 entries → 3-row VALUES dengan args count benar.
- **Integration test**: transaksi 3-entry (dengan fee) ter-insert benar, `SELECT COUNT(*) FROM ledger_entries WHERE transaction_id = $1` = 3, tiap baris punya `balance_after` yang benar.

### DoD
- [ ] `make test` dan integration test hijau.
- [ ] Insert entries jadi 1 round-trip DB per transaksi (bukan N).

---

## T3 — Cache untuk ResolveAccounts

**Masalah** (09 §C15): `GetAccountID`, `GetSystemAccountID`, `GetAccountCurrency` dipanggil berkali-kali per posting sebelum transaksi DB dimulai.

### Langkah
1. **System account IDs** (`GetSystemAccountID(type, qualifier)`): immutable setelah seed (`migrations/000002_seed_system_accounts.up.sql`). Tambah in-process cache di `internal/ledger/repository/account_repository.go`:
   ```go
   type accountRepo struct {
       db database.DatabaseSQL
       systemAccountCache sync.Map // key: "type:qualifier" -> uuid.UUID
   }
   ```
   `GetSystemAccountID`: cek cache dulu, kalau miss query DB lalu simpan ke cache. TIDAK perlu TTL/invalidasi (system account tidak pernah berubah post-seed — kalau operator menambah gateway baru, restart proses adalah invalidasi yang cukup untuk MVP; dokumentasikan ini sebagai keputusan, bukan bug).
2. **User account (userID, type) → (accountID, currency)**: TTL cache, karena akun user BISA berubah (pocket baru dibuat, dsb — walau accountID sendiri tidak pernah berubah setelah dibuat, currency juga tidak berubah, jadi sebenarnya ini JUGA immutable per-key setelah dibuat pertama kali! Hanya keberadaannya yang bisa berubah — pocket baru muncul). Karena itu, gunakan cache **positive-only, tanpa TTL**, TAPI invalidasi eksplisit dipanggil dari `provision.Service.CreatePocket`/`CreateUserAccounts` setelah insert sukses (hapus entry cache untuk key yang baru dibuat supaya lookup berikutnya — kalau sebelumnya sempat cache "not found" — benar; kalau tidak pernah cache negative result, tidak perlu invalidasi sama sekali, LEBIH SIMPEL: **jangan cache hasil "not found"**, hanya cache hasil sukses. Maka tidak perlu invalidasi apa pun).
   Gunakan `sync.Map` sederhana di `accountRepo`, key `fmt.Sprintf("%s:%s:%s", ownerID, accountType, pocketCode)`.
3. Ukuran cache: karena hanya positive-cache tanpa eviction, di skala sangat besar (jutaan user aktif) ini bisa jadi unbounded memory growth. Untuk MVP/skala kecil (sesuai konteks resource-terbatas), ini diterima — tapi WAJIB catat sebagai TODO/limitasi di komentar kode: "unbounded cache; add LRU eviction (mis. `hashicorp/golang-lru`) before scaling past ~1M distinct accounts touched".

### Test yang wajib ditulis
- Unit test: `GetSystemAccountID` dipanggil 2x dengan qualifier sama → hanya 1 query ke DB (assert lewat sqlmock expectation count).
- Unit test: `GetAccountID` untuk user yang sama 2x → 1 query.
- Integration test: setelah `CreatePocket`, `GetAccountID` untuk pocket baru langsung ketemu tanpa restart proses (cache tidak menyimpan "not found" secara keliru).

### DoD
- [ ] `make test` hijau.
- [ ] Query count untuk ResolveAccounts pada posting berulang turun signifikan (dibuktikan lewat test assertion di atas).
- [ ] Limitasi unbounded cache didokumentasikan.

---

## T4 — UUIDv7 untuk ID insert-heavy

**Masalah** (09 §C16): `uuid.New()` (v4 random) dipakai untuk `ledger_transactions.id`, `ledger_entries.id`, `outbox_events.id` → index locality buruk pada tabel yang tumbuh terus.

### Langkah
1. Cek versi `github.com/google/uuid` di `go.mod` — pastikan >= v1.6.0 (support `NewV7()`). Kalau kurang, `go get -u github.com/google/uuid`.
2. Ganti `uuid.New()` menjadi `uuid.Must(uuid.NewV7())` di titik pembuatan ID untuk:
   - `internal/ledger/service/handle/service.go` — `txID := uuid.New()` (di `execTransfer`).
   - `internal/ledger/repository/ledger_entry_repository.go` — `uuid.New()` per entry (sekarang di dalam T2, sesuaikan generator di situ).
   - `internal/ledger/repository/outbox_event_repository.go` — ID event kalau di-generate di Go (cek — kalau `InsertEvents` menerima ID sudah jadi dari `model.OutboxEvent`, cari sumbernya di processor `OutboxEvents()` methods dan ganti di situ).
3. **JANGAN** ganti `accounts.id` — akun dibuat jarang (bukan insert-heavy), dan mengubahnya berisiko menyentuh kode provisioning tanpa manfaat proporsional. Fokus HANYA pada tabel high-frequency-insert: `ledger_transactions`, `ledger_entries`, `outbox_events`.
4. Kolom tipe `UUID` di Postgres tidak berubah — v7 tetap 128-bit UUID standar, hanya susunan bit yang time-ordered. Tidak ada perubahan migrasi/schema diperlukan.

### Test yang wajib ditulis
- Unit test: ID yang dihasilkan berturut-turut untuk `txID`/`entry.ID` naik secara leksikografis (assert `id2.String() > id1.String()` untuk pembuatan berurutan dalam waktu singkat — v7 punya timestamp di prefix, jadi harus monoton non-decreasing dalam presisi milidetik; test dengan toleransi kalau dibuat dalam ms yang sama).
- Regression: semua test existing yang mengasumsikan `uuid.UUID` sebagai tipe (bukan format v4 spesifik) tetap hijau tanpa modifikasi.

### DoD
- [ ] `make test` hijau.
- [ ] `go test -tags=integration -race ./...` hijau — verifikasi index Postgres tidak error dengan ID v7 (harusnya transparan, tapi wajib dibuktikan).

---

## T5 — Timeout Postgres + tuning pool untuk box kecil

**Masalah** (09 §B1, §C18): tidak ada `statement_timeout`/`lock_timeout`/`idle_in_transaction_session_timeout`; pool default 25/25 terlalu besar untuk single small VPS.

### Langkah
1. **`internal/config/config.go`** (`DSN()`, sekitar baris 305-310): tambah parameter ke connection string atau — lebih portable — set via `SET` setelah connect, ATAU (paling clean dengan `pgx`) tambahkan ke `DSN()` sebagai query params: `?options=-c statement_timeout=5000 -c lock_timeout=2000 -c idle_in_transaction_session_timeout=10000`. Buat semuanya configurable via env dengan default:
   - `PG_STATEMENT_TIMEOUT_MS` default `5000` (5s) — cukup untuk posting transaction wajar, mencegah query nyasar menahan lock selamanya.
   - `PG_LOCK_TIMEOUT_MS` default `2000` (2s) — kalau `FOR UPDATE` tidak dapat lock dalam 2s, gagal cepat dengan error jelas alih-alih antre tanpa batas (penting sekali dengan T1: lock akun user sekarang harus dapat lock CEPAT karena tidak ada lagi kontensi dari akun sistem, tapi tetap butuh guard untuk kasus lain).
   - `PG_IDLE_IN_TX_TIMEOUT_MS` default `10000` (10s) — mencegah koneksi yang "lupa" commit/rollback (bug di kode, atau proses yang macet) menahan koneksi+lock selamanya.
2. Karena `sql.LevelReadCommitted` dipakai untuk posting tx (`service/handle/service.go:196`) — timeout ini berlaku per-transaksi (`SET LOCAL` semantics kalau di-set lewat opsi koneksi startup berlaku default session, cukup untuk kasus ini karena setiap request pakai koneksi dari pool secara singkat).
3. **Pool defaults** (`config.go:145-148`): turunkan default `MaxOpenConns` dari 25 → **10**, `MaxIdleConns` dari 25 → **5** (idle rendah menghemat memori Postgres saat traffic rendah, `MaxOpen` cukup untuk throughput moderate di 1 vCPU/1GB box — operator bisa override via env `POSTGRES_MAX_OPEN_CONNS`/`POSTGRES_MAX_IDLE_CONNS` yang sudah ada). Dokumentasikan formula sizing di komentar: `max_open ≈ (vCPU × 2) + effective_spindle_count`, untuk VPS kecil 1-2 vCPU nilai 10 masuk akal; sebutkan juga default Postgres `max_connections=100` di server harus dibagi antar semua service+worker+migrate tool.
4. **Context timeout di worker loops**: `internal/ledger/worker/outbox_relay.go` — pastikan setiap panggilan `ClaimPending`/`ClaimFailedForRetry`/`MarkPublished`/`MarkFailed`/`ReapStuck` dibungkus `context.WithTimeout(parentCtx, 5*time.Second)` per-call (bukan cuma mengandalkan parent ctx worker yang berumur panjang tanpa deadline) — cegah satu query nyangkut menahan goroutine worker selamanya.

### Test yang wajib ditulis
- Integration test: set `PG_LOCK_TIMEOUT_MS` kecil (mis. 100ms) di test env, buat 2 goroutine berebut lock akun user yang sama dengan sleep di tengah transaksi (kalau memungkinkan diuji, atau simulasikan dengan transaksi manual yang sengaja menahan lock lalu goroutine kedua harus gagal dengan error lock timeout dalam waktu yang diharapkan, bukan menggantung).
- Konfirmasi `go test -tags=integration -race ./...` tetap hijau dengan timeout baru terpasang (pastikan operasi normal tidak pernah kena timeout dalam kondisi test wajar — kalau ada test yang lambat karena testcontainers overhead, longgarkan timeout KHUSUS test env lewat env var terpisah, jangan longgarkan default production).

### DoD
- [ ] `make test`, integration test hijau.
- [ ] Statement/lock/idle timeout aktif dan documented di `.env.example`.
- [ ] Pool default diturunkan, override tetap berfungsi.
- [ ] Worker loop punya per-call context timeout.

---

## T6 — Gabungkan gauge query outbox

**Masalah** (09 §C19, minor): `internal/ledger/worker/outbox_relay.go:184-208` memanggil `CountByStatus` dua kali (pending, dead) tiap 15 detik.

### Langkah
1. **`internal/ledger/repository/outbox_event_repository.go`**: ganti/tambah method `CountAllStatuses(ctx) (map[string]int, error)` — satu query `SELECT status, COUNT(*) FROM outbox_events GROUP BY status`.
2. `gaugeLoop` di `outbox_relay.go` pakai method baru, set kedua gauge dari satu hasil map.

### DoD
- [ ] `make test` hijau. Minor, tidak butuh integration test khusus (perubahan read-only, low-risk) — cukup regression test unit yang sudah ada untuk `outbox_relay_test.go`.

---

## Urutan Pengerjaan
T5 (timeout/pool) bisa duluan, tidak bergantung apa pun — kerjakan lebih dulu supaya test-test berikutnya sudah berjalan dengan guard timeout aktif. Lalu **T1 wajib sebelum T2** (T2 mengasumsikan `InsertEntries` menerima balance gabungan user+sistem dari hasil T1). T3, T4, T6 independen, kerjakan kapan saja setelah T1.

## Verifikasi Akhir Fase 2b
```bash
go build ./...
make lint
make test
go test -tags=integration -race ./...   # wajib Docker aktif — termasuk test concurrency baru T1
```
Smoke test manual: ulangi skenario di [10](10-phase2a-security-gating.md) DoD (money_in, transfer_p2p, money_out via HTTP asli) + jalankan `fn_verify_ledger_balance()`/`v_account_balance_audit` di akhir — harus tetap 0 diskrepansi.
