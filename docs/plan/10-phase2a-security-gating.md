# 10 — Phase 2a: Security & API Gating

> Prasyarat: baca [09-hardening-review.md](09-hardening-review.md) bagian A dan D dulu — ini task-nya. Setiap task independen kecuali disebutkan. Jalankan `make test` setelah tiap task; jalankan `go test -tags=integration -race ./...` (Docker) sebelum menandai T1 dan T2 selesai (keduanya menyentuh wiring/SQL).

## T1 — Pisahkan router internal untuk tipe transaksi sistem

**Masalah**: `internal/ledger/transport/http.go:19-29` (`adminOnlyTypes`) hanya menggate 7 tipe lewat 1 router publik. Semua tipe lain (termasuk `money_in`, `refund`, semua `withdraw_*settle*`/`withdraw_cancel*`, `escrow_release`, `escrow_refund`, `fee_collect`) callable oleh user biasa lewat `/api/v1/ledger/transactions`.

**Keputusan (K1, lihat 09)**: pisahkan jadi dua router + dua listener HTTP:
- **Router publik** (yang sudah ada, `internal/handler/router.go`, port `APP_PORT`): hanya menerima tipe yang legitimate dipicu langsung oleh end-user: `transfer_p2p`, `transfer_pocket`, `withdraw_initiate`, `escrow_hold`. Semua tipe lain → 403.
- **Router internal baru**: menerima SEMUA tipe (termasuk yang di router publik, untuk fleksibilitas admin/ops), listen di port terpisah yang **default bind ke `127.0.0.1`** (`INTERNAL_APP_PORT`, default `8081`), dimaksudkan untuk dipanggil modul lain / payment-gateway-webhook-handler / ops tooling lewat jaringan internal (docker network, VPC), TIDAK pernah di-expose ke internet.

### Langkah

1. **`internal/ledger/transport/http.go`**: ganti `adminOnlyTypes` (map "harus admin") jadi dua whitelist eksplisit:
   ```go
   // publicUserTypes are the only transaction types reachable from the
   // public-facing router. Everything else — money movement to/from system
   // accounts (money_in, refund, withdraw settlement, escrow release,
   // fee_collect) plus compliance actions (freeze_*, adjustment_*, reversal,
   // chargeback) — is only reachable via the internal router (see NewInternalRouter).
   var publicUserTypes = map[string]bool{
       "transfer_p2p":     true,
       "transfer_pocket":  true,
       "withdraw_initiate": true,
       "escrow_hold":      true,
   }
   ```
