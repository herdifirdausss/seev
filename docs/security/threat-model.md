# Threat Model — Seev Internal Service Plane

> Dokumen hidup (docs/plan/49 K1). Ditulis 2026-07-21, diverifikasi terhadap
> topologi LIVE saat T1 (bukan disalin dari docs/plan/49 §2 — beberapa fakta
> di sana sudah usang, lihat catatan amandemen di §5). Setiap task doc 49
> berikutnya (T2–T6) menambah/menutup entri register di §6; jangan
> membekukan dokumen ini setelah gate final — perbarui saat topologi
> berubah.

## 1. Ringkasan

Bidang internal (gRPC + HTTP antar service, di belakang gateway/BFF) HARI
INI bukan trust boundary yang memadai: identitas service tidak
diverifikasi kriptografis, token internal opsional dan default kosong,
secrets tersebar sebagai env plaintext. Dokumen ini memetakan aset, batas
kepercayaan, dan ancaman STRIDE per hop sebagai dasar pengerasan doc 49
T2–T6.

## 2. Aset

| Aset | Di mana | Dampak jika kompromi |
|---|---|---|
| Saldo & buku besar (uang) | `seev_ledger` (`ledger_transactions`, `account_balances`, `ledger_entries`) | Kehilangan/penggandaan uang — dampak tertinggi di sistem |
| `JWT_SECRET` (HS256, dipakai SEMUA service) | env, shared | Pemalsuan token user DAN admin di seluruh sistem sekaligus |
| `INTERNAL_GRPC_TOKEN` | env, shared, default KOSONG | Saat ini TIDAK melindungi apa pun secara default (lihat TM-01) |
| Password Postgres per-role | compose (hardcode `role==password`) | Akses langsung ke DB service tertentu (RLS/grant membatasi blast radius per service) |
| PII/KYC (`kyc_submissions.payload`, dokumen terenkripsi doc 46) | `seev_auth` | Kebocoran identitas nasabah; kewajiban regulasi |
| JWT access/refresh token pengguna | klien, `auth_users`/`refresh_tokens` (hashed) | Pengambilalihan sesi user individual |
| Sesi admin-bff + audit log aksi admin (doc 47) | `seev_adminbff` | Pengambilalihan operator; audit trail bisa dipalsukan jika sesi dibajak |
| Vendor secrets (`VENDOR_MOCKVENDOR_SECRET`, dst.) | env | Pemalsuan webhook vendor (dev-only hari ini, tapi pola sama untuk vendor riil) |
| Temuan assurance/screening events (doc 48/46) | `seev_assurance`, `seev_fraud` | Manipulasi bukti korelasi payin–payout–ledger atau riwayat screening |

## 3. Trust boundary

1. **Edge publik ↔ gateway/auth** — `gateway :8080`, `auth-service :8082`
   (publik), `POST /webhooks/{vendor}` (publik, HMAC-gated). Klien tidak
   dipercaya sama sekali; semua request diautentikasi per-request (JWT atau
   HMAC webhook).
2. **Edge publik ↔ bidang internal (via proxy)** — gateway→ledger
   `ledger_remote.go` reverse-proxy ke `http://ledger-service:8090`
   (listener PUBLIK ledger, hanya dijangkau lewat gateway secara desain,
   TIDAK ADA kredensial service yang menambah lapisan — hanya JWT user yang
   diteruskan).
3. **Bidang internal (service-ke-service)** — SEMUA hop gRPC + HTTP
   admin/internal antar sembilan service (§4). **INI BOUNDARY YANG PALING
   LEMAH HARI INI** — flat Docker network, tanpa identitas kriptografis,
   token opsional default kosong. Target utama doc 49 T2/T3.
