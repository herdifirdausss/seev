# 46 — Track A4: Compliance Naik Kelas — Retry Queue KYC, Downgrade, Mode Per-Rule, Sanctions Screening, Dokumen Terenkripsi

> Lahir dari track **A4** di [42-long-term-roadmap.md](42-long-term-roadmap.md).
>
> **Status verifikasi: SIAP DIEKSEKUSI (2026-07-17).** Semua fakta kode
> (path, identifier, sequence migrasi, skema) diverifikasi langsung terhadap
> repo pada tanggal tersebut. Fakta EKSTERNAL (provider KYC mana yang punya
> sandbox self-service, format unduhan dataset OpenSanctions terkini, image
> MinIO) sengaja TIDAK ditulis detail — eksekutor wajib memverifikasinya
> saat eksekusi (§6). Line reference bergeser; verifikasi dengan grep.

## 1. Trigger dan tujuan

Bukti trigger (pola doc 42 §2 poin 1, jalur trigger belajar):

- **[39](39-phase7d-kyc-tiers.md) selesai** — tier L0/L1/L2, `ApplyKycTier`
  gRPC, `policy_tier_limits`, gate `KYC_REQUIRED`; terverifikasi ulang dalam
  `make verify-full` 2026-07-17. Dependensi lain: [37](37-phase7b-fraud-seam.md)
  (fraud di edge) dan [20](20-phase3d-aml-reporting.md) (pipeline screening)
  keduanya selesai.
- **Keputusan sadar 2026-07-17**: user mengaktifkan A4 sebagai track keempat,
  dengan tiga keputusan desain diambil eksplisit lewat sesi tanya-jawab:
  provider KYC = **tunda ke eksekutor dengan kriteria** (K7), sumber
  AML/sanctions = **dataset OpenSanctions → Postgres lokal** (K6), mitigasi
  staleness JWT = **TTL pendek + hard-control limits** (K3).

Tujuan bisnis (dari track A4): regulator dan partner menuntut provider KYC
riil, audit trail screening yang durable, dan kontrol per-rule yang bisa
diubah tanpa deploy. Hutang terdokumentasi yang dilunasi:

| Hutang | Sumber | Dilunasi oleh |
|---|---|---|
| Retry queue async `ApplyKycTier` (gagal = re-trigger manual) | deferral doc 36 | T1 |
| KYC downgrade (level turun tidak mungkin secara struktural) | deferral doc 36 | T2 |
| Staleness klaim `kyc_level` di JWT | limitasi doc 39 | T2 (K3) |
| Tabel mode per-rule log-only↔enforce ("per-rule table = nanti") | scoped-out doc 20 | T3 |
| Persist screening-event durable ("best-effort, log-on-error") | scoped-out doc 20 | T4 |
| Vendor AML/sanctions di belakang interface screening yang sama | scoped-out doc 20, diteruskan doc 45 §8 | T5 |
| Provider KYC riil + penyimpanan dokumen terenkripsi | deferral doc 36 | T6 |

## 2. Fakta repo saat dokumen ditulis

Semua diverifikasi 2026-07-17.

**Alur KYC auth (target T1/T2/T6):**

- Interface provider: `internal/kycvendor/kycvendor.go` —
  `Provider{Name() string; Verify(ctx, Submission) (Decision, error)}`;
  `Submission{UserID, LevelRequested, Payload map[string]any}`;
  `Decision{Verdict approve|reject|refer, Ref, Reason}`. mockkyc: L2 SELALU
  `refer` (dicek sebelum `mock_mode`); L1 default approve. **Tidak ada
  dokumen/file yang disimpan hari ini** — `Payload` JSON adalah seluruh
  permukaan; tidak ada MinIO/S3 di go.mod maupun docker-compose.yml.
- Submit: `POST /api/v1/users/me/kyc` → `SubmitKYC` (`internal/auth/auth.go`)
  → validasi `levelRequested == user.KYCLevel+1` → row `kyc_submissions`
  status `pending` (partial unique index: satu pending per user) → provider
  Verify → approve = `approveSubmission(..., "system")`; refer = tetap
  pending menunggu admin (`POST /api/v1/admin/kyc/submissions/{id}/approve`
  di listener internal :8083).