2. Tambah `NewRouter(svc Service) http.Handler` tetap ada (dipakai publik) — di `postTransaction`, ganti cek `adminOnlyTypes[req.Type] && !isAdmin(r)` menjadi `!publicUserTypes[req.Type]` → `response.Forbidden(w, "this transaction type is not available on the public API")`.
3. Tambah `NewInternalRouter(svc Service) http.Handler` — sama seperti `NewRouter` tapi **tanpa** batasan `publicUserTypes` (semua tipe terdaftar di `processors.NewDefaultRegistry` boleh). Ekstrak handler struct/logic yang sudah ada supaya tidak duplikasi (satu `handler` struct, dua konstruktor mux yang beda daftar route guard). Pertimbangkan: `postTransaction` menerima parameter `allowedTypes map[string]bool` (nil = semua boleh) di-set lewat closure per-router, bukan duplikasi seluruh handler.
4. `isAdmin`/`adminOnlyTypes` yang lama untuk `freeze_*`, `adjustment_*`, `reversal`, `chargeback` **tetap dipertahankan sebagai lapis kedua di ROUTER INTERNAL** — meski internal, compliance actions tetap butuh role admin, defense in depth. Jadi router internal: semua tipe boleh KECUALI 7 tipe admin-only lama yang tetap butuh `isAdmin(r)`.
5. **`internal/ledger/ledger.go`**: `Module` tambah method `InternalRouter() http.Handler` yang memanggil `transport.NewInternalRouter(m)`.
6. **`internal/config/config.go`**: tambah field `AppConfig.InternalPort` dan `AppConfig.InternalBindAddr` (default `127.0.0.1`), env `INTERNAL_APP_PORT` default `"8081"`, `INTERNAL_APP_BIND_ADDR` default `"127.0.0.1"`. Validasi: di production, `InternalBindAddr` TIDAK BOLEH `"0.0.0.0"` — kalau operator eksplisit override, terima tapi log warning saat startup (jangan hard-fail, beberapa deployment container-network legitimate perlu 0.0.0.0 dengan firewall/security-group terpisah).
7. **`internal/handler/router.go`**: tambah fungsi `NewInternalRouter(cfg *config.Config, deps *Dependencies, logger *slog.Logger) http.Handler` — mount `deps.Ledger.InternalRouter()` di bawah `/api/v1/ledger/`, wrap dengan chain yang SAMA (auth + JSON) TAPI **tanpa rate limit publik** (internal caller sudah trusted network-level; kalau mau tetap rate-limit, gunakan limit jauh lebih tinggi, bukan wajib untuk task ini) — auth JWT tetap wajib (service token dengan role khusus, misal `role: "service"`, lihat T2 untuk idempotency scope). Middleware global (`WithSecurityHeaders`, dll) tetap dipakai untuk konsistensi tapi CORS tidak relevan (internal-only, tidak masalah tetap ada).
8. **`internal/server/server.go`** atau **`cmd/server/main.go`**: jalankan SATU proses, DUA `http.Server` pada goroutine terpisah, masing-masing `ListenAndServe()` di address berbeda (`:8080` publik, `127.0.0.1:8081` internal). Perhatikan graceful shutdown: kedua server harus di-`Shutdown(ctx)` saat SIGINT/SIGTERM, urutan sama seperti sekarang (drain HTTP dulu, baru cleanup dependencies) — cek existing `Server.StartWithSignals` di `internal/server/server.go` dan perluas jadi menerima >1 `*http.Server`, ATAU buat instance `Server` kedua dan jalankan `Start`-nya di goroutine dengan shared cleanup yang HANYA dipanggil sekali (pakai `sync.Once`).
9. **`.env.example`**: dokumentasikan `INTERNAL_APP_PORT=8081`, `INTERNAL_APP_BIND_ADDR=127.0.0.1`, dan catatan "jangan expose port ini ke internet — untuk service-to-service/ops saja".

### Test yang wajib ditulis
- `transport/http_test.go`: request `money_in` ke router PUBLIK → 403 dengan pesan yang jelas; request `money_in` ke router INTERNAL dengan JWT non-admin → 201 (tidak butuh admin karena bukan compliance action); request `freeze_initiate` ke router internal dengan JWT non-admin → 403 (masih butuh admin); `transfer_p2p` ke keduanya → 201.
- Integration/smoke: pastikan `curl localhost:8081/...` dari luar container gagal connect (bind ke `127.0.0.1` benar-benar tidak reachable dari host lain) — cukup didokumentasikan sebagai manual check di DoD, tidak perlu automated test untuk network binding.

### DoD
- [ ] `go build ./...`, `make test` hijau.
- [ ] Router publik menolak semua tipe di luar 4 whitelist dengan 403.
- [ ] Router internal menerima semua tipe, tetap admin-gate 7 tipe compliance lama.
- [ ] Dua listener HTTP aktif bersamaan, graceful shutdown menutup keduanya tanpa panic/goroutine leak (`go test -race`).
- [ ] `.env.example` dan README diperbarui menyebut port internal.

---

## T2 — Idempotency scope = userID

**Masalah**: `transport/http.go:100-107` membangun `processors.Command` tanpa pernah mengisi `IdempotencyScope`. Unique index `uq_ltx_idempotency (idempotency_key, COALESCE(idempotency_scope,''))` (`migrations/000001_ledger_core.up.sql:81-82`) membuat SEMUA user berbagi satu namespace idempotency key.

### Langkah
1. **`internal/ledger/transport/http.go`**, di `postTransaction` — setelah `userID` diambil dari JWT, isi:
   ```go
   cmd := processors.Command{
       IdempotencyKey:   req.IdempotencyKey,
       IdempotencyScope: userID.String(),
       ...
   }
   ```