4. **Bidang internal ↔ operator manusia** — admin-bff (doc 47) sesi HttpOnly
   + CSRF; operator memegang kredensial auth-service, BUKAN token internal.
   Boundary ini SUDAH punya kontrol (sesi + maker/checker) dari doc 47,
   di luar ulang-kerja doc 49 kecuali dampak turunannya ("BFF adalah klien
   ke-9+ yang harus ikut mTLS", K4).
5. **Bidang internal ↔ observability** — Prometheus scrape `/metrics`
   SEMUA service, hari ini TANPA autentikasi apa pun; target T3 (`tls_config`
   + identitas `prometheus`).
6. **Bidang internal ↔ data store** — service → Postgres/Redis/RabbitmQ.
   SUDAH punya boundary parsial (role Postgres per-service + RLS `FORCE`,
   TLS opsional di `parseTLSConfig`) — DI LUAR SCOPE doc 49 (anti-scope §3,
   tidak disentuh).
7. **CI/nightly ↔ secrets** — `nightly.yml` generate secret segar per-run;
   `ci.yml` pakai default compose. Vault (T4) TIDAK masuk jalur CI —
   boundary ini tetap env-based, dicatat sebagai residual (TM-10).

## 4. Topologi live — AMANDEMEN atas docs/plan/49 §2

**Temuan T1 kritis**: docs/plan/49 §2 ditulis SEBELUM eksekusi doc 48 (A10
product assurance) selesai diverifikasi ulang di sini — dokumen itu
menghitung **7 service** (gateway, auth, ledger, payin, payout, fraud,
admin-bff). Topologi LIVE hari ini punya **9 proses**:

| # | Proses | Peran jaringan |
|---|---|---|
| 1 | gateway | Publik (:8080) + internal HTTP (:8081) + gRPC client |
| 2 | auth-service | Publik (:8082) + internal HTTP (:8083) + gRPC client |
| 3 | ledger-service | Publik-proxied (:8090) + internal HTTP (:8091) + gRPC server (:9091) |
| 4 | payin-service | Admin HTTP (:8092) + gRPC server (:9092) + gRPC client |
| 5 | payout-service | Admin HTTP (:8093) + gRPC server (:9093) + gRPC client |
| 6 | fraud-service | Admin HTTP (:8094) + gRPC server (:9094) |
| 7 | admin-bff-service | Admin HTTP (:8095), HTTP-only, TANPA gRPC (doc 47) |
| 8 | **assurance-service** (BARU, tidak ada di §2 doc 49) | Admin HTTP (:8096) + gRPC client (payin/payout/ledger) — `cmd/assurance-service/main.go`, terverifikasi lewat `docker-compose.yml:369+` dan `scripts/lib.sh:562` |
| 9 | sanctions-loader (CLI batch, `cmd/sanctions-loader/main.go`) | **TANPA jaringan sama sekali** — hanya akses DB langsung dari file lokal; DI LUAR scope mTLS/allowlist (tidak ada hop untuk diamankan) |

`assurance-service` menambah **3 dial-site gRPC** yang tidak tercatat di
docs/plan/49 K3/K4/K6: `assurance → payin` (`PAYIN_GRPC_ADDR`),
`assurance → payout` (`PAYOUT_GRPC_ADDR`), `assurance → ledger`
(`LEDGER_GRPC_ADDR`), semua meneruskan `INTERNAL_GRPC_TOKEN`
(`cmd/assurance-service/main.go:95,100,105`). Ia JUGA butuh identitas SAN
sendiri (`spiffe://seev/assurance`) dan harus ditambahkan ke allowlist
gRPC ledger/payin/payout (K4), serta masuk allowlist HTTP internal yang
menerima `dev-operator`/`prometheus`/`admin-bff` (assurance's `/metrics`
:8096 discrape Prometheus juga — cek `deploy/observability/prometheus/
prometheus.yml` saat T3, target itu KEMUNGKINAN BESAR juga belum
terdaftar di sana dan perlu ditambah di task yang sama).

**Konfirmasi langsung**: `deploy/observability/prometheus/prometheus.yml`
diperiksa penuh (bukan diasumsikan) — hanya 6 job terdaftar (gateway,
auth, ledger, payin, payout, fraud); **admin-bff (:8095) dan
assurance-service (:8096) TIDAK di-scrape sama sekali**, bukan cuma
"kemungkinan belum terdaftar". Ini gap observability yang sudah ada
sebelum doc 49 (di luar scope T3 untuk MENAMBAHKANNYA sebagai fitur baru),
tapi begitu T3 memindahkan scrape ke `tls_config`, kedua target yang hilang
ini harus diputuskan eksplisit: ikut ditambahkan (lebih konsisten) atau
didokumentasikan sebagai gap observability terpisah dari mTLS. Dicatat di
TM-09.

**Keputusan T1**: ini BUKAN perubahan keputusan desain K1–K10 (yang
mengatakan "SEMUA hop gRPC+HTTP internal" — assurance-service SECARA
JELAS termasuk niat itu), melainkan koreksi enumerasi yang usang. T2/T3
WAJIB memperlakukan assurance-service setara admin-bff di semua langkah
(cert issuance, allowlist, sweep harness, wiring compose). Dicatat sebagai
TM-09 di register (§6) supaya tidak terlewat.

**Koreksi kutipan hutang**: docs/plan/42 §A6 dan docs/plan/49 §1 mengutip
`PROJECT_GUIDE.md` untuk frasa persis "mTLS + rotated service identity" —
frasa itu **tidak ditemukan verbatim** di `PROJECT_GUIDE.md` versi saat ini
(144 baris, diperiksa penuh). Yang ADA dan relevan: `PROJECT_GUIDE.md:52`
— *"Internal gRPC authentication is not a replacement for user
authorization."* — prinsip yang sejalan (defense-in-depth, bukan
either/or) tapi bukan sumber kutipan hutang yang literal. Tidak mengubah
substansi (mTLS memang belum ada), hanya mengoreksi sitasi; T6 akan
menulis entri hutang yang benar-benar match saat payoff.

