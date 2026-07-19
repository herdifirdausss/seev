# 44 — Track A2 (bagian CI): CI Naik Kelas — Full-Stack Gate di GitHub Actions

> Lahir dari track **A2** di [42-long-term-roadmap.md](42-long-term-roadmap.md)
> §4. Dokumen ini HANYA paruh CI dari track A2 — paruh Kubernetes-nya adalah
> eksekusi [35-phase6j-kubernetes.md](35-phase6j-kubernetes.md) yang SUDAH ada
> (aturan doc 42 §2 poin 4: bagian itu tidak melahirkan dokumen baru).
> Self-contained untuk eksekusi, tetapi eksekutor tetap WAJIB mematuhi
> [PROJECT_GUIDE.md](../../PROJECT_GUIDE.md), boundary test, dan constraint di bawah.
>
> **Status verifikasi: SIAP DIEKSEKUSI (2026-07-17).** Fakta repo (nama job,
> path script, variabel lib.sh, port) diverifikasi via grep pada tanggal itu;
> fakta eksternal (versi actions, spec runner) WAJIB diverifikasi ulang
> eksekutor saat eksekusi — lihat Constraint #9.

## 1. Bukti trigger

Trigger A2 bagian CI di doc 42 = **"kapan saja (murah, boleh bahkan sebelum
36)"** — jalur keputusan sadar, diambil 2026-07-17. Dependensi hijau:

- Skrip gate dari [34](34-phase6i-verification.md) semuanya ada dan terbukti
  hijau: `scripts/smoke-test.sh`, `scripts/business-e2e.sh`,
  `scripts/chaos-test.sh` (8 skenario + `all`).
- Bukti empiris kebutuhan (tujuan bisnis A2, dari sejarah repo sendiri):
  bug nyata — NULL scan panic saat startup, timezone SQL, multipart
  RequireJSON, `FOR UPDATE OF` alias — HANYA tertangkap smoke/e2e melawan
  Postgres riil, persis kelas gate yang CI hari ini TIDAK jalankan
  (CI = lint + unit + integration testcontainers saja).

## 2. Baseline CI saat ini (hasil audit 2026-07-17)

Satu workflow `.github/workflows/ci.yml`, trigger `push`/`pull_request` ke
`main`, dua job paralel di `ubuntu-latest`:

| Job | Isi | Catatan |
|---|---|---|
| `lint-and-test` | golangci-lint `v1.64.8` (install-mode `goinstall` karena module Go 1.25.6) + `go test -race -cover ./...` | sudah mencakup `boundary_test.go` (tanpa build tag) |
| `integration` | `go test -tags=integration -race ./...` | testcontainers-go pakai Docker daemon runner; tanpa `services:` block |

Pola yang DIPERTAHANKAN: versi Go dibaca dari `go.mod` (step `goversion`),
cache `setup-go` bawaan. Gap yang ditutup dokumen ini: tidak ada
`permissions`/`concurrency`/`timeout-minutes`; smoke/business-e2e/chaos tidak
pernah jalan di CI; tidak ada nightly; tidak ada build image.

## 3. Anti-scope (disalin dari track A2 + turunan dokumen ini)

- **Bukan CD ke cloud; bukan GitOps penuh; bukan multi-cluster** (doc 42).
- Turunan yang dikunci dokumen ini:
  - TIDAK push image ke registry mana pun — build-only (keputusan user).
  - TIDAK menambah secrets repo/organisasi apa pun; kredensial dummy CI
    (JWT, token gRPC internal) di-generate per-run.
  - TIDAK ada self-hosted runner.
  - TIDAK ada migrasi database (butir migrasi checklist §10 doc 42 = N/A).
  - TIDAK mengeksekusi doc 35 (K8s) — itu paruh terpisah track A2.

## 4. Keputusan desain terkunci (K1–K7 — eksekutor DILARANG mengubah)

### K1 — Platform & prinsip workflow

GitHub Actions, runner `ubuntu-latest` (repo: `github.com/herdifirdausss/seev`).
Semua workflow WAJIB:

- `permissions: contents: read` di level workflow (least privilege; tidak ada
  job yang butuh write).
- `concurrency: { group: <workflow>-${{ github.ref }}, cancel-in-progress: true }`
  untuk workflow PR (nightly TIDAK pakai cancel-in-progress).