2. Untuk router INTERNAL (T1): scope harus identitas si PEMANGGIL internal, bukan `userID` target transaksi (karena `userID` di internal call bisa jadi user yang DIKENAI transaksi, bukan yang memicu). Gunakan `claims.Subject` (service account ID) dari JWT internal jika berbeda dari `userID` target — untuk MVP task ini, cukup: scope = `"internal:" + claims.Role + ":" + userID.String()` ATAU field baru `req.IdempotencyScope` yang boleh diisi eksplisit oleh caller internal (trusted), fallback ke `userID.String()` kalau kosong. Pilih opsi kedua (field eksplisit) — lebih fleksibel untuk sistem pemanggil (payment gateway webhook) yang idempotency key-nya berasal dari provider eksternal per-transaksi, bukan per-user.
3. Tambah field `IdempotencyScope string \`json:"idempotency_scope,omitempty"\`` ke `postTransactionRequest` (dto.go) — TAPI **hanya dihormati di router internal**; di router publik field ini diabaikan/dioverride paksa jadi `userID.String()` supaya user tidak bisa spoof scope.

### Test yang wajib ditulis
- Unit test transport: dua user berbeda kirim `idempotency_key` yang SAMA via router publik → keduanya sukses sebagai transaksi independen (scope berbeda otomatis).
- Integration test (Docker): verifikasi di level DB — dua baris `ledger_transactions` dengan `idempotency_key` sama tapi `idempotency_scope` beda tidak melanggar unique index.

### DoD
- [ ] `make test` dan integration test hijau.
- [ ] User A dan User B mengirim idempotency_key sama → dua transaksi independen, bukan konflik.
- [ ] Replay key yang sama oleh user YANG SAMA tetap idempoten (regression pada perilaku existing — jangan sampai rusak).

---

## T3 — Metadata allowlist + fee dihitung server-side

**Masalah**: `metadata` dari body diteruskan verbatim ke processor (`http.go:106`); `gateway`, `fee_amount`, `fee_gateway` semua dibaca dari situ oleh processor (`processors/processors.go:182,196,210`). User bebas set fee sendiri dan pilih akun settlement/fee manapun.

### Langkah
1. **Buat fee policy config**: `internal/ledger/feepolicy/feepolicy.go` (package baru dalam modul ledger, TIDAK diimpor dari luar modul — internal detail). Struct:
   ```go
   type Policy struct {
       // key: "<transaction_type>:<gateway>" → fee rule
       rules map[string]Rule
   }
   type Rule struct {
       FlatMinorUnits   int64           // fixed fee in minor units
       PercentBasisPts  int64           // e.g. 150 = 1.5%
       FeeGateway       string          // which fee[gateway] account to credit
   }
   func (p *Policy) Resolve(txType, gateway string, amount decimal.Decimal) (feeAmount decimal.Decimal, feeGateway string, ok bool)
   ```
   Isi rules dari config/env sederhana untuk MVP (mis. hardcode default map + override lewat env `FEE_POLICY_JSON` kalau mau fleksibel — TIDAK wajib untuk task ini, hardcoded map cukup, tandai TODO untuk admin-configurable di Phase 3).
2. **`internal/ledger/transport/http.go`**: sebelum membangun `cmd.Metadata`, JANGAN teruskan `req.Metadata` mentah. Sebagai gantinya:
   - Validasi `gateway` (jika ada di `req.Metadata["gateway"]`) terhadap allowlist gateway yang dikenal (`internal/ledger/constant` — cek apakah sudah ada daftar gateway valid; kalau belum, tambahkan `var ValidGateways = map[string]bool{"bca": true, "gopay": true, "ovo": true, ...}` di `internal/ledger/constant/constant.go`, sesuaikan dengan gateway yang benar-benar dipakai di seed `000002_seed_system_accounts.up.sql`). Gateway tidak dikenal → 400.
   - **Hapus `fee_amount` dan `fee_gateway` dari path client sepenuhnya** di router PUBLIK — field itu tidak boleh ada di request body user-facing. Kalau client mengirimkannya, abaikan (jangan error keras supaya tidak breaking untuk client lama — cukup log warning + strip).
   - Panggil `feepolicy.Resolve(req.Type, gateway, amount)` untuk mendapat `fee_amount`/`fee_gateway` yang BENAR, lalu suntikkan ke `cmd.Metadata` sendiri (server yang mengisi, bukan client).
   - Sisa `req.Metadata` (kalau ada key lain seperti `note`, `external_ref`) di-allowlist: hanya key yang ada di daftar putih (`note`, `external_ref`, `authorized_by` — yang terakhir dipakai `adjustment_credit.go`) yang diteruskan; key lain di-drop diam-diam.
