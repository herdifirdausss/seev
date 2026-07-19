# 39 — Phase 7d: KYC Bertingkat L0/L1/L2 + Limit per Tier

> Baca master reference di [36-phase7a-request-tracing.md](36-phase7a-request-tracing.md). Prasyarat: doc 38 selesai.

## Konteks

`auth_users` hari ini tidak punya konsep KYC — siapa pun yang register langsung bisa transaksi penuh. Fase ini menambahkan KYC bertingkat: **L0** = baru daftar (tidak bisa transaksi), **L1** = KYC dasar (limit rendah), **L2** = KYC penuh (limit tinggi; payload boleh berisi data KYB/badan usaha). Verifikasi identitas memakai **mock provider** (pola `internal/vendorgw/mockvendor`) + **review admin**. Penegakan limit MENUMPANG policy engine existing (`internal/policy`, `policy_limits` di seev_ledger — per-user limits dari doc 17).

Keputusan terkunci (dari master): status KYC hidup di `seev_auth`; propagasi via JWT claim `kyc_level` (staleness dibatasi TTL access token — AMAN karena kontrol keras = `policy_limits` yang diupdate SINKRON saat approve; gate gateway = UX); gating di gateway (satu choke point); upgrade tier menulis `policy_limits` via gRPC ledger baru `ApplyKycTier` dari tabel template `policy_tier_limits`; mock provider auto-decide L1, L2 SELALU `refer` → review admin manual.

## T1 — Skema auth (migrasi auth 000002)

### Langkah
1. `migrations/auth/000002_kyc.up/down.sql`:
   - `ALTER TABLE auth_users ADD COLUMN kyc_level SMALLINT NOT NULL DEFAULT 0 CHECK (kyc_level IN (0,1,2));`
   - `CREATE TABLE kyc_submissions (id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES auth_users(id), level_requested SMALLINT NOT NULL CHECK (level_requested IN (1,2)), status TEXT NOT NULL CHECK (status IN ('pending','approved','rejected')), payload JSONB NOT NULL, provider TEXT NOT NULL, provider_ref TEXT, decided_by TEXT, decision_reason TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), decided_at TIMESTAMPTZ);`
   - Partial unique index `ON kyc_submissions(user_id) WHERE status = 'pending'` (satu submission terbuka per user).
   - Grants + RLS pola DB auth existing.

### Test wajib
- up→down→up bersih terhadap Postgres nyata.

### DoD
- [x] Skema KYC siap dengan constraint yang mencegah submission ganda dan level liar.

### Hasil
`migrations/auth/000002_kyc.up/down.sql` persis sesuai spek: `auth_users.kyc_level SMALLINT NOT NULL DEFAULT 0 CHECK (kyc_level IN (0,1,2))`; `kyc_submissions` (11 kolom, `level_requested CHECK (1,2)`, `status CHECK ('pending','approved','rejected')`); partial unique index `idx_kyc_submissions_one_pending ON kyc_submissions(user_id) WHERE status='pending'` (satu submission terbuka per user); grants+RLS pola `000001_auth`. Diverifikasi up→down→up terhadap `seev-postgres-1` riil (`\d auth_users`/`\d kyc_submissions` menunjukkan constraint dan index persis seperti di atas).

## T2 — Mock KYC provider

### Langkah
1. `internal/kycvendor/kycvendor.go`: interface `Provider { Name() string; Verify(ctx, Submission) (Decision, error) }`, `Decision{Verdict: approve|reject|refer; Ref, Reason string}`. Registry sederhana bila perlu (satu provider di MVP).
2. `internal/kycvendor/mockkyc/`: pola mockvendor — field payload `mock_mode` ∈ `approve|reject|refer|timeout` menentukan hasil; `level_requested == 2` SELALU `refer` apa pun mock_mode (aturan L2 = review manual); tanpa mock_mode → approve L1.
3. `boundary_test.go`: `internal/kycvendor` = milik auth-service.

