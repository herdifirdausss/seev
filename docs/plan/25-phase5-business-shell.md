# 25 — Phase 5: Business Shell MVP (auth, topup intent, fee/revenue, notifikasi, ops)

Prasyarat: [22](22-phase4a-payin-vendorgw.md) + [23](23-phase4b-payout-orchestration.md) selesai. Keputusan terkunci yang dipakai: [01 D12](01-target-architecture.md) (auth = modul setelah ledger MVP), [24 outline `internal/auth`](24-extraction-playbook.md) (bentuk tabel/facade/JWT), [21 K-T5](21-service-topology-review.md) (prefix tabel per modul), aturan verifikasi [09](09-hardening-review.md) penuh.

**Tujuan**: menutup gap antara "mesin selesai" dan "bisnis jalan". Audit 2026-07-13 menemukan end user belum bisa memakai produk sama sekali (auth 501, provisioning tanpa route, top-up tidak bisa diinisiasi) dan bisnis belum menghasilkan revenue (fee policy kosong). Setelah dokumen ini: **register → login → top-up → transfer P2P berbayar → withdraw berbayar → notifikasi → ops harian melihat revenue** — semuanya jalan end-to-end dan dibuktikan `scripts/business-e2e.sh`.

**Keputusan bisnis** (user, 2026-07-13): revenue = fee **withdraw** + fee **transfer P2P** (topup gratis); notifikasi = **in-app inbox** (consumer RabbitMQ pertama); ops fixes ikut scope.

**Bukan scope**: email/push notification (in-app dulu); KYC/verifikasi identitas; payment intent kompleks (VA number per bank, QRIS — reference string dulu); fee di `withdraw_pending_settle` (jalur T+n tetap tanpa fee, dicatat); admin UI (tetap HTTP/JSON).

---

## T1 — Modul `internal/auth`: register / login / refresh / me

### Langkah
1. Migrasi `000021_auth.up.sql` + `.down.sql` (RLS FORCE + grants pola 000019/000020):
   ```sql
   CREATE TABLE auth_users (
       id         UUID PRIMARY KEY,
       email      TEXT NOT NULL,
       full_name  TEXT NOT NULL DEFAULT '',
       role       TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
       status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
       created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
       updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   CREATE UNIQUE INDEX idx_auth_users_email ON auth_users (lower(email));

   CREATE TABLE auth_credentials (
       user_id       UUID PRIMARY KEY REFERENCES auth_users(id),
       password_hash TEXT NOT NULL,
       updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
   );

   CREATE TABLE auth_refresh_tokens (
       id          UUID PRIMARY KEY,
       user_id     UUID NOT NULL REFERENCES auth_users(id),
       token_hash  TEXT NOT NULL UNIQUE,          -- SHA-256 hex dari token opaque
       expires_at  TIMESTAMPTZ NOT NULL,
       created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
       revoked_at  TIMESTAMPTZ NULL,
       replaced_by UUID NULL
   );
   CREATE INDEX idx_auth_refresh_user ON auth_refresh_tokens (user_id, expires_at);
   ```
2. Modul `internal/auth/{auth.go,http.go,errors.go,model/,repository/}` pola payin/payout. bcrypt cost 12 (`golang.org/x/crypto/bcrypt` — promosikan dari indirect). Refresh token: 32 byte random base64url, DB hanya simpan SHA-256; **rotasi** tiap `/auth/refresh` (revoke lama + `replaced_by`); reuse token yang sudah revoked → revoke SEMUA token user + 401 (replay containment).
3. JWT via `middleware.GenerateToken` dengan kontrak claims existing (UserID/Email/Role/Exp/Iss) — ledger/policy/middleware TIDAK berubah. Access expiry & refresh expiry dari config JWT existing.
4. `Register` → validasi → insert users+credentials → `ledger.ProvisionUser(ctx, id, DefaultCurrency)` via structural interface `Provisioner` (pola `payin.Poster`); ProvisionUser sudah idempotent (upsert) jadi retry aman. Login juga lazy re-provision kalau akun belum ada (self-heal register setengah jalan).
5. Router publik: ganti 4 placeholder 501 (`/auth/register|login|refresh`, `/users/me`) dengan handler nyata, nil-guarded pola Payout; endpoint auth di chain rate-limit `RateLimitByIPAndPath` TANPA JWT; `/users/me` tetap chain authed.
6. Bootstrap admin: env `AUTH_BOOTSTRAP_ADMIN_EMAIL/PASSWORD` → `EnsureBootstrapAdmin` idempotent di main.go (BUKAN seed migration — hash password jangan masuk VCS).

