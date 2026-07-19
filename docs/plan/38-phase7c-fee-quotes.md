# 38 — Phase 7c: Fee Quote — Fee yang Dilihat User Dihormati

> Baca master reference di [36-phase7a-request-tracing.md](36-phase7a-request-tracing.md). Prasyarat: doc 37 selesai (execTransfer sudah bersih dari hook — konsumsi quote masuk ke jalur yang sederhana).

## Konteks

Hari ini fee dihitung server-side SAAT POSTING (`buildMetadata` → `feepolicy.Resolve`) dan payout menghitung fee SAAT SETTLE (`ResolveFee` gRPC) — user tidak pernah melihat fee sebelum berkomitmen, dan jika admin mengubah `fee_rules` di antara UI menampilkan fee dan transaksi diproses, user membayar fee yang belum pernah dia lihat. Tidak fair. Fase ini menambahkan **fee quote**: user minta quote → dapat `quote_id` + fee + masa berlaku → UI tampilkan → transaksi membawa `quote_id` → ledger menghormati fee quoted PERSIS atau menolak 422 (`QUOTE_EXPIRED`/`QUOTE_MISMATCH`) — TIDAK PERNAH diam-diam reprice. Jalur tanpa quote (internal/system flow) tetap = perilaku sekarang.

## T1 — Tabel `fee_quotes` (migrasi ledger 000021)

> Catatan: nomor digeser dari 000020 semula ke 000021 — doc 36 T5 mengambil
> 000020 untuk `ledger_transactions.request_id` setelah ditemukan
> `ledger_transactions` tidak punya kolom metadata JSONB generik seperti
> yang diasumsikan awal (lihat tabel keputusan terkunci master doc 36).

### Langkah
1. `migrations/ledger/000021_fee_quotes.up/down.sql`:
```sql
CREATE TABLE fee_quotes (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    transaction_type TEXT NOT NULL,
    gateway TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    fee_amount BIGINT NOT NULL CHECK (fee_amount >= 0 AND fee_amount < amount),
    fee_gateway TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    consumed_by_ref TEXT,          -- 'tx:<uuid>' | 'payout:<uuid>'
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_fee_quotes_user ON fee_quotes(user_id, created_at);
CREATE INDEX idx_fee_quotes_expiry ON fee_quotes(expires_at) WHERE consumed_at IS NULL;
```
   + grants + RLS pola house (doc 16 T3).

### Test wajib
- up→down→up bersih terhadap Postgres nyata; schema contract test diperbarui bila mengunci daftar tabel.

### DoD
- [x] Skema quote siap dengan CHECK yang mencegah fee ≥ amount sejak lahir.

### Hasil
- `migrations/ledger/000021_fee_quotes.up/down.sql` dibuat persis skema di
  Langkah (kolom, index, CHECK `fee_amount >= 0 AND fee_amount < amount`,
  CHECK `amount > 0`) + grants (`app_service` SELECT/INSERT/UPDATE,
  `app_readonly` SELECT) + RLS (`ENABLE`+`FORCE ROW LEVEL SECURITY`, policy
  `pol_all_service`/`pol_read_readonly`) mengikuti pola persis
  `migrations/ledger/000019_fee_rules` (doc 33 T1) — satu-satunya migrasi
  tabel baru dengan RLS sejak doc 16 T3 mengunci pola tsb.
  Tidak ada `updated_at`/trigger `fn_set_updated_at` — baris `fee_quotes`
  hanya di-INSERT sekali lalu di-UPDATE tepat sekali (konsumsi), tidak ada
  "edit ulang" yang butuh audit `updated_at`.
  `internal/ledger/schema_contract_test.go`'s
  `TestSchemaContract_AppReadonlyRole_CanReadEverythingElse` TIDAK diperbarui
  — daftar tabelnya bukan daftar LENGKAP seluruh tabel skema (tidak memuat
  `fee_rules` juga), jadi bukan kontrak yang "mengunci daftar tabel" sesuai
  syarat Test Wajib; tidak ada schema-contract test lain yang mengasumsikan
  jumlah/nama tabel tetap.
- Diverifikasi up→down→up terhadap `seev-postgres-1` yang sedang berjalan:
  `\d fee_quotes` menunjukkan seluruh kolom/index/CHECK/policy sesuai
  desain; setelah `migrate down` tabel hilang total
  (`Did not find any relation named "fee_quotes"`); setelah `migrate up`
  lagi tabel kembali dengan definisi identik.

## T2 — CreateQuote/ConsumeQuote di `internal/ledger/feepolicy`

### Langkah
1. `CreateQuote(ctx, userID, txType, gateway, currency, amount) (Quote, error)`: panggil `Resolve` existing (spesifisitas + clamp dipertahankan), insert row, `expires_at = now() + TTL` (env `FEE_QUOTE_TTL`, default 10m, wire di `internal/config` + `cmd/ledger-service`).
2. `ConsumeQuote(ctx, execer, quoteID, userID, txType, currency, amount, ref) (fee decimal.Decimal, feeGateway string, err error)`: SATU statement atomik —
   `UPDATE fee_quotes SET consumed_at = now(), consumed_by_ref = $ref WHERE id = $1 AND user_id = $2 AND consumed_at IS NULL AND expires_at > now() RETURNING transaction_type, gateway, currency, amount, fee_amount, fee_gateway`.
   0 row → sentinel `ErrQuoteExpired` (consumed/expired/not-found SENGAJA tak dibedakan ke client); row kembali tapi txType/currency/amount tidak cocok → `ErrQuoteMismatch`. Terima `execer` (interface query existing) supaya bisa dijalankan DI DALAM tx posting — rollback tx = un-consume otomatis, itu perilaku benar.
3. Single-use, mengikat amount EKSAK.

### Test wajib
- Unit + integration: happy path; expired → ErrQuoteExpired; konsumsi kedua → ErrQuoteExpired; amount selisih 1 → ErrQuoteMismatch; race dua goroutine konsumsi quote sama → tepat satu menang (test `-race`).

### DoD
- [x] Konsumsi quote atomik, single-use, tidak bisa dobel di bawah konkurensi.