3. Router INTERNAL (T1): boleh mengizinkan `fee_amount`/`fee_gateway` eksplisit dari caller (trusted) — tidak melalui `feepolicy.Resolve` secara wajib, tapi tetap divalidasi `FeeAmountValidator` yang sudah ada.
4. Update `internal/ledger/processors/*.go` TIDAK perlu diubah — mereka tetap membaca dari `cmd.Metadata`, sumbernya saja yang berubah (server-injected, bukan client-passthrough).

### Test yang wajib ditulis
- Transport test: kirim `metadata: {"gateway":"bca","fee_amount":"1","fee_gateway":"attacker_controlled"}` ke router PUBLIK untuk `transfer_p2p` (tidak butuh fee) → `fee_amount`/`fee_gateway` client diabaikan, transaksi tetap posted tanpa fee client-controlled.
- Kirim `gateway: "not_a_real_bank"` → 400.
- Kirim `metadata: {"malicious_key": "x"}` → key tersebut tidak sampai ke processor (assert via mock service captured Command.Metadata).

### DoD
- [ ] `make test` hijau.
- [ ] Client tidak bisa lagi mengatur fee_amount/fee_gateway di router publik.
- [ ] Gateway divalidasi terhadap allowlist.
- [ ] Metadata lain di-allowlist.

---

## T4 — Amount wajib integral (minor unit)

**Masalah**: `decimal.NewFromString` menerima fraksional; `PositiveAmountValidator` hanya cek `>0`; `IntPart()` di repository men-truncate diam-diam.

### Langkah
1. **`internal/ledger/transport/dto.go`**: ubah `decimalFromString` jadi menerima parameter `currency string` (atau exponent), pakai `pkg/currency` (`currency.go` sudah ada `MinorUnit` per currency code) untuk memvalidasi:
   ```go
   func decimalFromString(s, currencyCode string) (decimal.Decimal, error) {
       amt, err := decimal.NewFromString(s)
       if err != nil {
           return decimal.Decimal{}, err
       }
       cur, ok := currency.Lookup(currencyCode) // tambahkan fungsi lookup di pkg/currency kalau belum ada exported getter
       if !ok {
           return decimal.Decimal{}, currency.ErrInvalidCurrency
       }
       if amt.Exponent() < -int32(cur.MinorUnit) || !amt.Truncate(int32(cur.MinorUnit)).Equal(amt) {
           return decimal.Decimal{}, fmt.Errorf("amount must have at most %d decimal places for %s", cur.MinorUnit, currencyCode)
       }
       return amt, nil
   }
   ```
   **Catatan**: `postTransactionRequest` saat ini TIDAK punya field currency eksplisit — currency ditentukan oleh akun (`ResolveAccounts`), bukan input user. Untuk MVP task ini, cukup validasi **amount adalah bilangan bulat (exponent >= 0)** di transport (tanpa tahu currency spesifik dulu — semua currency yang ada saat ini, IDR, punya MinorUnit=0, jadi ini equivalent). Tambahkan TODO comment: kalau multi-currency dengan MinorUnit>0 diaktifkan (lihat 08 S2), validasi harus pindah setelah `ResolveAccounts` tahu currency akun, atau currency wajib dikirim di body.
   Fungsi minimal untuk task ini:
   ```go
   func decimalFromString(s string) (decimal.Decimal, error) {
       amt, err := decimal.NewFromString(s)
       if err != nil {
           return decimal.Decimal{}, err
       }
       if !amt.Equal(amt.Truncate(0)) {
           return decimal.Decimal{}, errors.New("amount must be an integer (minor units, no fractional part)")
       }
       return amt, nil
   }
   ```
