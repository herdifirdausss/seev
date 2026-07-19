# 18 — Phase 3b: Multi-Currency (S2)

Prasyarat: 14 selesai (✅ — payload event sudah membawa currency dengan benar, prasyarat K-S S2). Keputusan desain: [13 K-S](13-p1-backlog-review.md) butir S2. Kerjakan **berurutan T1 → T2 → T3** — T2 (lookup per-currency) butuh registry T1; T3 (FX) butuh akun sistem per-currency dari T2.

**Kontrak wire yang DIKUNCI di dokumen ini (klarifikasi atas K-S S2, bukan re-litigasi)**: amount di API dan di DB **tetap minor-unit integer untuk SEMUA currency** (IDR kirim rupiah karena exponent 0; USD kirim sen). `minor_unit` per currency TIDAK mengubah aturan integral-amount — aturan itu tetap "harus bilangan bulat" apapun currencynya. `minor_unit` dipakai untuk: (a) konversi tampilan major-unit di statement/report, (b) validasi currency terdaftar, (c) perhitungan FX. Alternatif "API menerima major-unit desimal per exponent" DITOLAK: mengubah kontrak wire yang sudah dipakai, dan menciptakan dua representasi uang di satu sistem — sumber bug pembulatan klasik.

Aturan verifikasi 09 berlaku penuh: T1/T2 menyentuh skema + jalur posting → integration test + smoke test wajib.

---

## T1 — Registry currency: tabel + kode satu sumber (08 S2 butir 1)

**Masalah**: `IDR` di-hardcode di `internal/ledger/service/provision/provision.go:34` (`supportedCurrency`), `pkg/currency/currency.go` hanya berisi IDR, dan tidak ada rujukan DB untuk currency yang sah.

### Langkah
1. Migrasi `000011_currencies.up.sql` (+down):
   ```sql
   CREATE TABLE currencies (
       code       CHAR(3)     PRIMARY KEY,   -- ISO 4217 alpha
       minor_unit SMALLINT    NOT NULL CHECK (minor_unit BETWEEN 0 AND 4),
       enabled    BOOLEAN     NOT NULL DEFAULT true,
       created_at TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   INSERT INTO currencies (code, minor_unit) VALUES ('IDR', 0), ('USD', 2);
   ```
   Grant+RLS di migrasi yang sama (pola 000009/000010): `app_service` SELECT+INSERT+UPDATE, `app_readonly` SELECT, ENABLE+FORCE+policy. TIDAK ada FK dari `accounts.currency`/`ledger_transactions.currency` ke tabel ini (kolom lama CHAR(3) tanpa FK — menambah FK = full table rewrite + lock di tabel insert-heavy; validasi cukup di application layer, konsisten keputusan "owner_id tanpa FK" di 000001).
2. `pkg/currency` di-refactor jadi registry yang bisa diisi runtime (tetap TANPA import `internal/` — aturan PROJECT_GUIDE.md):
   - Pertahankan master data in-code sebagai **fallback/bootstrap** (IDR), tambah `func Load(list []Currency)` yang mengganti isi map secara atomic (`sync/atomic.Pointer` ke map immutable — JANGAN mutasi map yang sedang dibaca).
   - `func MinorUnit(code string) (int16, bool)`, `func IsValid(code string) bool`, `func ToMajor(minor decimal.Decimal, code string) decimal.Decimal` (pembagi `10^minor_unit`, untuk display/report SAJA — tidak pernah dipakai di jalur posting).
3. Loader di ledger module: `repository` baru method `ListCurrencies(ctx) ([]pkg-currency.Currency-shape, error)` di `AccountRepository` ATAU repository kecil terpisah `CurrencyRepository` (pilih terpisah — `AccountRepository` sudah besar). `ledger.NewModule` memanggil loader sekali saat startup → `currency.Load(...)`; gagal load = fatal (fail-fast, konsisten pola config). TIDAK ada refresh berkala — menambah currency = INSERT + restart/deploy; currency bukan konfigurasi panas.
4. Provisioning lepas hardcode: `provision.go` ganti `currency != supportedCurrency` menjadi `!currency.IsValid(...)`. Pesan error menyebut currency yang didukung dari registry.
5. Transport: validasi `currency` di endpoint yang menerimanya (saat ini provisioning; `POST /transactions` tidak menerima currency — currency diresolusi dari akun, JANGAN ubah itu).