### Hasil
- `internal/ledger/feepolicy/quote.go` (baru): `Quote` struct, `CreateQuote`,
  `ConsumeQuote`, sentinel `ErrQuoteExpired`/`ErrQuoteMismatch`,
  `DefaultQuoteTTL = 10m`.
  - `CreateQuote` memanggil `Resolve` existing PERSIS (spesifisitas +
    clamp dipertahankan, nol perubahan) lalu INSERT; `ttl <= 0` jatuh ke
    `DefaultQuoteTTL`. Quote tetap dibuat walau tidak ada rule cocok
    (`FeeAmount` nol) — "tidak ada fee" adalah fakta yang sah untuk di-quote.
  - **Koreksi desain terhadap Langkah #2 dokumen** (ditemukan saat menulis
    test T4-nya sendiri, Test Wajib T2/T4): SQL contoh di Langkah hanya
    memfilter `WHERE id, user_id, consumed_at IS NULL, expires_at > now()`
    lalu MENCOCOKKAN txType/currency/amount SETELAH `UPDATE...RETURNING` —
    tapi Test Wajib T4 eksplisit mensyaratkan "quote tidak berubah status
    (mismatch)" yaitu percobaan dengan amount/tipe salah TIDAK BOLEH
    membakar quote. Kedua persyaratan itu kontradiktif jika SQL hanya
    match pada (id, user_id): UPDATE tunggal yang match hanya di situ PASTI
    ikut menandai `consumed_at` bahkan saat txType/amount berbeda.
    **Keputusan**: `consumeQuoteQuery` memasukkan `transaction_type =
    $3 AND currency = $4 AND amount = $5` LANGSUNG ke `WHERE` UPDATE-nya —
    jadi UPDATE hanya benar-benar mengubah baris pada match PERSIS di
    kelima dimensi (id, user, type, currency, amount). Saat 0 baris
    ter-update, `classifyQuoteQuery` (SELECT terpisah, tanpa filter
    type/currency/amount) membedakan "baris masih ada & valid & belum
    consumed" (→ `ErrQuoteMismatch`, quote TIDAK berubah) dari "baris benar2
    hilang/expired/sudah consumed" (→ `ErrQuoteExpired`). Race jinak antara
    kedua statement (baris dikonsumsi caller lain tepat di antaranya) hanya
    memengaruhi PESAN error yang dipilih, bukan uang — UPDATE pertama tetap
    satu-satunya sumber kebenaran soal konsumsi.
  - `execer` interface lokal (`QueryRowContext` saja) — dipenuhi baik oleh
    `*sql.Tx` (dipanggil DI DALAM tx posting, T4) maupun
    `database.DatabaseSQL` (dipanggil di tx pendek terpisah milik payout,
    T5) tanpa perlu meng-thread `*sql.Tx` lintas gRPC boundary.
- `internal/config/config.go`: `LedgerConfig` mendapat field baru
  `FeeQuoteTTL time.Duration`, di-load dari env `FEE_QUOTE_TTL` (default
  10 menit, `parseDuration` existing). **Belum dipakai** di
  `cmd/ledger-service/main.go` — baru benar-benar disambungkan ke router di
  T3 saat endpoint `POST /fees/quote` dibuat (satu-satunya pemanggil
  `CreateQuote`); mencatatnya di sini sesuai urutan Langkah dokumen T2 poin
  1, tapi wiring aktual menunggu T3 supaya tidak ada parameter yang belum
  terpakai.
- Test wajib:
  - Unit (`quote_test.go`, sqlmock): happy path create+consume, quote tanpa
    rule cocok (fee 0 tetap ter-quote), consume sukses, 0-row→
    `ErrQuoteExpired` (lewat `classifyQuoteQuery` juga 0 baris), 0-row+ada
    baris valid→`ErrQuoteMismatch`.
  - Integration (`quote_integration_test.go`, testcontainers Postgres real,
    `-race`): happy path create→consume; expired (di-backdate manual via
    SQL — `CreateQuote` sendiri men-treat `ttl<=0` sebagai "pakai default",
    BUKAN "buat langsung expired", jadi test memundurkan `expires_at`
    langsung, bukan lewat parameter `CreateQuote`); consume kedua atas
    quote yang sama → `ErrQuoteExpired` (dobel-konsumsi tak dibedakan dari
    expired, sesuai desain); amount salah satu digit →
    `ErrQuoteMismatch` DAN diverifikasi `consumed_at IS NULL` tetap, LALU
    quote yang SAMA dengan amount BENAR tetap berhasil dikonsumsi
    (membuktikan mismatch benar-benar tidak membakar); 10 goroutine
    konkuren konsumsi quote yang sama (`go test -race`) → tepat 1 menang, 9
    sisanya `ErrQuoteExpired`.
- Verifikasi: `go build`/`go vet` (default + `-tags=integration`) bersih;
  `gofmt -l` bersih; seluruh test di atas hijau (unit ~0.6s, integration
  dengan `-race` ~15s total untuk 5 test termasuk container Postgres
  per-test).

## T3 — Endpoint quote publik (lewat proxy existing)

### Langkah
1. `internal/ledger/transport/http.go`: `POST /fees/quote` di router PUBLIC user (otomatis reachable sebagai `/api/v1/ledger/fees/quote` via proxy gateway — TANPA perubahan gateway). Body `{transaction_type, amount, currency?, gateway?}`; `user_id` dari JWT. Response `{quote_id, amount, fee_amount, fee_gateway, total_debit, currency, expires_at}` (amount decimal string, konsisten API existing).
2. Topup (`money_in`) boleh di-quote — hasil fee 0 selama tidak ada rule (fee topup = deferral resmi).

### Test wajib
- Transport: quote dibuat untuk user JWT (bukan user body); validasi tipe/amount; TTL benar.

### DoD
- [x] UI bisa menampilkan fee pasti sebelum user berkomitmen.

### Hasil
- `internal/ledger/transport/dto.go`: `quoteRequest`/`quoteResponse` +
  `toQuoteResponse`. Response: `quote_id, amount, fee_amount, fee_gateway,
  total_debit, currency, expires_at` — `total_debit = amount + fee_amount`
  (decimal string, konsisten konvensi API existing).