## 5. STRIDE per hop

Kolom **Ancaman realistis hari ini** hanya mencatat kategori STRIDE yang
BENAR-BENAR applicable dengan kontrol saat ini (bukan seluruh 6 kategori
untuk setiap baris). S=Spoofing, T=Tampering, R=Repudiation,
I=Information Disclosure, D=Denial of Service, E=Elevation of Privilege.

### 5.1 gRPC (13 hop)

| Hop | Kontrol hari ini | Ancaman realistis | TM |
|---|---|---|---|
| gateway→ledger/payin/payout | Token opsional (default kosong) | **S**: proses apa pun di network compose bisa menyamar jadi gateway. **E**: tanpa identitas, tidak ada batas siapa boleh panggil apa. | TM-01, TM-02 |
| auth→ledger | sda | sda | TM-01, TM-02 |
| auth→fraud (lazy) | sda | sda | TM-01, TM-02 |
| payin→ledger | sda | sda | TM-01, TM-02 |
| payin→fraud (lazy) | sda | sda | TM-01, TM-02 |
| payout→ledger | sda | sda | TM-01, TM-02 |
| payout→fraud (lazy) | sda | sda | TM-01, TM-02 |
| ledger→fraud (lazy) | sda | sda | TM-01, TM-02 |
| **assurance→payin** | sda (BARU, §4) | sda + **belum ada identitas terdaftar sama sekali** | TM-01, TM-02, TM-09 |
| **assurance→payout** | sda | sda | TM-01, TM-02, TM-09 |
| **assurance→ledger** | sda | sda | TM-01, TM-02, TM-09 |

Catatan bersama: transport `insecure.NewCredentials()` di semua 13 hop
(`pkg/grpcx/grpcx.go:63,80`) berarti **T (Tampering) dan I (Information
Disclosure) juga realistis** — payload gRPC (termasuk amount, PII,
verdict fraud) mengalir plaintext di wire; siapa pun yang bisa membaca
traffic docker network (mis. container lain yang di-exploit) bisa
menyadap atau memodifikasi in-flight tanpa terdeteksi (tidak ada MAC per
pesan di luar TLS). D (DoS) TIDAK istimewa di sini dibanding endpoint HTTP
mana pun — tidak dicatat berulang.

### 5.2 HTTP internal (dikelompokkan; hop identik secara struktural)

| Kelas hop | Anggota | Kontrol hari ini | Ancaman realistis | TM |
|---|---|---|---|---|
| gateway→ledger (proxy publik) | 1 hop | Hanya JWT user diteruskan, TANPA kredensial service tambahan | **S**: proses lain di network bisa langsung memanggil `ledger-service:8090` tanpa lewat gateway sama sekali (tidak ada yang membedakan "datang dari proxy" vs "datang dari mana pun") — bypass rate-limit/CORS/security-header gateway. | TM-02 |
| admin-bff→admin API tiap service | ledger, auth, payin, payout, fraud, **assurance (jika ada — verifikasi T3)** | JWT admin per-request (K5 doc 47) TAPI transport plaintext + tanpa identitas BFF | **S/T/I** sama seperti gRPC: siapa pun di network yang tahu URL bisa memanggil endpoint admin tanpa jadi BFF; JWT admin yang disadap dari traffic plaintext bisa dipakai ulang. | TM-01\*, TM-02, TM-03 |
| dev-operator/harness→listener internal | `scripts/lib.sh` + smoke/business-e2e/chaos, 9 target | Sama sekali tanpa kredensial service (curl polos localhost) | Bukan ancaman produksi (harness lokal), TAPI pola ini membuktikan "endpoint internal bisa dipanggil siapa saja yang tahu port" — inilah TM-03 dalam bentuk paling langsung. | TM-03 |
| Prometheus→`/metrics` tiap service | 6 target terdaftar di `prometheus.yml` (gateway, auth, ledger, payin, payout, fraud) — **admin-bff dan assurance-service TIDAK di-scrape sama sekali**, dikonfirmasi langsung | **TANPA autentikasi sama sekali** | **I**: siapa pun di network baca metrik operasional (request rate, breaker state, dsb — TIDAK ada nama/PII per label doc 43 K5, jadi dampak dibatasi tapi topologi/health tetap bocor). Host-publish tetap `127.0.0.1` saja jadi eksposur EKSTERNAL rendah. | TM-04, TM-09 |

