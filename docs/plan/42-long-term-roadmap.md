# 42 — Roadmap Jangka Panjang: Peta Track Pasca-MVP

Tanggal: 2026-07-16 (ditulis setelah 26–34 selesai; rangkaian MVP 36–41 sudah direncanakan, belum dieksekusi).

> **Status: referensi — JANGAN dieksekusi langsung.** Peran dokumen ini seperti [21](21-service-topology-review.md) terhadap 22–23 dan [24](24-extraction-playbook.md) terhadap extraction: dokumen eksekusi (43+) lahir DARI dokumen ini, HANYA saat trigger aktivasi sebuah track terpenuhi. Dokumen ini menggantikan [02](02-feature-roadmap.md) (roadmap P0–P3 yang sudah habis dieksekusi) sebagai peta jangka panjang, dan mengasumsikan [36–41](36-phase7a-request-tracing.md) sebagai baseline "MVP product".

Dua framing kunci:
1. **Learning-first, business-framed.** Repo ini repo belajar — track diprioritaskan berdasar **nilai belajar engineering**, tapi setiap track tetap membawa **rasionale bisnis**, karena bentuk sistem harus tetap bentuk bisnis fintech sungguhan (itulah yang membuat belajarnya bernilai).
2. **Disiplin S4/S5 berlaku untuk seluruh dokumen.** Warisan langsung [13 K-S](13-p1-backlog-review.md): *"tulis dokumennya nanti HANYA setelah metrics membuktikan perlu, jangan dikerjakan spekulatif."* Track tanpa trigger terpenuhi tidak ditulis dokumen eksekusinya.

## 1. Prinsip penyusunan

- Tiap track punya tujuh field wajib: **Nama · Tujuan bisnis · Nilai belajar · Trigger aktivasi · Dependensi · Sketsa pekerjaan (3–6 butir) · Anti-scope**.
- **Tiga horizon**:
  - **Horizon 1 — "setelah 36–41"**: fondasi operasional. Trigger umumnya "36–41 selesai + ingin belajar X"; beberapa punya sinyal kondisi nyata.
  - **Horizon 2 — "kondisional-terukur"**: HANYA aktif dengan bukti metrik (disiplin S4/S5).
  - **Horizon 3 — "aspirasional / business enablement"**: taruhan lebih besar; trigger = dependensi + keinginan belajar.
- ★ menandai nilai belajar tertinggi (lihat §7).
- Kelengkapan: SEMUA hutang yang sudah terdokumentasi (future work PROJECT_GUIDE.md, deferral doc 36, scoped-out docs 18/19/20, limitasi 12/34/39/40, S4/S5, doc 35, machinery doc 24 yang dilewati, gap CI dan observability) terpetakan ke track — tabel traceability §9 membuktikan tidak ada yang jatuh.

## 2. Cara memakai dokumen ini (melahirkan dokumen 43+)

1. Pilih **SATU** track. Verifikasi trigger-nya terpenuhi — untuk trigger terukur, **tulis buktinya** (angka, grafik, atau insiden) di bagian pembuka dokumen eksekusi; untuk trigger belajar, cukup keputusan sadar + dependensi hijau.
2. Tulis dokumen eksekusi bernomor berikutnya (43, 44, …) self-contained gaya repo: keputusan desain dikunci di awal, task T1..Tn dengan Langkah/Test wajib/DoD/Hasil, migrasi bernomor per-service, gate penuh per fase (lint + test + vet dua tag + smoke + business-e2e + chaos hijau).
3. Update tabel [README](README.md) dan status track di dokumen ini setelah selesai.
4. Aturan: satu track boleh melahirkan lebih dari satu dokumen eksekusi. Sebagian track TIDAK butuh dokumen baru — bagian K8s track A2 = eksekusi [35](35-phase6j-kubernetes.md) yang sudah ada. Track H2 tanpa bukti ukur = **NOL dokumen**.
5. Track boleh dikerjakan tidak berurutan lintas horizon (H3 boleh mendahului H1 kalau dependensinya terpenuhi), kecuali field Dependensi menyatakan sebaliknya.

## 3. Peta ringkas