### Test wajib
- Table-driven test provider: keempat mode + aturan L2-selalu-refer + default.

### DoD
- [x] Provider mock deterministik untuk semua jalur test.

### Hasil
`internal/kycvendor/kycvendor.go`: `Provider{Name() string; Verify(ctx, Submission) (Decision, error)}`, `Decision{Verdict, Ref, Reason}`. `internal/kycvendor/mockkyc/mockkyc.go`: `mock_mode` ∈ `approve|reject|refer|timeout`; `level_requested == 2` SELALU `refer` apa pun `mock_mode` (dicek PALING AWAL, sebelum membaca `mock_mode` sama sekali); tanpa `mock_mode` → default `approve`. `boundary_test.go`: `"auth-service": {"auth": true, "kycvendor": true}` — kepemilikan modul benar. Test tabel-driven (`mockkyc_test.go`) mencakup keempat mode + aturan L2-selalu-refer (disapu lintas semua mode) + default + mode tak dikenal → error.

## T3 — Submit + status + review admin di auth-service

### Langkah
1. `internal/auth/kyc.go`: `SubmitKYC(ctx, userID, levelRequested, payload)` — validasi level = current+1 SAJA (tidak boleh lompat 0→2), tolak bila ada pending; panggil provider: `approve` → satu fungsi shared `approveSubmission` (bump `kyc_level` + panggil `ApplyKycTier` T5 + mark approved — ATOMIK: `ApplyKycTier` gagal ⇒ approval gagal, level TIDAK naik, submission tetap pending; gotcha #10 master); `reject` → mark rejected + reason; `refer` → status pending menunggu admin.
2. HTTP publik (`cmd/auth-service/router.go`, sebelah `/users/me`): `POST /api/v1/users/me/kyc` (submit), `GET /api/v1/users/me/kyc` (level sekarang + submission terakhir).
3. Admin (internal :8083): `GET /api/v1/admin/kyc/submissions?status=pending`, `POST /api/v1/admin/kyc/submissions/{id}/approve`, `POST /api/v1/admin/kyc/submissions/{id}/reject {reason}` — approve memakai `approveSubmission` yang sama, `decided_by` = user admin dari JWT.

### Test wajib
- Unit + integration: 0→1 auto-approve; 0→2 ditolak (lompat); 1→2 refer→admin approve; reject; pending ganda ditolak; `ApplyKycTier` gagal → level tidak berubah + submission tetap pending.

### DoD
- [x] Alur submit→verifikasi→review→level naik lengkap dan atomik terhadap limits.

### Hasil
- `internal/auth/auth.go` `SubmitKYC`: validasi `levelRequested == user.KYCLevel+1` DAN dalam rentang [1,2] (menolak lompat 0→2 dan turun/lompat sembarang); menolak bila ada submission `pending`; memanggil provider dan bercabang pada verdict — `approve` → `approveSubmission` (atomik, lihat di bawah); `reject` → `RejectKYCSubmission` (status `rejected` + alasan provider, level TIDAK berubah); `refer` → baris tetap `pending` menunggu admin.
- HTTP: `POST/GET /api/v1/users/me/kyc` (publik, authed) dan admin internal (:8083) `GET /api/v1/admin/kyc/submissions?status=`, `POST .../{id}/approve`, `POST .../{id}/reject` — approve memakai `approveSubmission` yang SAMA, `decided_by` dari JWT admin.
- **Koreksi arsitektur ditemukan saat mengerjakan T5**: `approveSubmission` semula melakukan type-assertion runtime (`m.provisioner.(interface{ ApplyKycTier(...) error })`) alih-alih memperluas interface `Provisioner` secara statis seperti yang diminta Langkah T5 ("Provisioner interface diperluas"). Type-assertion ini akan SELALU gagal terhadap `pkg/ledgerclient.Client` yang sesungguhnya karena `ApplyKycTier` belum pernah diimplementasikan di sana sama sekali (lihat Hasil T5) — sehingga SETIAP approval (baik auto-approve L1 maupun admin-approve L2) akan gagal permanen di produksi walau seluruh test unit hijau (test unit memakai stub yang KEBETULAN mengimplementasikan method itu). Diperbaiki dengan memperluas `Provisioner` interface itu sendiri menambah `ApplyKycTier(ctx, uuid.UUID, int) error` — `approveSubmission` kini memanggil `m.provisioner.ApplyKycTier` langsung, dicek oleh compiler, tidak lagi oleh reflection saat runtime.
- Atomicity (`internal/auth/repository/repository.go` `ApproveKYCSubmission`): satu `WithTx`, `SELECT ... FOR UPDATE` mengunci baris submission, memanggil `applyTier` DI DALAM transaksi — gagal ⇒ rollback total (level tidak naik, submission tetap pending) — gotcha #10 master terpenuhi.
- Test tambahan sesi ini: `TestSubmitKYC_RejectVerdict_MarksRejectedNoLevelChange` (unit, melengkapi cakupan verdict `reject` yang sebelumnya hanya diuji di level provider) + 3 integration test BARU di `internal/auth/kyc_integration_test.go` terhadap Postgres+ledger riil (bukan mock) — `TestAuth_KYC_L0ToL1_AutoApprove_AppliesRealLedgerTier`, `TestAuth_KYC_L1ToL2_ReferThenAdminApprove_UpgradesRealLedgerTierInPlace`, `TestAuth_KYC_Reject_LevelUnchangedNoLedgerCall` — inilah test yang SEHARUSNYA menangkap gap T5 sejak awal (test unit dengan stub tidak bisa, karena stub-nya sendiri yang "kebetulan benar"); ketiganya PASS setelah T5 selesai, membuktikan vertikal penuh: submit → provider → approve → `ApplyKycTier` gRPC nyata → `policy_limits` ter-upsert di `seev_ledger` riil.

## T4 — JWT claim `kyc_level` + gating gateway

### Langkah
1. `internal/auth/auth.go`: claims + `KYCLevel int`; issue login + refresh membaca level dari DB (refresh = jalur penyegaran claim). Claim ABSEN di token lama = level 0, BUKAN error (gotcha #11). Bootstrap admin: set level 2.
2. `pkg/middleware` (atau helper claims existing): parsing claim baru tersedia untuk gateway dan ledger.
3. Gateway `internal/handler`: middleware `requireKYC(min int)` di `POST /topup`, `POST /payout`, dan guard method+path di depan proxy ledger untuk `POST /api/v1/ledger/transactions*`. GET dan `POST /api/v1/ledger/fees/quote` tetap boleh L0 (user boleh lihat fee sebelum KYC). Respons 403 `{code:"KYC_REQUIRED", min_level:1}`.
4. Defense-in-depth: transport ledger sudah parse JWT (router publik pakai `WithAuth`) → tambah cek claim `kyc_level >= 1` di handler posting publik ledger juga (jalur langsung internal tidak terpengaruh — internal router beda listener).

### Test wajib
- Router gateway: token L0 → 403 di tiga rute; token L1 lolos; token tanpa claim = L0; GET/quote lolos L0.
- Transport ledger: posting publik token L0 → 403.

### DoD
- [x] User L0 tidak bisa menggerakkan uang lewat jalur mana pun yang user-facing.

### Hasil
- `pkg/middleware.Claims` menambah `KYCLevel int \`json:"kyc_level"\`` — token lama tanpa field ini otomatis decode ke 0 (gotcha #11, gratis dari perilaku zero-value JSON Go, tanpa kode tambahan). `internal/auth/auth.go` `issueTokensWithID` mengisi `KYCLevel` dari `u.KYCLevel` di LOGIN maupun REFRESH; bootstrap admin diberi `KYCLevel: 2`.
- **Bug produksi ditemukan & diperbaiki**: `requireKYC(min)` di `internal/handler/router.go` awalnya SATU fungsi dipakai di TIGA tempat (`POST /api/v1/ledger/*` proxy, `POST /payout`, `POST /topup`), dengan logika internal `if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/v1/ledger/transactions") { next.ServeHTTP(w,r); return }` yang HANYA masuk akal untuk mount proxy ledger (yang melayani banyak sub-path — `/accounts`, `/fees/quote`, `/transactions`, dst — sehingga perlu membedakan sendiri sub-path mana yang digerbang). Tapi `POST /payout` dan `POST /topup` didaftarkan sebagai pola EXACT-MATCH via `net/http.ServeMux` di belakang `http.StripPrefix("/api/v1", apiMux)` — begitu request sampai ke handler-nya, `r.URL.Path` SELALU `"/payout"`/`"/topup"`, TIDAK PERNAH berawalan `/api/v1/ledger/transactions` — sehingga cek path di atas SELALU true dan requireKYC diam-diam MELEWATKAN gerbang KYC sepenuhnya di kedua rute itu, walau dibungkus `requireKYC(1)(...)`. User L0 SEBENARNYA BISA topup/payout tanpa terhalang, bertentangan langsung dengan DoD di atas. Diperbaiki dengan memisahkan jadi dua fungsi: `requireKYC(min)` (sekarang menggerbang TANPA SYARAT — cocok untuk mount rute exact-match seperti `/payout`/`/topup`, karena SETIAP request yang sampai ke situ MEMANG rute yang digerbang) dan `requireKYCForLedgerPostings(min)` (mempertahankan cek method+path lama, dipakai KHUSUS di mount proxy ledger yang melayani banyak sub-path). Bug ini persis jenis yang seharusnya tertangkap "Test wajib" doc — dan memang baru ketahuan saat menulis test tsb (lihat di bawah), bukan dari inspeksi kode semata.
- Gateway `internal/handler/router.go`: `/api/v1/ledger/` proxy → `requireKYCForLedgerPostings(1)`; `POST /payout` dan `POST /topup` → `requireKYC(1)` (fungsi yang sudah diperbaiki). Respons 403 `{"code":"KYC_REQUIRED","min_level":1}` di ketiganya.
- Defense-in-depth `internal/ledger/transport/http.go` `postTransaction`: pada router PUBLIK (`h.allowedTypes != nil`) mengecek `claims.KYCLevel < 1` → 403 `KYC_REQUIRED` yang sama, SEBELUM body di-decode — router internal (listener terpisah) tidak tersentuh.
- Test BARU (sebelumnya nol untuk fitur ini): `internal/handler/router_test.go` — `TestRequireKYCForLedgerPostings_GatesOnlyPostTransactions` (L0→403 di `POST /transactions`; L1 lolos; GET lolos di L0; `POST /fees/quote` lolos di L0) dan `TestRequireKYC_ExactRouteMount_GatesUnconditionally` (REGRESSION TEST untuk bug di atas — L0→403 persis di path `/payout` dan `/topup`, L1 lolos; test ini akan gagal lagi bila cek path lama pernah dikembalikan). `internal/ledger/transport/http_test.go` — `TestPostTransaction_KYCLevelZero_Forbidden`, `TestPostTransaction_KYCLevelOne_Passes`. Semuanya PASS.

## T5 — Limit tier di ledger (migrasi ledger 000022 + gRPC ApplyKycTier)

> Catatan: nomor digeser dari 000021 semula ke 000022 — lihat catatan
> pergeseran nomor migrasi di doc 38 T1 dan tabel keputusan terkunci master
> doc 36.

### Langkah
1. `migrations/ledger/000022_policy_tier_limits.up/down.sql`: tabel template `policy_tier_limits (kyc_level SMALLINT NOT NULL, transaction_type TEXT NOT NULL, max_per_tx BIGINT, max_daily_amount BIGINT, max_daily_count INT, max_monthly_amount BIGINT, PRIMARY KEY (kyc_level, transaction_type))` + SEED: L1 kecil (mis. `transfer_p2p` max_per_tx 1.000.000; `money_in` 5.000.000; `withdraw_initiate` 1.000.000) dan L2 besar (100x L1) — angka final ditetapkan saat implementasi, yang penting L1 cukup kecil untuk test pelanggaran limit di e2e. Grants + RLS.
2. Proto ledger: `rpc ApplyKycTier(ApplyKycTierRequest{user_id, kyc_level}) returns (ApplyKycTierResponse{})` (additive). Implement `internal/ledger/grpcserver` + repository: untuk tiap row template level itu, upsert `policy_limits` (`ON CONFLICT (user_id, transaction_type) DO UPDATE`) — idempoten; level tak dikenal → InvalidArgument.
3. `pkg/ledgerclient` + method `ApplyKycTier`; `internal/auth` `Provisioner` interface diperluas (auth-service sudah memegang koneksi ledger untuk `ProvisionUser` — reuse).

### Test wajib
- grpcserver integration: apply L1 lalu L2 meng-upgrade row in-place; policy engine `Check` menegakkan cap baru (pakai harness test `internal/policy` existing); level tak dikenal → InvalidArgument; idempoten (apply dua kali = satu hasil).
- `make proto-breaking` hijau; `go vet` dua tag (interface Provisioner berubah).

### DoD
- [x] Naik tier langsung mengubah limit efektif di ledger tanpa deploy/SQL.

### Hasil
- `migrations/ledger/000022_policy_tier_limits.up/down.sql`: tabel template persis spek (`kyc_level SMALLINT CHECK(1,2)`, `transaction_type`, empat kolom limit nullable, PK `(kyc_level, transaction_type)`) + seed L1 (`transfer_p2p` 1jt/5jt/20x/50jt; `money_in` 5jt/10jt/5x/100jt; `withdraw_initiate` 1jt/5jt/5x/50jt) dan L2 = 100x L1 di semua kolom. Grants SELECT saja (template read-only di runtime) + RLS. Diverifikasi up→down→up terhadap Postgres riil.
- Proto `ApplyKycTier(ApplyKycTierRequest{user_id, kyc_level}) returns (ApplyKycTierResponse{})` — additive, sudah ada di proto sebelum sesi ini; `gen/` diregenerasi ulang (`make proto`), `make proto-lint` bersih, `make proto-breaking` gagal dengan limitasi lingkungan YANG SAMA sudah didokumentasikan di T1/T4/T5 doc 37/38 (repo `main` tidak pernah punya file proto ter-commit) — bukan regresi.
- **Ditemukan sebagai gap KRITIS saat mensurvei kode yang sudah ada**: skema+proto sudah ada, tapi TIDAK ADA implementasi apa pun di sisi Go — `internal/ledger/grpcserver.Server` tidak meng-override `ApplyKycTier` (mewarisi `UnimplementedLedgerServiceServer`, jadi memanggilnya SELALU `codes.Unimplemented`), tidak ada method di `internal/ledger.Module`, tidak ada di `pkg/ledgerclient.Client`. Ini membuat SELURUH alur upgrade tier T3 nonfungsional total di produksi walau kode auth-nya sendiri benar (lihat catatan T3 di atas).
- Diimplementasikan lengkap:
  - `internal/ledger/repository/kyc_tier_repository.go` (baru): `KycTierRepository.Apply(ctx, userID, kycLevel int32) error` — SATU `WithTx`: `SELECT` semua baris `policy_tier_limits` untuk `kyc_level`; nol baris → `apperror.ErrUnknownKycTier`; tiap baris di-`INSERT ... ON CONFLICT (user_id, transaction_type) DO UPDATE` ke `policy_limits` — idempoten (re-apply level sama = upsert tanpa efek), upgrade/downgrade menimpa baris yang SAMA (bukan menambah baris baru). Mengapa bukan reuse `internal/policy.Repository.Upsert`: `internal/policy` punya komentar arsitektur eksplisit "ledger module itself never imports this package" — SQL raw langsung di sini (pola yang sama seperti `internal/ledger/feepolicy` memiliki tabelnya sendiri) menjaga invarian itu tanpa membuka coupling baru, walau `boundary_test.go` sendiri akan MENGIZINKAN import lintas modul ini (ledger & policy sama-sama dimiliki ledger-service).
  - `internal/ledger/apperror`: sentinel baru `ErrUnknownKycTier = errors.New("UNKNOWN_KYC_TIER")` — kesalahan INPUT caller (level tak terdaftar di `policy_tier_limits`), bukan kegagalan bisnis, sehingga dipetakan terpisah ke `codes.InvalidArgument` (bukan lewat `mapError` generik yang mereservasi `FailedPrecondition` untuk kegagalan bisnis).
  - `internal/ledger/ledger.go`: `Module.ApplyKycTier(ctx, userID, kycLevel int32) error` — passthrough tipis ke `kycTierRepo.Apply`.
  - `internal/ledger/grpcserver/server.go`: `Service` interface + implementasi `ApplyKycTier` — `errors.Is(err, apperror.ErrUnknownKycTier)` dipetakan eksplisit ke `InvalidArgument`; error lain lewat `mapError` generik seperti biasa.
  - `pkg/ledgerclient/client.go`: `Client.ApplyKycTier(ctx, userID uuid.UUID, kycLevel int) error` — signature `int` (BUKAN `int32`) SENGAJA disamakan persis dengan `internal/auth.Provisioner` interface (lihat catatan T3) supaya `*ledgerclient.Client` memenuhi interface itu tanpa lapisan adaptor; konversi ke `int32` proto terjadi di dalam method ini.
  - `internal/testutil/ledger.go`: `LedgerHarness.ApplyKycTier(ctx, userID uuid.UUID, kycLevel int) error` — passthrough ke module, dipakai `internal/auth`'s integration test agar bisa menguji lewat `Provisioner` yang sama seperti produksi.
  - Call-site fallout: `internal/ledger/grpcserver/server_test.go`'s `fakeService` menambah `ApplyKycTier`.
- Test wajib: 4 integration test BARU di `internal/ledger/grpcserver/kyc_tier_integration_test.go` (Postgres riil via testcontainers + gRPC bufconn, sama pola `TestPostMoneyInEndToEndOverGRPC`) — `TestApplyKycTier_L1ThenL2_UpgradesInPlace` (apply L1 lalu L2, baris `policy_limits` yang SAMA berubah nilai, tetap 3 baris bukan 6), `TestApplyKycTier_PolicyEngineEnforcesNewCap` (`internal/policy.Engine.Check` nyata menegakkan cap L1 lalu cap L2 baru — Engine baru per fase untuk menghindari cache limit 60s bawaan Engine, bukan soal DB), `TestApplyKycTier_UnknownLevel_InvalidArgument` (level 3 → `codes.InvalidArgument`), `TestApplyKycTier_Idempotent_ApplyingTwiceIsOneResult` (apply L1 dua kali → tetap 3 baris, nilai sama). Semua PASS.
- Verifikasi: `go build`/`go vet` (default + `-tags=integration`) bersih; `gofmt -l` bersih (satu file tak terkait, pre-existing); `make lint` bersih; `make test` hijau.

## T6 — Fixture script + E2E + index README

### Langkah
1. **GOTCHA KRITIS (#9 master)**: semua script existing (business-e2e, smoke, chaos) membuat user lalu langsung transaksi — SEMUA pecah di gate KYC. Update fixture di `scripts/lib.sh`: helper pembuatan user melakukan KYC dance (submit L1 mock-approve → refresh token; untuk fixture yang butuh limit besar: submit L2 → approve via admin API) — SATU tempat, dipakai semua script. `provision_user` jalur psql/internal tetap bebas gate (internal listener).
2. `business-e2e.sh` journey KYC: register → transfer → 403 KYC_REQUIRED → submit L1 (auto-approve) → refresh token → transfer kecil OK → transfer di atas cap L1 → 422 policy limit → submit L2 (refer) → admin approve (:8083) → refresh → transfer besar OK.
3. Update `docs/plan/README.md`.

### Test wajib
- business-e2e + smoke + chaos SEMUA hijau dengan fixture baru (bukti gotcha #9 tertangani).

### DoD
- [x] Journey KYC lengkap terbukti; seluruh suite existing tetap hijau di bawah gating.

### Hasil
- `cmd/gentoken/main.go`: usage diperluas jadi `<user-id> [role] [ttl] [kyc_level]`, `kyc_level` DEFAULT **1** bila diomit. Ini SATU-satunya perubahan yang dibutuhkan `scripts/smoke-test.sh`/`scripts/chaos-test.sh` — keduanya sama sekali TIDAK memakai auth-service asli (memakai `provision_user`/`gen_token` langsung, bukan register/login), jadi men-default-kan level di titik minting token ini menutup gotcha #9 untuk keduanya SEKALIGUS tanpa menyentuh satu baris pun di kedua skrip tsb. `scripts/lib.sh`'s `gen_token` wrapper didokumentasikan ulang untuk menjelaskan default ini.
- `scripts/lib.sh` menambah dua helper KYC BARU (dipakai skrip yang benar-benar mendaftar user ASLI lewat auth-service — hanya `business-e2e.sh`):
  - `kyc_approve_l1(auth_port, access_token, refresh_token)`: submit L1 (mock auto-approve tanpa `mock_mode`) → refresh → mengembalikan RESPONS MENTAH refresh (bukan cuma `access_token`) karena refresh token BERPUTAR (single-use) — pemanggil WAJIB menyimpan `refresh_token` baru bila akan melakukan dance KEDUA (mis. L1 lalu L2) atau refresh kedua akan mereplay token yang sudah dicabut.
  - `kyc_submit_l2_and_admin_approve(auth_port, auth_internal_port, admin_token, access_token, refresh_token)`: submit L2 (mock SELALU refer) → admin approve via listener internal (:8083) → refresh.
- `scripts/business-e2e.sh` `onboard()`: menangkap `refresh_token` A/B dari respons login, memanggil `kyc_approve_l1` untuk KEDUANYA sebelum section manapun bertransaksi — ini SATU-satunya tempat perbaikan gotcha #9 untuk skrip ini, karena onboard() adalah satu-satunya titik registrasi user asli.
- **Section 3 BARU: `kyc_journey()`** (disisipkan setelah topup, sebelum transfer — perlu topup ROUTE yang sudah aktif dari section 2) — journey PERSIS Langkah #2 memakai user C (aktor) + D (penerima pasif, tak pernah post apa pun sehingga tak butuh KYC sama sekali — gate & policy engine sama-sama key di CALLER, bukan penerima):
  1. Register C, D. C login (L0, murni baru).
  2. C mencoba transfer → 403 `KYC_REQUIRED` (gerbang gateway menolak SEBELUM cek saldo/policy apa pun).
  3. Submit L1 (auto-approve) → refresh.
  4. Topup C 3.000.000 (di bawah cap `money_in` L1 5.000.000) — sekarang lolos gerbang.
  5. Transfer kecil 50.000 (jauh di bawah cap `transfer_p2p` L1 1.000.000) → sukses.
  6. Transfer 2.000.000 (DI ATAS cap L1) → 422 `policy limit exceeded`.
  7. Submit L2 (mock SELALU refer) → admin approve (:8083) → refresh.
  8. Transfer 2.000.000 yang SAMA kini sukses di bawah cap L2 (100.000.000).
- **Bug ditemukan & diperbaiki selama verifikasi lokal (staleness cache policy engine)**: langkah 8 di atas GAGAL pada run pertama dengan 422 walau `ApplyKycTier` sudah sukses — root cause: `internal/policy.Engine` (dikonstruksi SEKALI saat startup `ledger-service`, dipakai bersama semua request) meng-cache limit efektif per (`user_id`,`transaction_type`) dengan TTL 60 detik (docs/plan/17 T1) TANPA mekanisme invalidasi — `ApplyKycTier` mengubah baris `policy_limits` di DB, tapi cache in-process yang SUDAH menyimpan limit L1 (dari cek over-cap sesaat sebelumnya) tetap dipakai sampai TTL habis. Ini BUKAN bug baru — sudah menjadi tradeoff yang didokumentasikan sejak doc 17 ("limit change ... takes effect within this window ... no pub/sub invalidation needed") — kenaikan tier KYC mewarisi staleness yang SAMA karena keduanya menulis ke tabel `policy_limits` yang sama. Diperbaiki dengan menambah knob `POLICY_CACHE_TTL` (env, default tetap 60s di produksi — TIDAK ada perubahan perilaku produksi) yang di-wire ke `policy.WithCacheTTL` di `cmd/ledger-service/main.go` (`internal/config.LedgerConfig.PolicyCacheTTL`, pola persis `FeeQuoteTTL`); `business-e2e.sh` meng-ekspor `POLICY_CACHE_TTL=2s` untuk proses ledger-service milik skripnya sendiri SAJA, plus `sleep 3` eksplisit sebelum retry langkah 8 dengan komentar yang menjelaskan alasannya persis. Diverifikasi: journey lengkap PASS setelah perbaikan ini.
- **Bug ditemukan & diperbaiki selama verifikasi lokal (gerbang KYC tanpa efek di 2 dari 3 rute)** — sebenarnya ditemukan saat mengerjakan T4, dicatat ulang di sini karena persis inilah yang membuat gotcha #9 relevan: lihat Hasil T4 di atas untuk detail (`requireKYC` yang semula satu fungsi dipakai untuk tiga mount, gagal menggerbang `/payout`/`/topup` karena cek path internalnya tak pernah cocok di mount exact-match).
- Update `docs/plan/README.md`: baris index doc 39 status `⬜ todo` → `✅ done`.
- Verifikasi Test Wajib: `./scripts/business-e2e.sh` (dari `docker compose down -v` bersih) — SEMUA 8 section PASS termasuk section 3 KYC journey baru; `./scripts/smoke-test.sh` PASS TANPA perubahan sama sekali pada isi skripnya; `./scripts/chaos-test.sh all` — ketujuh skenario PASS TANPA perubahan sama sekali pada isi skripnya (bukti nyata gotcha #9 tertangani di SATU tempat, `gentoken`/`gen_token`). `go build`/`go vet` (default + `-tags=integration`) bersih; `gofmt -l` bersih (satu file tak terkait, pre-existing); `make lint` bersih; `make test` (race+cover, seluruh paket termasuk `internal/kycvendor`, `internal/auth`, `internal/handler`) hijau.

---

## Verifikasi akhir dokumen
Gate standar master doc 36 hijau semua → lanjut [40-phase7e-vendor-resilience.md](40-phase7e-vendor-resilience.md).

Dijalankan dari `docker compose down -v` (volume Postgres/Redis/RabbitMQ bersih) pada 2026-07-16, semua hijau:
- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l .` (satu file tak terkait — `internal/policy/alert_test.go`, untracked pre-existing, bukan bagian doc 39).
- `make lint` bersih.
- `make test` (race+cover, seluruh paket) hijau.
- `./scripts/smoke-test.sh` — semua assertion PASS, TANPA perubahan pada skrip.
- `./scripts/business-e2e.sh` — semua 8 section PASS termasuk KYC gate/tier journey T6.
- `./scripts/chaos-test.sh all` — ketujuh skenario PASS, TANPA perubahan pada skrip.

Doc 39 (KYC bertingkat) selesai penuh (T1–T6), termasuk dua bug produksi nyata yang ditemukan dan diperbaiki selama eksekusi (gerbang KYC yang diam-diam bypass di `/payout`/`/topup`; provisioner tier yang tidak pernah terhubung ke gRPC) → lanjut [40-phase7e-vendor-resilience.md](40-phase7e-vendor-resilience.md).