### Test wajib
- Unit `pkg/currency`: Load atomic (race test dua goroutine Load+MinorUnit), ToMajor per exponent (0 dan 2), IsValid.
- Integration: startup module dengan tabel berisi IDR+USD → provisioning USD sukses; currency di luar tabel → 400.
- Migrasi up+down.

### DoD
- [x] `grep -rn "\"IDR\"" internal/ledger/service/provision/` = kosong (tidak ada hardcode tersisa di jalur provisioning).
- [x] Registry terbukti dipakai (bukan dua sumber kebenaran yang bisa menyimpang): `supportedCurrency` const dihapus.

### Hasil

Dibangun sesuai spesifikasi, tanpa penyimpangan dari Langkah 1-5.

- **Migrasi `000011_currencies.up.sql`/`.down.sql`**: tabel `currencies(code, minor_unit, enabled, created_at)`, seed IDR(0)+USD(2), grant+RLS pola 000009/000010. Tidak ada FK dari `accounts.currency` (sesuai keputusan — tetap application-layer validation).
- **`pkg/currency`**: registry `atomic.Pointer[map[string]Currency]`, `Load()` mengganti map secara atomic (bukan mutasi in-place), bootstrap fallback IDR via `init()`. `IsValid`/`MinorUnit`/`ToMajor` diimplementasikan persis sesuai spek (ToMajor hanya untuk display, tidak pernah dipakai di jalur posting).
- **`internal/ledger/repository/currency_repository.go`** (baru, terpisah dari `AccountRepository` sesuai keputusan): `CurrencyRepository.ListEnabled(ctx)`.
- **`ledger.Module.LoadCurrencies(ctx)`**: dipanggil sekali di `cmd/server/main.go` sebelum `StartWorkers`; gagal load (termasuk tabel kosong) = fatal (`os.Exit(1)`), konsisten fail-fast.
- **`provision.go`**: `supportedCurrency` const dihapus, kedua fungsi (`CreateUserAccounts`, `CreatePocket`) memvalidasi via `currencyreg.IsValid(currency)` (aliased import untuk menghindari shadowing parameter `currency string`).

**Bug ditemukan & diperbaiki selama implementasi (di luar scope T1 langsung, tapi wajib dicatat)**:
1. Compile error `minor.Shift(-exp)` — `exp` bertipe `int16`, `Shift` butuh `int32`; fix `Shift(-int32(exp))`.
2. Import collision: alias `currencyreg` untuk `pkg/currency` di `provision.go` karena kedua fungsi punya parameter bernama `currency`.

**Test wajib — semua terpenuhi**:
- Unit `pkg/currency`: 7 test (bootstrap, Load replace, MinorUnit known/unknown, ToMajor exponent 0 & 2, ToMajor currency tidak dikenal, race Load+read concurrent).
- Integration: `TestSchemaContract_CurrencyRepository_ListEnabled`, `TestSchemaContract_LoadCurrencies_PopulatesRuntimeRegistry`, dan `TestSchemaContract_Provisioning_RespectsLoadedCurrencyRegistry` (end-to-end: load registry dari tabel real → provisioning USD sukses; currency `"XYZ"` di luar tabel → `apperror.ErrValidation`).
- Migrasi 000011: siklus up → down → up diverifikasi manual terhadap container Postgres sekali pakai — down membuang tabel bersih, up mengembalikan seed IDR+USD identik.

**Verifikasi penuh**: `go build ./...` bersih, `go vet ./...` bersih, `go test -race -count=1 ./...` semua PASS, `go test -tags=integration -race -count=1 ./...` semua PASS (termasuk `internal/ledger` 117s, `internal/policy` 18.9s). Mock `internal/ledger/repository/currency_repository_mock.go` berhasil digenerasi via `mockgen` (sempat terblokir tool-availability sementara, sudah pulih dan dikonfirmasi ter-generate dengan benar).

---

## T2 — Lookup akun sistem per-currency (08 S2 butir 3, TODO 05-1b.1)