### Test wajib
- Unit: register (409 duplicate email case-insensitive), login (sukses/wrong password/disabled), refresh rotation, reuse-revoked → revoke-all + 401.
- Integration (testcontainers): register → login → JWT diverifikasi `middleware.WithAuth` existing → `ledger.ListAccounts` menunjukkan 4 akun ter-provision → `/users/me` mengembalikan profil.

### DoD
- [x] Register/login/refresh/me hidup di router publik (501 hilang); JWT yang diterbitkan lolos middleware existing tanpa perubahan apa pun di ledger/policy.

### Hasil
Terimplementasi penuh sesuai langkah: migrasi `000021_auth.up/down.sql` (tiga tabel + RLS FORCE + grants pola 000019/000020, up→down→up teruji); modul `internal/auth` (facade `Register/Login/Refresh/Me/UpdateMe/EnsureBootstrapAdmin`, bcrypt cost 12, refresh token opaque 32-byte → SHA-256 di DB, rotasi + reuse-revoked → revoke-all); JWT via `middleware.GenerateToken` dengan kontrak claims existing; `Register` → `Provisioner.ProvisionUser` (structural interface) + lazy re-provision saat login; 4 placeholder 501 di router publik diganti handler nyata (chain `authPublic` = rate-limit by IP tanpa JWT); bootstrap admin dari env `AUTH_BOOTSTRAP_ADMIN_EMAIL/PASSWORD` dipanggil idempotent di main.go.

Penyimpangan kecil dari draft: (1) `PUT /users/me` ikut diimplementasikan (update full_name) karena placeholder-nya memang sudah ada — bukan scope-creep, cuma mengganti stub; (2) error surface memakai sentinel `ErrEmailTaken/ErrInvalidCredentials/ErrUserDisabled/ErrInvalidRefreshToken` yang di-map ke 409/401/403/401 di http.go — `ErrInvalidCredentials` sengaja dipakai untuk "email tidak ditemukan" DAN "password salah" (jangan bocorkan eksistensi email); (3) rate limit auth memakai limiter global existing (10 req/menit per IP+path) — cukup untuk MVP, dicatat bahwa produksi perlu limiter khusus yang lebih ketat untuk login.

18 unit test + 5 integration test hijau (register→login→JWT lolos middleware existing→4 akun ledger ter-provision→me; duplicate email 409; refresh rotation; reuse-revoked revoke-all; bootstrap admin idempotent). `TestModuleBoundaries` hijau — `internal/auth` otomatis tercakup rule root-facade.

---

## T2 — Fee transfer_p2p + withdraw (revenue), boundary-clean

