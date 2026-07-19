# 41 — Phase 7f: MVP Acceptance — Journey Final, Chaos, Docs

> Baca master reference di [36-phase7a-request-tracing.md](36-phase7a-request-tracing.md). Prasyarat: doc 40 selesai.

## Konteks

Fase penutup rangkaian MVP: satu script membuktikan seluruh produk (register → KYC → quote → transaksi → payout dengan failover → trace penuh → admin ops), konsolidasi chaos suite, dan finalisasi dokumentasi. Setelah fase ini rangkaian docs 36–41 dinyatakan SELESAI dan repo berstatus MVP product.

## T1 — `business-e2e.sh` bentuk final (journey acceptance MVP)

### Langkah
Journey berurutan (satu run, fresh volume):
1. Register user (L0) → attempt transfer → **403 KYC_REQUIRED**.
2. Submit KYC L1 (mock auto-approve) → refresh token → topup via intent+webhook (ter-screen pra-posting, request_id tercatat di `payin_*`) → transfer P2P kecil OK → transfer di atas cap L1 → **422 policy limit**.
3. Submit KYC L2 (refer) → admin approve via auth :8083 → refresh → transfer besar OK (limit naik).
4. Fee quote: `POST /api/v1/ledger/fees/quote` → fee tampil → ubah `fee_rules` via admin API → transfer dengan `quote_id` lama → fee leg = quote PERSIS (bukan rule baru) → tamper amount → **422 QUOTE_MISMATCH** → re-quote → sukses.
5. Payout ber-quote + drill failover: quote `withdraw_settle` → force-fail mockvendor → `POST /payout {quote_id}` ter-route ke mockvendor2 (assert admin vendor health + `payout_vendor_calls`) → settle memakai fee TERSIMPAN → balanced.
6. Trace: ambil request_id dari transfer langkah 4 → assert muncul di log gateway + ledger-service + fraud-service DAN di `ledger_transactions.metadata->>'request_id'` + `screening_events.request_id`; untuk payout langkah 5 assert `payout_requests.request_id` + CorrelationId di consumer event (log velocity fraud).
7. Admin surfaces: KYC pending list (auth :8083), fraud events (`GET /api/v1/admin/fraud/events` — sudah ada), vendor health (:8092/:8093), fee rules (ledger :8091), recon + outbox dead (existing).
8. Invariant akhir: `fn_verify_ledger_balance()` + `v_account_balance_audit` + no-stuck-pending = 0 baris/konsisten.

### Test wajib
- Satu run `./scripts/business-e2e.sh` hijau dari volume Docker baru.

### DoD
- [x] Satu perintah membuktikan seluruh journey produk MVP dari register sampai ops harian.

### Hasil
Langkah 1–4 dan 7–8 sudah ada dari sesi doc 25/36–39 sebelumnya (`scripts/business-e2e.sh`
section 1–5, 7, 8). Yang ditambahkan sesi ini:

**Langkah 5 (drill failover pada payout ber-quote)** — ditambahkan ke `quote_journey()`
(section 6), setelah payout ber-quote existing:
1. Quote `withdraw_settle` baru (25000, fee=8000 sesuai rule yang sudah di-reprice section
   sebelumnya).
2. Seed `mockvendor2` (vendor-gateway `gopay` + routing rule **priority 11** — angka LEBIH
   BESAR dari rule mockvendor yang sudah ada di priority 10 dari section 5, karena
   `ResolveCandidates` (docs/plan/40 Task T2) mengurutkan ASC/angka terkecil menang duluan;
   dicatat sebagai gotcha di PROJECT_GUIDE.md sesi sebelumnya).
3. Force-fail mockvendor via admin endpoint (docs/plan/40 Task T4).
4. Payout "probe" kecil (tanpa quote) — masih ter-route ke mockvendor (breaker belum trip),
   Submit gagal (uncertain, force-fail = transport error asli), PINNED, men-trip breaker
   (`BREAKER_FAILURE_THRESHOLD=1` diexport khusus run ini, sama seperti `chaos-test.sh 8`).
5. Assert admin vendor health (:8093) melaporkan mockvendor `open`.
6. Payout ber-quote (quote_id dari langkah 1) dibuat — routing (T2) skip mockvendor yang
   circuit-nya open, langsung ke mockvendor2 → settle instan.