\* TM-01 relevan di sini juga karena `INTERNAL_GRPC_TOKEN` TIDAK dipakai
sama sekali di jalur HTTP admin — otorisasi murni JWT user/admin, bukan
identitas service; dicatat silang.

### 5.3 Edge publik (di luar scope mTLS T2/T3, tapi tetap dipetakan agar residual sadar)

| Hop | Kontrol hari ini | Ancaman realistis | Status |
|---|---|---|---|
| Klien→gateway :8080 / auth :8082 | JWT (login), rate limit, CORS, security headers | **S**: TLS/HTTPS terminasi = concern deployment (anti-scope §3), plain HTTP di dev. **T/I**: kredensial user/JWT bisa disadap di jaringan tak terenkripsi kalau deployment nyata memakai HTTP polos. | Residual SADAR — bukan celah tak-diketahui; edge TLS = follow-up deployment, bukan A6. |
| Vendor→`POST /webhooks/{vendor}` | HMAC-SHA256 `hmac.Equal` timing-safe, TANPA timestamp dalam signature | **R (Repudiation)/replay**: signature valid yang tertangkap bisa dikirim ulang persis; dibatasi HANYA oleh freshness `OccurredAt` (field bisnis, bukan bagian signature) + dedup `VendorEventID`. Jika `VendorEventID` bisa diprediksi/diulang dengan `OccurredAt` baru, replay lolos. | TM-08 — dinilai T5, diputuskan T6. |

## 6. Register temuan

Format: `TM-nn` — Ringkasan — Severity — Task pemilik — Status.