- **Jalur gagal `ApplyKycTier` (debt T1)**:
  `repository.ApproveKYCSubmission` (`internal/auth/repository/repository.go:292-332`)
  menjalankan SATU `WithTx`: mark approved + `UPDATE auth_users SET
  kyc_level = $1 ... WHERE kyc_level + 1 = $1` + callback `applyTier` (gRPC
  sinkron ke ledger) DI DALAM tx. `applyTier` gagal → SELURUH tx rollback →
  submission tetap `pending`, level tidak naik, TIDAK ADA yang re-drive —
  pemulihan = admin klik approve lagi (manual). Atomicity ini SENGAJA
  (gotcha #10 master: "kyc_level must never advance ahead of its enforced
  limits") — yang hilang adalah re-drive otomatis, bukan atomicity-nya.
- Guard `kyc_level + 1 = $1` di UPDATE membuat downgrade mustahil struktural
  lewat jalur existing. CHECK constraint kolom mengizinkan 0/1/2.
- **auth-service TIDAK punya broker** (`cmd/auth-service/main.go`: Postgres +
  Redis opsional + gRPC ledger client saja) — retry queue berbasis RabbitMQ
  berarti infra baru; pola DB-outbox (cetak biru
  `internal/ledger/repository/outbox_event_repository.go`: claim
  `FOR UPDATE SKIP LOCKED`, backoff eksponensial+jitter di SQL, dead-letter
  trigger) adalah fit alami karena auth sudah punya Postgres di transaksi
  yang sama.
- JWT: `middleware.Claims{UserID, Email, Role, KYCLevel, ...}` diisi dari
  `auth_users.kyc_level` HANYA saat Register/Login/Refresh
  (`issueTokensWithID`); sebelum T2 `JWT_ACCESS_EXPIRY` default **15m**
  (`internal/config/config.go:417`), kini dikunci menjadi **5m**. Gate
  membaca CLAIM, bukan DB:
  gateway `requireKYC`/`requireKYCForLedgerPostings`
  (`internal/handler/router.go`) + defense-in-depth ledger transport.
  `auth_users.full_name` ADA (TEXT NOT NULL DEFAULT '').
- Ledger: `policy_tier_limits` (`migrations/ledger/000022`) —
  `CHECK (kyc_level IN (1, 2))`, **tidak ada row L0**; `ApplyKycTier` repo
  (`kyc_tier_repository.go`) sudah idempotent upsert per (user,
  transaction_type) — komentar kode eksplisit "a fresh downgrade/upgrade is
  idempotent"; level tanpa row template → `ErrUnknownKycTier`. Policy engine
  meng-cache limit in-process `POLICY_CACHE_TTL` default 60s TANPA
  invalidasi (bug staleness yang sudah tercatat di doc 39).
- Migrasi berikutnya: auth `000003`, ledger `000023`, fraud `000003`.

**Sisi fraud/screening (target T3/T4/T5):**

- Dua rule (`internal/fraud/rules/`): `amount_threshold`, `velocity_anomaly`.
  Interface: `Rule{Name() string; Screen(ctx, ScreenInput) (Verdict, error)}`.
  **Mode GLOBAL** via env `SCREENING_MODE=off|monitor|block` (default `off`),
  di-parse SEKALI saat startup dan dioper identik ke semua rule — per-rule
  tidak ada; mengubah mode = redeploy. `off` = tidak ada rule terdaftar.
- **`screening_events` best-effort**: INSERT dilakukan DI DALAM masing-masing
  rule, error DITELAN (log-only), tanpa transaksi, tanpa retry — persis
  keputusan doc 20 ("kalau insert gagal, log ERROR cukup") yang track ini
  gantikan. Verdict CHECK `('flagged','blocked')`. DB `seev_fraud`;
  fraud-service SUDAH punya Postgres + RabbitMQ + Redis (DB 1).
- **Watchlist/sanctions: TIDAK ADA sama sekali** (grep sanction/watchlist/
  blacklist = nol kode Go). Greenfield.
- `ScreenRequest` proto (`api/proto/seev/fraud/v1/fraud.proto`): `tx_type,
  user_id, amount, currency, request_id, flow` — **TIDAK ada field nama**.
  `pkg/fraudcheck.Check` timeout 500ms, kontrak fail-open (error non-nil =
  infra, caller fail open; Block=true = definitif, wajib dihormati).
- Admin fraud hari ini cuma `GET /api/v1/admin/fraud/events` (:8094).

## 3. Anti-scope

Disalin dari track A4 doc 42 + turunan dokumen ini:

- Bukan perizinan/lisensi compliance riil (anti-goal doc 42 §8).
- Case-management UI penuh menyusul via track A5 (admin console) — track ini
  hanya menambah endpoint admin JSON yang BFF A5 kelak konsumsi.
- Provider KYC riil config-gated, TIDAK pernah masuk jalur CI/verify-full —
  mockkyc tetap provider semua gate; kredensial tidak pernah masuk repo.
- Dataset sanctions = data terbuka OpenSanctions (lisensi non-komersial,
  cukup untuk proyek belajar) — BUKAN langganan vendor komersial.
- TIDAK menghapus/melemahkan gotcha #10 ("level tidak pernah mendahului
  limits") — retry queue mempertahankannya, bukan menggantinya.
- TIDAK menyentuh `execTransfer` ledger, RLS existing, `pkg/messaging`.
- Enkripsi dokumen = envelope AES-GCM dengan key dari env — BUKAN KMS/HSM
  produksi (future work bila A6 menghadirkan secrets management).

## 4. Keputusan desain terkunci

### K1 — Retry queue `ApplyKycTier`: limits-first, DB-outbox di auth, tanpa broker

- **Invarian gotcha #10 DIPERTAHANKAN**: level TIDAK PERNAH naik sebelum
  limits terpasang. Jalur cepat existing tetap: `ApproveKYCSubmission`
  mencoba `applyTier` inline di dalam tx (sukses = selesai seperti hari ini).
- **Yang berubah**: saat `applyTier` GAGAL, alih-alih rollback-dan-menyerah,
  auth menulis satu row intent durable ke tabel baru `kyc_apply_retries`
  (migrasi auth `000003`; kolom: id, submission_id, user_id, level, status
  `pending|succeeded|dead`, retry_count, next_attempt_at, last_error,
  timestamps) di transaksi TERPISAH setelah rollback. Submission tetap
  `pending` (secara eksternal identik dengan hari ini — belum approved).
- Relay worker baru di auth (pola cron `pkg/scheduler` + lock existing;
  BUKAN RabbitMQ — auth tetap broker-free): claim intent due
  (`FOR UPDATE SKIP LOCKED`, backoff eksponensial + jitter, dead setelah max
  retry — pola SQL `outbox_event_repository.go` ledger), lalu jalankan ULANG
  `approveSubmission` penuh (applyTier-lalu-approve atomik). Sukses = intent
  `succeeded` + submission approved; user tidak perlu tindakan manual.
- Admin re-trigger manual existing TETAP bekerja (idempotent — intent yang
  sudah tidak relevan diselesaikan relay sebagai no-op/succeeded).
- Alert saat ada intent `dead` (K8).

### K2 — KYC downgrade: admin-initiated, limits-first, audit trail

- Endpoint admin baru di listener internal auth:
  `POST /api/v1/admin/kyc/users/{id}/downgrade` body `{level, reason}` —
  `level < kyc_level` saat ini, reason wajib.
- **Urutan limits-first (cermin gotcha #10 untuk arah turun)**: panggil
  `ApplyKycTier(level_baru)` DULU (limits mengetat), baru `UPDATE
  auth_users.kyc_level` — di dalam satu alur dengan retry intent yang sama
  dengan K1 kalau gRPC gagal. Turun tidak pernah mendahului... kebalikannya:
  LIMITS turun dulu, level menyusul — jendela antaranya aman (limits lebih
  ketat dari level = fail-safe; kebalikan urutan upgrade).
- **Template L0 baru** di ledger (migrasi `000023`): perluas
  `CHECK (kyc_level IN (0, 1, 2))` + seed row L0 untuk ketiga
  transaction_type dengan limit NOL (`max_per_tx=0` dst.) — downgrade ke L0
  langsung memblokir semua transaksi lewat policy engine (kontrol keras,
  sinkron), tidak bergantung gate JWT. `ApplyKycTier(0)` berhenti
  `ErrUnknownKycTier`.
- Repository auth: jalur UPDATE downgrade terpisah (`WHERE kyc_level > $1`),
  guard `kyc_level + 1 = $1` existing untuk upgrade TIDAK disentuh.
- Audit: tabel `kyc_level_changes` (migrasi auth `000003` yang sama; user_id,
  from_level, to_level, direction, reason, decided_by, created_at) — SEMUA
  perubahan level (upgrade & downgrade) dicatat mulai sekarang.
- Submission pending user yang di-downgrade: tetap pending (boleh diproses
  normal setelahnya — naik lagi dari level barunya; sequence check existing
  menegakkan).

### K3 — Staleness JWT: TTL pendek + hard-control limits (keputusan user)

- `JWT_ACCESS_EXPIRY` default turun **15m → 5m** (`internal/config`,
  `.env.example`, compose) — refresh flow existing tidak berubah.
- Kontrol keras downgrade = K2's limits (sinkron); gate klaim JWT menyusul
  maksimal 5 menit — konsisten filosofi doc 39 yang dikunci ("kontrol keras
  = policy_limits; gate = UX"), zero coupling lintas service baru.
- **Jendela staleness yang diterima dan DIDOKUMENTASIKAN**: maks
  `JWT_ACCESS_EXPIRY (5m) + POLICY_CACHE_TTL (60s)` — cache limit policy
  engine di ledger tanpa invalidasi (limitasi doc 39 yang sudah tercatat)
  ikut menentukan; TIDAK menambah mekanisme invalidasi cache lintas service
  di track ini (kalau kelak butuh instan → kandidat A5/A6, dicatat, bukan
  dikerjakan diam-diam).
- Fixture test (`scripts/lib.sh` gen_token TTL 1h) tidak terpengaruh —
  gentoken mint token sendiri; e2e yang mengandalkan refresh flow diverifikasi
  tetap hijau (gotcha #9: perubahan gate WAJIB cek fixture).

### K4 — Mode screening per-rule: tabel DB, ubah tanpa deploy

- Tabel baru `screening_rule_modes` (migrasi fraud `000003`): `rule TEXT PK,
  mode TEXT CHECK (off|monitor|block), updated_by, updated_at`. Seed dari
  nilai env saat migrate TIDAK dilakukan — seed eksplisit per rule existing
  (`amount_threshold`, `velocity_anomaly`) dengan mode dari perilaku default
  (`off`), plus rule baru T5 (`sanctions_watchlist`).
- Resolusi mode saat Screen: lookup DB dengan cache in-process TTL pendek
  (~10s) + fallback ke env `SCREENING_MODE` bila row tidak ada — env menjadi
  DEFAULT global, tabel menjadi override per-rule. Perubahan mode aktif ≤
  TTL cache tanpa restart.
- Admin CRUD baru di fraud (:8094): `GET/PUT
  /api/v1/admin/fraud/rules/{rule}/mode` — PUT tervalidasi enum + audit
  kolom `updated_by` dari claims. (UI menyusul di A5.)
- `ModeOff` per-rule = rule terdaftar tapi no-op cepat (BUKAN seperti global
  `off` hari ini yang tidak mendaftarkan rule sama sekali — supaya mode bisa
  dinyalakan tanpa restart).

### K5 — Screening events durable: tulis terpusat + spill retry, kerugian terukur

Konteks jujur: pasca doc 37 screening hidup di fraud-service TANPA transaksi
bisnis apa pun untuk ditumpangi — "outbox di dalam tx posting" versi doc 20
tidak lagi punya tx untuk ditumpangi. Adaptasinya:

- Penulisan event PINDAH dari dalam masing-masing rule ke SATU tempat:
  `Module.Screen` (setelah verdict terkumpul) — rule mengembalikan verdict +
  event tanpa menulis sendiri.
- INSERT dicoba SINKRON sebelum verdict dikembalikan (masih dalam budget
  500ms caller). Gagal → event masuk **spill queue in-memory ber-batas**
  (ring buffer) yang di-flush worker background dengan backoff sampai DB
  pulih; overflow → drop TERTUA dengan counter kerugian.
- Metric wajib: `fraud_screening_event_write_failures_total`,
  `fraud_screening_event_spill_depth`, `fraud_screening_events_lost_total` +
  alert (K8). Kerugian residual (crash proses dengan spill terisi)
  DIDOKUMENTASIKAN sebagai batas desain yang diterima — bukan diam-diam.
- Blocked verdict TETAP dikembalikan meski insert gagal (fail-open audit,
  fail-closed keputusan — keputusan block tidak boleh hilang hanya karena DB
  audit sedang down; kehilangan audit-nya terukur lewat metric).

### K6 — Sanctions screening: dataset OpenSanctions lokal, KYC-time, interface sama (keputusan user)

- **Kenapa BUKAN per-posting**: matching sanctions butuh NAMA; `ScreenRequest`
  tidak membawa nama, `auth_users.full_name` hanya ada di DB auth, dan
  fraud-service DILARANG query DB service lain (boundary rule). Menaruh
  full_name di JWT menambah PII di token. Praktik compliance nyata pun
  screening nama terjadi saat ONBOARDING + re-screen berkala, bukan per
  transaksi. Maka:
- **Seam**: perluas proto `ScreenRequest` dengan field opsional
  `subject_name` (dan `birth_date` opsional) — `make proto proto-lint`,
  commit `gen/`. Rule baru `sanctions_watchlist` implementasi
  `rules.Rule` yang HANYA aktif bila `subject_name` terisi — interface
  screening tetap SATU (janji doc 20 "vendor = implementasi lain dari
  interface yang sama" ditepati), flow posting existing tidak mengirim nama
  → rule no-op untuk mereka.
- **Pemanggil**: auth memanggil `pkg/fraudcheck` (extend Check dengan
  varian ber-nama, flow=`kyc`) saat `SubmitKYC` — hit sanctions →
  verdict per mode K4 (`monitor` = flagged + lanjut; `block` = submission
  langsung `rejected` dengan reason). Re-screen berkala: job cron di auth
  (pola scheduler existing) yang menyaring ulang seluruh user L1+ terhadap
  dataset (batch, off-peak) — hit → log + flag event (tindakan manusia via
  admin; auto-downgrade TIDAK dilakukan di track ini).
- **Data**: tabel `sanctions_entries` di `seev_fraud` (migrasi fraud
  `000004`): entity_id, name, normalized_name, birth_date, countries,
  source, updated_at + index normalized_name. Loader = command/job yang
  mengunduh dataset consolidated OpenSanctions (eksekutor verifikasi format/
  URL unduhan terkini; subset field yang dipakai saja), normalisasi nama
  (case/diacritic folding), refresh berkala via cron + bisa dipicu manual.
  Matching MVP: exact pada normalized_name + token-sort; ambang fuzzy =
  keputusan eksekutor dengan default KONSERVATIF (false positive →
  `monitor`/refer, bukan auto-block).
- Gate/CI: dataset di-load dari file lokal di test (fixture kecil komit di
  repo, BUKAN unduhan network saat test) — chaos/CI tetap offline.

### K7 — Provider KYC riil: kontrak ditulis, pilihan ditunda ke eksekutor (keputusan user)

- Dokumen ini mengunci KONTRAK, bukan vendor: adapter baru
  `internal/kycvendor/<provider>/` mengimplementasi `kycvendor.Provider`
  existing TANPA perubahan interface untuk alur verdict; verifikasi dokumen +
  selfie; hasil async (webhook/polling) dipetakan ke `approve|reject|refer`.
- **Kriteria pemilihan (eksekutor verifikasi saat eksekusi lalu pilih)**:
  sandbox self-service tanpa KYB/kontak sales; gratis atau trial cukup untuk
  verifikasi end-to-end; dukungan dokumen identitas + liveness/selfie; ada
  mekanisme idempoten/reference-id. Kandidat awal untuk dicek (fakta
  bergerak): Sumsub, Didit, Veriff — TIDAK dijamin dokumen ini.
- Pola integrasi = persis Xendit di doc 45 K4: config-gated
  (`KYC_PROVIDER_ENABLED` default false + kredensial env), registrasi di
  composition root belakang flag, integration test env-gated `t.Skip` tanpa
  kredensial, TIDAK pernah di jalur CI/verify-full — mockkyc tetap provider
  semua gate. Satu verifikasi sandbox end-to-end manual dicatat di Hasil.

### K8 — Dokumen terenkripsi (MinIO) + observability + paritas gate

- **MinIO**: service compose baru (image pinned digest, hardened pola doc 43
  K1: read_only/cap_drop/no-new-privileges/tmpfs/memory limit ~256M,
  port loopback-only, kredensial via secret file pola grafana) — bagian
  profile `app`? TIDAK: profile BARU `kycstore` (opt-in, pola observability)
  supaya budget RAM 4GB gate default tidak bertambah; auth menoleransi MinIO
  absen (fitur upload dokumen 503 saat storage off, alur KYC JSON existing
  tetap jalan).
- Upload: endpoint auth `POST /api/v1/users/me/kyc/documents` (multipart,
  cap ukuran, MIME allowlist) → **envelope encryption AES-GCM** per dokumen
  (DEK acak per file, dibungkus KEK dari env `KYC_DOC_KEK`) → objek di
  bucket MinIO; metadata (object key, sha256 plaintext, size, content_type)
  di kolom/tabel auth (migrasi `000003` yang sama). Download admin-only,
  didekripsi on-the-fly, audit-logged. Kunci TIDAK pernah di log; rotasi KEK
  = future work A6 (dicatat).
- **Observability** (paritas doc 43): metric intent retry KYC
  (pending/dead), spill screening (K5), sanctions match counter, panel
  dashboard + alert `seev-op-*` baru (intent dead, spill loss, mode berubah
  tanpa audit — annotation runbook). Business-e2e `kyc_journey` diperluas:
  downgrade + re-approve + sanctions-hit path (fixture lokal). Chaos baru:
  ledger mati saat approve → intent queued → ledger hidup → relay drain →
  approved tanpa intervensi manusia.

## 5. Task eksekusi

Urutan: T1 dulu (merestrukturisasi transaksi approval yang jadi fondasi
T2), T2 downgrade+staleness, T3/T4 fraud-side (independen dari auth-side),
T5 sanctions (butuh T3 untuk mode + T4 untuk event durable), T6 dokumen +
provider (paling bebas), T7 penutup. Setiap task diakhiri `### Hasil` berisi
bukti nyata. Satu commit per task.

### T1 — Retry queue `ApplyKycTier` (K1)

**Langkah**

1. Migrasi auth `000003` (bagian retry): tabel `kyc_apply_retries` +
   `kyc_level_changes` (audit K2, sekalian — satu migrasi) + tabel metadata
   dokumen (K8, kolom saja — fitur menyusul T6). RLS pola auth existing.
2. Repository intent (insert/claim/mark, pola SQL outbox ledger) + mock.
3. Relay worker auth (cron `pkg/scheduler` + lock existing; interval 30s).
4. Refactor `ApproveKYCSubmission`: fast-path inline tetap; failure applyTier
   → tulis intent di tx terpisah → return error yang membedakan "queued for
   retry" dari kegagalan lain (HTTP 202-style semantics di admin approve).
5. Unit + integration test; update asersi e2e bila pesan error berubah.

**Test wajib**

- Unit: applyTier gagal → intent tertulis + submission tetap pending;
  relay sukses → submission approved + level naik + intent succeeded;
  relay gagal berulang → dead + alert metric; idempotensi (approve manual
  saat intent masih pending → keduanya konvergen tanpa double-apply).
- Integration (tag `integration`): matikan koneksi ledger → approve →
  intent queued; pulihkan → relay drain → approved end-to-end.
- `make verify-full` HIJAU dari volume bersih — **GATE 1**.

**DoD**: kegagalan `ApplyKycTier` tidak pernah lagi butuh re-trigger manual;
gotcha #10 terbukti utuh (tidak ada jendela level>limits di test).

### Hasil

> T1 selesai pada 2026-07-19. Auth migration `000003_compliance_foundation`
> sekarang memiliki `kyc_apply_retries` (lease, cursor due, status pending /
> succeeded / dead), `kyc_level_changes`, dan metadata `kyc_documents` dengan
> RLS/grant mengikuti tabel auth existing. Repository menyediakan enqueue,
> `FOR UPDATE SKIP LOCKED` claim, success acknowledgement, dan backoff/dead
> update yang idempotent.
>
> Approval tetap menjalankan `ApplyKycTier` di dalam transaksi fast-path.
> Setelah rollback karena dependency error, auth menulis intent terpisah dan
> mengembalikan `ErrKYCApplyQueued`; endpoint submit/admin approve menjawab
> HTTP 202 dengan `retry_id`, sementara submission tetap pending. ID intent
> diturunkan deterministik dari submission agar klik admin yang bersamaan
> tidak menggandakan intent.
>
> Relay auth memakai lease DB, lock Redis (atau memory single-node), interval
> 30 detik, retry eksponensial+jitter, dead-letter setelah 10 kegagalan, dan
> mengonvergensikan approval manual yang menang lebih dulu sebagai sukses.
> Metric queue/attempt/dead serta log dead-intent tersedia; tidak ada auto
> correction di ledger maupun perubahan gotcha limits-first.
>
> Bukti: `go test ./internal/auth/... ./cmd/auth-service` dan
> `go test -tags=integration ./internal/auth -run 'TestAuth_KYC_' -count=1`
> hijau. Integration chaos khusus memutus ledger saat approval masih menjadi
> gate T1 yang perlu dijalankan pada environment Docker yang tersedia; smoke
> lokal tanpa Docker gagal hanya karena socket Docker sandbox tidak dapat
> diakses.

### T2 — Downgrade + template L0 + staleness TTL (K2+K3)

**Langkah**

1. Migrasi ledger `000023`: perluas CHECK `kyc_level IN (0,1,2)` + seed L0
   limit nol (3 transaction_type existing).
2. Auth: endpoint admin downgrade + jalur repo downgrade (limits-first via
   K1 infrastructure) + audit `kyc_level_changes` untuk SEMUA perubahan.
3. `JWT_ACCESS_EXPIRY` default 5m (config + `.env.example` + compose).
4. Perluas `scripts/business-e2e.sh` kyc_journey: downgrade → transaksi
   ditolak policy (bukan cuma gate) → upgrade ulang.

**Test wajib**

- Unit: downgrade L2→L1→L0 (urutan limits-first terverifikasi lewat mock
  ordering), reason wajib, audit row tertulis, upgrade path existing tidak
  berubah (guard `kyc_level+1` utuh).
- Integration: user L1 di-downgrade ke L0 → posting DITOLAK oleh policy
  engine meski token lama ber-claim L1 masih dipakai (bukti kontrol keras);
  setelah re-login claim = 0 → gate 403.
- `make verify-full` HIJAU — bagian dari **GATE 1 lanjutan** (jalankan penuh
  setelah T2 selesai).

**DoD**: downgrade end-to-end aman dengan token stale; jendela staleness
terdokumentasi di kode + doc.

### Hasil

> T2 selesai pada 2026-07-19. Ledger migration `000023` menambahkan template
> L0 untuk `transfer_p2p`, `money_in`, dan `withdraw_initiate` dengan seluruh
> limit `0`, memperluas constraint level ke `0|1|2`, serta mengizinkan nilai
> nol pada `policy_limits` (nilai negatif tetap ditolak). Dengan demikian
> `ApplyKycTier(0)` benar-benar memasang hard deny yang dicek policy engine,
> bukan sekadar mengandalkan JWT.
>
> Auth downgrade admin (`POST /api/v1/admin/kyc/users/{id}/downgrade`) wajib
> menyertakan reason dan menjalankan `ApplyKycTier(level_baru)` lebih dulu;
> baru setelah sukses auth menurunkan `auth_users.kyc_level` dan menulis
> `kyc_level_changes`. Kegagalan dependency masuk ke intent retry T1 dengan
> arah `downgrade`; intent yang sudah lebih rendah diperlakukan idempotent.
> Upgrade existing tetap memakai guard `kyc_level + 1` dan sekarang juga
> diaudit.
>
> Default `JWT_ACCESS_EXPIRY` menjadi 5m di config dan `.env.example`; window
> staleness yang diterima tetap eksplisit `5m + POLICY_CACHE_TTL 60s`, dengan
> policy limits sebagai kontrol keras. Bukti: unit auth/config, serta
> integration `TestAuth_KYC_DowngradeL0_HardPolicyBeatsStaleToken` dan
> `TestApplyKycTier_L0HardControl` lulus pada Postgres nyata.

### T3 — Mode screening per-rule (K4)

**Langkah**

1. Migrasi fraud `000003`: `screening_rule_modes` + seed.
2. Resolusi mode per-Screen (cache TTL ~10s + fallback env) + refactor
   wiring `NewModule` (rule selalu terdaftar; `off` = no-op per-rule).
3. Admin GET/PUT mode + validasi + `updated_by`.

**Test wajib**

- Unit: perubahan mode aktif tanpa restart (lewati TTL cache di test),
  fallback env saat row absen, PUT invalid ditolak, no-op saat `off`.
- Integration: PUT mode `block` → Screen berikutnya memblokir; PUT `monitor`
  → flagged saja — tanpa restart service.
- `make test` + vet dua tag hijau.

**DoD**: mode berubah tanpa deploy; env tinggal default; audit siapa
mengubah.

### Hasil

> T3 selesai pada 2026-07-19. Fraud migration `000003_screening_rule_modes`
> menambahkan override per rule (`off|monitor|block`) untuk
> `amount_threshold`, `velocity_anomaly`, dan slot `sanctions_watchlist`,
> lengkap dengan `updated_by`, timestamp, grant, dan RLS.
>
> `Module.Screen` sekarang selalu memakai resolver mode dengan cache 10 detik;
> row DB menjadi override, row yang tidak ada memakai `SCREENING_MODE` sebagai
> fallback, dan `off` mengembalikan no-op tanpa menghapus rule dari wiring.
> PUT langsung menginvalidasi cache sehingga perubahan aktif segera (GET
> mengembalikan audit metadata). Endpoint admin tervalidasi enum dan rule
> allowlist serta mengambil `updated_by` dari JWT claims.
>
> Bukti: `go test ./internal/fraud/... ./cmd/fraud-service` hijau, termasuk
> unit perubahan mode tanpa restart, no-op off, dan suite Redis fail-closed.

### T4 — Screening events durable (K5)

**Langkah**

1. Pindahkan penulisan event ke `Module.Screen`; rule return verdict+event.
2. Spill queue ber-batas + flush worker + metric kerugian.
3. Alert provisioning (bagian K8 yang relevan).

**Test wajib**

- Unit: insert gagal → verdict tetap benar + event masuk spill; DB pulih →
  spill ter-flush urut; overflow → drop tertua + counter naik.
- Integration: matikan Postgres fraud saat Screen → verdict tetap; hidupkan
  → event muncul di `screening_events`.
- `make verify-full` HIJAU — **GATE 2**.

**DoD**: tidak ada kehilangan event yang tidak terukur; blocked verdict
tidak pernah hilang karena audit DB down.

### Hasil

> T4 selesai pada 2026-07-19. Rule tidak lagi menulis `screening_events`
> sendiri; verdict membawa event dan `Module.Screen` menjadi satu-satunya
> jalur persist. Kegagalan INSERT tidak mengubah verdict (termasuk block),
> tetapi memasukkan event ke FIFO spill queue in-memory bounded 1.000 row.
> Flusher background mempertahankan urutan, retry saat Postgres pulih, dan
> overflow membuang event tertua secara terukur.
>
> Metric yang tersedia: `fraud_screening_event_write_failures_total`,
> `fraud_screening_event_spill_depth`, dan
> `fraud_screening_events_lost_total`; loss akibat crash proses saat spill
> masih terisi tetap menjadi batas desain yang terdokumentasi. Bukti: unit
> central write, DB recovery flush, FIFO overflow, serta seluruh fraud suite
> lulus.

### T5 — Sanctions screening OpenSanctions (K6)

**Langkah**

1. **Verifikasi fakta eksternal dulu**: format/URL unduhan dataset
   consolidated OpenSanctions terkini; catat di Hasil.
2. Proto: `subject_name`/`birth_date` opsional di `ScreenRequest` →
   `make proto proto-lint`, commit `gen/`; extend `pkg/fraudcheck`.
3. Migrasi fraud `000004`: `sanctions_entries` + loader command + cron
   refresh + fixture lokal kecil untuk test/CI.
4. Rule `sanctions_watchlist` (aktif hanya bila subject_name terisi, mode
   via K4, event via K5) + pemanggilan dari auth `SubmitKYC` (flow `kyc`)
   + job re-screen berkala di auth.
5. Perluas e2e: KYC submit dengan nama yang match fixture → flagged/blocked
   sesuai mode.

**Test wajib**

- Unit: normalisasi nama (case/diacritic/token-sort), match/no-match,
  no-op tanpa subject_name, mode dihormati.
- Integration: loader memuat fixture → submit KYC nama match → mode
  `monitor` = pending + flagged event; mode `block` = rejected.
- Re-screen job: user existing match setelah dataset berubah → event flag
  (tanpa auto-downgrade).
- `make verify-full` HIJAU dari volume bersih.

**DoD**: sanctions screening nyata berjalan offline (dataset lokal), lewat
interface rule yang sama, ter-audit durable, mode bisa diubah tanpa deploy.

### Hasil

> Diisi saat T5 selesai.

### T6 — Dokumen terenkripsi + provider KYC riil (K7+K8)

**Langkah**

1. MinIO compose (profile `kycstore`, hardened, digest pinned — eksekutor
   verifikasi digest terkini) + Make target start/stop + README.
2. Upload/download dokumen di auth (multipart cap + MIME allowlist +
   AES-GCM envelope + metadata) — 503 graceful saat storage off.
3. **Verifikasi fakta eksternal**: pilih provider KYC per kriteria K7,
   catat proses evaluasi di Hasil; adapter `internal/kycvendor/<provider>/`
   config-gated + integration test env-gated; satu verifikasi sandbox
   end-to-end manual.

**Test wajib**

- Unit: enkripsi/dekripsi round-trip, KEK salah gagal, MIME/size ditolak,
  storage off → 503 + alur KYC existing tetap jalan.
- Integration (profile kycstore hidup): upload → objek terenkripsi di
  bucket (bukan plaintext — verifikasi byte), download admin ter-audit.
- Sandbox provider: satu flow verify end-to-end (env-gated, manual).
- `make test` hijau TANPA kredensial/tanpa MinIO.

**DoD**: dokumen tidak pernah tersimpan plaintext; default build tidak
menyentuh provider riil maupun MinIO.

### Hasil

> Diisi saat T6 selesai.

### T7 — Chaos, observability, dan dokumentasi penutup (K8)

**Langkah**

1. Chaos scenario baru: ledger mati saat approve KYC → intent queued →
   pulih → relay drain (bukti K1 end-to-end di chaos suite).
2. Panel dashboard + alert lengkap (intent dead, spill loss, sanctions
   match) + satu kalimat sumber alert di runbook terkait.
3. Update PROJECT_GUIDE.md: hapus deferral yang lunas (provider KYC riil +
   dokumen + downgrade; retry queue ApplyKycTier), catat profile `kycstore`
   di budget RAM, update runbook auth/fraud down.

**Test wajib**

- `./scripts/chaos-test.sh all` hijau dari volume bersih (termasuk scenario
  baru).
- Alert baru firing + resolve sintetis sekali.
- `make verify-full` HIJAU — **GATE 3/final**.

**DoD**: semua perbaikan track ter-chaos-kan dan ter-observasi; dokumentasi
hutang ter-update.

### Hasil

> Diisi saat T7 selesai.

## 6. Constraint eksekutor

1. Boleh breakdown task; DILARANG mengubah K1–K8 tanpa kembali ke user.
2. Do-not-touch: `execTransfer`; guard upgrade `kyc_level + 1` existing;
   kontrak fail-open `pkg/fraudcheck` (500ms, error=infra); lifecycle
   `scripts/lib.sh` (perbaikan di lib.sh); RLS; `pkg/messaging`.
3. Fakta eksternal WAJIB diverifikasi saat eksekusi: format/URL dataset
   OpenSanctions (T5), kandidat provider KYC + kebijakan sandbox-nya (T6),
   image+digest MinIO (T6). Jangan menebak detail yang belum diverifikasi.
4. Kredensial provider TIDAK PERNAH masuk repo/compose/log/fixture; KEK
   dokumen hanya env; dokumen KYC tidak pernah di-log (payload masking
   existing `pkg/logger` menutup jalur body — verifikasi field baru ikut
   ter-redact).
5. Gotcha #9: SETIAP perubahan gate/klaim WAJIB memverifikasi fixture
   `scripts/lib.sh` + ketiga script gate tetap hijau.
6. Setiap gate `docker compose down -v` dulu; `make verify-full` = bentuk
   gate kanonik. Jangan jalankan profile `kycstore` + observability +
   testcontainers bersamaan di budget 4GB.
7. Metric/label baru low-cardinality (rule/mode/flow dari allowlist
   internal); nama orang/entity sanctions TIDAK PERNAH jadi label metric
   atau isi log level INFO.
8. Butuh file/perilaku di luar task ini → berhenti, update dokumen dulu.

## 7. Definition of Done global

- [ ] `make lint`, `make test`, vet dua tag, `make verify-full` hijau dari
      volume bersih di ketiga gate.
- [ ] Kegagalan `ApplyKycTier` sembuh sendiri via relay; tidak ada jendela
      level>limits; downgrade aman dengan token stale (bukti integration).
- [ ] Mode screening per-rule berubah tanpa deploy; screening event tidak
      pernah hilang tanpa terukur.
- [ ] Sanctions screening jalan offline dari dataset lokal; KYC-time +
      re-screen berkala; fixture-based di CI.
- [ ] Dokumen KYC terenkripsi at rest (bukti byte di bucket); default
      build/CI tidak menyentuh MinIO maupun provider riil.
- [ ] Satu verifikasi provider sandbox end-to-end tercatat di Hasil T6.
- [ ] Observability paritas doc 43; alert baru firing+resolve terbukti.
- [ ] PROJECT_GUIDE.md deferral list + runbook ter-update; tidak ada kredensial
      di repo.

## 8. Penutup setelah GATE 3

- [ ] Isi semua `### Hasil` dengan bukti command + output ringkas.
- [ ] Update baris plan 46 di [README](README.md) menjadi `✅ done`.
- [ ] Update status A4 di [42](42-long-term-roadmap.md) menjadi selesai via 46.
- [ ] Catat: case-management UI + BFF admin = track A5; rotasi KEK + secrets
      management = track A6 — keduanya sengaja tidak disentuh di sini.