2. **`internal/ledger/processors/validators.go`**: tambah `IntegralAmountValidator{}` yang mengecek `!cmd.Amount.Equal(cmd.Amount.Truncate(0))` → `ErrValidation` — pasang sebagai lapis pertahanan kedua di `MultiValidator` untuk SETIAP processor yang saat ini punya `PositiveAmountValidator` (defense in depth: transport sudah menolak, tapi processor tidak boleh percaya buta ke caller manapun termasuk router internal/panggilan langsung `Module.Post` dari modul lain di masa depan).
3. **`internal/ledger/repository/account_balance_repository.go`**: `UpdateBalances` — ganti `newBalances[id].IntPart()` jadi validasi defensif: kalau `!newBalances[id].Equal(newBalances[id].Truncate(0))`, **return error** (`fmt.Errorf("internal invariant violated: non-integral balance for account %s: %s", id, newBalances[id])`) alih-alih diam-diam truncate. Ini adalah safety net terakhir — seharusnya tidak pernah kena kalau T4 langkah 1&2 benar, tapi mencegah silent money loss kalau ada jalur lain yang lolos.

### Test yang wajib ditulis
- Transport: `amount: "100.5"` → 400.
- Validator unit test: `IntegralAmountValidator` reject `100.5`, accept `100`.
- Repository: `UpdateBalances` dengan balance non-integral → error, bukan silent truncate (test dengan sqlmock cukup untuk ini karena hanya menguji Go-level guard sebelum SQL dikirim).

### DoD
- [ ] `make test` hijau.
- [ ] Amount fraksional ditolak di 3 lapis (transport, validator, repository safety net).
- [ ] `go test -tags=integration -race ./...` tetap hijau (regression check).

---

## T5 — Amount cap per tipe transaksi

**Masalah**: `MaxAmountValidator` ada tapi tidak diwire ke processor manapun; tidak ada limit di config.