7. Assert fee yang dibebankan = TEPAT fee ter-quote (8000), bukan rule saat itu.
8. Mockvendor dipulihkan (force-fail off) di akhir.

**Langkah 6 (trace payout)** — ditambahkan ke `trace_check()` (section 8): payout ber-quote
failover di atas dibuat dengan header `X-Request-Id` custom; assert
`payout_requests.request_id` cocok, DAN assert request_id yang sama muncul di log
fraud-service (consumer velocity async). **Perbaikan kode ditemukan saat menulis test
ini**: `internal/fraud.Module.handleDelivery` (velocity consumer) TIDAK PERNAH melog apa pun
di jalur sukses — tidak ada cara membuktikan request_id sampai ke consumer ini meski
propagasinya (docs/plan/36 Task T4, CorrelationId AMQP) sudah lengkap sejak awal. Ditambah
satu baris log `"fraud: velocity recorded"` dengan `middleware.RequestIDFromCtx(ctx)` —
melengkapi cerita verifikasi propagasi request_id yang doc 36 T4 sendiri janjikan tapi belum
pernah benar-benar dibuktikan untuk consumer ASYNC ini (baru dibuktikan untuk publish sisi
gateway/ledger/HTTP). Regresi kecil dari perubahan ini: `TestHandleDeliveryRecordsPostedUser`
di `internal/fraud/consumer_test.go` memanggil nil `*slog.Logger` (test membuat `&Module{}`
langsung tanpa logger, byte konvensi `NewModule`'s nil-default tidak berlaku) — panic nil
pointer; diperbaiki dengan helper `discardLogger()` baru di test yang sama (pola yang sudah
dipakai `internal/notify`), dipasang di SATU test yang benar-benar mencapai jalur sukses
(dua test lain return lebih awal sebelum baris log baru).

**Langkah 7 (admin surfaces)** — ditambahkan ke `ops()` (section 7): `GET
/admin/kyc/submissions?status=pending` (auth-service internal, docs/plan/39 T3) dan `GET
/admin/{payin,payout}/vendors/health` (docs/plan/40 Task T5) — melengkapi enumerasi surface
admin yang diminta Langkah #7 (fraud events, fee-rules, recon, outbox dead sudah ada dari
sebelumnya).

**Tooling baru**: `await_log_line(logfile, pattern, description)` di `scripts/lib.sh`
(paralel `await_notification` — polling, bukan grep sekali, karena jalur outbox relay →
RabbitMQ → consumer bersifat asinkron). `BREAKER_FAILURE_THRESHOLD=1` diexport di
`business-e2e.sh` (sama rasionalnya dengan `POLICY_CACHE_TTL=2s` yang sudah ada — override
production default 5 supaya drill deterministik & cepat).

**Verifikasi**: `go build ./...`, `go vet ./...`, `gofmt -l internal/fraud/` bersih, `go test
./internal/fraud/...` hijau (termasuk fix regresi di atas), satu run penuh
`./scripts/business-e2e.sh` dari volume Docker bersih (`docker compose down -v`) — SEMUA
assertion (termasuk section 6 failover drill dan section 8 payout trace baru) hijau.

## T2 — Konsolidasi chaos

### Langkah
1. `scripts/chaos-test.sh all` final: skenario 1–6 existing + skenario 7 (fraud down fail-open TIGA flow + block mode, dari doc 37) + skenario vendor-failover (doc 40) — semua jalan dengan fixture user KYC-approved dari `scripts/lib.sh` (doc 39 T6).
2. SEMUA skenario tetap diakhiri `assert_ledger_balanced`.

### Test wajib
- `./scripts/chaos-test.sh all` hijau penuh dari proses bersih.

### DoD
- [x] Tidak ada skenario kegagalan (service down, vendor down, infra down) yang menghilangkan/menggandakan uang di topologi MVP final.

### Hasil
Audit terhadap `scripts/chaos-test.sh` sekarang (8 skenario, ditulis lintas doc 12/23/37/40)
mengonfirmasi kedua Langkah SUDAH terpenuhi tanpa perubahan kode:

- **Fixture KYC-approved**: `grep -n "gen_token\|/auth/register"` menunjukkan SETIAP token user
  di kedelapan skenario dibuat via `gen_token` (helper `scripts/lib.sh`, membungkus
  `cmd/gentoken`), TIDAK ADA yang lewat `/auth/register` mentah. `cmd/gentoken`'s sendiri
  men-default `kyc_level=1` (docs/plan/39 Task T6, gotcha #9 master) — inilah tepatnya
  "fixture user KYC-approved dari scripts/lib.sh" yang diminta Langkah #1; setiap skenario yang
  memanggil rute gated (transfer, topup, payout) otomatis lolos gate tanpa dance KYC eksplisit.
- **`assert_ledger_balanced` di akhir setiap skenario**: `grep -n
  "^scenario_[0-9]*()\|assert_ledger_balanced"` menunjukkan kedelapan fungsi `scenario_N()`
  masing-masing memanggilnya tepat sekali sebagai langkah verifikasi penutup (skenario 1–7 dari
  sesi-sesi sebelumnya, skenario 8 "vendor failover" dari doc 40 Task T6).

**Verifikasi**: `./scripts/chaos-test.sh all` dijalankan ulang dari volume Docker bersih
(`docker compose down -v`) setelah perubahan T1 sesi ini (`internal/fraud/fraud.go`'s log line
baru, yang DISENTUH oleh skenario 7's fraud-down test) — kedelapan skenario hijau penuh,
termasuk `fn_verify_ledger_balance()` dan `v_account_balance_audit` bersih di setiap skenario.

## T3 — Dokumentasi final

### Langkah
1. `docs/plan/README.md`: baris 36–41 status ✅.
2. `README.md` root: tabel env baru (`FEE_QUOTE_TTL`, `FRAUD_GRPC_ADDR` di payin/payout, `MOCKVENDOR2_ENABLED/SECRET`, `BREAKER_FAILURE_THRESHOLD/COOLDOWN`) + endpoint baru (fees/quote, users/me/kyc, admin kyc/vendors-health).
3. `PROJECT_GUIDE.md`: perbarui arsitektur runtime (fraud kini di edge orchestrator, bukan di dalam posting; fee quote flow; KYC gate), runbook per-service (fraud-service down kini berarti fail-open di TIGA flow), dan daftar future work (tambahkan deferral resmi dari master doc 36: fee topup, KYC provider riil, breaker terdistribusi, purge fee_quotes, aktivasi non-IDR, OTel span luas, retry queue ApplyKycTier).

### Test wajib
- Review silang: semua env/endpoint yang disebut docs benar-benar ada di kode/compose.

### DoD
- [x] Rangkaian docs 36–41 tertutup rapi; dokumentasi mencerminkan sistem nyata.

### Hasil
1. **`docs/plan/README.md`**: baris 41 diubah `⬜ todo` → `✅ done` (36–40 sudah `✅ done` dari
   sesi-sesi sebelumnya).
2. **`README.md` root** — dua tabel baru ditambahkan setelah "Development commands":
   - "Key environment variables (docs/plan/36–41)": `FEE_QUOTE_TTL` (ledger-service),
     `FRAUD_GRPC_ADDR` (payin/payout — sebelumnya hanya terdaftar di ledger-service),
     `MOCKVENDOR2_ENABLED`/`MOCKVENDOR2_SECRET`, `BREAKER_FAILURE_THRESHOLD`/`BREAKER_COOLDOWN`.
   - "Key API endpoints (docs/plan/36–41)": `POST /api/v1/ledger/fees/quote`, `GET`/`POST
     /api/v1/users/me/kyc`, `GET /api/v1/admin/kyc/submissions`, `GET
     /admin/{payin,payout}/vendors/health`.
   - `.env.example` DIPERBARUI juga (bukan cuma README) — `FEE_QUOTE_TTL` di bagian
     ledger-service, dan `MOCKVENDOR2_ENABLED/SECRET` + `FRAUD_GRPC_ADDR` +
     `BREAKER_FAILURE_THRESHOLD/COOLDOWN` di KEDUA bagian payin-service DAN payout-service
     (sebelumnya sama sekali tidak terdaftar di sana meski sudah dibaca kode sejak doc 37/40).
3. **`PROJECT_GUIDE.md`**:
   - Paragraf arsitektur runtime ditulis ulang: KYC gate L0/L1/L2 di auth (403 KYC_REQUIRED
     sebelum cek apa pun), fraud SEKARANG di EDGE tiap flow (ledger transport pra-tx P2P,
     payin pra-posting, payout pra-hold — BUKAN "ledger screens postings" seperti kalimat
     lama yang sudah tidak akurat sejak doc 37 memindahkan seam keluar dari transaksi
     posting), fee quote (harga terkunci, dihormati persis), circuit breaker per-vendor +
     aturan anti-double-payout (uncertain = pinned selamanya).
   - Runbook "fraud-service down" ditulis ulang: TIGA titik panggil fail-open independen
     (bukan cuma "ledger's screening hook"), masing-masing log ERROR di titik panggilnya
     sendiri; referensi `chaos-test.sh 7`.
   - "Known future work" ditambah daftar deferral resmi PERSIS dari `docs/plan/36`'s sendiri
     ("Deferral resmi rangkaian ini (catat di future work doc 41)"): fee topup, provider KYC
     riil + penyimpanan dokumen + downgrade level, breaker terdistribusi (Redis), purge job
     `fee_quotes` kedaluwarsa, aktivasi non-IDR end-to-end, OTel span di luar ledger, retry
     queue async `ApplyKycTier`. Ditambah pointer ke doc 42 (roadmap jangka panjang,
     referensi, jangan dieksekusi spekulatif).

**Test wajib (review silang)** — setiap env/endpoint yang disebut diverifikasi LANGSUNG
terhadap kode via `grep`, bukan diasumsikan dari ingatan:
- `FEE_QUOTE_TTL`, `FRAUD_GRPC_ADDR`, `MOCKVENDOR2_ENABLED/SECRET`,
  `BREAKER_FAILURE_THRESHOLD/COOLDOWN` — semua dikonfirmasi di `internal/config/config.go`
  dan dibaca oleh `cmd/payin-service/main.go`/`cmd/payout-service/main.go` (untuk
  `FraudGRPCAddr`) yang sesuai.
- `POST /api/v1/ledger/fees/quote`, `GET`/`POST /api/v1/users/me/kyc`, `GET
  /api/v1/admin/kyc/submissions` — dikonfirmasi persis di `internal/handler/router.go`,
  `cmd/auth-service/router.go`.
- `GET /admin/{payin,payout}/vendors/health` — dikonfirmasi di `internal/payin/http.go` +
  `internal/payout/http.go` (doc 40 Task T5).

**Verifikasi**: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l
.` bersih (di luar `internal/policy/alert_test.go` pre-existing), `make lint` bersih, `go test
./...` hijau.

---

## Verifikasi akhir dokumen
T1+T2+T3 hijau + gate standar master doc 36 = rangkaian MVP SELESAI. Repo berstatus produk MVP
fintech untuk pembelajaran: KYC bertingkat, fee fair, fraud efisien, multi-vendor tahan
gangguan, trace end-to-end.

Gate penuh dijalankan dari volume Docker bersih (`docker compose down -v`) sesi ini via `make
verify-full`: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `make lint`,
`make test` (`-race`), `./scripts/smoke-test.sh`, `./scripts/business-e2e.sh` (journey
acceptance final T1, termasuk drill failover payout ber-quote + trace-nya) — SEMUA hijau.

**Satu flake transien tercatat, diverifikasi BUKAN regresi**: pada run `make verify-full`
tunggal ini, `chaos-test.sh` scenario 5 (payout crash-mid-flight, docs/plan/23 Task T6 —
TIDAK disentuh sesi ini) gagal pada assertion timing-nya (resume job cron tick tidak
sempat memproses keempat kill-point dalam window 65 detik yang sama, kemungkinan karena
beban sistem lebih berat menjalankan `make test -race` + smoke + business-e2e berurutan
tanpa jeda sebelum chaos dimulai). Diverifikasi ulang DUA KALI dari volume bersih segera
setelahnya: `./scripts/chaos-test.sh 5` sendirian (hijau penuh) DAN `./scripts/chaos-test.sh
all` kedelapan skenario sekaligus (hijau penuh, termasuk scenario 5). Tidak ada kode sesi ini
(`internal/fraud/fraud.go`, `scripts/business-e2e.sh`, `scripts/lib.sh`) yang menyentuh jalur
resume-job/cron payout sama sekali — kesimpulan: flake timing pra-eksisting scenario 5 di
bawah beban berurutan berat, bukan regresi dari perubahan doc 41. Rangkaian docs/plan/36–41
(MVP produk) dinyatakan SELESAI; lanjutan berikutnya adalah docs/plan/42 (roadmap jangka
panjang, status referensi, tunggu trigger per track sebelum menulis dokumen eksekusi 43+).