### Langkah
1. Config baru (`internal/config`): `FEE_TRANSFER_P2P_FLAT`, `FEE_TRANSFER_P2P_BPS`, `FEE_WITHDRAW_FLAT`, `FEE_WITHDRAW_BPS` (int64, minor units / basis points, default 0 = fee off), `DEFAULT_CURRENCY` (default `IDR`).
2. Facade ledger: re-export `type FeeRule = feepolicy.Rule` + `SetFeeRules(map[string]FeeRule)` + `ResolveFee(txType, gateway, currency string, amount decimal.Decimal) (fee decimal.Decimal, feeGateway string, ok bool)` — supaya `cmd/` dan `internal/payout` TIDAK PERNAH import `internal/ledger/feepolicy` (boundary rule 1). `main.go` membangun rules dari config: key `transfer_p2p::IDR` dan `withdraw_settle::IDR`.
3. Transport: `defaultFeePolicy` package-level diganti policy yang diinjeksi dari Module (fee transfer_p2p langsung jalan — `buildMetadata` sudah resolve sejak 10-T3).
4. **Fee withdraw dibebankan saat SETTLE, bukan initiate.** Alasan (verified): `validateOriginalForClose` menuntut amount persis sama saat close & cancel mengembalikan hold penuh — fee di initiate membuat settle gagal atau fee hangus saat cancel. Tambah inline-fee ke prosesor `withdraw_settle` meniru persis `escrow_release` (template validateOriginalForClose + inline fee): entries `[hold debit amount, settlement credit amount−fee, fee[platform] credit fee]`. Cancel = refund penuh, fee nol. User menerima `amount − fee` di rail bank (semantik deduct-from-amount, konsisten dengan semua inline fee lain).
5. Payout: interface `Poster` tambah `ResolveFee`; `settle()` resolve `("withdraw_settle", "", currency, amount)` → set `fee_amount`/`fee_gateway` metadata di command settle.

### Test wajib
- Unit prosesor `withdraw_settle` dengan fee: 3 leg seimbang, fee ≥ amount ditolak (`FeeAmountValidator`), tanpa rule → 2 leg seperti sebelumnya.
- Integration payout: settle membawa fee (saldo fee account naik), **cancel refund PENUH** (fee tidak terpotong), `fn_verify_ledger_balance` bersih.
- Transport: transfer_p2p menghasilkan 3 leg dengan fee dari rules; klien yang menyuplai `fee_amount` sendiri tetap di-strip.

### DoD
- [x] Dengan env fee di-set: P2P dan withdraw menghasilkan leg `fee_collect` ke `fee[platform]`; dengan env kosong: perilaku byte-identical dengan sebelum dokumen ini.

### Hasil
Terimplementasi sesuai langkah. Config `FeeConfig` + `DEFAULT_CURRENCY` di `internal/config` (validasi: fee negatif ditolak, BPS > 10000 ditolak). Facade: `ledger.FeeRule` re-export + `SetFeeRules` + `ResolveFee` — transport handler kini memegang `*feepolicy.Policy` injected (bukan package-level `defaultFeePolicy`); `SetFeeRules` dipanggil main.go SEBELUM serving traffic. Prosesor `withdraw_settle` mendapat inline-fee persis pola `escrow_release`: `ResolveAccounts` menambah leg fee via `resolveInlineFee`, `Validate` menambah `FeeAmountValidator`, `BuildEntries` split `[hold debit amount, settlement credit amount−fee, fee credit fee]`. Payout `Poster.ResolveFee` + `settle()` set metadata fee.

Temuan saat verifikasi: smoke test existing (`scripts/smoke-test.sh`) memakai `withdraw_settle` manual via internal router TANPA fee env — tetap hijau (fee off = perilaku lama, membuktikan DoD backward-compat). Fee semantik deduct-from-amount dikonfirmasi di integration test: user withdraw 200.000 dengan fee 2.500 → hold didebet 200.000, settlement dikredit 197.500, fee[platform] +2.500.

9 unit test prosesor baru + 4 integration test payout/transport hijau; `./scripts/chaos-test.sh 5` tetap hijau dengan fee env di-set (fee leg tidak merusak crash-safety resume).

---

## T3 — Topup intent (extend `internal/payin`)

### Langkah
1. Migrasi `000022_payin_topup_intents.up/down.sql`:
   ```sql
   CREATE TABLE payin_topup_intents (
       id               UUID PRIMARY KEY,          -- uuidv7
       reference        TEXT NOT NULL UNIQUE,      -- "TOP-<uuidv7>", yang user pakai di vendor
       user_id          UUID NOT NULL,
       amount           BIGINT NOT NULL CHECK (amount > 0),
       currency         CHAR(3) NOT NULL,
       vendor           TEXT NOT NULL,
       status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','settled','expired')),
       settled_event_id UUID NULL REFERENCES payin_webhook_events(id),
       expires_at       TIMESTAMPTZ NOT NULL,
       created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
       updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   CREATE INDEX idx_topup_intents_user ON payin_topup_intents (user_id, created_at DESC);
   ```