- `timeout-minutes` eksplisit di SETIAP job (angka per job di K2).
- Versi actions dipin minimal ke major (`actions/checkout@v4`,
  `actions/setup-go@v5`, `actions/upload-artifact@v4`,
  `docker/setup-buildx-action@v3`) — verifikasi versi terkini saat eksekusi
  (Constraint #9); JANGAN memakai `@master`/`@main`.

### K2 — Gate policy: PR ringan, nightly berat (keputusan user)

| Job | Workflow / trigger | Timeout | Artefak |
|---|---|---:|---|
| `lint-and-test` (existing) | `ci.yml` — push/PR ke main | 15m | — |
| `integration` (existing) | `ci.yml` — push/PR ke main | 20m | — |
| `smoke-container` (BARU) | `ci.yml` — push/PR ke main | 20m | log compose bila gagal |
| `business-e2e` (BARU) | `nightly.yml` — cron + `workflow_dispatch` | 30m | WORK_DIR logs (selalu) |
| `chaos-all` (BARU) | `nightly.yml` — cron + `workflow_dispatch` | 45m | WORK_DIR logs + step summary (selalu) |

- Kegagalan nightly TIDAK memblokir merge (informasional), tapi kebijakannya:
  **nightly merah = buka issue hari itu juga** dengan link run + artefak;
  issue ditutup hanya oleh fix atau penjelasan flake yang diverifikasi ulang
  (pola kejujuran doc 41: flake dicatat, tidak disembunyikan).
- Job existing TIDAK diubah perilakunya — hanya diberi
  `timeout-minutes` dan payung `permissions`/`concurrency` level workflow.

### K3 — Script baru `scripts/smoke-container.sh`

Mengotomasi round-trip manual doc [34](34-phase6i-verification.md) T3 —
selama ini satu-satunya bukti topologi container penuh dan hanya pernah
dijalankan tangan:

1. `docker compose down -v` (WAJIB di awal script — jangan asumsikan bersih).
2. `docker compose --profile app up --build -d` → poll sampai SEMBILAN
   container healthy (3 infra + 6 service) dengan timeout eksplisit.
3. Register + login user baru via auth-service publik `:8082`.
4. Buat topup intent via gateway publik `:8080` (routing rule fallback
   hasil seed migrasi).
5. Kirim webhook mockvendor bertanda tangan HMAC (`openssl dgst -sha256
   -hmac` — pola persis `scripts/smoke-test.sh` bagian payin) ke
   `POST /webhooks/mockvendor` gateway → harus `2xx`.
6. Assert via `docker exec ... psql`: `payin_topup_intents.status='settled'`
   di `seev_payin` DAN saldo cash user naik tepat sebesar amount webhook di
   `seev_ledger`.
7. `docker compose --profile app down` di trap exit; exit non-zero pada
   assert gagal mana pun.

Aturan implementasi: port yang dipakai adalah port CONTAINER stack (8080,
8082) — BUKAN port 18xxx milik mode binary-host lib.sh; script ini TIDAK
memakai `start_services`/`build_server` lib.sh (itu lifecycle binary-host).
Boleh source `lib.sh` hanya untuk helper log/psql BILA tidak menarik
lifecycle; kalau ternyata menyulitkan, tulis standalone kecil — TAPI JANGAN
menduplikasi bootstrap binary-host (aturan PROJECT_GUIDE.md: lifecycle bersama
milik `lib.sh`). Target Make baru: `smoke-container`.

### K4 — Build image: build-only + cache layer GHA (keputusan user)

- Keenam image dibangun dari SATU `Dockerfile` existing (`ARG SERVICE`,
  default `gateway`) via `docker compose --profile app build` di job
  `smoke-container`.
- Cache layer: `docker/setup-buildx-action` + cache backend `type=gha`.
  Eksekutor pilih SATU mekanisme dan dokumentasikan di `### Hasil`:
  (a) `docker/bake-action` atas compose file dengan `cache-from/cache-to
  type=gha`, atau (b) compose build biasa dengan driver buildx + env
  `BUILDX_BAKE`/cache args. Kriteria terpenuhi bila run kedua terbukti
  lebih cepat dari run pertama (cache hit terlihat di log build).
- TIDAK ada `docker push` ke registry mana pun; TIDAK ada login registry.

### K5 — Nightly workflow `.github/workflows/nightly.yml`

- Trigger: `schedule` (cron `0 19 * * *` UTC ≈ 02:00 WIB) + `workflow_dispatch`.
- Urutan dalam satu job per script (dua job: `business-e2e`, `chaos-all` —
  boleh sekuensial via `needs:` untuk hemat runner Docker state):
  `docker compose down -v` → jalankan script dengan `KEEP_WORK_DIR=1` →
  `docker compose down -v` lagi sebelum fase berikutnya (gotcha PROJECT_GUIDE.md:
  smoke/chaos menumpuk state; saldo absolut smoke).
- Artefak (`actions/upload-artifact`, `if: always()`, retention 14 hari):
  seluruh `WORK_DIR` (`/tmp/seev-*`) berisi log per service + pid + output.
- `$GITHUB_STEP_SUMMARY`: tabel ringkas per journey/skenario (lulus/gagal)
  yang diparse dari output script.

### K6 — Kompatibilitas lib.sh di runner CI

- `COMPOSE_PROJECT_NAME: seev` di-set eksplisit di env workflow — lib.sh
  hardcode nama container `seev-postgres-1`/`seev-redis-1`/`seev-rabbitmq-1`
  (baris 15–17); checkout dir CI kebetulan bernama `seev`, tapi guard
  eksplisit lebih aman daripada kebetulan.
- `JWT_SECRET` dan `INTERNAL_GRPC_TOKEN` di-generate per-run di step awal
  (`openssl rand -hex 32`) dan diekspor sebagai env job — BUKAN secrets
  repo; nilai default lib.sh hanya fallback dev lokal.
- Port host binary-host 18xxx (gateway 18080/18081, ledger 18090/18091/19091,
  auth 18082/18083, payin 18092/19092, payout 18093/19093, fraud 18094/19094)
  aman di runner GitHub yang bersih; `docker port` autodetect Postgres di
  lib.sh sudah CI-safe.
- `openssl` dan `curl` tersedia default di `ubuntu-latest`; TIDAK perlu
  install psql host (semua akses DB via `docker exec`).

### K7 — Ekonomi CI

- Target wall-time PR < 15 menit total (ketiga job paralel; `smoke-container`
  ~5–8 menit dengan cache hangat, lebih lama di run perdana).
- Nightly boleh 30–60 menit (chaos didominasi `sleep 65` cron resume ×
  beberapa skenario — JANGAN mencoba "mempercepat" dengan mengubah cron
  service atau sleep script; itu mengubah semantik uji).
- Repo di bawah akun personal membakar menit Actions bila private —
  kalau kuota jadi masalah, turunkan cadence nightly ke 3×/minggu dengan
  mengubah cron SAJA (keputusan operasional, tidak butuh revisi dokumen).

## 5. Task

Setiap task: kerjakan Langkah berurutan, tulis Test wajib, penuhi DoD, isi
`### Hasil` setelah selesai. Karena dokumen ini mengubah CI itu sendiri,
"hijau di Actions" dibuktikan dengan LINK run di `### Hasil`.

### T1 — `scripts/smoke-container.sh` + `make smoke-container`

**Langkah**
1. Tulis script per K3 (shebang + `set -euo pipefail`, pola gaya
   `smoke-test.sh`).
2. Tambah target `smoke-container` di `Makefile` (dokumentasikan di komentar
   `## smoke-container:` mengikuti gaya target lain).
3. Tambah satu baris di PROJECT_GUIDE.md bagian build/verification yang menyebut
   script ini (mode container vs mode binary-host).

**Test wajib**
- Lokal: `make smoke-container` hijau 2× berturut-turut, masing-masing dari
  `docker compose down -v` (dibuktikan script melakukannya sendiri).
- Negatif: ubah sementara satu nilai assert (mis. amount) → script exit
  non-zero dan pesan gagal jelas; kembalikan.
- `shellcheck scripts/smoke-container.sh` bersih (atau temuan dicatat +
  dibenarkan di Hasil).

**DoD**
- Round-trip doc 34-T3 kini repeatable satu perintah, exit code benar,
  cleanup di trap; belum ada perubahan workflow CI di task ini.

### Hasil

_(diisi setelah task selesai)_

### T2 — Upgrade `ci.yml`: hardening + job `smoke-container`

**Langkah**
1. Tambah di level workflow: `permissions: contents: read`, `concurrency`
   per K1.
2. Tambah `timeout-minutes` ke `lint-and-test` (15) dan `integration` (20) —
   TANPA mengubah step existing.
3. Job baru `smoke-container` (timeout 20): checkout → setup buildx →
   build keenam image dengan cache `type=gha` (K4) →
   `COMPOSE_PROJECT_NAME=seev` + kredensial per-run (K6) →
   `./scripts/smoke-container.sh` → bila gagal, upload log compose
   (`docker compose logs`) sebagai artefak `if: failure()`.

**Test wajib**
- Branch + PR nyata: ketiga job hijau; link run dicatat.
- Run kedua (re-run atau commit kosong): waktu build `smoke-container`
  turun — bukti cache hit ditempel di Hasil.
- Job existing tetap identik perilakunya (diff `ci.yml` hanya menambah,
  tidak mengubah step lama).

### Hasil

_(diisi setelah task selesai)_

### T3 — `.github/workflows/nightly.yml`

**Langkah**
1. Workflow baru per K5: `schedule` + `workflow_dispatch`;
   `permissions: contents: read`; dua job `business-e2e` (30m) dan
   `chaos-all` (45m, `needs: business-e2e`).
2. Tiap job: checkout → setup Go (pola goversion `ci.yml`) →
   `docker compose down -v` → script dengan `KEEP_WORK_DIR=1` →
   upload artefak `WORK_DIR` `if: always()` (retention 14 hari).
3. Step summary: parse output script menjadi tabel journey/skenario di
   `$GITHUB_STEP_SUMMARY`.
4. Tulis kebijakan "nightly merah = buka issue" (K2) di README root bagian CI.

**Test wajib**
- `workflow_dispatch` manual → kedua job hijau end-to-end di runner; link
  run + isi artefak (log per service ada) dicatat di Hasil.
- Uji jalur gagal di branch uji: rusak sementara satu assert → job merah
  TAPI artefak tetap terupload; kembalikan.

### Hasil

_(diisi setelah task selesai)_

### T4 — Dokumentasi & penutupan gate policy

**Langkah**
1. README root: badge status CI + tabel gate policy K2 (apa yang jalan di
   PR vs nightly, di mana melihat artefak).
2. PROJECT_GUIDE.md: perbarui bagian verifikasi — sebut `make smoke-container` dan
   fakta bahwa nightly menjalankan business-e2e + chaos di Actions.
3. `docs/plan/README.md`: status 44 → ✅ setelah semua gate hijau.

**Test wajib**
- Semua link/badge yang ditambahkan valid (badge menunjuk workflow `CI`).

### Hasil

_(diisi setelah task selesai)_

## 6. Gate

- Setelah T1: `make verify-full` lokal hijau dari volume bersih.
  **Catatan**: run `verify-full` untuk refactor repo-layer 2026-07-17
  terputus tanpa hasil tercatat — gate pertama dokumen ini (atau gate fase 1
  doc 43, mana pun yang jalan duluan) sekaligus melunasinya.
- Setelah T2 dan T3: bukti run GitHub Actions hijau (link di `### Hasil`)
  MENAMBAH, bukan menggantikan, `make verify-full` lokal.
- Penutupan dokumen: checklist §10 doc 42 dicentang; README + status track
  A2 di doc 42 diperbarui (lihat Penutup).

## 7. Constraint untuk eksekutor (baca sebelum mulai)

**Boleh**: memecah task jadi sub-langkah; memilih mekanisme cache GHA di
antara dua opsi K4; menyesuaikan detail YAML selama K1–K7 terpenuhi.
**Dilarang**: mengubah K1–K7; menyentuh daftar berikut.

1. JANGAN mengubah lifecycle `scripts/lib.sh` kecuali benar-benar perlu
   untuk CI-compat — dan bila perlu, perbaiki DI `lib.sh` (satu tempat),
   jangan duplikasi bootstrap di script lain (aturan PROJECT_GUIDE.md).
2. JANGAN mengubah semantik `make verify-full` (urutan/isi step).
3. JANGAN menambah secrets repo/organisasi; kredensial CI di-generate
   per-run (K6).
4. JANGAN mengubah trigger, permission, atau step job `lint-and-test`/
   `integration` di luar yang dispesifikasikan T2.
5. JANGAN mengubah cron service (resume job) atau `sleep 65` script untuk
   "mempercepat" nightly (K7) — itu mengubah semantik uji.
6. JANGAN menjalankan profile `observability` (doc 43) di job CI mana pun —
   di luar scope dokumen ini dan membebani runner.
7. Smoke/chaos menumpuk state: SETIAP fase nightly diawali
   `docker compose down -v` (gotcha PROJECT_GUIDE.md #1).
8. `.github/workflows/*` dilint dengan `actionlint` bila tersedia di
   lingkungan eksekusi; temuan dicatat di Hasil.
9. **Verifikasi fakta eksternal saat eksekusi** (fakta bergerak, jangan
   percaya dokumen ini buta): versi terbaru actions yang dipakai, spec
   runner `ubuntu-latest` (vCPU/RAM — mempengaruhi kelayakan timeout),
   perilaku cache `type=gha` terkini. Fakta REPO di dokumen ini
   (nama job, path, port, variabel lib.sh) diverifikasi 2026-07-17.
10. Dokumen ini TIDAK menyentuh kode Go sama sekali — kalau sebuah langkah
    tampak butuh perubahan kode service, berhenti dan tanyakan.

## Penutup (dikerjakan setelah gate akhir)

- [ ] `docs/plan/README.md`: status baris 44 → `✅ done`.
- [ ] `docs/plan/42-long-term-roadmap.md`: perbarui status track A2 —
      bagian CI selesai via dokumen ini; track A2 PENUH baru boleh ditandai
      selesai setelah [35](35-phase6j-kubernetes.md) dieksekusi ATAU ada
      keputusan sadar tertulis menutup track tanpa K8s.
- [ ] Semua `### Hasil` terisi (termasuk link run Actions).
- [ ] Checklist §10 doc 42 lengkap dicentang.