| ID | Ringkasan | Severity | Owner | Status |
|---|---|---|---|---|
| TM-01 | `INTERNAL_GRPC_TOKEN` kosong = `authInterceptor` no-op, gRPC server menerima SEMUA call tanpa kredensial apa pun (`pkg/grpcx/grpcx.go:172-176`); default kosong di `.env.example:59` dan seluruh compose. Sama sekali tidak dipakai di jalur HTTP admin. | **Critical** | T2 (K5) | **resolved (T2)** — `NewServer`/`Dial`/`DialLazy` sekarang gagal boot/dial keras saat token kosong; dibuktikan `TestNewServer_EmptyTokenFailsFast`. |
| TM-02 | Tidak ada mTLS/identitas kriptografis di hop mana pun (gRPC maupun HTTP internal) — transport `insecure.NewCredentials()` di grpcx, HTTP internal polos. Spoofing, tampering, dan information disclosure semuanya realistis dari proses mana pun di network compose yang sama. | **Critical** | T2 (gRPC) + T3 (HTTP) | **resolved (T2+T3)** — seluruh hop gRPC + HTTP internal (8 listener) sekarang mTLS dengan allowlist identitas per-hop. |
| TM-03 | Router internal/admin HTTP (7+ listener) dijaga JWT USER/admin SAJA — tidak ada lapisan identitas service terpisah; endpoint internal bisa dipanggil langsung tanpa lewat BFF/gateway selama penyerang punya token admin valid (mis. hasil sadap traffic plaintext, TM-02). `/metrics` malah tanpa auth sama sekali (lihat TM-04). | **High** | T3 (transport) + T5 (verifikasi authz bypass) | **resolved (T3, dikonfirmasi live T5)** — dibuktikan langsung: request tanpa cert klien atau dengan identitas di luar allowlist DITOLAK di TLS handshake, SEBELUM JWT pernah dicek (lihat Hasil T5 §authz bypass). |
| TM-04 | `/metrics` tanpa autentikasi di semua service; scrape Prometheus juga plaintext. Eksposur eksternal dibatasi (`127.0.0.1` host-publish saja), tapi lintas-container di network compose tetap terbuka. | **Medium** | T3 (K6, `tls_config` + identitas `prometheus`) | **resolved (T3)** — seluruh `/metrics` di balik mTLS, allowlist termasuk identitas `prometheus`; scrape config bertambah `scheme: https` + `tls_config`. |
| TM-05 | Seluruh secrets aplikasi (JWT_SECRET, INTERNAL_GRPC_TOKEN, password Postgres per-role, vendor secrets, admin bootstrap password) tersimpan sebagai env plaintext di `.env`/compose — satu-satunya pengecualian baik adalah `grafana_admin_password` (file 0600 gitignored). Tidak ada check "tolak secret default di production". | **High** | T4 (K7, Vault dev + fallback env) | **partially addressed (T4)** — Vault dev-mode kini TERSEDIA sebagai sumber alternatif (`vaultGetenv`, opt-in), tapi TIDAK wajib: default (VAULT_ADDR/TOKEN unset) tetap plaintext env sepenuhnya sesuai desain K7. Residual TETAP terbuka sampai ada mekanisme yang MEWAJIBKAN Vault di produksi — di luar scope doc 49. |
| TM-06 | `pkg/middleware/cors.go:25` default `AllowedOrigins: []string{"*"}` — permisif untuk SEMUA origin (mitigasi parsial: `AllowCredentials:false`). Dipakai di service mana pun yang memanggil `DefaultCORSConfig()` tanpa override. | **Medium** | T5 (verifikasi pemakaian nyata) → T6 (fix ke allowlist eksplisit atau kosongkan untuk API-only) | **resolved (T6)** — `DefaultCORSConfig()` sekarang `AllowedOrigins: nil` (kosong = API-only, konfirmasi arsitektur: gateway+auth-service 100% bearer-token, tidak pernah `http.SetCookie`, jadi tidak ada apapun untuk di-CSRF/leak lewat cross-origin credentialed request). `authCORS`/`corsConfig` (satu-satunya 2 pemakai nyata `DefaultCORSConfig()` — internal service lain tidak browser-facing) sekarang: production tetap pin ke `App.BaseURL` (tidak berubah), non-production baca allowlist eksplisit dari `CORS_ALLOWED_ORIGINS` (comma-separated, field `AppConfig.AllowedOrigins` yang sebelumnya sudah ada di struct tapi tidak pernah di-wire ke env) — default kosong bila env tidak diset. Dibuktikan unit test end-to-end lewat router asli (`TestAuthServiceRouter_NoWildcardCORSByDefault`): origin sembarang lewat preflight `OPTIONS` TIDAK lagi mendapat header `Access-Control-Allow-Origin` sama sekali. |
| TM-07 | `pkg/middleware/auth.go:94` — validasi issuer JWT di-skip total saat `expectedIssuer==""`; `JWT_ISSUER` tidak wajib diisi di config manapun (hanya warning produksi). Token dari konfigurasi lain (atau dites lupa di-set) tetap diterima. | **Medium** | T5 (konfirmasi dampak nyata) → T6 (wajibkan issuer di semua service) | **resolved (T6)** — `internal/config.validate()` sekarang menolak boot bila `JWT_ISSUER` kosong, di SEMUA environment (bukan cuma warning produksi) — pola sama dengan `JWT_SECRET` dan K5's token fail-closed. Warning produksi lama di `Config.Warnings()` DIHAPUS karena sudah tidak bisa pernah terpicu (validate() sudah memblokir boot lebih dulu). Blast radius diselesaikan penuh: `docker-compose.yml` — `JWT_ISSUER: ${JWT_ISSUER:-seev}` ditambahkan ke SEMUA 8 service block (sebelumnya hanya admin-bff-service yang punya baris ini, dengan default kosong); `scripts/lib.sh` — `JWT_ISSUER="${JWT_ISSUER:-seev}"` + di-export di 9 titik start-service + `gen_token()`; `.env.example` — `JWT_ISSUER=seev` (bukan kosong); `cmd/gentoken` (dipakai chaos/smoke test untuk mint token) sekarang WAJIB `JWT_ISSUER` di environment dan menyertakan klaim `iss` — tanpa ini token yang di-generate harness sendiri akan ditolak begitu semua service mewajibkan issuer. `nightly.yml` tidak perlu diubah (JWT_ISSUER bukan secret, warisan default `seev` dari lib.sh sudah cukup); `ci.yml` tidak perlu diubah (docker-compose default sudah menutupi). Dibuktikan unit test (`internal/config`: `TestLoadFromEnv_MissingJWTIssuer` + `TestLoadFromEnv_MissingRequiredVars` assert pesan error menyebut `JWT_ISSUER`) + full `go build ./... && go test ./...` hijau lintas repo. |
| TM-08 | Webhook HMAC (`internal/vendorgw/mockvendor/mockvendor.go`) tidak mengikat timestamp ke dalam signature — replay dibatasi murni oleh freshness bisnis `OccurredAt` + dedup `VendorEventID`, bukan kriptografis. | **Low** | T5 (nilai risiko nyata: apakah `VendorEventID` cukup unpredictable) → T6 (perbaiki atau accepted-risk tertulis) | **accepted-risk (T6)** — TIDAK diperbaiki, dengan alasan tertulis: (1) `mockvendor` secara eksplisit adalah test-double buatan sendiri (dokumentasi paket sendiri: "made up... not modeled on any real vendor" — `mockvendor.go:1-6`), dipakai HANYA untuk menguji jalur dedup/posting `internal/payin` end-to-end, bukan integrasi sungguhan; (2) risiko sudah dibatasi ganda tanpa perlu perbaikan kriptografis — signature (`Sign(secret, rawBody)`, mencakup SELURUH body termasuk `occurred_at`) tetap wajib valid (penyerang butuh secret vendor bocor lebih dulu), DAN dedup `VendorEventID` mencegah event yang SAMA diproses dua kali (dibuktikan live T5: request kedua dengan `event_id` sama tidak menggandakan saldo) — celah nyata HANYA replay `event_id` BARU dengan `occurred_at` basi, yang tetap butuh secret yang sama untuk ditandatangani; (3) mendesain kebijakan freshness-window (mis. tolak `occurred_at` > N menit) terhadap wire format buatan sendiri TIDAK men-generalisasi ke vendor sungguhan — kebijakan clock-skew/redelivery real vendor (yang bisa mengirim ulang webhook berjam-jam setelah outage) harus ditentukan berdasarkan SLA vendor asli saat diintegrasikan nanti, bukan diputuskan sekarang untuk test-double ini. Residual risk yang tersisa (replay `event_id` baru + `occurred_at` basi oleh penyerang yang SUDAH memegang secret vendor) dianggap dapat diterima karena penyerang pada skenario itu sudah bisa memalsukan APAPUN — freshness-window tidak menaikkan bar keamanan secara berarti terhadap kompromi secret, hanya terhadap replay MITM/log-leak murni, yang sudah dibatasi dedup. Follow-up: kebijakan freshness sungguhan (jika perlu) didesain bersamaan saat vendor real pertama diintegrasikan (`internal/vendorgw` sibling package baru), bukan di sini. |
| TM-09 | `assurance-service` (doc 48, 9 proses total bukan 7) tidak tercatat di docs/plan/49 K3 (daftar service certgen)/K4 (matriks allowlist)/K6 (daftar listener flip) saat dokumen ditulis. Menambah 3 dial-site gRPC + 1 listener HTTP admin (:8096). Dikonfirmasi langsung: admin-bff DAN assurance-service SAMA-SAMA tidak ada di target scrape Prometheus. Bukan celah baru, tapi risiko proses: kalau terlewat di T2/T3, assurance-service TETAP plaintext/tanpa identitas setelah gate "selesai". | **Medium** (risiko proses, bukan kerentanan aktif) | T2 + T3 (WAJIB perlakukan setara admin-bff, lihat §4) | **resolved (T2+T3)** — assurance-service memuat identitas TLS sendiri, dial gRPC-nya mTLS, listener HTTP-nya mTLS, dan job scrape Prometheus-nya ditambahkan di T3. |
| TM-10 | Vault dev-mode (T4, K7) akan bicara HTTP plaintext ke klien config-loader di network compose yang sama — secrets yang diambil dari Vault sendiri melintasi hop tanpa TLS kecuali T2/T3's mTLS turut menutupinya (Vault BUKAN salah satu identitas di K3/K4 hari ini). | **Medium** (residual sadar per K7) | T4 (dokumentasikan eksplisit); TLS listener Vault = follow-up di luar doc 49 | accepted-risk (per K7) — dikonfirmasi tetap berlaku setelah T4 selesai; TLS listener Vault tetap follow-up di luar doc 49. |
| TM-11 | Rate limiter (`pkg/middleware/rate_limit.go:52,74`) memakai `r.RemoteAddr` MENTAH sebagai kunci — termasuk PORT EFEMERAL sisi klien. Setiap koneksi TCP baru dari klien yang sama (mis. curl tanpa `--keepalive`, atau browser yang membuka koneksi baru) mendapat kunci rate-limit BARU, sehingga limit efektif per-klien nyaris tidak pernah tercapai. | **Medium** | T5 (dikonfirmasi live) → T6 (kunci berdasar IP saja, strip port, atau tambah dimensi user/token) | open — **dikonfirmasi live T5, dua-arah**: 15 request cepat dari 15 KONEKSI BARU → 0 dari 15 kena 429 (limiter efektif tidak aktif); 15 request yang sama lewat SATU koneksi keep-alive (port tetap) → 10 lolos lalu 5 kena 429, PERSIS sesuai konfigurasi `Requests:10, Per:1m` (`internal/handler/router.go:207`) — membuktikan limiter itu sendiri BENAR, tapi kunci yang dipakai membuatnya trivial dihindari. Sudah dikenal sejak chaos scenario 9 (docs/plan/45) sebagai catatan desain; T5 pertama kali mendaftarkannya sebagai temuan formal dengan bukti. |
| TM-12 | `pkg/logger.ReadAndMaskRequestBody` (dipanggil dari `pkg/middleware.WithLogger`, dipakai di 12 router) membaca body request untuk keperluan LOGGING dengan `io.LimitReader(r.Body, max+1)` (`max`=16KiB) — TAPI kemudian me-REKONSTRUKSI `r.Body` dari potongan yang SUDAH DIPOTONG itu (`pkg/logger/masking.go:183-189`), bukan mengembalikan body asli utuh ke handler berikutnya. Efeknya: SETIAP request berbody >16KiB lewat middleware ini terpotong SENYAP sebelum sampai ke handler sungguhan — bukan hanya untuk logging. Ditemukan langsung lewat live-test webhook oversize (K8): body 70KB terkirim utuh (`Content-Length: 70210` dikonfirmasi di request), TAPI handler menerima versi terpotong 16KB → signature HMAC tidak cocok lagi → response `401 Unauthorized` yang MENYESATKAN (menyaru kegagalan auth), bukan `413 Request Entity Too Large` yang sebenarnya dimaksud desain `maxWebhookBodyBytes=64KiB` di `internal/handler/webhook.go:17`. Proteksi ukuran 64KiB itu sendiri jadi DEAD CODE untuk request >16KB — batas EFEKTIF adalah 16KB, senyap, di layer yang salah. | **Medium-High** | T5 (ditemukan lewat eksekusi live, bukan review) → T6 (baca body SEKALI sebelum logging+handler, atau logging middleware jangan pernah mengganti `r.Body` dengan potongan) | open — bukan kerentanan bypass-auth (signature TETAP wajib valid terhadap APAPUN yang benar-benar diterima handler), tapi bug korup-request nyata yang memengaruhi SEMUA 12 router berbody >16KB, bukan cuma webhook — ditandai Medium-High karena luas cakupan + error message yang menyesatkan operator men-debug. |
| TM-13 | `pkg/tlsx/config.go`'s `ServerConfig` (K2, ditulis T2) menetapkan `ClientCAs: src.CAPool()` sebagai SNAPSHOT saat `*tls.Config` dikonstruksi — Go's `tls.RequireAndVerifyClientCert` memverifikasi klien terhadap pool BEKU itu SELAMANYA, tidak pernah dibaca ulang. Komentar T2 sendiri di kode SUDAH mencatat ini sebagai "a T6 concern" tapi belum diperbaiki. Efeknya: setelah `certgen rotate`, SEMUA listener server (gRPC ATAU HTTP internal, di KE-8 service — satu titik `ServerConfig` dipakai semua) menolak klien yang baru direissue SELAMANYA, sampai proses server itu sendiri di-restart — bukan jendela sempit yang pulih sendiri, tapi kegagalan PERMANEN yang membatalkan premis inti "rotasi tanpa restart" K2/K9. | **High** | T6 K9 (ditemukan LANGSUNG oleh rotation drill `scripts/rotation-drill.sh` — bukan lewat review kode; drill awal melaporkan 58/64 request gagal dan TIDAK PERNAH pulih dalam observasi 15 detik, dan analisis log `ledger-service` mengonfirmasi "certificate reloaded" tercatat tapi handshake tetap gagal SESUDAHNYA) → diperbaiki T6 | **resolved (T6)** — `ServerConfig` diubah: `ClientAuth: tls.RequireAnyClientCert` (bukan lagi `RequireAndVerifyClientCert`, yang memaksa Go memverifikasi terhadap pool beku) + `VerifyConnection` sekarang memanggil `verifyChainAgainst(cs, src.CAPool(), x509.ExtKeyUsageClientAuth)` secara manual — `src.CAPool()` dipanggil FRESH di setiap handshake, pola yang sama yang SUDAH BENAR dipakai `ClientConfig` untuk arah sebaliknya (dial keluar). Field `ClientCAs` yang beku dihapus total dari `ServerConfig`; field `RootCAs` yang setara-mati di `ClientConfig` (tidak pernah dipakai karena `InsecureSkipVerify:true`) diberi komentar penjelas eksplisit agar jebakan yang sama tidak terulang. Dibuktikan: (a) unit test baru `TestServerConfig_HotRotatesCAWithoutRebuildingConfig` (`pkg/tlsx/config_test.go`) — SATU `*tls.Config` yang sama, tidak pernah dikonstruksi ulang, menerima klien yang di-reissue di bawah CA baru DAN menolak klien pra-rotasi, lulus 15/15 run stress `-race -count=15`; (b) `scripts/rotation-drill.sh` live end-to-end terhadap ledger-service sungguhan: SEBELUM fix, 58/64 request loop gagal dan TIDAK PERNAH pulih (klien baru maupun lama sama-sama ditolak selamanya); SESUDAH fix, hanya 9/93 gagal, seluruhnya dalam jendela 10 detik pertama pasca-rotasi (bukan lagi tanpa batas), lalu 100% pulih sendiri untuk sisa loop — DAN klien pra-rotasi tetap konsisten ditolak (`curl exit 56`, TLS handshake gagal) tanpa batas waktu. Residual yang JUJUR dicatat (bukan bug baru, karakteristik desain K2 yang melekat): reload berbasis POLL (bukan push/fsnotify) tetap punya jendela pendek (dibatasi `defaultPollInterval=5s`, teramati live ~2 detik) di mana klien yang BARU SAJA direissue transient tertolak sampai poll berikutnya — drill diberi kriteria lulus yang jujur mencerminkan ini (nol kegagalan sebelum rotasi + semua kegagalan dalam grace window 2× poll interval + ekor loop setelahnya 100% sukses), bukan menuntut nol kegagalan mutlak yang tidak realistis untuk desain poll-based apa pun. |