2. `POST /api/v1/topup` (authed): `{amount, vendor}` → validasi vendor terdaftar → currency via `GetUserCurrency` (extend `payin.Poster`) → intent pending, TTL dari config `TOPUP_INTENT_TTL` (default 24h) → return `{id, reference, expires_at, amount, vendor}`. `GET /api/v1/topup/{id}` (ownership, 404 non-owner).
3. Resolusi webhook: reference dibawa di field **`external_ref` yang sudah ada** (nol perubahan vendorgw/mockvendor). `HandleWebhook` setelah dedup: cari intent `pending` by `reference = ev.ExternalRef` → pakai `intent.user_id` (cross-check amount+currency; mismatch → event `failed`, jalur admin replay); tidak ketemu → **fallback ke `user_id` payload** (backward compatible — semua flow/test payin existing tetap jalan).
4. Settling idempotent dua-langkah (pola payout): post `money_in` dulu (idempotency key existing), lalu conditional `UPDATE ... SET status='settled' WHERE reference=$1 AND status='pending'` — di `postAndFinalize` supaya redelivery/replay menyembuhkan crash di tengah.
5. Expiry **lazy** (tanpa job): `pending AND expires_at < now()` diperlakukan expired saat dibaca (GET flip opportunistik); webhook untuk intent expired → business `failed`, admin replay bisa memaksa.

### Test wajib
- Unit: create intent (vendor tak dikenal 400), ownership GET, expiry lazy, webhook resolve intent user, fallback payload user_id, amount mismatch → failed.
- Integration: journey penuh intent → webhook ber-signature dengan `external_ref=reference` → saldo naik → intent `settled`; redelivery idempotent (satu money_in).

### DoD
- [x] User bisa memulai top-up dari API dan uang masuk ke akunnya via webhook vendor tanpa vendor pernah tahu `user_id` internal.

### Hasil
Terimplementasi sesuai langkah. Migrasi `000022` (up→down→up teruji). `POST /api/v1/topup` + `GET /api/v1/topup/{id}` mount di router publik pola payout (nil-guarded, chain authed). `payin.Poster` diperluas dengan `GetUserCurrency`. Resolusi webhook di `HandleWebhook`: intent lookup by `external_ref` SEBELUM `GetOrInsert` event (supaya `user_id` yang dipersist di event row sudah hasil resolusi); cross-check amount+currency; fallback ke payload `user_id` bila reference tidak match intent pending mana pun. Settle intent di `postAndFinalize` (conditional UPDATE, idempotent). Expiry lazy di kedua titik baca.

Penyimpangan kecil: draft menaruh intent lookup "setelah dedup" — implementasi menaruhnya SEBELUM `GetOrInsert` karena event row menyimpan `user_id` (kolom NOT NULL); resolusi harus terjadi dulu supaya event yang dipersist konsisten dengan user yang benar-benar dikredit. Redelivery tetap idempotent karena dedup by `(vendor, vendor_event_id)` tidak berubah.

11 unit test + 3 integration test hijau (journey penuh, redelivery satu money_in, mismatch → failed + replay-able).

---

## T4 — Notifikasi in-app (modul baru `internal/notify` — consumer RabbitMQ pertama)

### Langkah
1. Migrasi `000023_notify.up/down.sql`:
   ```sql
   CREATE TABLE notif_notifications (
       id         UUID PRIMARY KEY,
       user_id    UUID NOT NULL,
       event_id   UUID NOT NULL,
       type       TEXT NOT NULL,
       title      TEXT NOT NULL,
       body       TEXT NOT NULL,
       payload    JSONB NOT NULL DEFAULT '{}',
       read_at    TIMESTAMPTZ NULL,
       created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
       UNIQUE (event_id, user_id)                 -- dedup at-least-once delivery
   );
   CREATE INDEX idx_notif_user ON notif_notifications (user_id, created_at DESC);
   ```
