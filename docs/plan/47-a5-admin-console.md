# 47 — Track A5: Admin Console — BFF Tipis, Sesi Admin, Maker/Checker per Role, Audit Log, UI htmx

> Lahir dari track **A5** di [42-long-term-roadmap.md](42-long-term-roadmap.md).
>
> **Status verifikasi: SIAP DIEKSEKUSI (2026-07-19).** Semua fakta kode
> (path, identifier, route, sequence migrasi) diverifikasi langsung terhadap
> repo pada tanggal tersebut. Fakta EKSTERNAL (versi htmx dan PicoCSS
> terkini) sengaja TIDAK ditulis — eksekutor wajib memverifikasinya saat
> eksekusi (§6 butir 4). Line reference bergeser; verifikasi dengan grep.
> Inventori route admin di [24](24-extraction-playbook.md) adalah PETA,
> router live adalah KEBENARAN (§6 butir 7).

## 1. Trigger dan tujuan

Bukti trigger (pola doc 42 §2 poin 1, jalur trigger belajar):

- **36–41 selesai** (acceptance final [41](41-phase7f-mvp-acceptance.md)
  hijau) dan dependensi track A5 semuanya hijau: inventori route internal
  [24](24-extraction-playbook.md) (beku 2026-07-12, mendefinisikan admin
  console = service BARU pemanggil internal API — bukan split), admin
  surface [33](33-phase6h-fee-rules.md) (fee rules),
  [39](39-phase7d-kyc-tiers.md) (KYC review), [40](40-phase7e-vendor-resilience.md)
  (vendor health/routing). Track A3 ([45](45-a3-external-resilience.md))
  juga selesai dan MENAMBAH kebutuhan baru: replay dead vendor-command
  payout hanya bisa lewat SQL manual hari ini.
- **Keputusan sadar 2026-07-19**: user mengaktifkan A5 sebagai track kelima,
  dengan tiga keputusan desain diambil eksplisit lewat sesi tanya-jawab:
  frontend = **Go html/template + htmx, tanpa Node** (K8), model authn
  admin = **sesi BFF + peran maker/checker di auth-service** (K4/K5/K6),
  lingkup = **semua panel sketsa A5 dalam satu dokumen ini**.

Catatan jujur status track lain: [44](44-a2-ci-pipeline.md) (A2 CI) dan
[46](46-a4-compliance.md) (A4 compliance) BELUM dieksekusi saat dokumen ini
ditulis — dokumen ini TIDAK bergantung pada keduanya, dan panel fraud (T6)
hanya mencakup surface live hari ini (daftar events), BUKAN endpoint mode
per-rule yang baru direncanakan doc 46.

Tujuan bisnis (dari track A5): operasi harian — recon, maker-checker,
replay, fee/routing rules, KYC review, payout stuck — hari ini = curl ke
listener internal dengan token hasil `gentoken`; operator non-engineer
tidak bisa bekerja. Hutang terdokumentasi yang dilunasi:

| Hutang | Sumber | Dilunasi oleh |
|---|---|---|
| "Dedicated admin BFF instead of exposing service-specific admin surfaces" | PROJECT_GUIDE.md Known future work | seluruh dokumen |
| Operasi admin = curl manual ke enam listener internal | doc 42 §A5 | T5/T6 |
| Replay dead vendor-command payout tanpa endpoint HTTP (repo method saja) | gap doc 45 T0/T1 | T4 (K9) |
| Tidak ada audit trail aksi admin di service mana pun | doc 42 §A5 sketsa (3) | T3 (K7) |
| Pemisahan ROLE maker/checker tidak ada — setiap `admin` bisa create DAN approve (self-approval SUDAH dicegah, lihat §2) | limitasi doc 16 K8 | T3 (K6) |
| KYC review hanya via curl (endpoint doc 39 ada, UI tidak) | doc 39 | T6 |

## 2. Fakta repo saat dokumen ditulis

Semua diverifikasi 2026-07-19.

**Admin surface per service (target agregasi BFF).** Semua digate JWT user
dengan `role=="admin"` — helper inline `isAdmin(r)` di ledger/policy/payin/
payout/fraud, `middleware.WithRole("admin")` (variadic,
`pkg/middleware/auth.go:140`) hanya di auth-service:

- **ledger-service**, listener internal `:8091` (dev 18091), dirakit di
  `cmd/ledger-service/main.go` `internalRouter()` — mount
  `module.InternalRouter()` di bawah `/api/v1/ledger/` dan
  `/api/v1/admin/ledger/`, plus policy admin di `/api/v1/admin/policy/`.
  Route admin terdaftar di `internal/ledger/transport/http.go` `mux()`:
  outbox dead list/`{id}`/replay/replay-all; **adjustments
  create/approve/reject/list/get** (satu-satunya maker-checker, tabel
  `ledger_adjustments`, satu-satunya jalur `adjustment_credit/debit`);
  recon batches create/list/get + `items/{id}/resolve` (bermuara ke
  adjustments, tidak pernah memindah uang langsung); schedules/run;
  disbursements; savings; reports/`{kind}`; fee-rules list/create/update.
  Policy: `PUT/GET /admin/policy/limits` (`internal/policy/http.go:28`).
- **auth-service**, listener internal `:8083` (dev 18083),
  `cmd/auth-service/router.go` `internalRouter()`: KYC
  `GET /api/v1/admin/kyc/submissions`, `POST .../{id}/approve`,
  `POST .../{id}/reject`.
- **payin-service** `:8092` (dev 18092), `internal/payin/http.go` +
  `routing_http.go`: events list + `events/{id}/replay`; routing-rules
  CRUD; vendor-gateways get/put; vendors/health.
- **payout-service** `:8093` (dev 18093), `internal/payout/http.go` +
  `routing_http.go`: requests list + `{id}/cancel` + `{id}/retry`;
  routing-rules CRUD; vendor-gateways; `vendors/{vendor}/force-fail`;
  vendors/health. **GAP**: `ReplayDeadCommand` /
  `ReplayAllDeadCommands` ada di interface + implementasi repository
  (`internal/payout/repository/vendor_command_repository.go:95,100,447,469`)
  tapi TIDAK ada transport HTTP yang memanggilnya — doc 45 hanya membangun
  repo + relay; replay dead command hari ini = SQL manual. Juga TIDAK ada
  method list dead command (hanya `CountCommandsByStatuses`).
- **fraud-service** `:8094` (dev 18094), `internal/fraud/http.go:27`:
  `GET /api/v1/admin/fraud/events` SAJA. Tidak ada rule-config CRUD.

**Maker-checker hari ini.** `internal/ledger/service/adjustments/adjustments.go`:
`Approve()` (baris 146) SUDAH menolak self-approval —
`pa.RequestedBy == approverID` → `apperror.ErrSelfApproval` (baris 151),
dan approver dicatat sebagai `authorized_by` di metadata posting. Yang
TIDAK ada: pemisahan role — `approveAdjustment` transport
(`internal/ledger/transport/http.go:1061`) hanya cek `isAdmin(r)`
(`role=="admin"`), jadi SETIAP admin boleh jadi maker sekaligus checker
untuk request yang berbeda; tidak ada role yang HANYA boleh create atau
HANYA boleh approve.

**Authn.** JWT HS256 shared (`pkg/middleware/auth.go`,
`Claims{sub,email,role,kyc_level,exp,iss}`); `JWT_SECRET`+`JWT_ISSUER`
sama di semua service (PROJECT_GUIDE.md hard rule 6). Role disimpan di
`auth_users.role` dengan CHECK constraint
`role IN ('user','admin')` (`migrations/auth/000001_auth.up.sql:13`;
konstanta `internal/auth/model/model.go:13`). Bootstrap admin idempoten:
`EnsureBootstrapAdmin` (`internal/auth/auth.go:276`), env
`AUTH_BOOTSTRAP_ADMIN_EMAIL`/`_PASSWORD` (`internal/config/config.go:469`,
tervalidasi harus berpasangan). `cmd/gentoken` mencetak token role apa pun
(dev/test); `gen_token <uid> admin` dipakai fixture ketiga script gate.

**Audit.** TIDAK ada audit trail aksi admin di mana pun. Queue
`ledger.events.audit` adalah audit EVENT ledger (binding `#`), bukan log
siapa-memanggil-endpoint-admin-apa. Field `created_by`/`requested_by`/
`approved_by` adalah atribusi domain per-tabel, bukan log terpusat.