**Masalah**: `GetSystemAccountID` (`internal/ledger/repository/account_repository.go:137-145`) tidak memfilter currency — TODO eksplisit ditinggal di sana. Dengan >1 currency, `settlement[bca]` IDR dan USD adalah dua akun berbeda; lookup tanpa filter mengembalikan salah satu secara arbitrer → **entri currency campur = korupsi data**.

### Langkah
1. Ubah signature: `GetSystemAccountID(ctx, accountType, qualifier, currency string) (uuid.UUID, error)`. Cache key di `systemAccountCache` ikut membawa currency.
2. Update 18 call-site di `internal/ledger/processors/*.go`. Masalah urutan: banyak processor me-resolve akun sistem SEBELUM tahu currency user (mis. `money_in` resolve settlement dulu, lalu `GetAccountCurrency(cashID)`). **Balik urutannya di setiap processor**: resolve akun user dulu → `GetAccountCurrency` → baru resolve akun sistem dengan currency itu. Audit satu per satu (pola audit 22-processor di 14-T1); processor tanpa akun user (mis. `adjustment_suspense_*` — gateway only) memakai currency dari... **keputusan**: suspense/settlement/fee per gateway per currency; `adjustment_suspense_*` menerima metadata `currency` opsional default `IDR` (satu-satunya tempat default IDR boleh tersisa, dengan komentar kenapa).
3. Seed akun sistem USD: migrasi `000012_seed_usd_system_accounts.up.sql` — baris settlement/fee/escrow/adjustment/chargeback/confiscated/suspense untuk USD, pola persis 000002+000008 (ID UUID literal berurutan lanjutan). Down = DELETE baris itu.
4. `fn_verify_ledger_balance`/verifier TIDAK berubah (agregat per transaction_id, currency-agnostic — entri satu transaksi selalu satu currency karena `validateAccounts` sudah menolak currency mismatch antar akun; assertion ini yang menjaga).
5. `feepolicy` (`internal/ledger/feepolicy/feepolicy.go`): tabel fee per (type, gateway) saat ini implisit IDR — tambah dimensi currency ke key resolusi, fee USD dikonfigurasi terpisah; kombinasi tanpa entri = tanpa fee (perilaku existing).

### Test wajib
- Integration: provisioning user USD → `money_in` USD via settlement[bca] USD → saldo benar; `transfer_p2p` antara user IDR→user USD DITOLAK `ErrCurrencyMismatch` (guard existing di `validateAccounts` — test membuktikan tetap bekerja end-to-end).
- Integration: dua posting paralel IDR dan USD gateway sama → masing-masing menyentuh akun settlement yang benar (assert per account_id).
- Unit: cache key per-currency tidak saling menimpa.

### DoD
- [x] TODO di `account_repository.go:143-145` dihapus (bukan sekadar dikerjakan — komentarnya diganti penjelasan kontrak baru).
- [x] Audit tabel processor × currency-resolution di PR description (pola 14-T1).

### Hasil