2. Enrich event: field opsional `user_id`/`target_user_id` (`*uuid.UUID, omitempty`) di `events.TransactionPosted`, diisi `newPostedEvent` dari `cmd.UserID`/`cmd.TargetUserID`. Kebijakan versioning events: penambahan opsional = non-breaking (update golden tests, TANPA bump SchemaVersion). Ditolak: consumer query balik facade (N query + putus saat extraction).
3. Modul `internal/notify`: `Start(ctx)` declare queue `ledger.events.notifications` (routing key `TypeTransactionPosted`) → `broker.Consume` (PrefetchCount 10, MaxDeliveryAttempts 5). Handler: filter tipe `{money_in, transfer_p2p, withdraw_settle, withdraw_cancel}`; penerima: money_in/withdraw → `user_id`; transfer_p2p → keduanya ("terkirim"/"diterima"). `INSERT ... ON CONFLICT (event_id, user_id) DO NOTHING`; ack sukses/duplikat, nack error (parkir setelah max attempts). Import HANYA `internal/ledger/events` (exception boundary sah).
4. HTTP: `GET /api/v1/notifications?limit=&before=` (rows milik sendiri, keyset pagination) + `POST /api/v1/notifications/{id}/read` (ownership 404). Mount router publik chain authed. `Start/Stop` di main.go samping payout workers.

### Test wajib
- Unit handler: decode/filter/recipient mapping/dedup (mock repo).
- Golden events: field baru muncul di JSON, schema version tidak bump.
- Integration real stack: post money_in → row notifikasi muncul ≤ N detik; duplikat delivery → tetap 1 row.

### DoD
- [x] User melihat notifikasi "dana masuk / transfer terkirim-diterima / withdraw berhasil-dibatalkan" di inbox API-nya, digerakkan oleh event outbox yang sama yang sudah ada.

### Hasil
Terimplementasi sesuai langkah. Event enrichment: `events.TransactionPosted` + `UserID`/`TargetUserID` opsional (golden test diperbarui, SchemaVersion tetap 1). Modul `internal/notify` — consumer RabbitMQ pertama di codebase: queue `ledger.events.notifications` bind ke exchange existing, `broker.Consume` dengan ack/nack + MaxDeliveryAttempts 5. Judul/body notifikasi dalam Bahasa Indonesia ("Dana masuk", "Transfer terkirim/diterima", "Penarikan berhasil/dibatalkan") dengan amount diformat minor-units apa adanya di `payload` JSONB (formatting presentasi = urusan client).

Catatan operasional yang ditemukan saat integration test: notifikasi withdraw_cancel juga terpicu dari admin cancel payout (jalur yang sama `withdraw_cancel`) — perilaku benar (user memang perlu tahu), dicatat sebagai perilaku, bukan bug. Konsumer memakai `messaging.Broker` interface existing — worker disabled (`WORKER_ENABLED=false`) berarti notify juga tidak jalan, konsisten dengan outbox relay.

9 unit test + golden + 2 integration test hijau (money_in → row muncul; duplicate delivery → 1 row).

---

## T5 — Ops fixes

### Langkah
1. `GET /admin/recon/batches?limit=&offset=` — list batches terbaru dulu (repo `ListBatches` + facade + route internal router admin-gated pola siblings).
2. `GET /admin/outbox/dead?limit=&offset=` — list dead events (id, event_type, retry_count, last_error, created_at) supaya operator tidak perlu SQL sebelum replay.
3. Policy fail-open alert: `policy.Engine` dapat optional `alerting.AlertFunc` (internal/policy → pkg/alerting legal); SEMUA branch fail-open (load limit gagal, counter unavailable) juga fire alert `severity=warning`, throttle 1×/60 detik via atomic timestamp (Redis outage ≠ alert storm). Wire dari `AlertWebhookURL` di main.go.

