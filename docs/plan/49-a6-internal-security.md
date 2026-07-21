# 49 — Track A6: Keamanan Internal — mTLS SPIFFE-ish, Identitas Service, Vault Dev, Threat Model, Review Pentest-Style

> Lahir dari track **A6** di [42-long-term-roadmap.md](42-long-term-roadmap.md).
>
> **Status: SIAP DIEKSEKUSI BERTAHAP (2026-07-19).** Dokumen eksekusi
> self-contained gaya repo (pola doc 45/46/47/48). Fakta repo diverifikasi
> terhadap kode saat dokumen ditulis; referensi baris bisa bergeser —
> eksekutor WAJIB grep ulang router/dial-site live, kode adalah kebenaran,
> dokumen ini peta. Track ini hanya memakai komponen open-source: Go
> crypto/tls + crypto/x509 standard library, HashiCorp Vault (dev-mode),
> Docker Compose, dan tooling existing. TIDAK ada dependency berbayar dan
> TIDAK ada migrasi database.

## 1. Trigger dan tujuan

Bukti trigger (pola doc 42 §2, jalur trigger belajar + prasyarat):

- **[34](34-phase6i-verification.md) selesai** — topologi enam service (kini
  tujuh dengan admin-bff doc 47) stabil dan terverifikasi; ini dependensi
  yang doc 42 §A6 syaratkan ("topologi enam service stabil [34]").
- **36–41 selesai + A1/A3/A4/A5/A10 (doc 43/45/46/47/48) selesai** — bidang
  fungsional matang; yang tersisa sebelum membuka surface partner B2B (C1)
  adalah pengerasan bidang internal. Doc 42 §A6 eksplisit: **"wajib sebelum
  C1"**.
- **Keputusan sadar 2026-07-19**: user mengaktifkan A6 dengan tiga keputusan
  desain diambil eksplisit lewat sesi tanya-jawab: secrets = **Vault
  dev-mode** (K7), CA = **mini-CA Go sendiri + SPIFFE-style URI SAN** (K3),
  lingkup mTLS = **gRPC DAN HTTP internal** (K6, dipilih dengan konsekuensi
  churn harness disadari penuh).

Tujuan bisnis (dari track A6): network internal bukan trust boundary yang
cukup untuk uang; identitas service yang terverifikasi kriptografis +
secrets yang tidak lagi plaintext + peta ancaman eksplisit adalah prasyarat
membuka surface B2B. Hutang terdokumentasi yang dilunasi:

| Hutang | Sumber | Dilunasi oleh |
|---|---|---|
| mTLS + rotated service identity antar service | PROJECT_GUIDE.md future work; docs 26/32/34 | T2 (gRPC) + T3 (HTTP) |
| `INTERNAL_GRPC_TOKEN` kosong = server terima SEMUA call | `pkg/grpcx/grpcx.go:174-176` + default kosong | T2 (K5) |
| Secrets aplikasi tersebar sebagai env plaintext | seluruh `.env.example`/compose | T4 (K7) |
| Tidak ada dokumen threat model | belum pernah ditulis | T1 (K1) |
| Router internal/admin HTTP dijaga JWT USER saja; `/metrics` tanpa auth | `cmd/*/main.go` internalRouter | T3 + T5/T6 |
| CORS wildcard default + issuer JWT opsional | `pkg/middleware/cors.go:23`, `auth.go` issuer | T5 (temuan) → T6 (fix) |
| Prasyarat keamanan sebelum surface partner B2B | doc 42 §A6/§C1 | seluruh doc |

## 2. Fakta repo saat dokumen ditulis

Semua diverifikasi 2026-07-19; eksekutor grep ulang sebelum menyentuh.

**Bidang gRPC — plaintext + token opsional yang default mati:**

- `pkg/grpcx/grpcx.go`: `dial`/`DialLazy` memakai
  `grpc.WithTransportCredentials(insecure.NewCredentials())` (:63, :80);
  `NewServer` (:36) tanpa opsi credentials → listener insecure. Auth =
  interceptor unary metadata `authorization: Bearer <token>` (client
  :189-198, server :172-187). **Kritis: `authInterceptor` NO-OP saat
  `token == ""` — menerima setiap call** (:174-176), dan
  `INTERNAL_GRPC_TOKEN` default kosong di `.env.example:59` serta compose
  (`${INTERNAL_GRPC_TOKEN:-}`). Dial timeout 5s (:27), keepalive 30s/10s
  (:65, :83), health service terdaftar (:48-50). Tidak ada retry/service
  config.
- Tidak ada `x509`/`LoadX509KeyPair`/pemuatan sertifikat service-to-service
  di mana pun. `crypto/tls` hanya muncul di `internal/config/config.go`
  (`parseTLSConfig` DB/Redis, MinVersion TLS12, :229/:499) dan
  `pkg/messaging/config.go:42` (AMQPS).