| TM-14 | `internal/fraud/velocity_store.go`'s `FailClosedVelocityStore.Get()`/`Record()` passed the CALLER's full remaining context straight into the live Redis attempt taken while `switcher.Healthy()` is still (stale-)true — the window right after Redis actually goes down but before `Degrade()` fires on that failed attempt. A `docker stop`'d Redis container can leave a network black-hole (no RST, no response) rather than an immediate connection-refused, so that one attempt can hang far longer than `pkg/fraudcheck`'s 500ms `screenTimeout` — the CALLER's own identical-origin deadline (propagated via gRPC) then expires with a raw `context.DeadlineExceeded`, which does NOT match `errors.Is(err, fraudcheck.ErrDependencyUnavailable)`, so `internal/ledger/transport/http.go`'s handler falls through to its fail-OPEN branch ("screening check error, failing open") instead of the fail-closed one — money moves that should have been rejected. | **High** (money-safety fail-open, not a mTLS/threat-model K1–K10 finding — pre-existing, unrelated to doc 49's own scope) | Out of scope for doc 49/T6 by the doc's own §6 constraint (fail-closed fraud logic is explicitly protected money-safety code) — found live by T6's isolated GATE 3 run (chaos scenario 9, 100% reproducible in isolation), confirmed via `git diff HEAD -- scripts/chaos-test.sh` to be **zero-diff or pristine** for scenario 9's own code, i.e. present in already-committed HEAD, unrelated to any change this session (mTLS, rate-limit, JWT-issuer, or the pre-existing unrelated KYC-rescreen work). Explicit user authorization obtained mid-session to fix it as part of T6 rather than leave GATE 3 permanently red or silently skip it. | **resolved (T6, explicitly authorized out-of-scope fix)** — added `redisAttemptTimeout = 150ms` inside `FailClosedVelocityStore.Get()`/`Record()`: the live Redis attempt now runs under `context.WithTimeout(ctx, redisAttemptTimeout)`, a hard sub-deadline of the caller's own context (never longer — `context.WithTimeout` on an already-deadlined parent takes whichever is sooner) — a hung/black-holed attempt now fails fast enough to leave ample room (350ms+) for a correctly-classified `ErrDependencyUnavailable`/503 to reach the caller within its own 500ms budget. Proven: (a) new unit test `TestFailClosedVelocityStore_SlowFirstAttempt_FailsClosedWithinBudget` (`internal/fraud/velocity_store_test.go`) uses a synthetic `hangingVelocityStore` that blocks until context cancellation — exactly the network-black-hole failure mode a `docker stop`'d container produces and a locally-`Close()`'d miniredis (the existing test fixture) does NOT reproduce, which is why this bug passed unit tests while failing live — asserts fail-closed within 250ms of a 500ms budget; (b) chaos scenario 9 re-run live end-to-end, twice (isolated and via the default stack): "withdraw_initiate fails closed with 503 DEPENDENCY_UNAVAILABLE" and "fail-closed rejection moved no money" both pass, where they previously failed 100% reproducibly. `pkg/cache.FailoverLimiter`/`FailoverCounter` (rate limiter, policy counter) were deliberately NOT touched — not observed exhibiting this symptom (their callers carry much larger outer timeout budgets that already absorb one slow attempt), and touching shared `pkg/cache` code has a blast radius across every rate-limited/policy-counted path in every service, well beyond what was authorized. |

## 7. Referensi

- Keputusan desain: [docs/plan/49](../plan/49-a6-internal-security.md) K1–K10.
- Fakta live diverifikasi 2026-07-21 terhadap: `pkg/grpcx/grpcx.go`,
  `cmd/{gateway,auth-service,ledger-service,payin-service,payout-service,
  fraud-service,admin-bff-service,assurance-service}/main.go`,
  `pkg/middleware/{cors.go,auth.go}`, `internal/vendorgw/mockvendor/
  mockvendor.go`, `docker-compose.yml`, `scripts/lib.sh`,
  `deploy/observability/prometheus/prometheus.yml`, `PROJECT_GUIDE.md`.
- Update berikutnya: setiap kali T2–T6 menutup satu TM, ubah kolom Status
  jadi `fixed` (+ commit/PR ref) atau `accepted` (+ alasan tertulis di sini,
  bukan hanya di commit message).