- `internal/ledger/transport/http.go`:
  - `mux()` mendaftarkan `POST /fees/quote` HANYA saat `h.allowedTypes !=
    nil` (public router) — pola kebalikan dari blok `if h.allowedTypes ==
    nil { ...internal-only... }` yang sudah ada. Otomatis reachable sebagai
    `/api/v1/ledger/fees/quote` lewat proxy gateway existing, NOL perubahan
    gateway (sesuai Langkah #1).
  - `createQuote` handler: `userID` SELALU dari JWT (`currentUserID`),
    PERSIS pola `postTransaction` — body tidak pernah membawa user_id.
    Validasi: `transaction_type` harus terdaftar (reuse interface
    `transactionTypeValidator` yang sama dipakai `validateFeeRuleRequest`),
    `gateway` (jika diisi) harus salah satu `constant.ValidGateways`,
    `amount` lewat `decimalFromString` existing (menolak fraksi + non-decimal
    dengan pesan yang sama seperti `postTransaction`), harus positif.
    `currency` opsional — kosong maka di-resolve dari `GetUserCurrency`
    (fallback silent ke "" bila gagal, sama seperti `buildMetadata`, tidak
    pernah memblokir quote hanya karena currency lookup gagal).
  - **money_in sengaja BISA di-quote** di endpoint ini walau `money_in`
    BUKAN anggota `publicUserTypes` (tidak bisa di-POST langsung lewat
    router publik) — sesuai Langkah #2: quote tidak pernah memindahkan
    uang, jadi mengizinkannya di-quote tidak membuka kembali lubang yang
    dijaga `allowedTypes` untuk POST /transactions.
  - `handler` struct + `NewRouterWithFraud`/`NewRouterWithOptions` mendapat
    parameter baru `feeQuoteTTL time.Duration` (di ujung, mengikuti
    konvensi append-only yang sudah dipakai T3/T4/T5 doc 37) — `<=0` jatuh
    ke `feepolicy.DefaultQuoteTTL` (fallback ganda: transport meneruskan
    nilai apa adanya, `feepolicy.CreateQuote` sendiri yang menerapkan
    default).
  - `ledger.NewModule` (di `internal/ledger/ledger.go`) mendapat parameter
    baru `feeQuoteTTL time.Duration` di ujung, diteruskan ke
    `transport.NewRouterWithFraud`. `cmd/ledger-service/main.go` kini benar2
    menyambungkan `cfg.Ledger.FeeQuoteTTL` (env `FEE_QUOTE_TTL`, dimuat di
    T2) — menyelesaikan wiring yang di T2 sengaja ditunda.
  - 4 call site `ledger.NewModule`/`NewRouterWithFraud` lain (
    `internal/testutil/ledger.go`, `internal/notify/notify_integration_test.go`,
    `internal/ledger/grpcserver/server_integration_test.go`,
    `internal/ledger/transport/http_test.go` ×2) diberi argumen tambahan
    `0`/`0` — seluruhnya berarti "pakai default 10 menit", tidak mengubah
    perilaku test manapun.
- Test wajib (`internal/ledger/transport/http_test.go`, sqlmock lewat
  `*feepolicy.Policy` real, bukan interface tiruan — sama pola
  `newFeeAdminRouter` existing):
  - `TestCreateQuote_Success_UsesJWTUserID_NotBody` — INSERT `fee_quotes`
    diverifikasi memakai `user_id` dari JWT (bukan body, body memang tidak
    punya field user_id sama sekali); response `fee_amount`/`total_debit`
    benar; `expires_at` dalam rentang `now + DefaultQuoteTTL ± 5s`.
  - `TestCreateQuote_UnknownTransactionType_400`,
    `TestCreateQuote_NonIntegralAmount_400`, `TestCreateQuote_NoToken_401`.
  - `TestCreateQuote_MoneyIn_Quotable` — membuktikan poin "money_in boleh
    di-quote" di atas secara konkret (201, bukan 403/400).
- Verifikasi: `go build`/`go vet` (default +
  `-tags=integration`) bersih; `gofmt -l` bersih; `make lint` bersih;
  `make test` (seluruh package, termasuk `boundary_test.go`) hijau —
  paket `internal/ledger/transport` sendiri: 5 test baru + seluruh test
  lama tetap hijau (regresi nol terhadap perubahan signature
  `NewRouterWithFraud`/`ledger.NewModule`).

## T4 — Posting P2P menghormati quote

### Langkah
1. DTO transfer publik (`internal/ledger/transport/dto.go`) + field opsional `quote_id`. Quote_id mengalir ke command service lewat FIELD TYPED (BUKAN metadata — `buildMetadata` men-strip metadata client; gotcha #5 master).
2. Ada `quote_id` → transport SKIP resolve-fee-at-posting; `execTransfer` panggil `ConsumeQuote` SEGERA setelah buka tx dan SEBELUM `LockBalances` (fail fast tanpa memegang lock; UPDATE satu row), ref `tx:<tx_id>`; `fee_amount`/`fee_gateway` hasil konsumsi mendorong fee leg PERSIS — tidak pernah re-resolve.
3. Mapping error: `ErrQuoteExpired` → 422 `QUOTE_EXPIRED`; `ErrQuoteMismatch` → 422 `QUOTE_MISMATCH` — sentinel `apperror` baru + mapping transport + `pkg/ledgererr` (paritas gRPC untuk caller internal masa depan).
4. Tanpa `quote_id` → perilaku sekarang PERSIS (resolve at posting; internal router tidak berubah).
5. Idempotency: lookup idempotency key tetap SEBELUM konsumsi quote — replay request sukses mengembalikan tx asli walau quote sudah consumed (verifikasi urutan existing: lookup pre-tx, pertahankan).

### Test wajib
- KUNCI: posting ber-quote membayar fee PERSIS sesuai quote walau `fee_rules` diubah admin di antara quote dan post.
- Expired/mismatch → 422, TIDAK ada tx, TIDAK ada entries, quote tidak berubah status (mismatch) / tetap consumed=null (expired).
- Replay idempoten setelah sukses → tx asli.
- Konkurensi: satu quote dipakai dua transfer berbeda → tepat satu sukses.
- `go vet` dua tag (command berubah).

### DoD
- [x] "Fee yang dilihat user = fee yang dibayar" terbukti di level `ledger_entries`.

### Hasil
- `internal/ledger/processors/processors.go`: `Command` mendapat field baru
  `QuoteID string` — TYPED, bukan lewat `Metadata` (gotcha #5 master doc 36:
  `buildMetadata` men-strip key metadata tak dikenal di router publik,
  sehingga quote_id lewat metadata akan lenyap sebelum sempat dibaca).
- `internal/ledger/transport/dto.go`: `postTransactionRequest` mendapat
  `QuoteID string \`json:"quote_id,omitempty"\`` diteruskan ke
  `cmd.QuoteID` di `postTransaction`.
- `internal/ledger/transport/metadata.go`: `buildMetadata` SKIP
  `h.feePolicy.Resolve(...)` sepenuhnya saat `req.QuoteID != ""` — mencegah
  fee hasil resolve-baru menimpa fee yang sudah di-quote.
- **Temuan arsitektur penting (ditemukan lewat kegagalan test KUNCI saat
  pertama ditulis — saldo akun fee tetap 0 padahal `ConsumeQuote` sukses
  mengembalikan fee=500)**: setiap processor (`transfer_p2p` dkk.)
  menentukan APAKAH akun `fee[gateway]` dimasukkan ke `AccountIDs` di dalam
  `ResolveAccounts` — yang dipanggil `Handle()` SEBELUM tx dibuka, jauh
  SEBELUM `execTransfer` (dan konsumsi quote di dalamnya) pernah berjalan.
  `ResolveAccounts` memutuskan ini murni dari `cmd.Metadata["fee_amount"]`
  (`resolveInlineFee`) — kalau kosong (karena transport sengaja skip
  resolve saat ada quote_id), akun fee TIDAK PERNAH masuk `AccountIDs`, dan
  `BuildEntries`'s `hasFee(cmd)` mensyaratkan `len(cmd.AccountIDs) >= 3` —
  jadi menstempel `fee_amount` ke metadata DI DALAM `execTransfer` (setelah
  `ResolveAccounts` sudah lewat) datang TERLAMBAT: fee leg tidak pernah
  terbentuk sama sekali, walau `ConsumeQuote` sendiri sukses.
  **Solusi**: `internal/ledger/feepolicy/quote.go` mendapat method BARU
  `GetQuote(ctx, quoteID, userID) (Quote, error)` — PEEK read-only (TANPA
  efek samping, tidak menandai consumed) yang mengembalikan `ErrQuoteExpired`
  dengan semantik sama seperti `ConsumeQuote` bila tidak ada baris valid.
  `internal/ledger/service/handle/service.go`'s `Handle()` (BUKAN
  `execTransfer`) memanggil `GetQuote` ini SEBELUM `processor.ResolveAccounts`
  dipanggil, dan best-effort menstempel `cmd.Metadata["fee_amount"/
  "fee_gateway"]` dari hasil peek — HANYA supaya `ResolveAccounts` tahu akun
  fee perlu disertakan. `fee_amount` sebuah quote IMMUTABLE sejak dibuat
  (perubahan `fee_rules` setelahnya tidak pernah menyentuh baris quote yang
  sudah ada — itulah esensi fitur quote), jadi peek ini TIDAK PERNAH bisa
  berbeda dari hasil `ConsumeQuote` yang sebenarnya beberapa saat kemudian —
  peek murni soal *account resolution timing*, bukan soal *source of truth*
  fee. Kegagalan peek (quote tak ditemukan/user salah/format UUID salah)
  sengaja DIABAIKAN di titik ini — `ConsumeQuote` di dalam `execTransfer`
  TETAP satu-satunya sumber kebenaran yang mengembalikan
  `ErrQuoteExpired`/`ErrQuoteMismatch` definitif, peek yang gagal hanya
  berarti tidak ada fee leg optimistis, dan tx akan rollback total tetap
  sama seperti mestinya.
- `internal/ledger/service/handle/service.go` `execTransfer`: blok BARU
  "── 1b. FEE QUOTE CONSUMPTION" disisipkan SEGERA setelah
  `RELEASE SAVEPOINT sp_idem` (akhir gerbang idempotency) dan SEBELUM
  "── 2. SPLIT ACCOUNTS & LOCK" — persis "SEGERA setelah buka tx dan
  SEBELUM LockBalances" sesuai Langkah. Memanggil
  `s.feePolicy.ConsumeQuote(ctx, tx, quoteID, cmd.UserID, cmd.Type,
  cmd.Currency, cmd.Amount, "tx:"+txID.String())` — INI (bukan peek di atas)
  satu-satunya konsumsi ATOMIK, single-use, exact-match yang sesungguhnya.
  - Verdict sukses → `cmd.Metadata["fee_amount"/"fee_gateway"]` ditimpa
    dengan hasil konsumsi RESMI (bukan hasil peek — walau secara nilai
    selalu identik, kode tetap memperlakukan hasil `ConsumeQuote` sebagai
    otoritatif).
  - `ErrQuoteExpired`/`ErrQuoteMismatch` → **DIKEMBALIKAN LANGSUNG** (bukan
    lewat `markFailed`+`businessErr` seperti kegagalan validasi processor di
    step 4) — `WithTx` ROLLBACK SELURUH transaksi TERMASUK insert header
    baris `ledger_transactions` di step 1. **Koreksi terhadap pola
    "commit-as-failed" existing di codebase ini** (dipakai processor
    business-validation, didokumentasikan sebagai "FIX #2 iter2"): Test
    Wajib T4 eksplisit mensyaratkan "TIDAK ada tx" untuk quote
    expired/mismatch — berbeda dari kegagalan validasi processor biasa yang
    SENGAJA commit baris `status='failed'` untuk audit trail. Keputusan:
    kegagalan quote diperlakukan seperti kegagalan STRUKTURAL (step 3
    `validateAccounts`, yang juga `return err` langsung/rollback), bukan
    seperti kegagalan bisnis processor — karena kegagalan quote adalah
    PRA-KONDISI yang belum sempat "dicoba" secara bermakna, bukan hasil
    percobaan posting yang gagal. Efek samping yang benar: idempotency_key
    yang sama bisa langsung dipakai ulang (mis. quote baru) tanpa pernah
    menyentuh `handleDuplicate`.
  - Error selain kedua sentinel (infra) → `fmt.Errorf("consume fee quote:
    %w", qerr)` — rollback juga, tapi BUKAN diklasifikasi sebagai business
    error (retryable-check `generalerror.IsRetryable` tetap hanya mengenali
    kode Postgres 40001/40P01, jadi kegagalan quote TIDAK PERNAH di-retry
    oleh `transfer()`'s retry loop — sengaja, retry tidak masuk akal untuk
    penolakan bisnis yang definitif).
- `internal/ledger/apperror/apperror.go`: sentinel baru
  `ErrQuoteExpired = errors.New("QUOTE_EXPIRED")`,
  `ErrQuoteMismatch = errors.New("QUOTE_MISMATCH")`.
- `internal/ledger/transport/errors.go`: `writeError` menambahkan kedua
  sentinel ke daftar `case` yang memetakan ke `response.UnprocessableEntity`
  (422) — persis pola `ErrScreeningBlocked` (doc 37 T3): `err.Error()`
  memuat `[QUOTE_EXPIRED] pesan...`/`[QUOTE_MISMATCH] pesan...` via
  `apperror.NewBizErr`, sehingga body respons memuat string kode tsb tanpa
  perlu field "code" terpisah.
- **Paritas gRPC "gratis"** (sesuai Langkah #3, tanpa kode baru di
  `pkg/ledgererr`): `internal/ledger/grpcserver/server.go`'s `mapError`
  SUDAH generik menangani APAPUN `*apperror.LedgerError` (via
  `errors.As`) → `codes.FailedPrecondition` + `ErrorInfo{Reason:
  ledgerError.Code}`; `pkg/ledgererr.FromStatus` SUDAH generik mendekode
  APAPUN `FailedPrecondition` + `Reason` non-kosong →
  `&LedgerError{Code: info.Reason, ...}` — mekanisme yang SAMA PERSIS sudah
  dipakai `SCREENING_BLOCKED` (doc 37) tanpa sentinel `pkg/ledgererr` khusus.
  `QUOTE_EXPIRED`/`QUOTE_MISMATCH` otomatis ikut mekanisme ini — TIDAK ADA
  perubahan di `internal/ledger/grpcserver` atau `pkg/ledgererr` yang
  diperlukan sama sekali.
- `internal/ledger/ledger.go`: `NewModule` mendapat parameter baru
  `feeQuoteTTL time.Duration` (di ujung); `feepolicy.New(db)` kini dibuat
  SEKALI sebagai `feeQuotePolicy` dan dipakai baik untuk konsumsi
  (`ledgerhandle.New(..., feeQuotePolicy)`) maupun untuk quote publik
  (`m.feePolicy`) — satu instance, bukan dua salinan terpisah tak berguna.
- `internal/ledger/service/handle/service.go`: `Service` mendapat field
  `feePolicy *feepolicy.Policy`; `New(...)` mendapat parameter baru
  `feePolicy *feepolicy.Policy` di ujung. `nil` = valid (quote_id pada
  Command manapun akan diam-diam diabaikan — tidak ada caller di codebase
  ini yang melakukan itu, transport hanya set QuoteID setelah Service
  dikonstruksi dengan feePolicy nyata).
- Call site yang diperbarui dengan argumen tambahan: `cmd/ledger-service/
  main.go` (menyambungkan `cfg.Ledger.FeeQuoteTTL`, WIRING YANG DITUNDA
  DARI T2 KINI SELESAI), `internal/testutil/ledger.go`,
  `internal/notify/notify_integration_test.go`,
  `internal/ledger/grpcserver/server_integration_test.go`,
  `internal/ledger/schema_contract_test.go` (feePolicy asli, bukan nil,
  karena file ini butuh menguji quote juga), 14 pemanggilan `New(...)` di
  `internal/ledger/service/handle/service_test.go` (semua `nil` — test lama
  ini tidak menguji quote).
- Test wajib:
  - **KUNCI** — `internal/ledger/execquote_integration_test.go` (baru,
    testcontainers Postgres real, package `ledger_test` sama seperti
    `schema_contract_test.go`, reuse `setupSchemaTestDB`/`newService`/
    `createUserCashAccount`):
    - `TestSchemaContract_ExecTransfer_QuoteHonoredExactly_EvenIfFeeRuleChangesAfterQuote`
      — quote dibuat saat `fee_rules.flat_minor_units=500`; rule diubah
      admin jadi `9999` SETELAH quote dibuat, SEBELUM posting; posting
      ber-quote membayar TEPAT 500 (dibaca dari
      `account_balances.balance` akun `fee[platform][IDR]` yang di-seed
      migrasi 000002, id tetap `00000000-0000-0000-0000-000000000003`).
    - `TestSchemaContract_ExecTransfer_QuoteExpired_RollsBackEntirely_NoTxNoEntries`
      — quote di-backdate `expires_at` manual, posting → `ErrQuoteExpired`,
      `count(*) FROM ledger_transactions WHERE idempotency_key=...` = 0.
    - `TestSchemaContract_ExecTransfer_QuoteMismatch_RollsBackAndQuoteStaysUsable`
      — amount berbeda dari quote → `ErrQuoteMismatch`, nol baris tx,
      `consumed_at IS NULL` tetap benar, LALU retry dengan amount BENAR
      pakai quote yang SAMA tetap berhasil (bukti quote tidak terbakar).
    - `TestSchemaContract_ExecTransfer_ReplayAfterQuoteSuccess_IdempotentNoReconsumption`
      — replay idempotency_key yang sama (quote sudah consumed) tetap
      sukses idempoten, TIDAK mencoba re-consume.
    - `TestSchemaContract_ExecTransfer_ConcurrentDifferentTransfersSameQuote_ExactlyOneSucceeds`
      — 5 goroutine, 5 idempotency_key BERBEDA, quote_id SAMA → tepat 1
      menang, 4 sisanya `ErrQuoteExpired`.
    - `TestSchemaContract_ExecTransfer_NoQuoteID_BehavesExactlyAsBefore` —
      transfer tanpa quote_id sama sekali, tanpa fee_rule → akun fee tetap
      nol, membuktikan blok 1b adalah no-op total saat `QuoteID == ""`.
    - Seluruh suite `TestSchemaContract_*` lama (161 detik total) dijalankan
      ulang penuh — nol regresi.
  - `internal/ledger/transport/http_test.go` (sqlmock/mock Service, unit):
    `TestPostTransaction_QuoteID_PassedThroughAsTypedField_NotMetadata`,
    `TestPostTransaction_QuoteExpired_Maps422`,
    `TestPostTransaction_QuoteMismatch_Maps422`.
- Verifikasi: `go build`/`go vet` (default + `-tags=integration`) bersih;
  `gofmt -l` bersih; `make lint` bersih; `make test` (termasuk
  `boundary_test.go`) hijau; integration test khusus T4 di atas hijau
  semua; regresi `TestSchemaContract_*` penuh hijau.

## T5 — Payout memakai fee tersimpan

### Langkah
1. Proto ledger: RPC baru `ConsumeFeeQuote(ConsumeFeeQuoteRequest{quote_id, user_id, transaction_type, currency, amount, consumed_by_ref}) returns (ConsumeFeeQuoteResponse{fee_amount, fee_gateway})` (additive; `ResolveFee` TETAP sebagai fallback tanpa-quote). Implement di `internal/ledger/grpcserver` di atas T2 (tx pendek sendiri — konsumsi payout tidak berada di dalam tx posting ledger; row payout adalah komitmennya). `pkg/ledgerclient` + method baru.
2. Proto payout: `CreatePayoutRequest` + `string quote_id` opsional; gateway `internal/handler/payout.go` meneruskan dari body HTTP.
3. `migrations/payout/000004_quoted_fee.up/down.sql`: `ALTER TABLE payout_requests ADD COLUMN fee_quote_id UUID, ADD COLUMN fee_amount BIGINT, ADD COLUMN fee_gateway TEXT;`
4. `internal/payout/orchestrate.go` `Create` — urutan ANTI-BURN: insert row payout (status `created`) → `ConsumeFeeQuote` (txType `withdraw_settle`, gateway `""`, ref `payout:<id>`) → hold. Konsumsi gagal expired/mismatch → row jadi terminal `rejected` + 422 ke user (map kode ledgererr); quote hangus maksimal biaya satu re-quote, TIDAK PERNAH uang.
5. `settle`: bila row punya `fee_amount` tersimpan → pakai itu (SKIP `ResolveFee`); tanpa quote → fallback `ResolveFee` existing. TTL hanya menjaga Create — settle berjam-jam kemudian tetap memakai fee tersimpan (keputusan terkunci master).

### Test wajib
- Integration: fee settle = quote walau `fee_rules` berubah sebelum settle; jalur resume-job settle memakai fee tersimpan; payout tanpa quote = perilaku lama; quote expired → 422, TIDAK ada hold, ledger tak tersentuh, row payout terminal `rejected`.
- `make proto-breaking` hijau.

### DoD
- [x] Fee payout yang dilihat user saat create = fee yang dipotong saat settle, apa pun yang terjadi di antaranya.

### Hasil
- Proto ledger: RPC additive `ConsumeFeeQuote` di `api/proto/seev/ledger/v1/ledger.proto` (`ConsumeFeeQuoteRequest{quote_id, user_id, transaction_type, currency, amount, consumed_by_ref}` → `ConsumeFeeQuoteResponse{fee_amount, fee_gateway}`); `ResolveFee` tetap ada sebagai fallback tanpa-quote untuk semua caller lama. Proto payout: `CreatePayoutRequest` + `string quote_id = 5` opsional. `make proto` (buf generate) dijalankan, `gen/` dikomit; `make proto-lint` hijau; `make proto-breaking` gagal dengan limitasi lingkungan yang SAMA dan sudah didokumentasikan di T1/T4 (`main` tidak pernah punya file proto ter-commit sebelum sesi doc 36+, jadi buf tidak punya baseline untuk dibandingkan) — bukan regresi baru.
- `internal/ledger/apperror`: sentinel `ErrQuoteExpired`/`ErrQuoteMismatch` (dari T4) dipakai ulang. `internal/ledger/feepolicy/quote.go` menambah `ConsumeQuoteStandalone` (memanggil `ConsumeQuote` dengan `p.db` sendiri sebagai `execer`, tanpa perlu tx eksternal — payout mengonsumsi quote lewat gRPC pendek, bukan di dalam tx posting ledgernya sendiri). `internal/ledger/ledger.go` menambah `Module.CreateQuote`/`Module.ConsumeFeeQuote` (yang terakhir menerjemahkan sentinel `feepolicy` mentah menjadi `apperror.NewBizErr(...)` sebelum dikembalikan) plus re-export `type Quote = feepolicy.Quote` dari root facade — dua boundary violation ditemukan dan diperbaiki dengan pola yang sama persis (lihat di bawah).
- `internal/ledger/grpcserver/server.go`: handler `ConsumeFeeQuote` baru memanggil `mapError` GENERIK yang sudah ada (yang sudah menangani `*apperror.LedgerError` apa pun via `errors.As` sejak `SCREENING_BLOCKED` di doc 37) — nol kode baru di `pkg/ledgererr` dibutuhkan; `QUOTE_EXPIRED`/`QUOTE_MISMATCH` mendapat parity gRPC↔HTTP gratis. `pkg/ledgerclient` menambah method `ConsumeFeeQuote`.
- Migrasi `migrations/payout/000004_quoted_fee.up/down.sql`: `payout_requests` menambah `fee_quote_id UUID`, `fee_amount BIGINT`, `fee_gateway TEXT`, dan constraint `payout_requests_status_check` diperluas menambah `'rejected'` (Postgres mewajibkan DROP+ADD constraint, tidak ada ALTER CHECK in-place). Diverifikasi up→down→up terhadap `seev-postgres-1` riil, termasuk `\d payout_requests` dan listing constraint.
- `internal/payout/model`: status terminal baru `StatusRejected` (docstring membedakannya dari `StatusFailed` — rejected = quote ditolak sebelum hold pernah dibuat; failed = kegagalan submit/vendor pasca-hold). `PayoutRequest` menambah `FeeQuoteID *uuid.UUID`, `FeeAmount *decimal.Decimal` (pointer supaya NULL vs "quote fee nol" bisa dibedakan), `FeeGateway string`.
- `internal/payout/repository`: interface + implementasi menambah `TransitionToRejected` (UPDATE bersyarat `WHERE status = 'created'`) dan `SetQuotedFee`; ketiga query SELECT (`Get`/`List`/`ListStuck`) diperluas mengembalikan kolom fee baru; mock diregenerasi via `mockgen`.
- `internal/payout/orchestrate.go` `Create` — urutan ANTI-BURN persis sesuai Langkah 4: `repo.Insert` (status `created`) → jika `quoteID != ""`: `poster.ConsumeFeeQuote` → sukses: `repo.SetQuotedFee` (status tak berubah); gagal (UUID invalid / expired / mismatch / gagal persist): `repo.TransitionToRejected` + return error TANPA pernah memanggil `hold()`. Hasilnya: quote yang ditolak TIDAK PERNAH menyentuh uang — worst case adalah re-quote, bukan dana tertahan.
- `settle()`: bila `req.FeeQuoteID != nil` → pakai `req.FeeAmount`/`req.FeeGateway` TERSIMPAN langsung (skip `ResolveFee` sepenuhnya); tanpa quote → fallback `ResolveFee` seperti sebelumnya, tidak berubah. TTL quote hanya menjaga `Create` — resume job yang settle jam kemudian tetap menghormati fee tersimpan berapa pun `fee_rules` berubah di antaranya (keputusan terkunci master doc, dibuktikan oleh test kedua di bawah).
- `internal/handler/payout.go` (gateway): `createPayoutRequest` menambah field `quote_id` opsional; switch error map menambah dua case baru mencocokkan prefix `[QUOTE_EXPIRED]`/`[QUOTE_MISMATCH]` (format `ledgererr.LedgerError.Error()`) → kode JSON `QUOTE_EXPIRED`/`QUOTE_MISMATCH` terpisah, konsisten dengan pola T4 di jalur P2P.
- **Dua boundary violation ditemukan & diperbaiki** (keduanya di `internal/testutil/ledger.go`, yang menjadi harness bersama untuk integration test payout DAN ledger): percobaan pertama mengimpor `internal/ledger/feepolicy` langsung untuk memanggil `ConsumeQuoteStandalone` — gagal `boundary_test.go` ("hanya `internal/ledger` sendiri yang boleh mengimpor subpackage-nya"). Percobaan kedua (setelah menambah passthrough `Module.ConsumeFeeQuote`) tetap gagal karena method itu awalnya mengembalikan sentinel `feepolicy` MENTAH, memaksa `testutil` mengklasifikasikannya sendiri (butuh impor `feepolicy` lagi). Diperbaiki dengan menerjemahkan sentinel menjadi `*apperror.LedgerError` DI DALAM `ledger.Module.ConsumeFeeQuote` sendiri, sehingga `testutil`'s `translateLedgerErr` yang SUDAH ADA (generik untuk `*ledger.LedgerError` mana pun) menangani tanpa kode baru — `testutil/ledger.go` sekarang nol impor `feepolicy`.
- Test: 3 test integration BARU di `internal/payout/payout_integration_test.go` terhadap Postgres riil (testcontainers) — `TestPayout_QuoteHonoredAtSettle_EvenIfFeeRuleChangesBeforeSettle` (quote dibuat dengan fee 2500, `fee_rules` diubah jadi 9999 SEBELUM settle, fee[platform] yang dikreditkan tetap 2500), `TestPayout_ResumeJobSettle_UsesStoredFee` (jalur async + `ResumeStuck`, `fee_rules` diubah di antara Create dan resume-triggered settle, fee tetap yang tersimpan 1200 bukan 7777 baru), `TestPayout_QuoteExpired_Returns422_NoHold_LedgerUntouched_RowRejected` (quote di-backdate `expires_at`, Create menolak, `HoldTxID` tetap nil, cash user TIDAK berubah, row `StatusRejected`) — ketiganya PASS. "Payout tanpa quote = perilaku lama" diverifikasi regresi via test pre-existing `TestPayout_Create_WithWithdrawFee_SettleChargesFee`/`TestPayout_Create_WithWithdrawFee_CancelledRefundsFullAmount_NoFeeCharged` (masih PASS, tak diubah). Seluruh suite `internal/payout` (unit + integration, termasuk `grpcserver` dan `repository`) dijalankan ulang — semua PASS, tak ada regresi.
- Ditemukan kode mati saat memperbaiki compile: `internal/payout/http.go`'s `CreateHandler()` tidak pernah dipasang di router produksi manapun (`cmd/payout-service/router.go` hanya memasang `AdminRouter()`), hanya dilatih oleh `http_test.go`-nya sendiri. Perbaikan minimal (tambah `, ""` di panggilan `m.Create(...)`) dilakukan agar tetap kompilasi; penghapusan penuh di-flag lewat `spawn_task` sebagai pekerjaan terpisah (di luar scope T5), belum dieksekusi.
- Gate penuh dijalankan ulang dan hijau: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l .` (satu file tak terkait — `internal/policy/alert_test.go`, untracked, pre-existing, bukan bagian sesi ini), `make lint`, `make test` (race+cover, semua paket hijau).

## T6 — E2E + index README

### Langkah
1. `business-e2e.sh`: quote → transfer ber-quote (assert fee leg = quote PERSIS) → ubah `fee_rules` via admin API → transfer ber-quote lama tetap fee lama → tamper amount → 422 QUOTE_MISMATCH → konsumsi quote yang sama lagi → 422 QUOTE_EXPIRED → re-quote → sukses. Payout ber-quote: fee settle = quote walau rule diubah di tengah.
2. Update `docs/plan/README.md`.

### Test wajib
- business-e2e hijau end-to-end.

### DoD
- [x] Journey quote terbukti dari API sampai entries, termasuk jalur penolakan.

### Hasil
- `scripts/business-e2e.sh` menambah **Section 5: Fee quote journey** (`quote_journey()`), disisipkan setelah withdraw (section 4) dan sebelum daily ops (section 5 lama → digeser jadi 6, request tracing → 7), sehingga assertion `ops()` (ledger balanced/no stuck pending) juga memvalidasi seluruh journey quote, bukan hanya transaksi sebelumnya.
- `onboard()` diperluas menangkap id rule fee yang disemai sebagai global baru `TRANSFER_FEE_RULE_ID`/`WITHDRAW_FEE_RULE_ID` (dari respons `POST /admin/ledger/fee-rules`) — dipakai `quote_journey()` untuk `PUT /admin/ledger/fee-rules/{id}` (re-price) SETELAH quote dibuat, membuktikan quote immutable terhadap perubahan rule berikutnya.
- Journey `quote_journey()` lengkap sesuai Langkah #1, dijalankan berurutan terhadap server nyata (bukan mock):
  1. `POST /api/v1/ledger/fees/quote` (transfer_p2p, 50000) → fee=500 (A's per-user override).
  2. Admin `PUT` fee_rules A jadi 9000 — quote SUDAH dibuat, tidak boleh terpengaruh.
  3. `POST /api/v1/ledger/transactions` dengan `quote_id` → fee[platform] naik TEPAT 500, bukan 9000.
  4. Quote KEDUA dibuat (fee 20000), lalu di-tamper dengan amount 99999 → 422 dengan body memuat `[QUOTE_MISMATCH]` (format `apperror.LedgerError.Error()`, kode HTTP generik `UNPROCESSABLE_ENTITY` — sesuai desain T4: ledger tidak memberi kode granular terpisah di response envelope untuk endpoint P2P, cukup substring pesan, berbeda dari payout yang punya field `code` terpisah).
  5. Quote yang SAMA (belum terbakar — WHERE clause `consumeQuoteQuery` T2 tidak match amount 99999 sehingga 0 baris ter-UPDATE) di-post ULANG dengan amount BENAR (20000) → sukses 2xx, membuktikan tamper tidak membakar quote.
  6. Quote yang SAMA dikonsumsi LAGI (sudah `consumed_at` terisi dari langkah 5) → 422 dengan body memuat `[QUOTE_EXPIRED]` (single-use terbukti dari API, bukan cuma dari integration test Go).
  7. Re-quote baru → sukses.
  8. Quote payout (withdraw_settle, 40000) → fee=2000; admin re-price withdraw_settle jadi 8000 SETELAH quote dibuat; `POST /api/v1/payout` dengan `quote_id` → settled instan, fee[platform] naik TEPAT 2000 bukan 8000 — membuktikan T5's keputusan kunci ("fee di-lock saat Create, dihormati di settle apa pun yang terjadi") dari sisi API publik, bukan hanya integration test Go.
- **Bug ditemukan & diperbaiki saat verifikasi lokal**: dua assignment `mismatch_resp="$(curl ...)"` dan `reuse_resp="$(curl ...)"` awalnya kehilangan tanda kutip penutup setelah `)` penutup command substitution (pola `var="$(...)"` butuh SATU LAGI `"` di akhir dibanding pola `var=$(...)` tanpa kutip pembungkus yang dipakai di baris lain pada fungsi yang sama) — `bash -n` gagal dengan "syntax error near unexpected token `('" di baris jauh setelahnya (bukan di baris sumber masalah, karena parser bash baru sadar string belum ditutup saat menemukan token berikutnya). Ditemukan lewat bisection manual (`bash -n` pada potongan file yang makin diperbesar) sampai baris persis yang kehilangan kutip penutup teridentifikasi; diperbaiki dengan menambah `"` di akhir kedua baris tsb. `bash -n` bersih setelahnya.
- **Temuan environment (bukan bug kode)**: run pertama `business-e2e.sh` gagal di section 1 ("global fee seed got HTTP 500", "withdraw fee seed got HTTP 500") karena Postgres dev container (`seev-postgres-1`, sudah berumur 2 jam dari sesi-sesi sebelumnya hari ini) masih menyimpan baris `fee_rules` global (`user_id IS NULL`) untuk `transfer_p2p`/`withdraw_settle` dari run business-e2e SEBELUMNYA — `POST /admin/ledger/fee-rules` bukan upsert, jadi baris duplikat memicu pelanggaran constraint unik → 500. Ini konsisten dengan PROJECT_GUIDE.md: skrip ini didesain dijalankan di atas volume Postgres yang di-reset (`docker compose down -v`) sebagai bagian dari gate verifikasi penuh, bukan idempoten lintas run tanpa reset. Diperbaiki dengan `docker compose down -v` lalu rerun — journey PENUH (semua 6 section, termasuk quote) PASS bersih dari awal.
- Update `docs/plan/README.md`: baris index doc 38 status `⬜ todo` → `✅ done`.
- Verifikasi: `bash -n scripts/business-e2e.sh` bersih; `./scripts/business-e2e.sh` PENUH (dari `docker compose down -v` bersih) — SEMUA section PASS termasuk section 5 quote journey baru (13 assertion baru, semuanya `[ pass]`), tanpa regresi di section 1-4/6-7 yang sudah ada sebelumnya.

---

## Verifikasi akhir dokumen
Gate standar master doc 36 hijau semua → lanjut [39-phase7d-kyc-tiers.md](39-phase7d-kyc-tiers.md).

Dijalankan dari `docker compose down -v` (volume Postgres/Redis/RabbitMQ bersih) pada 2026-07-16, semua hijau:
- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l .` (satu file tak terkait — `internal/policy/alert_test.go`, untracked pre-existing, bukan bagian doc 38).
- `make lint` bersih.
- `make test` (race+cover, seluruh paket) hijau.
- `./scripts/smoke-test.sh` — semua assertion PASS.
- `./scripts/business-e2e.sh` — semua 7 section PASS termasuk quote journey T6.
- `./scripts/chaos-test.sh all` — ketujuh skenario PASS (broker down/recover, Postgres restart mid-traffic, Redis down + fail-open + restart-recovery, payout crash-mid-flight ×4 kill point + ledger-down mid-resume, payin-service down + redelivery heal, fraud-service down fail-open + block-mode pra-write di ketiga alur).

Doc 38 (fee quotes) selesai penuh (T1–T6) → lanjut [39-phase7d-kyc-tiers.md](39-phase7d-kyc-tiers.md).