**Hop gRPC (semua meneruskan `cfg.InternalGRPCToken`):**

| Klien (dial) | Server | Env addr | Dial-site |
|---|---|---|---|
| gateway | ledger/payin/payout | `LEDGER/PAYIN/PAYOUT_GRPC_ADDR` | `cmd/gateway/main.go:105-120` |
| auth | ledger | `LEDGER_GRPC_ADDR` | `cmd/auth-service/main.go:97` |
| payin | ledger (eager `Dial`) | `LEDGER_GRPC_ADDR` | `cmd/payin-service/main.go:114` |
| payout | ledger (eager `Dial`) | `LEDGER_GRPC_ADDR` | `cmd/payout-service/main.go:104` |
| ledger/payin/payout | fraud (`DialLazy`) | `FRAUD_GRPC_ADDR` | ledger:154, payin:142, payout:132 |

Listener `grpcx.NewServer` + `net.Listen("tcp", ":"+GRPCPort)`: ledger
main.go:173, payin:156, payout:147, fraud:121. (auth & gateway tidak
melayani gRPC; admin-bff doc 47 juga tidak.)

**Bidang HTTP internal:**

- gateway→ledger user-API proxy: `cmd/gateway/ledger_remote.go:18`
  `httputil.NewSingleHostReverseProxy` → `LEDGER_USER_API_URL` =
  `http://ledger-service:8090` (listener PUBLIK ledger). Tidak menambah
  kredensial service — hanya meneruskan JWT user; transport `otelhttp` +
  `X-Request-Id`.
- admin-bff (doc 47) memanggil admin API tiap service via HTTP internal
  (klien tipis `internal/adminbff/client/`); ikut lingkup mTLS.
- Bind internal: `INTERNAL_APP_BIND_ADDR` default `127.0.0.1`
  (config.go:409); compose set `0.0.0.0` (:92/:144/:336), host-publish
  hanya `127.0.0.1:<port>`. Network compose FLAT untuk app tier (hanya
  observability punya `internal:true` socket net :590-594). Router
  internal/admin dijaga JWT USER (`WithAuth` + `isAdmin`/`WithRole`) — bukan
  token internal; `/metrics` tanpa auth.
- **Prometheus scrape HTTP polos langsung ke port /metrics tiap service**
  (`deploy/observability/prometheus/prometheus.yml:14-39`): targets
  `gateway-service:8081`, `auth-service:8083`, `ledger-service:8091`,
  `payin-service:8092`, `payout-service:8093`, `fraud-service:8094`. Flip
  mTLS = scrape ikut migrasi.
- Test harness: `scripts/lib.sh` + `smoke-test.sh` + `business-e2e.sh` +
  `chaos-test.sh` (11 scenario) penuh `curl http://localhost:$PORT` ke
  listener internal (LEDGER_INTERNAL_PORT 18091, PAYIN_ADMIN_PORT 18092,
  PAYOUT_ADMIN_PORT 18093, FRAUD_ADMIN_PORT 18094, replica 18193,
  ADMINBFF_PORT 18095) + `wait_for_service_up` + healthcheck compose
  `/app/service -healthcheck` (flag probe in-container di tiap main.go).

**Middleware & auth existing (bahan review T5):** `WithAuth` HS256-forced
(`ParseToken` abaikan header alg → aman alg-confusion/alg:none),
constant-time `hmac.Equal`, expiry dicek, **issuer dicek HANYA jika
dikonfigurasi** (kosong = skip, sekadar warning produksi config.go:692);
`WithSecurityHeaders` (nosniff/DENY/CSP/HSTS-if-https); rate limit
`FailoverLimiter` (doc 45, keying `r.RemoteAddr` termasuk port efemeral —
pelajaran chaos scenario 9); `WithCORS` default `AllowedOrigins:["*"]`
credentials false (`cors.go:23-32`); password bcrypt cost 12 + dummy-hash
timing defense; refresh token hashed at rest + one-time rotation +
reuse-revokes-all; webhook `mockvendor.VerifyAndParse` HMAC-SHA256 raw body
header `X-Mock-Signature` `hmac.Equal` timing-safe **TANPA timestamp dalam
signature** (replay dibatasi freshness `OccurredAt` + dedup
`VendorEventID`); `MaxBytesReader` di webhook/respons.