**Wiring service baru (template + titik sentuh).** Template terkecil =
`cmd/fraud-service` (`main.go` + `router.go`): flag `-healthcheck`, loader
config tipis (`internal/config/config.go:307-327` — perlu
`LoadAdminBFFService()` baru), default port di-set inline di main.go,
chain middleware standar (`WithRequestID, WithRoutePattern, WithTracing,
WithHTTPMetrics, WithLogger, WithRecovery, WithSecurityHeaders,
WithTimeout`), `GET /health` + `GET /metrics` (promhttp). Titik sentuh
service ketujuh:

- `docker-compose.yml`: blok profile `app` + `build.args.SERVICE` (satu
  Dockerfile multi-target, `ARG SERVICE`).
- `Makefile` `build-all` (baris 17-25): enam baris `go build` hardcoded —
  tambah baris ketujuh (dan perbarui komentar "six").
- `scripts/lib.sh`: fungsi `start_*_service` per service; port host yang
  sudah terpakai 18080-18094 + 18193 (replica payout doc 45), gRPC
  19091-19094 + 19193 — **bebas: app 18095 (container 8095)**; BFF tidak
  butuh gRPC.
- `boundary_test.go` (root): map `serviceModules` (baris 42-50) — tambah
  `"admin-bff-service": {"adminbff": true}`.
- `scripts/postgres-init/02-service-dbs.sh` + `03-service-migrations.sh`:
  KEDUANYA hardcode list `ledger auth payin payout fraud gateway` — DB
  `seev_adminbff` + role `adminbff_app` berarti menyentuh keduanya.
- `.github/workflows/ci.yml`: enumerasi service di DUA titik — daftar
  target bake (baris 204-211) dan loop verifikasi label revision
  (baris 224).
- `.env.example`: satu blok komentar per service.

**Sequence migrasi live**: `migrations/auth/` terakhir = `000002_kyc` —
tapi [46](46-a4-compliance.md) (belum dieksekusi) sudah MEMESAN
`migrations/auth/000003` untuk outbox KYC. Lihat §6 butir 8.

**Helper e2e yang bisa dipakai ulang**: `scripts/lib.sh` punya
`gen_token`, `provision_user`, `kyc_approve_l1`/`kyc_submit_l2_and_admin_approve`
(dipakai `business-e2e.sh`) — `admin-e2e.sh` (T6) memakai lifecycle lib.sh
yang sama (source SEKALI, pola PROJECT_GUIDE.md Debugging).

## 3. Anti-scope

Disalin dari doc 42 §A5 + turunannya:

- **BFF tetap tipis** — TIDAK ada logika bisnis pindah ke BFF: validasi
  bisnis, guard uang, dan keputusan tetap di service pemilik data; BFF
  hanya authn/z, agregasi, audit, dan render.
- **Bukan multi-tenant** (doc 42).
- Bukan SSO/OIDC/2FA/WebAuthn — login memakai kredensial auth-service
  existing; hardening identitas → track A6.
- Bukan RBAC engine — tepat TIGA role admin (`admin`, `admin_maker`,
  `admin_checker`), tanpa permission matrix/granular scopes.
- Tanpa Node/npm/bundler; tanpa WebSocket/SSE — refresh data = polling
  htmx sederhana.
- TIDAK menambah endpoint baru ke service lain KECUALI dua yang eksplisit:
  endpoint dead vendor-command payout (K9) dan pengetatan role
  maker/checker di ledger (K6).
- Retensi/purge `audit_log` dan `sessions` lama → track A8; dokumen ini
  hanya mengerjakan cleanup session EXPIRED (job terjadwal).
- Panel breaker = tampilan read-only `vendors/health`; TIDAK menyentuh
  semantics breaker (doc 40/45).
- Panel fraud = daftar events live hari ini SAJA; UI mode per-rule
  menyusul JIKA doc 46 tereksekusi (handoff §8).

## 4. Keputusan desain terkunci

### K1 — Bentuk service: `admin-bff-service`, HTTP-only, tanpa gRPC