### Test wajib
- Unit: list handlers (pagination validation, admin-gate 403); policy alert fired-once-per-window.
- Integration: dead event muncul di list → replay → hilang dari list.

### DoD
- [x] Operator bisa melihat recon batches & dead events tanpa akses SQL; Redis down memicu alert, bukan cuma warn log.

### Hasil
Terimplementasi sesuai langkah. `ListBatches`/`ListDead` repo+facade+route (internal router, admin-gated, pagination pola payin/payout list). Policy: `Engine.SetAlerter(alerting.AlertFunc)` — semua 3 branch fail-open fire alert dengan throttle 60 detik (`atomic.Int64` last-fired unix); alert message menyebut dimensi yang gagal supaya operator tahu limit mana yang sedang tidak terproteksi. Wire di main.go dari `AlertWebhookURL` yang sama dengan verifier.

7 unit test + 2 integration test hijau.

---

## T6 — `scripts/business-e2e.sh` + verifikasi akhir

### Langkah
Skrip baru `scripts/business-e2e.sh` sourcing `scripts/lib.sh` (set env fee + bootstrap admin + TTL sebelum start server), menjalankan **journey bisnis lengkap sebagai acceptance test MVP**:

1. **Onboarding**: register 2 user via `POST /auth/register` → login → JWT asli (bukan gentoken) → akun ledger ter-provision otomatis.
2. **Top-up**: A `POST /topup {500000, mockvendor}` → dapat reference → webhook mockvendor ber-signature `external_ref=reference` → saldo A 500.000, intent settled, notifikasi "dana masuk".
3. **Transfer P2P berbayar**: A transfer 100.000 ke B → A −100.000, B +(100.000−fee), fee[platform] +fee → notifikasi A "terkirim" & B "diterima".
4. **Withdraw berbayar**: A `POST /payout {200000}` → vendor settle → hold −200.000, settlement +(200.000−fee_w), fee[platform] +fee_w → notifikasi "berhasil". Kasus cancel (async + admin cancel): refund PENUH, fee nol.
5. **Ops harian**: `fn_verify_ledger_balance` 0 baris; `v_account_balance_audit` konsisten; `GET /admin/reports/position` → saldo fee account == revenue yang diharapkan; `GET /admin/outbox/dead` kosong; `GET /admin/recon/batches` hidup.

Verifikasi akhir standar:
```bash
go build ./... && go vet ./... && go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all
./scripts/smoke-test.sh all
./scripts/business-e2e.sh
```
Migrasi 000021–000023 up→down→up teruji. Setelah selesai: DoD + "Hasil" di dokumen ini, status di [README.md](README.md).

### DoD
- [x] `./scripts/business-e2e.sh` hijau end-to-end — definisi operasional "MVP bisa dijalankan dari end user sampai ops harian yang menghasilkan revenue".
- [x] `./scripts/chaos-test.sh all` + `./scripts/smoke-test.sh all` tetap hijau (fee leg + modul baru tidak merusak crash-safety).

### Hasil
`scripts/business-e2e.sh` (sourcing `scripts/lib.sh`, env `FEE_TRANSFER_P2P_FLAT=1000 FEE_WITHDRAW_FLAT=2500 AUTH_BOOTSTRAP_ADMIN_*`) — 27 assertion hijau mencakup seluruh journey di atas termasuk assertion revenue: setelah 1×P2P + 1×withdraw settle, saldo `fee[platform]` == 3.500 persis. Verifikasi akhir: build/vet hijau, `make test` hijau, integration suite hijau (3 flaky pre-existing di internal/ledger schema-contract & policy TTL — bukan regresi, terjadi juga di baseline sebelum dokumen ini, dicatat), `chaos-test.sh all` hijau dari volume fresh, `smoke-test.sh all` hijau, `business-e2e.sh` hijau. Migrasi 000021–000023 up→down→up teruji.

---

## Verifikasi akhir dokumen

Lihat T6 — `business-e2e.sh` ADALAH verifikasi akhir dokumen ini, plus sweep standar build/test/integration/chaos/smoke.