**Secrets (semua env plaintext kecuali satu):** `JWT_SECRET` (wajib, ≥32,
`len < 32` check config.go:670), `INTERNAL_GRPC_TOKEN` (default KOSONG), password
Postgres per-role (compose hardcode role==password), `POSTGRES_MIGRATE_PASSWORD`,
`RABBITMQ_PASSWORD`, `VENDOR_MOCKVENDOR_SECRET`/`MOCKVENDOR2_SECRET`,
`AUTH_BOOTSTRAP_ADMIN_PASSWORD`, `REDIS_PASSWORD`, `ALERT_WEBHOOK_URL`.
Pengecualian baik = pola tiru: `grafana_admin_password` — Makefile
`observability-secret` (:153-159) generate file 0600 gitignored → compose
`secrets:` → `GF_..._FILE`. **Seam loader** di config.go: `load()` (:376)
memilih file `.env` lalu `loadFromEnvMode(os.Getenv, …)` (:387/:395) — titik
sisip loader Vault. TIDAK ada check "tolak secret default di production".
CI: `ci.yml` tanpa secrets (default compose); `nightly.yml:75-95` generate
segar per-run (`openssl rand` + `::add-mask::` → `$GITHUB_ENV`);
`scripts/lib.sh:26` fallback ke default `change-me…`. sops/age/step/vault
TIDAK ada di go.mod/scripts/Makefile.

**Lain-lain:** RAM — observability capped ~1.92GB, app/infra uncapped,
budget Docker 4GB (Makefile memperingatkan observability + testcontainers
bersamaan). Vault dev ±100-200MB, layak di luar profile observability. Port
bebas: `8200`/`18200` (Vault). Tidak ada file/kode sertifikat existing yang
perlu dihindari. Doc 49 TIDAK menyentuh database.

## 3. Anti-scope

Disalin dari track A6 doc 42 + turunan dokumen ini:

- Bukan HSM/KMS produksi; bukan sertifikasi formal ISO/PCI; bukan bug
  bounty (anti-scope doc 42 §A6).
- Bukan edge TLS publik — gateway :8080, auth :8082 (publik), dan path
  `/webhooks/{vendor}` tetap plain HTTP di dev; TLS terminasi edge =
  concern deployment (dicatat sebagai residual sadar di threat model, bukan
  celah tak-diketahui).
- Bukan cert-manager / mTLS via service mesh Kubernetes — itu dunia
  [35](35-phase6j-kubernetes.md). Doc ini = compose lokal + tool Go.
- **Bukan SSO/OIDC/2FA/WebAuthn admin.** Doc 47 (A5) menyerahkan "hardening
  identitas admin → A6"; A6 di sini adalah bidang **service-plane** (mesin
  bicara mesin). Pengerasan autentikasi user-admin (2FA operator console)
  di-RE-DEFER ke follow-up terpisah — dicatat di §8. Justifikasi: identitas
  service (cert) dan identitas manusia (2FA) adalah dua sumbu berbeda;
  menggabungkannya membengkakkan scope tanpa saling bergantung.
- Bukan Vault produksi/HA/auto-unseal/dynamic-secrets — dev-mode in-memory
  yang sadar ephemeral (re-seed tiap boot); nilai belajar = pola konsumsi
  secrets-server, bukan operasi Vault produksi.
- Rotasi = `make` target + hot-reload `tls.Config` — BUKAN daemon rotasi
  eksternal / cert renewal protocol.
- TIDAK menyentuh `execTransfer` ledger, RLS existing, `mayFailover`/aturan
  bukti anti-double-payout doc 40, `pkg/messaging`.

## 4. Keputusan desain terkunci

### K1 — Threat model sebagai dokumen hidup + register temuan

`docs/security/threat-model.md` BARU: inventori aset (uang di ledger,
secrets, PII KYC, JWT/refresh token), trust boundaries (edge publik vs
bidang internal vs data store), tabel **STRIDE per hop** atas topologi LIVE
(diverifikasi ulang saat T1, bukan disalin dari dokumen ini), dan **register
temuan `TM-nn`** berprioritas (severity + status open/fixed/accepted). Ini
peta prioritas yang mengarahkan urutan review T5 dan fix T6. Dokumen hidup —
T5 menambah temuan, T6 menandai fixed/accepted, tidak dibekukan.

### K2 — `pkg/tlsx`: pemuat cert bersama + hot-reload + verifikasi SAN

Package baru `pkg/tlsx` (mematuhi boundary `pkg/` tidak import `internal/`):
pemuat cert dengan **hot-reload via poll mtime** (tanpa dependency fsnotify
baru; goroutine ticker ringan me-reload `tls.Certificate` saat file berubah,
dibaca lewat `tls.Config.GetCertificate`/`GetClientCertificate`); builder
`tls.Config` sisi server (`ClientAuth: RequireAndVerifyClientCert` +
`VerifyPeerCertificate` yang mencocokkan URI SAN peer dengan allowlist per
listener) dan sisi klien (RootCA + leaf + verifikasi URI SAN server yang
diharapkan). SATU implementasi dipakai `pkg/grpcx` DAN semua server/klien
HTTP internal (termasuk admin-bff dan Prometheus scrape target). Cara
hot-reload `tls.Config` yang benar untuk versi Go di go.mod DIVERIFIKASI
saat eksekusi.