Service ketujuh: `cmd/admin-bff-service` (main.go + router.go, template
wiring = fraud-service) + module `internal/adminbff`. Port container 8095,
host dev 18095. TIDAK ada listener gRPC — BFF tidak dipanggil service lain
dan tidak memanggil gRPC (K2). `serviceModules` di `boundary_test.go`
mendapat entri `"admin-bff-service": {"adminbff": true}`; module
`adminbff` TIDAK boleh diimport service lain, dan `internal/adminbff`
TIDAK mengimport module milik service lain (klien HTTP-nya berbicara wire,
bukan import Go). Loader config baru `LoadAdminBFFService()`.

### K2 — Downstream = HTTP; pengecualian terdokumentasi atas aturan "sync = gRPC"

BFF memanggil admin API keenam service lewat HTTP internal. Ini
pengecualian SADAR atas aturan PROJECT_GUIDE.md "komunikasi sinkron memakai
kontrak gRPC `gen/*`", dengan justifikasi: (a) seluruh admin surface
existing HTTP-only dan kontraknya sudah beku sebagai inventori doc 24;
(b) membuat mirror gRPC admin di enam service = churn masif yang justru
melanggar anti-scope "BFF tetap tipis". Klien = typed thin clients per
service di `internal/adminbff/client/` — marshal/unmarshal + mapping error
seragam (4xx downstream → pesan operator; 5xx/timeout → "service X tidak
tersedia") SAJA, tanpa logika bisnis. Semua request keluar membawa JWT
hasil K5 + timeout eksplisit per-call.

### K3 — DB sendiri: `seev_adminbff` (sessions + audit_log)

Audit log dan session butuh persistence, dan BFF HARAM menyentuh DB
service lain (PROJECT_GUIDE.md hard rule; sketsa A5 butir 5) → DB ketujuh
`seev_adminbff`, role `adminbff_app`, folder `migrations/adminbff/`
mulai `000001` (tabel `sessions`, `audit_log`; grant + RLS mengikuti pola
service lain, mis. `migrations/payout/000006`). Menyentuh
`02-service-dbs.sh` + `03-service-migrations.sh` (kedua list hardcoded)
dan target `migrate-up SERVICE=adminbff` di Makefile.

### K4 — Session server-side di DB, cookie HttpOnly, CSRF wajib

Alur login: halaman login BFF → BFF meneruskan kredensial ke endpoint
login auth-service → jika sukses DAN role hasil login ∈ {admin,
admin_maker, admin_checker} → insert row `sessions` (id acak 256-bit
crypto/rand, sub+email+role operator, `created_at`, `last_seen_at`;
idle TTL 30m, absolute TTL 8h) → set cookie `HttpOnly` + `Secure` +
`SameSite=Lax` berisi session id SAJA (bukan JWT). Server-side (bukan
signed-stateless cookie) karena revocable: logout = DELETE row, dan
operator yang dicabut role-nya mati sesinya saat validasi berikutnya.
Login akun non-admin → 403 generik tanpa membocorkan eksistensi akun.
CSRF: token acak per-session, di-embed di setiap form template, divalidasi
di SEMUA endpoint mutasi BFF; tolak tanpa/ salah token. Cleanup session
expired = job terjadwal pola `pkg/scheduler` existing.

### K5 — Token downstream: BFF mint admin-JWT per-request, TTL 60 detik

BFF memegang `JWT_SECRET` (sudah shared lintas service — bukan secret
baru) dan mencetak JWT berisi `sub`/`email`/`role` OPERATOR yang sedang
login untuk SETIAP request keluar, TTL 60s. Dengan ini identitas aktor
sampai utuh ke hilir: `requested_by`/`approved_by` maker-checker ledger
terisi operator asli (bukan identitas service BFF), dan `ErrSelfApproval
`+ penegakan K6 di ledger bekerja lintas-BFF maupun lintas-curl. Alternatif
menyimpan access/refresh token hasil login DITOLAK: liability penyimpanan
token panjang-umur + kompleksitas refresh; TTL 60s lebih pendek dari
access token mana pun.

### K6 — Maker/checker: role baru di auth_users + penegakan DOWNSTREAM di ledger

Dua nilai role baru di `auth_users`: `admin_maker` dan `admin_checker`
(migrasi auth: perlebar CHECK constraint
`role IN ('user','admin','admin_maker','admin_checker')`; konstanta baru
di `internal/auth/model`). Role `admin` lama = SUPERUSER, tetap sah untuk
semua aksi — kompatibilitas `gentoken`/fixture ketiga script gate (gotcha
#9 doc 39) dan bootstrap existing tidak berubah. Bootstrap maker/checker
opsional via pasangan env baru mengikuti pola `EnsureBootstrapAdmin`.

Penegakan (jujur fintech, di service PEMILIK DATA, bukan hanya BFF):

- Ledger `createAdjustment`: role ∈ {admin, admin_maker}.
- Ledger `approveAdjustment`/`rejectAdjustment`: role ∈ {admin,
  admin_checker}; guard self-approval existing
  (`adjustments.go:151`) TIDAK disentuh — tetap berlaku bahkan untuk
  superuser `admin`.
- `resolveReconItem` (bermuara ke create adjustment): role ∈ {admin,
  admin_maker}.
- SEMUA endpoint admin lain (read-only maupun mutasi non-maker-checker) di
  keenam service: menerima KETIGA role — sweep semua `isAdmin(r)` inline
  + `WithRole("admin")` auth-service menjadi menerima
  {admin, admin_maker, admin_checker} (WithRole sudah variadic).

BFF juga menegakkan di UI (maker tidak melihat tombol approve, checker
tidak melihat form create) — defense-in-depth; sumber kebenaran tetap
ledger, dibuktikan test curl-langsung-tanpa-BFF.

### K7 — Audit log append-only di BFF

Middleware pada SEMUA proxy mutasi (non-GET) mencatat satu row
`audit_log`: sub+email operator, timestamp, method, route pattern BFF,
service target, resource id (dari path), outcome (status HTTP downstream),
`request_id`. Body TIDAK disimpan mentah — hanya ringkasan field
non-sensitif (masking mengikuti konvensi `pkg/logger`: tanpa secret/PII;
amount boleh karena ini konteks admin internal, BUKAN log publik —
keputusan sadar, didokumentasikan di kode). GET tidak dicatat. Tidak ada
endpoint delete/update audit. UI: halaman list read-only + filter
operator/service/rentang waktu. Kegagalan tulis audit → mutasi TETAP
diteruskan + counter `adminbff_audit_write_failures_total` naik + log
ERROR (fail-open audit; alternatif fail-closed ditolak karena BFF bukan
jalur uang — uang punya audit domainnya sendiri di ledger).

### K8 — UI: html/template + htmx + PicoCSS, semuanya go:embed

`internal/adminbff/web/`: template `html/template`, asset statis
(htmx.min.js, pico.min.css) di-vendor ke repo dan di-serve via `go:embed`
— zero CDN, zero Node. **Versi htmx dan PicoCSS diverifikasi eksekutor
saat eksekusi** (pelajaran doc 43: fakta eksternal tidak ditulis dari
ingatan); file + checksum dicatat di Hasil T5. Browser bicara
form-urlencoded HANYA ke BFF; BFF menerjemahkan ke JSON downstream —
middleware `RequireJSON` service lain tidak pernah dilanggar karena htmx
tidak pernah menyentuh mereka langsung. Polling htmx ringan (interval
≥10s) untuk queue/stuck; tanpa SSE/WebSocket.

### K9 — Endpoint dead vendor-command payout (perubahan payout-service)

Melunasi gap doc 45: tiga route baru di admin payout-service, transport
tipis mencerminkan pola outbox admin ledger
(`/admin/outbox/dead*` di `internal/ledger/transport/http.go`):

- `GET  /admin/payout/vendor-commands/dead` — perlu method repo BARU
  `ListDeadCommands` (belum ada; cermin `listDeadEvents` ledger).
- `POST /admin/payout/vendor-commands/dead/{id}/replay` →
  `ReplayDeadCommand` existing.
- `POST /admin/payout/vendor-commands/dead/replay-all` →
  `ReplayAllDeadCommands` existing (cap + `older_than`, sama seperti
  ledger).

### K10 — Observability minimal; TANPA chaos scenario baru

Chain middleware standar sudah memberi RED metrics gratis. Tambahan hanya:
counter `adminbff_audit_write_failures_total` (K7) + satu panel kecil di
dashboard existing (pola doc 43). Chaos scenario BARU tidak ditambah —
justifikasi: BFF stateless terhadap uang, tanpa alur async/outbox/broker;
mode gagalnya = "admin UI down" (bukan critical path; semua service hilir
tetap benar sendiri); perilaku downstream-down dicover unit test
error-rendering klien (K2). `chaos-test.sh all` (11 skenario) tetap wajib
hijau di gate — membuktikan BFF tidak MERUSAK apa pun, bukan menambah
skenario.

## 5. Task eksekusi

Urutan sengaja: scaffold dulu (T1) supaya setiap task berikutnya diakhiri
sistem-utuh-hijau; sesi/authn (T2) sebelum apa pun yang butuh identitas;
role+audit (T3) sebelum proxy (T4) supaya penegakan hilir sudah ada saat
klien ditulis; UI (T5/T6) terakhir karena murni konsumen. Satu commit per
task; jangan mencampur T1–T6.

### T1 — Scaffold service + DB (K1, K3)

**Langkah:**

1. `cmd/admin-bff-service/` (main.go + router.go, template fraud-service:
   flag `-healthcheck`, chain middleware standar, `/health` + `/metrics`),
   `internal/adminbff/` kerangka module, `LoadAdminBFFService()` di
   `internal/config`.
2. Migrasi `migrations/adminbff/000001_core.up/down.sql`: `sessions` +
   `audit_log` + grant/RLS pola service lain; `seev_adminbff` +
   `adminbff_app` ke `02-service-dbs.sh` dan `03-service-migrations.sh`;
   Makefile `migrate-up SERVICE=adminbff`.
3. Blok compose profile `app` (build arg `SERVICE=admin-bff-service`,
   port `127.0.0.1:8095:8095`, healthcheck, `depends_on` postgres sehat,
   memory limit dicatat — budget RAM 4GB PROJECT_GUIDE.md).
4. Makefile `build-all` baris ketujuh (+ perbaiki komentar "six");
   `scripts/lib.sh` `start_adminbff_service` (port 18095) — TIDAK
   dimasukkan `start_services` (script uang tidak butuh BFF; admin-e2e.sh
   T6 yang memanggilnya); ci.yml DUA titik enumerasi; `boundary_test.go`
   `serviceModules`; blok `.env.example`.

**Test wajib:** build/lint/vet dua tag hijau; boundary test hijau;
`docker compose --profile app up` menaikkan container ketujuh sehat;
`make verify-full` bersih — **GATE 1**.

**DoD:** service ketujuh hidup di seluruh tooling (compose, CI, Makefile,
lib.sh, boundary) tanpa satu pun fungsi bisnis.

### Hasil T1

> Diisi saat T1 selesai.

### T2 — Login, session, CSRF, minting token (K4, K5)

**Langkah:**

1. Klien login tipis ke auth-service; validasi role ∈ tiga role admin.
2. Repository `sessions` + job cleanup expired (pola `pkg/scheduler`
   existing + `PrometheusMetrics` skip-tick doc 45).
3. Middleware `requireSession` (idle/absolute TTL) + cookie
   HttpOnly/Secure/SameSite=Lax; CSRF issue per-session + verifikasi di
   semua mutasi.
4. Minter JWT downstream TTL 60s (sub/email/role operator).
5. Layout template dasar + halaman login/logout.

**Test wajib:** unit — login admin sukses, non-admin 403 generik, expiry
idle dan absolute, logout mencabut sesi, CSRF menolak token salah/absen,
klaim+TTL minted benar; integration (tag `integration`) — login riil via
auth-service → cookie → halaman terproteksi → logout.

**DoD:** operator bisa masuk/keluar dengan aman; tidak ada endpoint
terproteksi yang bisa diakses tanpa session valid + CSRF.

### Hasil T2

> Diisi saat T2 selesai.

### T3 — Role maker/checker + penegakan ledger + audit log (K6, K7)

**Langkah:**

1. Migrasi auth (nomor: lihat §6 butir 8): perlebar CHECK constraint role;
   konstanta `RoleAdminMaker`/`RoleAdminChecker` di `internal/auth/model`;
   bootstrap opsional maker/checker (pasangan env, pola
   `EnsureBootstrapAdmin` + validasi berpasangan di config).
2. Sweep SEMUA cek role admin sesuai matriks K6 (daftar lokasi §2):
   inline `isAdmin` per service + `WithRole` auth-service.
3. Ledger: `createAdjustment`+`resolveReconItem` → {admin, admin_maker};
   `approveAdjustment`+`rejectAdjustment` → {admin, admin_checker};
   guard `ErrSelfApproval` existing TIDAK disentuh.
4. Middleware audit BFF (K7) + masking + counter failure.

**Test wajib:** unit — matriks role per endpoint (maker tidak bisa
approve, checker tidak bisa create, admin bisa keduanya, self-approval
tetap ditolak untuk KETIGA role); integration — siklus penuh maker create
→ checker approve memakai dua token role berbeda, LANGSUNG ke ledger tanpa
BFF (bukti penegakan downstream); audit row tercipta per mutasi via BFF,
GET tidak dicatat, masking terverifikasi; **fixture gotcha #9**: ketiga
script gate (smoke/business-e2e/chaos) tetap hijau dengan `gen_token ...
admin` existing; `make verify-full` — **GATE 2**.

**DoD:** pemisahan tugas ditegakkan di service pemilik data (bukan hanya
BFF), semua mutasi admin via BFF ter-audit.

### Hasil T3

> Diisi saat T3 selesai.

### T4 — Typed clients + proxy + endpoint dead-command payout (K2, K9)

**Langkah:**

1. Verifikasi ulang inventori route live vs doc 24 (grep router keenam
   service); catat drift di Hasil T4.
2. `internal/adminbff/client/` — satu klien tipis per service, error
   mapping seragam, timeout eksplisit, JWT K5 per request.
3. payout-service: method repo `ListDeadCommands` baru + tiga route K9 +
   test transport.
4. Wiring route proxy BFF (JSON; konsumen = UI T5/T6).

**Test wajib:** unit klien via httptest (happy/4xx/5xx/timeout →
pesan operator yang benar); unit + integration endpoint payout baru
(seed dead → list → replay → status kembali `failed`+eligible, replay-all
ber-cap); integration BFF→payout replay end-to-end.

**DoD:** keenam surface terjangkau dari BFF dengan identitas operator;
dead vendor-command bisa di-list dan di-replay via HTTP (bukan SQL).

### Hasil T4

> Diisi saat T4 selesai.

### T5 — Panel ops batch 1 (K8): maker-checker, payout stuck, recon

**Langkah:**

1. Vendor htmx + PicoCSS (verifikasi versi terkini, catat checksum),
   `go:embed`, layout + navigasi.
2. Panel maker-checker: antrean pending, form create (maker), tombol
   approve/reject (checker), status hasil.
3. Panel payout: daftar requests ber-filter status (stuck = non-terminal
   lama), cancel/retry, daftar dead vendor-commands + replay (T4).
4. Panel recon: import batch (upload CSV), daftar batch + drill-down
   report, resolve item → tampil di antrean adjustments.

**Test wajib:** unit handler render (elemen kunci ada di HTML; tanpa
golden-file ketat); integration — siklus adjustment penuh via endpoint
form BFF dengan dua operator (maker+checker); recon resolve → muncul di
antrean adjustments.

**DoD:** tiga alur ops harian (maker-checker, payout stuck/replay, recon)
bisa dikerjakan lewat browser tanpa curl.

### Hasil T5

> Diisi saat T5 selesai.

### T6 — Panel batch 2 + admin-e2e + penutup (K8, K10)

**Langkah:**

1. Panel fee-rules (ledger) + routing-rules payin/payout + vendor-gateways
   (CRUD form); panel KYC review (list/detail/approve/reject); panel
   kesehatan vendor/breaker (read-only, kedua service) + daftar fraud
   events; panel audit log (K7).
2. `scripts/admin-e2e.sh` BARU: source `lib.sh` SEKALI →
   `ensure_deps_up; build_server; start_services` + start BFF → login
   (cookie+CSRF via curl cookie-jar) → siklus maker-checker → KYC approve
   → payout dead-command replay → assert audit rows via psql. DILARANG
   memperluas `business-e2e.sh` (itu jalur uang user, bukan admin).
3. Metric + panel dashboard K10.
4. Update PROJECT_GUIDE.md (hapus hutang "Dedicated admin BFF", tabel service
   ketujuh + port, catatan RAM), README root, `docs/plan/README.md`,
   status A5 di doc 42.

**Test wajib:** unit handler panel; `./scripts/admin-e2e.sh` hijau dari
volume bersih; `./scripts/chaos-test.sh all` (11 skenario) tetap hijau
TANPA skenario baru (justifikasi K10); `make verify-full` — **GATE
3/final**.

**DoD:** semua panel sketsa A5 hidup; admin-e2e menjadi bukti eksekusi
end-to-end yang bisa diulang.

### Hasil T6

> Diisi saat T6 selesai.

## 6. Constraint eksekutor

1. Boleh breakdown task; DILARANG mengubah K1–K10 tanpa kembali ke user.
2. Do-not-touch: `execTransfer` (PROJECT_GUIDE.md hard rule 5), RLS existing,
   `pkg/messaging`, lifecycle `scripts/lib.sh` (perbaikan di lib.sh, bukan
   duplikasi), kontrak fail-open/fail-closed fraudcheck (doc 37/45).
3. BFF HARAM punya DSN/koneksi ke DB selain `seev_adminbff` — ditegakkan
   boundary test + review manual env compose/lib.sh.
4. Fakta eksternal WAJIB diverifikasi saat eksekusi: versi htmx + PicoCSS
   (vendored, checksum dicatat di Hasil T5) — jangan tulis dari memori
   model.
5. Setiap perubahan cek role (T3) WAJIB diverifikasi terhadap fixture
   `gen_token` + ketiga script gate (gotcha #9 doc 39: kyc_level/role
   default fixture).
6. Kredensial/secret tidak pernah masuk template, log, maupun audit_log;
   masking mengikuti `pkg/logger` (PROJECT_GUIDE.md Conventions).
7. Router live > doc 24 saat drift — doc 24 peta, kode kebenaran; drift
   yang ditemukan dicatat di Hasil T4.
8. **Nomor migrasi auth**: doc 46 (belum dieksekusi) memesan
   `migrations/auth/000003`. Saat mengerjakan T3, `ls migrations/auth/`
   dan pakai nomor bebas berikutnya SAAT ITU; kalau doc 46 belum jalan dan
   T3 memakai 000003, catat di Hasil T3 supaya eksekutor doc 46 menggeser
   nomornya (dua dokumen todo tidak boleh saling menabrak diam-diam).
9. Setiap GATE: `docker compose down -v` dulu (gotcha PROJECT_GUIDE.md — state
   volume kotor = false regression), lalu `make verify-full`.

## 7. Definition of Done global

- [ ] Ketiga GATE hijau dari volume bersih.
- [ ] Pemisahan tugas maker/checker terbukti DI LEDGER via curl langsung
      tanpa BFF (bukan hanya di UI).
- [ ] Semua mutasi admin via BFF menghasilkan audit row; GET tidak.
- [ ] Keenam panel berfungsi via browser tanpa curl dan tanpa Node.
- [ ] Boundary DB terbukti: BFF hanya `seev_adminbff`.
- [ ] Dead vendor-command payout bisa di-list + replay via HTTP.
- [ ] Role `admin` lama tetap superuser — ketiga script gate hijau tanpa
      perubahan fixture.
- [ ] `scripts/admin-e2e.sh` hijau dan terdaftar sebagai gate di T6.
- [ ] PROJECT_GUIDE.md/README/docs-plan-README/doc 42 ter-update.

## 8. Penutup

Setelah GATE 3:

- [ ] Isi semua `### Hasil` T1–T6 dengan command + output ringkas + commit.
- [ ] `docs/plan/README.md`: baris 47 → `✅ done`.
- [ ] Status A5 di [42](42-long-term-roadmap.md) → `✅ SELESAI`.
- [ ] Handoff eksplisit dicatat: SSO/2FA/secrets admin → A6; retensi
      `audit_log`/`sessions` → A8; UI mode per-rule fraud → menyusul
      eksekusi doc 46; developer portal partner menumpang pola UI ini →
      C1.