| ID | Track | Horizon | Trigger (ringkas) | ★ |
|---|---|---|---|---|
| A1 | Observability naik kelas: dashboards, SLO, alerting, log & trace | H1 | 36 selesai + debugging lintas service pertama >30 menit | ★ |
| A2 | Delivery pipeline: CI naik kelas + Kubernetes lokal | H1 | CI: kapan saja (murah); K8s: ingin belajar ([35] sudah ada) | |
| A3 | Resiliensi dependensi eksternal: vendor riil, outbox outbound, breaker terdistribusi, Redis semantics | H1 | [40] selesai + ingin belajar integrasi nyata | ★ |
| A4 | Compliance naik kelas: KYC & AML riil | H1 | [39] selesai + ingin belajar compliance engineering | core T1–T7 selesai; provider/MinIO/re-screen follow-up |
| A5 | Admin console: BFF + frontend | H1 | Operasi admin manual terasa menyakitkan, atau ingin belajar BFF | core T1–T6 selesai; gate Docker follow-up |
| A6 | Keamanan internal: mTLS, identitas service, secrets, threat model | H1 | Setelah 36–41; WAJIB sebelum C1 | |
| A7 | Backup, PITR & disiplin DR | H1 | Kapan saja setelah 36–41 (murah, nilai tinggi) | |
| A8 | Siklus hidup data & privasi | H1 | Setelah 36–41; purge fee_quotes lebih awal begitu [38] jalan | |
| A9 | Kontrak API & evolusi skema | H1 | Perubahan payload pertama yang merusak konsumen diam-diam; prasyarat C1 | |
| A10 | Product assurance & emergency intake control | H1 | Setelah 36–41 + ingin membuktikan konsistensi bisnis lintas payin–payout–ledger | |
| B0 | Harness load/performance + model kapasitas (GERBANG H2) | H2 | Setelah 36–41; wajib sebelum B1–B3 | ★ |
| B1 | S4: sub-sharding hot-account | H2 | Bukti lock-wait delta-apply akun sistem (via B0) | |
| B2 | S5: partisi + archival `ledger_entries` | H2 | `ledger_entries` mendekati ~50 juta row | |
| B3 | Cache resolusi fee-rule & routing-rule | H2 | B0 membuktikan resolusi per-call jadi hotspot | |
| C1 | Merchant/B2B API surface | H3 | A6 + A9 selesai + ingin belajar outbound delivery | |
| C2 | Data platform & analitik revenue (CDC → warehouse) | H3 | Query analitik pertama mengganggu OLTP, atau ingin belajar CDC | ★ |
| C3 | Notifikasi multi-channel | H3 | Ingin belajar delivery pipeline user-facing | |
| C4 | Aktivasi multi-currency end-to-end | H3 | Ingin belajar FX; prasyarat registry [18] done | |
| C5 | Produk finansial lanjutan: kapitalisasi bunga, jadwal, fee topup | H3 | [19]+[38] done + ingin belajar period-close | |
| C6 | Mesin migrasi zero-downtime: dual-write & shadow traffic | H3 | Migrasi data besar berikutnya (mis. B2), atau latihan sintetis | ★ |

## 4. HORIZON 1 — Fondasi Operasional (setelah 36–41)

### A1 ★ — Observability naik kelas: dashboards, SLO, alerting, log & trace

> **Status: ✅ SELESAI → dokumen eksekusi [43](43-a1-observability.md)** (dimulai
> 2026-07-17, jalur trigger belajar: keputusan sadar + dependensi hijau — bukti
> di pembuka 43; T1–T6 dan GATE 3/final semuanya hijau, `### Hasil` masing-masing
> task berisi bukti live-verification termasuk beberapa bug nyata yang ditemukan
> dan diperbaiki sepanjang eksekusi, bukan hanya diasumsikan dari desain).

- **Tujuan bisnis**: sistem uang tidak bisa dioperasikan buta; SLO = kontrak keandalan internal; waktu respons insiden turun drastis.
- **Nilai belajar**: desain SLI/SLO + burn-rate alerting, Grafana provisioning-as-code, agregasi log (Loki), visualisasi trace (Tempo/Jaeger), propagasi OTel lintas HTTP/gRPC/AMQP.
- **Trigger**: [36] selesai (request_id jadi tulang punggung korelasi) + pertama kali debugging lintas service memakan >30 menit karena grep log enam container.
- **Dependensi**: [36]; OTel opt-in dari 12-T5; metrics Prometheus per-service existing; 5 runbooks existing di `docs/runbooks/`.
- **Sketsa**: (1) compose profile `observability`: Prometheus + Grafana + Loki + Tempo (ingat budget RAM — profile terpisah, bukan default); (2) OTel default-on di ENAM service, span lintas gRPC dan publish/consume AMQP — melunasi deferral 36 "OTel di luar ledger"; (3) dashboard per-service + satu "dashboard uang" (posting rate, outbox lag, verifier status, payout stuck, breaker state); (4) definisi SLO (availability posting, latensi webhook→settle, lag notifikasi) + alerting rules yang memetakan ke runbooks existing; (5) log terstruktur terpusat berkorelasi `request_id`.
- **Anti-scope**: bukan APM berbayar; tidak menambah metric cardinality per-user.

### A2 — Delivery pipeline: CI naik kelas + Kubernetes lokal

> **Status: AKTIF sebagian → bagian CI = dokumen eksekusi [44](44-a2-ci-pipeline.md);
> bagian K8s = eksekusi [35](35-phase6j-kubernetes.md) (tetap ⬜ todo)** (2026-07-17,
> trigger CI "kapan saja" + keputusan sadar — bukti di pembuka 44).