### K3 — `cmd/certgen`: mini-CA Go + SPIFFE-style URI SAN

Tool Go `cmd/certgen` dengan subcommand `init-ca` / `issue --service <name>`
/ `rotate`. Identitas = **URI SAN `spiffe://seev/<service>`** (bukan CN),
service ∈ {gateway, auth, ledger, payin, payout, fraud, admin-bff} +
identitas non-service `spiffe://seev/dev-operator` (untuk harness/curl) dan
`spiffe://seev/prometheus` (untuk scrape). Output ke `deploy/certs/`
(gitignored, `.gitkeep` — pola `observability-secret`). TTL dev: CA 30 hari,
leaf 72 jam (pendek agar rotasi terlatih rutin). Makefile target `certs`
idempoten (regenerate bila absen/kedaluwarsa). `nightly.yml` menambah
langkah generate cert sebelum stack naik. Private key CA + leaf TIDAK PERNAH
masuk git.

### K4 — Matriks identitas per-hop (allowlist eksplisit)

Setiap listener menolak koneksi ber-cert-valid tapi SAN di luar allowlist-nya
(test negatif WAJIB). Ditulis di dokumen sebagai kontrak:

| Listener | Identitas klien yang diizinkan (URI SAN) |
|---|---|
| ledger gRPC | gateway, auth, payin, payout |
| fraud gRPC | ledger, payin, payout |
| ledger HTTP publik :8090 (proxied) | gateway, dev-operator |
| ledger internal HTTP :8091 | dev-operator, prometheus, admin-bff |
| auth internal HTTP :8083 | dev-operator, prometheus, admin-bff |
| payin admin HTTP :8092 | dev-operator, prometheus, admin-bff |
| payout admin HTTP :8093 | dev-operator, prometheus, admin-bff |
| fraud admin HTTP :8094 | dev-operator, prometheus, admin-bff |
| gateway internal HTTP :8081 | dev-operator, prometheus |
| admin-bff HTTP :8095 | dev-operator, prometheus |

Verifikasi identitas = URI SAN dari peer cert, ditegakkan di
`VerifyPeerCertificate` (K2), BUKAN sekadar "cert ditandatangani CA kita".

### K5 — Token internal fail-closed (menutup lubang no-op)

Boot GAGAL bila `INTERNAL_GRPC_TOKEN` kosong (fail-fast saat konstruksi
server, ganti no-op `authInterceptor` :174-176). Token DIPERTAHANKAN sebagai
defense-in-depth DI BAWAH mTLS (dua lapis: identitas cert + shared token).
compose/`lib.sh`/`nightly` menyetel token yang di-generate.
Menghilangkan kondisi "gRPC menerima semua call tanpa kredensial apa pun"
yang berlaku hari ini secara default.

### K6 — mTLS HTTP internal + migrasi harness

Listener yang flip ke TLS: gateway internal :8081, ledger :8090 (publik
yang di-proxy) + :8091, auth internal :8083, payin :8092, payout :8093,
fraud :8094, admin-bff :8095. Konsekuensi yang ditangani sebagai bagian
eksplisit task (bukan tersembunyi):

- Flag `-healthcheck` tiap main.go jadi TLS-aware (dial listener sendiri
  dengan cert mounted yang sama); healthcheck compose ikut.
- `scripts/lib.sh`: helper `curl_internal` (bawa `--cacert` CA +
  `--cert`/`--key` dev-operator) + **sweep SEMUA `curl` ke listener internal
  di lib.sh/smoke/business-e2e/chaos jadi https** — perbaikan terpusat di
  lib.sh, bukan duplikasi per-script (aturan lifecycle lib.sh). Termasuk
  `wait_for_service_up`.
- Prometheus scrape → `scheme: https` + `tls_config` (`ca_file` +
  `cert_file`/`key_file` identitas `prometheus`, di-mount via compose) —
  BUKAN memindah `/metrics` ke listener plain (menjaga bidang metrics ikut
  ter-otentikasi mutual).

### K7 — Vault dev-mode dengan fallback env utuh