### Langkah
1. **`internal/config/config.go`**: tambah `LedgerConfig` (atau extend config yang relevan) dengan `MaxAmountPerTxMinorUnits map[string]int64` — untuk MVP task ini cukup satu default global (bukan per-tipe): env `LEDGER_MAX_AMOUNT_PER_TX` (default besar tapi finite, mis. `1_000_000_000` — 1 miliar minor unit, sesuaikan skala bisnis realistis; dokumentasikan di `.env.example` bahwa ini adalah safety ceiling, bukan business limit — limit bisnis per-produk ada di [08 S1](08-phase-3-scale.md)).
2. **`internal/ledger/ledger.go`**: `NewModule` terima parameter tambahan `maxAmountPerTx decimal.Decimal` (atau `int64`), teruskan ke `processors.NewDefaultRegistry(accRepo, txRepo, maxAmountPerTx)`.
3. **`internal/ledger/processors/processors.go`**: `NewDefaultRegistry` terima parameter baru, teruskan ke setiap processor constructor yang butuh cap (atau — lebih simpel — pasang `MaxAmountValidator{Max: maxAmountPerTx}` secara SERAGAM di level `Service.execTransfer` (`service/handle/service.go`) SEBELUM memanggil `p.Validate` sebagai validator global tambahan, bukan per-processor. Ini lebih sedikit perubahan kode: satu tempat, bukan 22 processor constructor. **Pilih pendekatan ini** — tambahkan cek `if cmd.Amount.GreaterThan(s.maxAmountPerTx) { return apperror.ErrAmountTooLarge }` di awal `execTransfer` sebelum idempotency gate (fail fast, sebelum menyentuh DB).
4. `Service` (`service/handle/service.go`) tambah field `maxAmountPerTx decimal.Decimal`, di-set lewat `New(...)` constructor (tambah parameter).

### Test yang wajib ditulis
- Unit test service: `cmd.Amount` melebihi cap → error `ErrAmountTooLarge`, TIDAK ada row ditulis ke DB (assert lewat mock repo — no calls).
- Amount di bawah cap → proses normal (regression).

### DoD
- [ ] `make test` hijau.
- [ ] Cap dikonfigurasi via env, default masuk akal, didokumentasikan di `.env.example`.
- [ ] Cap dicek SEBELUM idempotency gate (tidak menulis row gagal untuk kasus ini — ini murni guard, bukan business validation yang perlu audit trail; tapi diskusikan: kalau tim ingin audit trail untuk percobaan amount-too-large juga, pindahkan setelah idempotency gate seperti business validation lain — DEFAULT untuk task ini: sebelum, karena ini bukan user error yang perlu di-track per idempotency key, ini abuse/bug signal).

---

## T6 — JWT iss/nbf, HSTS trust-proxy, /metrics protection

Task kecil, kerjakan sekaligus:

1. **JWT `iss`/`nbf`** (`pkg/middleware/auth.go`):
   - Tambah `Iss string` ke `Claims` struct, isi saat `GenerateToken` dari `cfg.JWT.Issuer` (yang sudah ada di config tapi tidak dipakai).
   - `ParseToken` (`:55-90`): setelah verifikasi HMAC, tambah cek `claims.Iss != expectedIssuer → error` (parameter baru di `ParseToken`, teruskan `cfg.JWT.Issuer` dari pemanggil `WithAuth`). Kalau `expectedIssuer` kosong (belum dikonfigurasi), skip cek ini (backward compatible) tapi log warning sekali saat startup kalau `JWT.Issuer` kosong di production (`config.go` validation, mirip pola `POSTGRES_SSL_MODE`).
   - `nbf` opsional untuk MVP ini — cukup dokumentasikan sebagai TODO di komentar kalau tidak ada use case sekarang (jangan over-engineer token-not-yet-valid tanpa kebutuhan nyata).
2. **HSTS trust-proxy** (`pkg/middleware/security.go:35`): ganti kondisi `r.TLS != nil` jadi juga menerima `r.Header.Get("X-Forwarded-Proto") == "https"` — TAPI hanya percaya header ini kalau `cfg.App.TrustProxyHeaders` (config baru, default `false`) diaktifkan eksplisit oleh operator (mencegah header spoofing kalau app diakses langsung tanpa proxy). Tambah `TrustProxyHeaders bool` ke `AppConfig`, env `TRUST_PROXY_HEADERS` default `false`.
3. **`/metrics` protection**: sudah ter-mitigasi oleh T1 kalau dipindah ke router internal (`127.0.0.1`) — pindahkan registrasi `root.Handle("GET /metrics", promhttp.Handler())` (`internal/handler/router.go:37`) dari router publik ke fungsi `NewInternalRouter` yang dibuat di T1 (langkah 7). Update README/`.env.example` note yang lama ("do not expose this port publicly") supaya konsisten — sekarang literally tidak bisa diakses publik karena beda listener.

### Test yang wajib ditulis
- `auth_test.go`: token dengan `iss` salah (saat `Issuer` dikonfigurasi) → ditolak; token tanpa cek issuer (Issuer config kosong) → tetap diterima (backward compat).
- `security_test.go`: `TrustProxyHeaders=true` + header `X-Forwarded-Proto: https` → HSTS diset; `TrustProxyHeaders=false` + header sama → HSTS TIDAK diset (default aman).

### DoD
- [ ] `make test` hijau.
- [ ] `/metrics` hanya reachable dari router internal.
- [ ] JWT issuer check aktif kalau dikonfigurasi, tidak breaking kalau tidak.
- [ ] HSTS proxy-aware, default tetap aman (tidak percaya header tanpa opt-in).

---

## Urutan Pengerjaan
T1 → T2 (T2 sedikit bergantung pada struktur router dari T1 untuk pembedaan scope publik/internal) → T3, T4, T5, T6 (independen satu sama lain, bisa paralel/urutan bebas).

## Verifikasi Akhir Fase 2a
```bash
go build ./...
make lint
make test
go test -tags=integration -race ./...   # wajib Docker aktif
```
Smoke test manual (lihat pola di sesi implementasi sebelumnya): jalankan `docker compose up -d`, migrate, start server, coba `money_in` lewat port publik (harus 403) dan lewat port internal (harus 201).