- **Tujuan bisnis**: regresi jalur uang ketahuan sebelum merge. Bukti empiris repo sendiri: beberapa bug nyata (NULL scan panic, timezone SQL, multipart RequireJSON) HANYA tertangkap smoke/e2e vs Postgres riil — persis gate yang CI hari ini tidak jalankan (CI = lint + unit + integration saja).
- **Nilai belajar**: CI dengan service containers, ekonomi test pyramid (gate PR vs nightly), nightly jobs; Kubernetes dasar (kind, Job migrasi, probes) via [35].
- **Trigger**: bagian CI — kapan saja (murah, boleh bahkan sebelum 36); bagian K8s — [35](35-phase6j-kubernetes.md) sudah ditulis, aktif saat ingin belajar K8s.
- **Dependensi**: skrip smoke/business-e2e/chaos [34]; [35] (**dokumen eksekusi K8s SUDAH ada — bagian itu tidak melahirkan doc baru**).
- **Sketsa**: (1) job CI smoke full-container (compose di runner GitHub Actions); (2) job business-e2e enam service; (3) nightly chaos suite + artefak laporan; (4) kebijakan gate: apa yang wajib di PR vs nightly; (5) eksekusi [35]; (6) build image per service + caching layer.
- **Anti-scope**: bukan CD ke cloud; bukan GitOps penuh; bukan multi-cluster.

### A3 ★ — Resiliensi dependensi eksternal: vendor riil, outbox outbound, breaker terdistribusi, Redis semantics

> **Status: ✅ SELESAI (scope inti) → dokumen eksekusi
> [45](45-a3-external-resilience.md)** (dimulai 2026-07-17, trigger: [40] ✅ +
> keputusan sadar; T0–T4 dan gate terisolasi final semuanya hijau, `### Hasil`
> masing-masing task di [45-2](45-2-a3-core-execution-reviewed.md) berisi bukti
> live-verification termasuk beberapa bug nyata yang ditemukan dan diperbaiki
> sepanjang eksekusi. Scope inti: durable payout command outbox + relay,
> breaker terdistribusi Redis ber-fallback lokal, hot-swap selektif
> Redis→memory tanpa restart, fraud velocity fail-closed, 3 chaos scenario
> baru. Adapter vendor sandbox riil (Xendit) dipindahkan ke follow-up
> opsional — bukan bagian Definition of Done 45-2, semua verifikasi tetap
> memakai `mockvendor` open-source).

- **Tujuan bisnis**: uang riil bergerak lewat vendor riil; vendor/Redis mati tidak boleh menghilangkan uang atau membutuhkan restart manual.
- **Nilai belajar**: integrasi API pihak-ketiga (sandbox), **transactional outbox untuk perintah OUTBOUND** (command dispatch andal — kebalikan arah outbox event yang sudah ada), circuit breaker terdistribusi, degradasi graceful saat runtime.
- **Trigger**: [40] selesai (breaker per-proses eksis) + ingin belajar integrasi nyata. Butir Redis: saat chaos suite membuktikan gap-nya menyakitkan.
- **Dependensi**: [40], [41]; interface `internal/vendorgw` existing.
- **Sketsa**: (1) adapter vendor sandbox riil di belakang interface vendorgw, mendampingi mockvendor (hutang PROJECT_GUIDE.md "real vendor adapter"); (2) transactional payout outbox untuk perintah vendor (hutang PROJECT_GUIDE.md) — row perintah + relay + polling status; (3) breaker state pindah ke Redis, shared lintas replika (deferral 36; limitasi per-proses doc 40); (4) semantics kematian Redis: hot-swap ke fallback memory tanpa restart ATAU keputusan eksplisit menolak (limitasi 12/34) + keputusan untuk velocity fraud yang kini Redis-only tanpa fallback; (5) chaos scenario baru per perbaikan.
- **Anti-scope**: bukan onboarding vendor produksi ber-KYB; screening AML vendor ada di A4; bukan Redis multi-region.

### A4 — Compliance naik kelas: KYC & AML riil

> **Status: core T1–T7 selesai → [46](46-a4-compliance.md); provider KYC riil,
> MinIO, dan re-screen deployment follow-up** (2026-07-19,
> trigger: [39] ✅ + keputusan sadar; tiga keputusan user — provider KYC ditunda
> ke eksekutor ber-kriteria, sanctions = dataset OpenSanctions lokal, staleness
> JWT = TTL 5m + hard-control limits — di pembuka 46).

- **Tujuan bisnis**: regulator dan partner menuntut provider KYC riil, audit trail screening yang durable, dan kontrol per-rule yang bisa diubah tanpa deploy.
- **Nilai belajar**: integrasi provider identitas, object storage + enkripsi dokumen, state machine level KYC dua arah (upgrade/downgrade), side-effect at-least-once (retry queue), penanganan staleness klaim JWT.
- **Trigger**: [39] selesai + ingin belajar compliance engineering.
- **Dependensi**: [39] (tiers), [37] (fraud di edge), [20].
- **Realisasi [46]**: retry queue async `ApplyKycTier`, downgrade limits-first + template L0, TTL JWT 5m, mode screening per-rule, sanctions lokal KYC-time, screening-event durable dengan spill terukur, serta envelope encryption dokumen melalui `DocumentStore`. **Follow-up deployment-gated**: adapter/provider KYC riil, profile MinIO, dan re-screen berkala.
- **Anti-scope**: bukan perizinan riil (lihat §8); case-management UI penuh menyusul via A5.