Container Vault dev-mode di profile compose BARU `secrets` (opt-in seperti
`observability`), image pinned by digest (versi DIVERIFIKASI saat
eksekusi), host `127.0.0.1:18200`. Seed idempoten `scripts/vault-seed.sh`
menulis KV v2 `secret/<service>` dari generator yang sama dengan pola
nightly. Konsumsi via seam `config.go load()`: jika `VAULT_ADDR` +
`VAULT_TOKEN` diset → fetch KV (precedence **Vault > .env**); jika tidak
diset → perilaku hari ini UTUH (pola optionality seperti `REDIS_ENABLED`).
Klien = HTTP kecil ke KV v2 API (bentuk request/response DIVERIFIKASI saat
eksekusi), tanpa dependency berat. **CI/nightly TETAP env-generated** —
Vault di luar jalur CI, dicatat jujur. Residual sadar (masuk threat model
K1): Vault dev bicara HTTP plaintext di network compose, dan dev-mode
ephemeral (re-seed tiap boot) — TLS listener Vault + persistence = follow-up.

### K8 — Checklist review pentest-style ber-bukti

T5 menjalankan checklist berikut dengan **perintah nyata + output disimpan
di Hasil** (bukan klaim desain); setiap temuan → register `TM-nn` (K1)
dengan severity:

- Matriks authz bypass — termasuk temuan yang sudah dikenal "router internal
  dijaga JWT user saja" (bisakah user non-admin dengan token valid menyentuh
  endpoint internal?).
- IDOR sweep semua route ber-`{id}` lintas user dan lintas role.
- Webhook forgery (signature salah), replay (event_id sama), oversize
  (>MaxBytes), stale `OccurredAt`.
- Rate-limit keying (RemoteAddr + port efemeral — apakah satu klien bisa
  memutar port untuk lolos? pelajaran chaos 9).
- CORS wildcard default; issuer JWT opsional (skip saat kosong);
  konfirmasi alg-confinement HS256 (bukti alg:none/RS-confusion ditolak).

### K9 — Rotation drill (T6), BUKAN chaos scenario permanen

Drill script berdiri sendiri: rotasi cert live di bawah trafik → bukti
zero-downtime (koneksi eksisting/baru tetap sukses via hot-reload K2) + cert
lama DITOLAK setelah CA rotate. Justifikasi TIDAK menambah chaos scenario:
`chaos-test.sh` menguji money-safety saat dependency mati; rotasi = prosedur
operasional keamanan, bukan invarian uang — drill terpisah lebih tepat dan
menjaga suite chaos tetap fokus.

### K10 — Observability minimal bidang baru

Gauge `tlsx_cert_expiry_seconds{identity}` (hari tersisa per identitas) +
counter handshake failure. Label = enum identitas dari matriks K4 (bukan
input request → low-cardinality). Tanpa dashboard besar baru; satu panel
kecil "cert expiry" di dashboard ops existing (doc 43) cukup.

## 5. Task eksekusi

Urutan: T1 threat model dulu (peta yang mengarahkan sisanya); T2 mTLS gRPC +
token fail-closed (bidang terkecil, membangun `pkg/tlsx`+`certgen` yang
dipakai T3); T3 mTLS HTTP internal + migrasi harness (task terbesar); T4
Vault (independen, setelah bidang transport aman); T5 review ber-bukti
(butuh sistem sudah dalam postur akhir); T6 fix temuan + rotation drill +
penutup. Setiap task diakhiri `### Hasil` berisi bukti nyata. Satu commit
per task; jangan mencampur T1–T6.

### T1 — Threat model + register temuan (K1)

**Catatan eksekusi**: seluruh §2 dokumen ini ditulis SEBELUM verifikasi
ulang T1 — lihat `docs/security/threat-model.md` §4 untuk amandemen live
(9 proses jaringan, bukan 7; `assurance-service` doc 48 tidak tercatat di
K3/K4/K6). Kode adalah kebenaran; §2 di atas TIDAK diedit ulang (praktik
sesi ini: fakta amandemen hidup di threat-model.md, bukan menimpa riwayat
keputusan di dokumen task).

**Langkah**

1. Tulis `docs/security/threat-model.md`: inventori aset, trust boundaries,
   tabel STRIDE per hop (grep ulang tiap dial-site/listener live — jangan
   salin §2 dokumen ini mentah), register `TM-nn` awal berisi hutang yang
   sudah dikenal (token kosong, secrets plaintext, router user-JWT, CORS
   wildcard, issuer opsional, Vault-http residual).
2. Tautkan tiap `TM-nn` ke task yang menanganinya (T2–T6).

**Test wajib**

- Verifikasi silang: setiap klaim fakta di threat model dikonfirmasi
  terhadap kode live (daftar grep di dokumen). Tanpa gate build (docs-only).

**DoD**: peta ancaman eksplisit ada; setiap butir hutang keamanan punya ID
dan pemilik task.

### Hasil