Dibangun sesuai spesifikasi Langkah 1-5, dengan satu perluasan scope yang dipertimbangkan sendiri (Langkah 5's currency dimension memerlukan sumber currency di transport layer — lihat catatan di bawah).

**Langkah 1 — `GetSystemAccountID` signature**: `GetSystemAccountID(ctx, accountType, qualifier, currency string)`; cache key `systemAccountCache` sekarang `"type:qualifier:currency"`; query SQL menambah `AND currency = $3`. Mock diregenerasi via `mockgen` (sempat terblokir tool-availability, pulih di sesi ini).

**Langkah 2 — audit 14 processor** (tabel di bawah). Pola: *reorder* = urutan resolve dibalik (akun user/currency dulu, baru akun sistem); *pass-through* = urutan sudah benar dari 14-T1, tinggal tambah parameter `currency`.

| Processor | Perubahan | Catatan |
|---|---|---|
| `money_in` | reorder | dulu resolve `settlement[gateway]` SEBELUM tahu currency user — dibalik: dest/pocket → currency → settlement |
| `money_out` | reorder | sama, arah sebaliknya (src → currency → settlement) |
| `fee_collect` | pass-through | urutan sudah benar |
| `chargeback` | reorder | cash → currency → chargeback[network] |
| `escrow_hold` | pass-through | qualifier escrow SUDAH = currency (lihat catatan desain) |
| `escrow_release` | pass-through | currency dari merchant account, urutan sudah benar |
| `escrow_refund` | pass-through | urutan sudah benar |
| `withdraw_settle` | reorder | hold → currency → settlement |
| `withdraw_pending_settle` | reorder | pending → currency → settlement |
| `freeze_confiscate` | reorder | frozen → currency → confiscated |
| `adjustment_credit` | reorder | dulu resolve adjustment account DULUAN tanpa currency — dibalik: cash → currency → adjustment |
| `adjustment_debit` | reorder | sama |
| `adjustment_suspense_credit` | metadata currency | TIDAK ada akun user (system-to-system) — pakai metadata `currency` opsional, default `"IDR"` (satu-satunya default IDR yang tersisa di codebase, dengan komentar kenapa) |
| `adjustment_suspense_debit` | metadata currency | sama |
| `transfer_p2p` | pass-through | urutan sudah benar dari 14-T1 |
| `withdraw_initiate` | pass-through | urutan sudah benar |

`resolveInlineFee` (shared helper dipakai 4 processor: money_in/money_out/escrow_hold/escrow_release/transfer_p2p/withdraw_initiate) menerima parameter `currency` baru — fee[gateway] sekarang di-lookup per currency juga.

**Langkah 3 — seed USD**: `migrations/000012_seed_usd_system_accounts.up.sql`/`.down.sql`, pola persis 000002+000008 (ID UUID literal `...013` s.d. `...024`, `allow_negative` sama persis per type). Diverifikasi manual: up bersih, down→up cycle mengembalikan 12 baris identik, `allow_negative` per type cocok dengan pool IDR.

**Langkah 4 — `fn_verify_ledger_balance`**: tidak diubah, sesuai keputusan (agregat per-transaction currency-agnostic, dijaga oleh `validateAccounts` yang menolak currency mismatch antar akun dalam satu transaksi — dibuktikan oleh `TestSchemaContract_MultiCurrency_TransferP2P_CrossCurrencyRejected`).

**Langkah 5 — feepolicy currency dimension**: `feepolicy.Policy.Resolve` sekarang `Resolve(txType, gateway, currency string, amount) (fee, feeGateway, ok)`; rule key `"<type>:<gateway>:<currency>"`. Transport layer perlu currency SEBELUM `ResolveAccounts` berjalan (fee dihitung di `buildMetadata`, jauh sebelum processor mana pun dipanggil) — currency tidak tersedia gratis di titik itu, jadi ditambahkan `Service.GetUserCurrency(ctx, userID, pocketCode) (string, error)` (delegasi tipis ke `AccountRepository`, sudah cache-backed dari 11-T3) dan dipanggil di `buildMetadata` sebelum `feePolicy.Resolve`. Best-effort: kegagalan resolve currency di titik ini TIDAK memblokir request (fallback `currency=""` → `Resolve` tidak menemukan rule → sama seperti "tidak ada fee dikonfigurasi") karena error currency yang sesungguhnya akan muncul lagi tak lama kemudian dari `ResolveAccounts` dengan pesan yang lebih tepat.

**Test wajib — semua terpenuhi**:
- Integration: `TestSchemaContract_MultiCurrency_MoneyIn_UsesCorrectSettlementPool` (provisioning USD → money_in via settlement[bca] USD → saldo benar, dan pool IDR terbukti TIDAK tersentuh).
- Integration: `TestSchemaContract_MultiCurrency_TransferP2P_CrossCurrencyRejected` (user IDR → user USD transfer_p2p ditolak `ErrCurrencyMismatch`, nol transaksi tercatat).
- Integration: `TestSchemaContract_MultiCurrency_ParallelIDRAndUSD_HitDistinctSettlementAccounts` (money_in IDR dan USD, gateway sama "bca" → masing-masing menyentuh `settlement[bca][IDR]`/`settlement[bca][USD]` yang berbeda, diverifikasi per account_id).
- Unit: `TestGetSystemAccountID_DifferentCurrency_MissesCache` (cache key per-currency tidak saling menimpa, dua currency sama gateway → dua akun berbeda, keduanya lalu cache-served).
- Unit: `TestResolve_DifferentCurrency_NoFee` (feepolicy rule table tidak lintas-currency).

**Verifikasi penuh**: `go build ./...` bersih, `go vet ./...` bersih (termasuk `-tags=integration`), `go test -race -count=1 ./...` semua PASS, `go test -tags=integration -race -count=1 ./...` semua PASS kecuali satu flake pra-existing tak terkait (`TestPolicy_Engine_CacheExpiresAndRefetchesFromRealDB`, timing-sensitive terhadap load paralel — lolos saat dijalankan sendirian). Migrasi 000012 diverifikasi up→down→up terhadap container sekali pakai. Chaos scenario 1 (kill -9 mid-posting, 40 concurrent transfer_p2p) dan scenario 4 (Redis down) dijalankan ulang karena pipeline posting tersentuh di setiap processor — keduanya PASS, nol transaksi unbalanced, nol saldo inkonsisten.

---

## T3 — FX orchestration (08 S2 butir 2) — DESAIN + AKUN SAJA, EKSEKUSI OPSIONAL

**Kontrak K-S S2**: FX **bukan fitur ledger** — konversi adalah orchestration dua transaksi ledger biasa. Task ini menyiapkan primitives; layanan FX penuh (quote engine, rate feed) di luar scope dokumen ini.

### Langkah
1. Akun sistem `fx_conversion` per pasangan: tambah type `fx_conversion` ke CHECK `accounts_type_check` (pola penambahan `suspense` di 000008) + seed `system_qualifier='IDRUSD'` untuk kedua currency (satu akun IDR qualifier IDRUSD, satu akun USD qualifier IDRUSD) — `allow_negative=true` keduanya (posisi FX platform bisa dua arah). Migrasi `000013_fx_accounts.up.sql`.
2. Dua processor baru (registry pattern, murah — pola `adjustment_suspense_*` di 16-T2): `fx_out` (user.cash[ccy1] → fx_conversion[pair][ccy1]) dan `fx_in` (fx_conversion[pair][ccy2] → user.cash[ccy2]). Metadata wajib: `quote_id`, `rate`, `pair` — `ValidateCommand` menolak tanpa ketiganya; `rate` disimpan string desimal di metadata (audit trail), TIDAK dipakai aritmetika oleh processor (amount kedua kaki sudah dihitung orchestrator).
3. Kedua tipe masuk `directPostBlockedTypes`? **Tidak** — masuk daftar tipe internal-router-only biasa (bukan publik): orchestrator FX adalah caller internal tepercaya. Blokir dari `publicUserTypes` saja.
4. Idempotency: orchestrator memakai key deterministik `fx:<quote_id>:out` dan `fx:<quote_id>:in` — retry salah satu kaki aman; kaki kedua gagal permanen = posisi terbuka di akun fx_conversion yang TERLIHAT (saldo non-zero per pair) — prosedur ops di runbook singkat `docs/runbooks/fx-position.md` (cek saldo pair, keputusan manusia: retry kaki kedua atau reversal kaki pertama).
5. Invariant penting yang HARUS ditulis di doc comment kedua processor: satu transaksi ledger tetap satu currency (kaki IDR dan kaki USD adalah DUA transaksi) — `fn_verify_ledger_balance` per transaksi tetap bermakna; "balance" lintas-currency TIDAK dijaga ledger (itu tugas rekonsiliasi posisi FX, level orchestrator/finance).

### Test wajib
- Integration: fx_out + fx_in dengan quote_id sama → saldo user ccy1 turun, ccy2 naik, kedua akun fx_conversion bergerak benar, verifier bersih.
- Integration: retry fx_in dengan key sama → idempoten (satu tx).
- Integration: fx_in gagal (akun tujuan suspended) → posisi terbuka terlihat di saldo fx_conversion (assert non-zero) — dan reversal fx_out mengembalikan ke nol.

### DoD
- [x] Kedua processor terdaftar + teraudit source/destination (pola 14-T1).
- [x] Runbook posisi FX ada.
- [x] Tidak ada perubahan apapun di `service/handle/service.go` (pipeline posting tidak tahu FX — bukti arsitektur "FX bukan fitur ledger").

### Hasil

Dibangun sesuai spesifikasi Langkah 1-5, tanpa penyimpangan.

**Langkah 1 — akun `fx_conversion`**: `migrations/000013_fx_accounts.up.sql`/`.down.sql`. CHECK constraint `accounts_type_check` menambah `'fx_conversion'` (pola persis penambahan `suspense` di 000008). Seed dua akun (ID `...025` IDR, `...026` USD), keduanya `system_qualifier='IDRUSD'`, `allow_negative=true`. Diverifikasi manual: up→down→up cycle terhadap container sekali pakai — down MEMBUKTIKAN constraint aktif kembali (INSERT `fx_conversion` ditolak setelah down), up mengembalikan kedua baris identik.

**Langkah 2 — dua processor baru**: `internal/ledger/processors/fx_out.go` (`FxOut`, type `fx_out`) dan `fx_in.go` (`FxIn`, type `fx_in`), pola murah `adjustment_suspense_*` (16-T2). `ValidateCommand` menolak tanpa `quote_id`/`rate`/`pair`; `rate` disimpan verbatim di catatan entri (audit trail), tidak pernah dipakai aritmetika. `fx_out` punya `SufficientFundsValidator` pada `AccountIDs[0]` (user cash, biasanya tidak allow_negative); `fx_in` sengaja TIDAK — `fx_conversion` yang didebit di sana allow_negative=true, pola sama seperti `money_in` mendebit `settlement`.

**Langkah 3 — internal-router-only**: TIDAK ada perubahan ke `publicUserTypes`/`adminOnlyTypes`/`directPostBlockedTypes` di `transport/http.go` — `fx_out`/`fx_in` otomatis hanya postable lewat `NewInternalRouter` (`allowedTypes: nil`) karena tidak pernah dimasukkan ke `publicUserTypes`.

**Langkah 4 — idempotency key convention**: didemonstrasikan di test integrasi via `"fx:<quote_id>:out"`/`"fx:<quote_id>:in"` — bukan dipaksakan oleh kode (orchestrator penuh di luar scope dokumen ini), hanya konvensi yang divalidasi test.

**Langkah 5 — invariant doc comment**: ditulis lengkap di kedua processor (satu transaksi = satu currency; "balance" lintas-currency adalah tanggung jawab rekonsiliasi posisi FX di level orchestrator, bukan ledger).

**Runbook**: [`docs/runbooks/fx-position.md`](../runbooks/fx-position.md) — model dua-transaksi, cara mendeteksi posisi terbuka (query saldo `fx_conversion` per pair), dan prosedur keputusan manusia (retry kaki yang hilang vs reversal kaki yang sudah posting).

**Catatan desain yang ditemukan selama implementasi (bukan penyimpangan dari spek, tapi perlu didokumentasikan)**: `user.cash[ccy]` notation di spek T3 tidak literal berarti satu `owner_id` yang sama bisa punya cash account di dua currency berbeda — `AccountRepository.GetAccountID` tidak memfilter currency dan mengasumsikan SATU cash account aktif per user (indeks unik `accounts` sebenarnya MENGIZINKAN banyak currency per owner, tapi lookup query saat ini tidak disambiguasi). Ini adalah gap pra-existing di luar scope T3 (bukan diperkenalkan oleh FX), relevan untuk model "satu user, banyak dompet currency" di masa depan jika dibutuhkan — dicatat di sini, TIDAK diperbaiki (di luar 3 task T1-T3 dokumen ini). Test integrasi merepresentasikan kedua kaki FX sebagai dua identitas user berbeda (pola omnibus/settlement account per currency yang realistis), menghindari ambiguitas ini sepenuhnya.

**Test wajib — semua terpenuhi** (`internal/ledger/schema_contract_test.go`):
- `TestSchemaContract_FX_OutThenIn_MovesBothLegsCorrectly`: fx_out lalu fx_in quote_id sama → cash ccy1 turun, cash ccy2 naik, kedua akun fx_conversion bergerak benar (kredit IDR, debit USD), `fn_verify_ledger_balance` bersih.
- `TestSchemaContract_FX_In_RetrySameKey_Idempotent`: retry fx_in dengan idempotency key sama → satu transaksi, saldo tidak dobel.
- `TestSchemaContract_FX_InFails_OpenPositionVisible_ReversalCloses`: fx_out sukses, fx_in gagal (akun tujuan disuspend) → posisi fx_conversion IDR tetap non-zero (terbuka, terlihat); reversal terhadap fx_out mengembalikan posisi ke nol dan saldo user ke keadaan semula. (Catatan: error aktual adalah `ErrAccountNotFound`, bukan `ErrAccountSuspended` — `GetAccountID` memfilter `status='active'` di query-nya sendiri, jadi akun suspended tidak pernah "ditemukan lalu ditolak", langsung tidak terlihat oleh lookup. Hasil akhir sama persis dengan yang diminta test wajib: kaki gagal, tidak ada yang posting, posisi tetap terbuka.)
- Unit (`fx_test.go`, `resolved_accounts_test.go`): `ValidateCommand` menolak tanpa masing-masing dari quote_id/rate/pair (4 test × 2 processor), source/destination teraudit untuk kedua processor.

**Verifikasi penuh**: `go build ./...` bersih, `go vet ./...` bersih (termasuk `-tags=integration`), `go test -race -count=1 ./...` semua PASS, `go test -tags=integration -race -count=1 ./internal/ledger/...` semua PASS. Migrasi 000013 up→down→up diverifikasi (termasuk constraint aktif-kembali setelah down). Chaos scenario 1 (kill -9 mid-posting) dijalankan ulang setelah 26 processor terdaftar di registry yang sama — PASS, nol transaksi unbalanced.

---

## Verifikasi akhir

```bash
go build ./... && go vet ./... && go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all
```
Smoke test manual: provisioning user USD → money_in USD → statement menampilkan currency benar. Migrasi 000011–000013 up+down teruji. Setelah selesai: update DoD + "Hasil" di dokumen ini, status di [README.md](README.md), dan supersede note S2 di [08](08-phase-3-scale.md).

### Hasil verifikasi akhir (semua langkah di atas dijalankan ✅)

- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...` — bersih.
- `make test` (`go test -race -cover ./...`) — semua package PASS.
- `go test -tags=integration -race -count=1 ./...` (seluruh project, bukan hanya `internal/ledger`) — semua package PASS, termasuk `internal/policy` yang sebelumnya sempat flake sekali karena load paralel (lolos bersih di run terpisah maupun di run penuh kali ini).
- `./scripts/chaos-test.sh all` (ke-4 skenario: kill -9 mid-posting, broker down, Postgres restart mid-traffic, Redis down) — semua PASS, nol transaksi unbalanced, nol saldo inkonsisten, di atas registry 26 processor (termasuk `fx_out`/`fx_in` yang baru).
- Migrasi 000011 (`currencies`), 000012 (`seed_usd_system_accounts`), 000013 (`fx_accounts`) — masing-masing diverifikasi up→down→up terhadap container Postgres sekali pakai; down 000013 dibuktikan benar-benar mengembalikan CHECK constraint lama (INSERT `fx_conversion` ditolak setelah down).
- Smoke test manual end-to-end via HTTP (server nyata + docker-compose Postgres/Redis/RabbitMQ, port Postgres di-remap sementara ke 5433 karena native Postgres di mesin dev menempati 5432 — direvert setelah selesai): provisioning user USD → `POST /api/v1/ledger/transactions` (`money_in`, gateway `bca`) → `GET /api/v1/ledger/accounts/{id}/balance` menunjukkan `"currency":"USD"` → `GET /api/v1/ledger/accounts/{id}/statement` menunjukkan `"currency":"USD"` dengan entri yang benar. Dicatat sebagai temuan sampingan: `Service.ProvisionUser` belum punya HTTP route terekspos di mana pun (`internal/handler/router.go`) — provisioning saat ini hanya reachable via pemanggilan Go langsung; ini gap pra-existing di luar scope dokumen 18, bukan regresi dari task ini.

**Status: docs/plan/18 (Phase 3b — Multi-Currency) SELESAI — T1, T2, T3 semua ✅.**