### A5 — Admin console: BFF + frontend

> **Status: core T1–T6 selesai → [47](47-a5-admin-console.md); gate Docker
> full-stack follow-up** (2026-07-19,
> trigger jalur belajar: keputusan sadar + dependensi hijau [24/33/39/40];
> tiga keputusan user — frontend Go templates+htmx tanpa Node, sesi BFF +
> peran maker/checker di auth-service, semua panel dalam satu dokumen —
> tercatat di pembuka 47).

- **Tujuan bisnis**: operasi harian (recon, maker-checker, replay, fee rules, KYC review, payout stuck) hari ini = curl ke listener internal; operator non-engineer tidak bisa bekerja.
- **Nilai belajar**: pola BFF, agregasi API lintas enam service, authz berbasis peran admin, audit log aksi admin, frontend minimal di atas API internal.
- **Trigger**: 36–41 selesai + frekuensi operasi admin manual terasa menyakitkan, ATAU ingin belajar BFF/frontend.
- **Dependensi**: inventori route internal [24] (kontrak BFF yang sudah dibekukan), admin surface docs 33/39/40.
- **Sketsa**: (1) service admin-bff internal yang memanggil admin API tiap service — kontrak dari inventori 24 (hutang PROJECT_GUIDE.md "admin BFF"); (2) authn/z admin terpisah dari JWT user + peran maker/checker; (3) audit log semua aksi admin; (4) UI: recon dashboard, antrean maker-checker, KYC review, CRUD fee/routing rules, payout stuck + replay, status breaker; (5) boundary test: BFF tidak pernah menyentuh DB service mana pun.
- **Anti-scope**: BFF tetap tipis — tidak ada logika bisnis pindah ke sana; bukan multi-tenant.

### A6 — Keamanan internal: mTLS, identitas service, secrets, threat model

> **Status: ✅ SELESAI → dokumen eksekusi [49](49-a6-internal-security.md)**
> (T1–T6 + GATE 3 project Compose terisolasi hijau, 2026-07-22; tiga
> keputusan user — secrets = Vault dev-mode, CA = mini-CA Go + SPIFFE-style
> URI SAN, lingkup mTLS = gRPC + HTTP internal penuh — di pembuka 49.
> Prasyarat C1 "A6 wajib sebelum C1" kini terpenuhi).

- **Tujuan bisnis**: network internal bukan trust boundary yang cukup untuk uang; review keamanan adalah prasyarat membuka surface partner B2B (C1).
- **Nilai belajar**: mTLS + rotasi sertifikat (SPIFFE-ish), secrets management (sops/age atau Vault dev), threat modeling STRIDE atas arsitektur riil, latihan review ala pentest.
- **Trigger**: setelah 36–41; **wajib sebelum C1** — jangan buka surface merchant sebelum ini.
- **Dependensi**: topologi enam service stabil [34].
- **Sketsa**: (1) dokumen threat model (aset, trust boundaries, STRIDE per hop) — jadi peta prioritas butir lain; (2) mTLS antar service gRPC + rotasi cert (hutang PROJECT_GUIDE.md "mTLS + rotated service identity"); (3) secrets keluar dari env plaintext → sops/age atau Vault dev; (4) review pentest-style gateway/auth: authz bypass, IDOR, webhook forgery, rate limit; (5) perbaikan temuan.
- **Anti-scope**: bukan HSM/KMS produksi; bukan sertifikasi formal (ISO/PCI); bukan bug bounty.

### A7 — Backup, PITR & disiplin DR

- **Tujuan bisnis**: ledger yang tidak bisa di-restore = perusahaan mati; drill yang tidak dilatih = tidak eksis.
- **Nilai belajar**: WAL archiving/PITR, RPO/RTO, verifikasi restore otomatis, disiplin game-day.
- **Trigger**: kapan saja setelah 36–41 (murah, nilai tinggi).
- **Dependensi**: runbook `docs/runbooks/dr-restore-drill.md` existing (langkah pertama: audit apa yang belum otomatis), fungsi verifier integritas.
- **Sketsa**: (1) audit runbook DR existing terhadap kondisi enam-DB pasca-split; (2) backup otomatis + PITR ke titik waktu sembarang (wal-g/pg_basebackup lokal); (3) **skrip drill otomatis**: restore → verifier integritas → rekonsiliasi silang lintas DB (baris payin/payout vs ledger entries) → laporan; (4) definisi RPO/RTO per DB (ledger paling ketat); (5) kadens drill berkala via nightly CI (nyambung A2).
- **Anti-scope**: bukan streaming standby/replika (itu wilayah C2/skala); bukan multi-region (anti-goal §8).

### A8 — Siklus hidup data & privasi