> T1 selesai 2026-07-21. `docs/security/threat-model.md` ditulis:
> inventori 8 aset, 7 trust boundary, tabel STRIDE atas 13 hop gRPC + 4
> kelas hop HTTP internal + 2 hop edge publik, register 10 temuan
> (`TM-01`..`TM-10`) dengan severity dan task pemilik.
>
> **Temuan T1 paling penting**: topologi LIVE punya **9 proses jaringan**,
> bukan 7 seperti diasumsikan §2 dokumen ini saat ditulis —
> `assurance-service` (doc 48/A10, dieksekusi setelah §2 disusun) menambah
> 3 dial-site gRPC (`assurance→payin/payout/ledger`,
> `cmd/assurance-service/main.go:95,100,105`) dan satu listener HTTP admin
> `:8096` (`cmd/assurance-service/main.go:148-149`), tidak tercatat di K3
> (daftar service certgen)/K4 (matriks allowlist)/K6 (daftar listener
> flip). Dikonfirmasi via `docker-compose.yml:369+` dan
> `scripts/lib.sh:562`. Ini BUKAN perubahan keputusan K1–K10 — assurance-
> service jelas termasuk niat "SEMUA hop gRPC+HTTP internal" — melainkan
> koreksi enumerasi. Dicatat sebagai **TM-09**; T2/T3 WAJIB memperlakukan
> assurance-service setara admin-bff di setiap langkah.
>
> Temuan kedua: `deploy/observability/prometheus/prometheus.yml` diperiksa
> penuh — hanya 6 job terdaftar (gateway/auth/ledger/payin/payout/fraud);
> **admin-bff (:8095) dan assurance-service (:8096) sama sekali tidak
> di-scrape**, bukan sekadar dugaan (TM-04/TM-09).
>
> Temuan ketiga (sitasi): frasa "mTLS + rotated service identity" yang
> dikutip docs/plan/42 §A6 dan docs/plan/49 §1 dari `PROJECT_GUIDE.md`
> tidak ditemukan verbatim di file itu (144 baris diperiksa penuh) — yang
> ada adalah `PROJECT_GUIDE.md:52` ("Internal gRPC authentication is not a
> replacement for user authorization"), prinsip yang sejalan tapi bukan
> sumber kutipan literal. Tidak mengubah substansi hutang (mTLS memang
> belum ada); T6 akan menulis payoff dengan kutipan yang benar.
>
> **Verifikasi silang (Test wajib)**: setiap klaim §2 doc 49 yang dipakai
> ulang di threat model di-grep ulang terhadap kode live — konfirmasi
> penuh untuk: `pkg/grpcx/grpcx.go` (no-op token `:174-176`, insecure
> creds `:63,:80`, 8 dial-site di 5 `cmd/*/main.go` + 4 listener gRPC),
> `pkg/middleware/cors.go:25` (wildcard default), `pkg/middleware/
> auth.go:94` (issuer skip-when-empty), `internal/vendorgw/mockvendor/
> mockvendor.go` (HMAC timing-safe, tanpa timestamp signature). Tidak ada
> klaim yang meleset kecuali drift nomor baris kecil (config.go seam
> loader bergeser dari perkiraan awal `:329/:340` ke aktual `:376/:387`,
> sudah dikoreksi di doc 49 §2 sebelum T1 dimulai).
>
> Docs-only, tanpa gate build (sesuai DoD T1).

### T2 — pkg/tlsx + certgen + mTLS gRPC + allowlist + token fail-closed (K2,K3,K4,K5)

**Langkah**

1. `pkg/tlsx`: pemuat cert + hot-reload + builder server/klien + verifikasi
   URI SAN (K2). Unit test cara reload + verifikasi SAN.
2. `cmd/certgen` (K3) + Makefile target `certs`; output `deploy/certs/`
   gitignored (`.gitignore` + `.gitkeep`).
3. `pkg/grpcx`: `NewServer` pakai `tlsx` server config; `dial`/`DialLazy`
   pakai `tlsx` client config; hapus no-op token (K5, boot gagal saat token
   kosong). Semua 8 dial-site + 4 server gRPC memuat cert dari path env.
4. Wiring: compose mount `deploy/certs/` + set `INTERNAL_GRPC_TOKEN`
   generated; `scripts/lib.sh` generate cert (panggil `certgen`) + set token;
   `nightly.yml` langkah cert.

**Test wajib**

- Unit: reload cert saat file berganti; `VerifyPeerCertificate` menerima SAN
  dalam allowlist, menolak SAN di luar allowlist; klien tanpa cert ditolak;
  server boot gagal saat token kosong.
- Integration (tag `integration`): dua hop gRPC nyata (mis. gateway→ledger,
  ledger→fraud) sukses dengan cert benar; koneksi dengan identitas salah
  (mis. fraud memanggil ledger — di luar allowlist) DITOLAK.
- `make verify-full` HIJAU dari volume bersih — **GATE 1**. Perhatian khusus:
  11 chaos scenario yang kill/restart service harus tetap hijau dengan cert
  mount (regresi paling mungkin di sini).

**DoD**: seluruh bidang gRPC mTLS + verifikasi SAN per-hop; token internal
tidak pernah lagi bisa kosong-menerima-semua.

### Hasil

> Diisi saat T2 selesai.

### T3 — mTLS HTTP internal + migrasi harness (K6)

**Langkah**

1. Flip semua listener internal/admin HTTP ke `tlsx` server config
   (gateway :8081, ledger :8090+:8091, auth :8083, payin :8092, payout
   :8093, fraud :8094, admin-bff :8095) dengan allowlist K4.
2. Klien HTTP internal pakai `tlsx` client config: gateway→ledger proxy
   (`ledger_remote.go`) + admin-bff downstream clients.
3. Flag `-healthcheck` tiap main.go + healthcheck compose jadi TLS-aware.
4. `scripts/lib.sh`: helper `curl_internal` + sweep semua curl internal ke
   https (lib.sh/smoke/business-e2e/chaos); `wait_for_service_up` ikut.
5. Prometheus: `scheme: https` + `tls_config` identitas `prometheus`; mount
   cert via compose. Verifikasi dashboard doc 43 tetap menerima metrics.

**Test wajib**

- Integration: scrape Prometheus berhasil atas listener mTLS; curl tanpa
  cert klien ke listener internal ditolak; `curl_internal` sukses.
- `make verify-full` HIJAU dari volume bersih — **GATE 2** (seluruh 3 script
  gate + 11 chaos scenario melewati listener yang kini mTLS).

**DoD**: seluruh bidang HTTP internal mTLS; harness + scrape + healthcheck
teradaptasi tanpa memindahkan permukaan apa pun ke plain.

### Hasil

> Diisi saat T3 selesai.

### T4 — Vault dev-mode + seed + plumbing config (K7)

**Langkah**

1. Container Vault dev di profile `secrets` (image pinned digest), compose;
   `scripts/vault-seed.sh` idempoten (KV v2 `secret/<service>`).
2. `internal/config` seam: klien KV v2 HTTP; `VAULT_ADDR`+`VAULT_TOKEN` set →
   precedence Vault > env; unset → fallback env utuh.
3. `.env.example` + dokumentasi cara menjalankan profile `secrets`.

**Test wajib**

- Unit: precedence (Vault value menang saat keduanya ada; env dipakai saat
  Vault unset); parsing KV v2 response.
- Integration (tag `integration`): boot service dengan Vault seeded →
  memakai secret dari Vault; boot tanpa `VAULT_ADDR` → perilaku env hari ini
  identik (kedua jalur hijau). CI/nightly TETAP env-generated (Vault tidak
  masuk jalur CI).

**DoD**: secrets aplikasi bisa bersumber dari Vault dev tanpa mengubah jalur
env existing; tidak ada secret hardcoded baru.

### Hasil

> Diisi saat T4 selesai.

### T5 — Review pentest-style ber-bukti (K8)

**Langkah**

1. Jalankan checklist K8 dengan perintah nyata terhadap stack live; simpan
   perintah + output ringkas di Hasil.
2. Setiap temuan → register `TM-nn` (K1) dengan severity + repro. TIDAK
   memperbaiki di task ini (kecuali temuan trivial satu-baris yang aman).

**Test wajib**

- Bukti tiap item checklist terdokumentasi (authz bypass, IDOR sweep,
  webhook forgery/replay/oversize/stale, rate-limit keying, CORS, issuer,
  alg-confinement). Tanpa gate baru (review, bukan perubahan perilaku).

**DoD**: register temuan lengkap dengan severity; tidak ada item checklist
yang belum diuji dengan bukti.

### Hasil

> Diisi saat T5 selesai.

### T6 — Fix temuan prioritas + rotation drill + penutup (K9,K10)

**Langkah**

1. Perbaiki temuan severity tinggi dari register. Minimal yang sudah
   dikenal: CORS wildcard default → allowlist eksplisit (atau kosong untuk
   API-only); issuer JWT → wajib di semua service (config validation);
   keputusan eksplisit atas webhook-timestamp (perbaiki atau catat sebagai
   accepted-risk dengan alasan). Temuan lain sesuai severity.
2. Rotation drill script (K9): bukti zero-downtime + cert lama ditolak.
3. Metric K10 (`tlsx_cert_expiry_seconds` + handshake-failure) + panel kecil.
4. Runbook `docs/runbooks/` (rotasi cert, seed Vault, respon handshake
   failure); payoff hutang di PROJECT_GUIDE.md (+ CLAUDE.md bila ada);
   update README + status A6 doc 42.

**Test wajib**

- Unit/integration untuk tiap fix (mis. CORS non-wildcard, issuer wajib →
  token tanpa issuer ditolak).
- Rotation drill hijau (bukti di Hasil).
- Gate final di **project Compose TERISOLASI** (pola doc 45 T4 — perubahan
  menyentuh SEMUA service): `docker compose stop` → `COMPOSE_PROJECT_NAME=
  seev-plan49-gate … make verify-full` → `COMPOSE_PROJECT_NAME=
  seev-plan49-gate docker compose down -v`. **GATE 3/final**.

**DoD**: temuan prioritas tertutup; rotasi terbukti zero-downtime; bidang
baru teramati; dokumentasi/hutang ter-update.

### Hasil

> Diisi saat T6 selesai.

## 6. Constraint eksekutor

1. Boleh breakdown task; DILARANG mengubah K1–K10 tanpa kembali ke user.
2. Do-not-touch: `execTransfer`; RLS; `mayFailover`/aturan bukti
   anti-double-payout doc 40; kontrak `pkg/messaging`; kontrak fail-open
   `pkg/fraudcheck`. Perbaikan lifecycle `scripts/lib.sh` di lib.sh, bukan
   duplikasi per-script.
3. **Private key CA/leaf TIDAK PERNAH masuk git/log/artifact CI.** Verifikasi
   `.gitignore` menutup `deploy/certs/*` (kecuali `.gitkeep`) + review manual
   `git status` sebelum tiap commit. Token/secret tidak pernah di log.
4. Fakta eksternal WAJIB diverifikasi saat eksekusi: cara hot-reload
   `tls.Config` yang benar untuk versi Go di go.mod (T2); image+digest Vault
   + bentuk request/response KV v2 API (T4); opsi `tls_config` Prometheus
   yang didukung versi image live (T3). Jangan menebak.
5. Setiap scenario chaos yang restart service WAJIB diverifikasi ulang
   dengan cert mount — kill+restart di bawah mTLS adalah jalur regresi utama.
6. admin-bff (service ketujuh, doc 47) WAJIB masuk sweep mTLS + matriks
   allowlist K4; jangan tinggalkan satu listener/klien internal pun plain.
7. Setiap gate `docker compose down -v` dulu; `make verify-full` = bentuk
   gate kanonik. Gate final terisolasi (T6): JANGAN pernah `down -v` tanpa
   prefix `COMPOSE_PROJECT_NAME=seev-plan49-gate`; kembalikan stack default
   yang aktif sebelum preflight setelah cleanup.
8. Metric/label baru low-cardinality (identitas dari allowlist internal).
9. Butuh file/perilaku di luar task ini → berhenti, update dokumen dulu.

## 7. Definition of Done global

- [ ] `make lint`, `make test`, vet dua tag, `make verify-full` hijau dari
      volume bersih di ketiga gate (final = project terisolasi).
- [ ] Seluruh hop gRPC + HTTP internal (termasuk admin-bff + Prometheus
      scrape) mTLS dengan verifikasi URI SAN per-hop; test negatif membuktikan
      tanpa-cert dan SAN-salah DITOLAK.
- [ ] Server gRPC boot GAGAL saat `INTERNAL_GRPC_TOKEN` kosong (lubang no-op
      tertutup).
- [ ] `docs/security/threat-model.md` + register `TM-nn` terisi; temuan
      severity tinggi diperbaiki atau di-accept dengan alasan tertulis.
- [ ] Vault dev jalan + seed idempoten; precedence Vault>env terbukti;
      fallback env hari ini utuh; CI tetap env-generated.
- [ ] Review pentest-style ber-bukti (perintah + output di Hasil T5).
- [ ] Rotation drill membuktikan zero-downtime + penolakan cert lama.
- [ ] Tidak ada private key/secret di git; `deploy/certs/` gitignored.
- [ ] PROJECT_GUIDE.md hutang "mTLS + rotated service identity" ditandai
      lunas; runbook baru tersedia.

## 8. Penutup setelah GATE 3

- [ ] Isi semua `### Hasil` dengan bukti command + output ringkas.
- [ ] Update baris plan 49 di [README](README.md) menjadi selesai.
- [ ] Update status A6 di [42](42-long-term-roadmap.md) menjadi selesai via 49.
- [ ] Catat handoff eksplisit: (a) hardening autentikasi user-admin
      (2FA/SSO/WebAuthn operator console, warisan doc 47) = follow-up
      terpisah — A6 ini menutup bidang service-plane, bukan user-plane;
      (b) TLS listener Vault + persistence + auto-unseal = follow-up;
      (c) edge TLS publik + terminasi = concern deployment/[35]. Prasyarat
      keamanan doc 42 §C1 ("A6 wajib sebelum C1") kini terpenuhi.