- **Tujuan bisnis**: retensi tak terbatas = liability privasi + biaya; user berhak atas datanya; tabel operasional tidak boleh tumbuh tak terkendali.
- **Nilai belajar**: **ketegangan retensi vs ledger append-only** (right-to-erasure di sistem immutable → pseudonymization/crypto-shredding), enkripsi PII at rest, purge job idempotent.
- **Trigger**: setelah 36–41; butir purge `fee_quotes` lebih awal — begitu [38] jalan, tabel tumbuh per-quote.
- **Dependensi**: [38] (fee_quotes), idempotency keys existing.
- **Sketsa**: (1) purge job `fee_quotes` kedaluwarsa (deferral 36); (2) kebijakan TTL/cleanup idempotency keys; (3) matriks retensi per tabel — apa boleh dihapus vs wajib disimpan X tahun; (4) enkripsi PII at rest untuk kolom sensitif auth/KYC + key management sederhana; (5) export data user (JSON) + delete/anonymize yang **TIDAK PERNAH menyentuh `ledger_entries`** (pseudonymize referensi user; keputusan desain dikunci di dokumen eksekusinya — aturan repo #3 mutlak).
- **Anti-scope**: bukan kepatuhan GDPR legal-formal; tidak pernah menghapus/meng-update baris ledger.

### A9 — Kontrak API & evolusi skema

- **Tujuan bisnis**: enam service + calon konsumen eksternal butuh jaminan perubahan tidak merusak diam-diam; proto sudah dijaga buf — HTTP dan event belum.
- **Nilai belajar**: contract testing, schema evolution expand-contract, kebijakan deprecasi API publik.
- **Trigger**: pertama kali perubahan payload HTTP/event merusak konsumen tanpa ketahuan CI; ATAU sebagai prasyarat C1.
- **Dependensi**: buf breaking-check existing; `internal/ledger/events` v1 existing.
- **Sketsa**: (1) contract tests HTTP gateway↔services (golden/OpenAPI-driven); (2) **jalur upgrade event v1→v2** ditulis sebagai disiplin (aturan tambah field, jendela dual-publish, tolerant reader) + test enforcer; (3) OpenAPI spec surface publik gateway + lint di CI; (4) kebijakan versioning & deprecasi publik `/api/v1`→`v2` + sunset headers; (5) enforcement gaya boundary_test untuk skema event.
- **Anti-scope**: bukan schema-registry server — cukup file + CI; tidak menyentuh gRPC (sudah dijaga buf).

### A10 — Product assurance & emergency intake control

> **Status: ✅ SELESAI → dokumen eksekusi [48](48-a10-product-assurance.md)**
> (2026-07-20, implementasi T0–T6 dan acceptance/final gate sudah hijau;
> trigger jalur belajar: baseline product 36–41 selesai dan keputusan sadar
> untuk membuktikan invariant bisnis lintas service secara durable). T1–T4
> independen dari A4/A5; emergency control T5 sudah memenuhi role
> maker/checker [47](47-a5-admin-console.md) T3.

- **Tujuan bisnis**: mendeteksi kurang dari tiga menit ketika status payin atau payout tidak lagi sesuai dengan uang yang benar-benar dibukukan ledger, menyimpan bukti dan lifecycle finding secara durable, serta memberi operator rem darurat yang hanya menghentikan intake baru tanpa menghambat penyelesaian uang yang sudah berjalan.
- **Nilai belajar**: continuous product assurance lintas database tanpa cross-DB query, keyset cursor yang tidak melewatkan row, evidence-safe invariant engine, baseline/backfill tanpa alert storm, durable command + maker/checker untuk emergency control.
- **Trigger**: 36–41 selesai + ingin membuktikan konsistensi bisnis lintas payin–payout–ledger, bukan hanya invariant double-entry di dalam ledger.
- **Dependensi**: kontrak gRPC dan korelasi [36], fee quote [38], payout failover [40], durable vendor command [45]; A1 untuk dashboard/alerting. Hanya task emergency control menunggu A5 [47] T3.
- **Sketsa**: (1) RPC assurance owner-side dengan cursor `(updated_at,id)` dan lookup ledger batch; (2) `assurance-service` + DB sendiri, backfill, finding lifecycle, metrics, dan alert dedup; (3) rules PA01–PA04 dan PO01–PO07; (4) pause intake payin/payout yang durable, idempotent, revision-checked, pause satu pihak dan resume dua pihak; (5) API operator, CLI, dashboard, runbook, serta chaos recovery.
- **Anti-scope**: bukan verifier double-entry baru; bukan compliance/KYC/fraud rules; bukan admin UI; bukan mTLS/secrets, DR, privasi/retensi, kontrak publik, atau performance tuning; tanpa SaaS berbayar, auto-correction, auto-pause, maupun auto-expiry.

## 5. HORIZON 2 — Kondisional-Terukur (disiplin S4/S5)

Seluruh B-series tunduk pada kalimat K-S [13]: *"tulis dokumennya nanti HANYA setelah metrics membuktikan perlu, jangan dikerjakan spekulatif."* **B0 adalah gerbangnya** — instrumen yang menghasilkan angka untuk membuktikan/membantah trigger B1–B3.

### B0 ★ — Harness load/performance + model kapasitas (GERBANG H2)

- **Tujuan bisnis**: kapasitas = biaya; SLA butuh angka, bukan rasa.
- **Nilai belajar**: k6, profiling Go (pprof) di bawah beban, Little's law, membaca lock contention & saturasi pool Postgres.
- **Trigger**: setelah 36–41; **wajib sebelum B1–B3 mana pun diaktifkan**.
- **Dependensi**: A1 disarankan (dashboard untuk membaca hasil).
- **Sketsa**: (1) skenario k6: posting P2P, burst webhook topup, batch payout, mixed journey MVP; (2) baseline: throughput/latensi per endpoint, outbox lag, saturasi pool; (3) model kapasitas sederhana (req/s per core + headroom); (4) tren nightly di CI (nyambung A2); (5) **definisi ambang numerik presisi yang men-trigger B1/B2/B3**.
- **Anti-scope**: bukan benchmark marketing; bukan tuning prematur — output utama = ANGKA untuk gate.

### B1 — S4: sub-sharding hot-account

- **Tujuan bisnis**: throughput posting saat volume naik (akun sistem = hot row klasik fintech).
- **Nilai belajar**: mitigasi hot-row, agregasi shard saat baca, backfill aman.
- **Trigger**: TETAP dari K-S — **bukti lock-wait pada delta-apply akun sistem** (kini bisa dibuktikan/dibantah lewat B0).
- **Dependensi**: B0; mekanisme delta-apply [11].
- **Sketsa**: (1) reproduksi contention via k6; (2) desain sub-shard row akun sistem (`fee[platform#0..#7]`) + agregasi baca; (3) migrasi + backfill; (4) verifier + snapshot tetap hijau; (5) ukur sebelum/sesudah.
- **Anti-scope**: tidak menyentuh akun user; tanpa bukti lock-wait = nol dokumen.

### B2 — S5: partisi + archival `ledger_entries`

- **Tujuan bisnis**: biaya storage dan kecepatan query saat data tua menumpuk.
- **Nilai belajar**: partisi range Postgres, archival tier, migrasi data besar.
- **Trigger**: TETAP dari K-S — `ledger_entries` mendekati **~50 juta row** (atau proyeksi B0 menembusnya dalam N bulan). Prasyarat snapshot [15] SUDAH done.
- **Dependensi**: B0; panduan 6-fase existing di `docs/design/legacy-schemas/ledgernew.sql` — **langkah pertama: validasi ulang panduan itu terhadap skema `seev_ledger` pasca-split** (ditulis pra-split).
- **Sketsa**: (1) validasi ulang panduan; (2) partisi by range waktu; (3) archival + jalur query `as_of` via snapshot; (4) gladi di data sintetis B0; (5) drill restore A7 atas DB terpartisi.
- **Anti-scope**: bukan sharding horizontal lintas DB; tanpa angka row = nol dokumen. Kalau ingin belajar dual-write untuk migrasi ini, pasangkan dengan C6.

### B3 — Cache resolusi fee-rule & routing-rule

- **Tujuan bisnis**: hemat satu query per call di jalur panas — HANYA kalau terbukti panas (kalimat PROJECT_GUIDE.md: "once traffic volume justifies it").
- **Nilai belajar**: cache invalidation nyata (TTL vs event-bust saat admin ubah rule) di jalur uang, di mana staleness = harga salah.
- **Trigger**: B0 membuktikan resolusi per-call jadi hotspot terukur.
- **Dependensi**: B0, [33], [38].
- **Sketsa**: (1) ukur dulu; (2) cache in-proc TTL pendek + invalidation via event admin-change; (3) **keputusan kunci: fee yang sudah di-quote SELALU dihormati dari `fee_quotes` [38], sehingga staleness cache tidak pernah mengubah harga yang dilihat user**; (4) metrik hit-rate; (5) chaos: rule berubah di bawah trafik.
- **Anti-scope**: bukan cache Redis terdistribusi di tahap pertama; jangan cache hasil policy limit.

## 6. HORIZON 3 — Aspirasional / Business Enablement

### C1 — Merchant/B2B API surface

- **Tujuan bisnis**: kanal revenue B2B (disbursement API, acceptance API) — raison d'être perusahaan pembayaran.
- **Nilai belajar**: lifecycle API key + HMAC signing, **webhook KELUAR ber-signed delivery + retry + DLQ** (kebalikan arah webhook masuk yang sudah ada), quota per-key, developer experience.
- **Trigger**: 36–41 + A6 + A9 selesai (keamanan dan kontrak dulu) + ingin belajar delivery semantics outbound.
- **Dependensi**: A6 (WAJIB), A9 (WAJIB), pola outbox existing.
- **Sketsa**: (1) API keys (issue/rotate/revoke, hash at rest, scopes); (2) rate limit & kuota per-key (naik kelas dari rate limit IP existing); (3) endpoint merchant (create disbursement, query status); (4) webhook keluar signed (HMAC+timestamp) dengan retry backoff + DLQ + console redelivery — pakai pola outbox; (5) developer portal minimal (bisa menumpang A5); (6) mode sandbox per merchant.
- **Anti-scope**: bukan onboarding/KYB merchant riil; bukan billing; strategi pricing di luar scope (§8).

### C2 ★ — Data platform & analitik revenue (nilai belajar tertinggi)

- **Tujuan bisnis**: unit economics (take-rate per route, biaya vendor vs fee revenue) = alat keputusan bisnis inti; query analitik tidak boleh mengganggu OLTP.
- **Nilai belajar**: **CDC dari WAL (Debezium/wal2json)**, pemisahan OLTP/OLAP, pemodelan warehouse atas fakta ledger, read replica — paradigma yang belum tersentuh repo sama sekali.
- **Trigger**: query analitik berat pertama mengganggu OLTP, ATAU ingin belajar CDC.
- **Dependensi**: longgar ke A1; reporting views [20].
- **Sketsa**: (1) read replica lokal (reporting views 20 pindah ke sana); (2) CDC WAL → stream → warehouse lokal (DuckDB/ClickHouse); (3) model fakta: revenue per route/vendor/fee-rule, volume per tier KYC; (4) dashboard revenue/unit-economics; (5) **recon silang: total warehouse vs verifier ledger** — bukti pipeline benar, gaya repo.
- **Anti-scope**: bukan data lake; bukan ML; regulatory reporting tetap dari views OLTP sampai terbukti perlu pindah.

### C3 — Notifikasi multi-channel

- **Tujuan bisnis**: notifikasi transaksi = fitur trust dasar.
- **Nilai belajar**: fan-out consumer RabbitMQ multi-channel, template system berversi, provider email lokal (MailHog), preferensi/opt-out, delivery idempotent per channel.
- **Trigger**: setelah 36–41, saat ingin belajar delivery pipeline user-facing.
- **Dependensi**: notify in-app existing [25]/[32].
- **Sketsa**: (1) abstraksi channel (in-app existing, email, push); (2) template berversi + rendering; (3) email via MailHog; (4) preferensi & opt-out per user; (5) retry/DLQ per channel; (6) digest/batching.
- **Anti-scope**: bukan marketing automation; bukan provider SMS/WA berbayar.

### C4 — Aktivasi multi-currency end-to-end

- **Tujuan bisnis**: koridor USD/remitansi = ekspansi produk lintas negara; saat ini registry display-only.
- **Nilai belajar**: manajemen posisi FX, rounding/minor-unit per currency, suspense per-currency, konsistensi double-entry lintas currency.
- **Trigger**: deferral 36 (non-IDR e2e) + ingin belajar FX. Prasyarat registry [18] done.
- **Dependensi**: [18]; lookup akun sistem per-currency sudah ada.
- **Sketsa**: (1) tutup primitives 18: refresh registry tanpa redeploy, FK/validasi `accounts.currency`, suspense adjustment per-currency (ketiganya scoped-out 18); (2) alur non-IDR e2e: topup USD → P2P USD → payout USD; (3) alur fx_trade + posisi FX — runbook `docs/runbooks/fx-position.md` existing jadi hidup; (4) statement/snapshot multi-currency; (5) policy limits per-currency; (6) e2e + guard chaos anti pencampuran currency tanpa fx.
- **Anti-scope**: bukan rate feed vendor riil (mock rate); bukan hedging; bukan rekening bank USD riil.

### C5 — Produk finansial lanjutan: kapitalisasi bunga, jadwal andal, fee topup

- **Tujuan bisnis**: wallet berbunga + jadwal andal + monetisasi topup = kelengkapan produk.
- **Nilai belajar**: **period-close semantics** (kapitalisasi bulanan idempotent), kebijakan kegagalan berulang, me-revisit keputusan desain lama secara sadar.
- **Trigger**: [19] done (accrual) + [38] done (untuk topup fees) + ingin belajar period-close.
- **Dependensi**: [19], [38].
- **Sketsa**: (1) job kapitalisasi bulanan accrual→saldo user dengan idempotensi period (scoped-out 19); (2) auto-pause schedule setelah N kegagalan beruntun + resume admin (scoped-out 19); (3) **revisit keputusan "scheduled exec bypass policy engine"** (keputusan terdokumentasi 19) — pertahankan dengan alasan baru ATAU tutup dengan policy check saat eksekusi, dikunci ulang di dokumen eksekusinya; (4) topup fees (deferral 36) via jalur fee_rules+quotes existing; (5) statement menampilkan bunga terkapitalisasi.
- **Anti-scope**: bukan produk kredit/pinjaman; bukan bunga majemuk real-time.

### C6 ★ — Mesin migrasi zero-downtime: dual-write & shadow traffic

- **Tujuan bisnis**: sistem uang produksi tidak punya maintenance window; migrasi hidup-hidup = kapabilitas ops kelas dunia.
- **Nilai belajar**: dual-write + diff verification, shadow-read replay, cutover gradual per-persen + rollback instan — **persis machinery yang [24] sengaja lewati** saat rangkaian split belajar memilih cutover-window.
- **Trigger**: migrasi data besar berikutnya (mis. B2, atau pindah tabel antar service) + ingin belajar tekniknya; boleh juga murni latihan atas migrasi sintetis.
- **Dependensi**: [24] (gate produksi yang belum terpakai), A1 (membandingkan dua jalur).
- **Sketsa**: (1) pilih sasaran migrasi nyata/sintetis; (2) shadow-read: trafik baca digandakan ke jalur baru, diff hasil, laporan mismatch; (3) dual-write + reconciliation job; (4) cutover bertahap + rollback instan; (5) tulis ulang bagian gate [24] berdasar pengalaman nyata.
- **Anti-scope**: tidak mengulang split microservices; bukan blue-green infra penuh.

## 7. Ranking nilai belajar (★)

Urutan yang DISARANKAN ketika bebas memilih — bukan urutan wajib:

1. **C2 — CDC/OLAP**: satu-satunya paradigma yang belum tersentuh repo sama sekali.
2. **A3 — outbox outbound + breaker terdistribusi + vendor riil**: melengkapi separuh cerita reliability yang belum ada (arah keluar), di jalur uang.
3. **B0 — performance engineering**: keahlian mengukur; sekaligus kunci gerbang seluruh H2.
4. **A1 — SLO/burn-rate/trace**: mengubah "sistem jalan" jadi "sistem yang bisa dioperasikan".
5. **C6 — dual-write/shadow**: teknik migrasi produksi paling dicari, yang sengaja dilewati saat belajar split.

Honorable mention: A6 (mTLS/threat model), C4 (FX).

## 8. Anti-goals global & batas scope

- **Multi-region / active-passive: ANTI-GOAL eksplisit.** Satu region; kelangsungan = backup + PITR + drill (A7). Ini keputusan, bukan kelalaian (pola anti-goals [21]).
- **Lisensi/perizinan uang riil: di luar batas.** Repo belajar — tidak pernah memegang uang riil; disebut hanya sebagai boundary.
- **Go-to-market / pricing / marketing: out of scope** per keputusan penyusunan roadmap ini.
- **Tidak ada rangkaian "split ulang"** — topologi enam service adalah bentuk final untuk repo ini.

## 9. Traceability: inventori hutang → track

Audit "tidak ada yang jatuh" — setiap item yang pernah dijanjikan "nanti" di dokumen mana pun terpetakan ke track:

| Sumber | Item → Track |
|---|---|
| PROJECT_GUIDE.md future work | admin BFF→A5; mTLS/identity→A6; payout vendor outbox→A3; fee/routing cache→B3; real vendor adapter→A3 |
| Deferral doc 36 | topup fees→C5; provider KYC riil + MinIO deployment→A4 follow-up; downgrade + doc encryption + retry queue ApplyKycTier→A4 [46]; distributed breaker→A3; fee_quotes purge→A8; non-IDR e2e→C4; OTel di luar ledger→A1 |
| K-S measurement-gated [13] | S4→B1; S5→B2 |
| Doc 35 | kind/K8s→A2 (dokumen eksekusi sudah ada) |
| Scoped-out doc 19 | auto-pause schedule→C5; revisit policy-bypass→C5; kapitalisasi bunga→C5 |
| Scoped-out doc 20 | mode per-rule screening + persist screening durable→A4 [46]; vendor AML/sanctions provider riil→A4 follow-up |
| Primitives doc 18 | registry refresh→C4; FK currency→C4; suspense per-currency→C4 |
| Limitasi doc 12/34 | Redis hot-swap→A3; fraud velocity tanpa fallback→A3 |
| Limitasi doc 39/40 | kyc_level staleness→A4; breaker per-proses→A3 |
| Doc 24 | dual-write/shadow + gate produksi tak terpakai→C6 |
| Gap CI | smoke/e2e/chaos hanya laptop→A2 |
| Gap observability | dashboards/SLO/alerting/log/trace→A1 |
| Kandidat baru diterima | load harness→B0; backup/PITR→A7; secrets management→A6; API versioning + kontrak HTTP + evolusi event→A9; product assurance + emergency intake control→A10; data platform→C2; B2B/merchant→C1; admin UI→A5; notifikasi multi-channel→C3; siklus data/PII→A8; rate-limit per-key→C1; TTL idempotency→A8; threat model/pentest→A6 |
| Kandidat baru DITOLAK | multi-region (anti-goal §8); lisensi riil (boundary §8) |

## 10. Kriteria dokumen eksekusi 43+

Checklist yang harus dipenuhi setiap dokumen yang lahir dari track ini:

- [ ] Bukti trigger tertulis di bagian pembuka (angka/grafik/insiden untuk trigger terukur; keputusan sadar untuk trigger belajar).
- [ ] Keputusan desain dikunci di awal dokumen (pola K-*).
- [ ] Task T1..Tn bernomor + migrasi bernomor per-service melanjutkan sequence existing.
- [ ] Setiap fase diakhiri gate penuh: lint + test + vet dua tag + smoke + business-e2e + chaos hijau (pola 36–41).
- [ ] Anti-scope track disalin ke dokumen dan dihormati.
- [ ] Setelah selesai: update [README](README.md) + status track di dokumen ini.
