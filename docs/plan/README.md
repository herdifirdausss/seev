# Seev — Ledger-First Fintech Modular Monolith: Implementation Plan

Dokumen ini adalah **indeks plan implementasi**. Setiap file di folder ini self-contained dan ditulis agar dapat dieksekusi tanpa konteks percakapan sebelumnya. Kerjakan **berurutan** — setiap fase mengasumsikan fase sebelumnya selesai.

## Urutan Eksekusi

| # | Dokumen | Isi | Status |
|---|---------|-----|--------|
| 0 | [00-current-state.md](00-current-state.md) | Audit kondisi repo saat ini — **baca dulu sebelum mengerjakan apapun** | referensi |
| 1 | [01-target-architecture.md](01-target-architecture.md) | Arsitektur target, aturan modular monolith, keputusan yang sudah dikunci | referensi |
| 2 | [02-feature-roadmap.md](02-feature-roadmap.md) | Riset fitur ledger fintech kelas dunia, prioritas P0–P3 | referensi |
| 3 | [03-phase-0-cleanup.md](03-phase-0-cleanup.md) | Bersih-bersih repo: migrasi, struktur `cmd/`, file mati, README, CI | ✅ done |
| 4 | [04-phase-1-schema.md](04-phase-1-schema.md) | Skema database kanonik (DDL lengkap) + perubahan kode terkait | ✅ done |
| 5 | [05-phase-1-core-wiring.md](05-phase-1-core-wiring.md) | Implementasi `AccountRepository`, account provisioning, HTTP API, wiring DI | ✅ done |
| 6 | [06-phase-1-workers.md](06-phase-1-workers.md) | Outbox relay worker + job verifikasi integritas ledger | ✅ done |
| 7 | [07-phase-2-hardening.md](07-phase-2-hardening.md) | Rekonsiliasi, snapshot saldo harian, kontrak event, hardening lifecycle | 🔀 di-detail-kan ke 14–16 (H9 superseded oleh 12) — kerjakan lewat dokumen 14–16, bukan langsung dari sini |
| 8 | [08-phase-3-scale.md](08-phase-3-scale.md) | Multi-currency, limits, maker-checker, partisi, compliance hooks | 🔀 di-detail-kan ke 17–20 (S4/S5 measurement-gated, tanpa dokumen) — kerjakan lewat dokumen 17–20, bukan langsung dari sini |
| 9 | [09-hardening-review.md](09-hardening-review.md) | Review resource/efisiensi/security/chaos menyeluruh atas MVP yang sudah jalan — temuan + keputusan desain terkunci (referensi, baca sebelum 10–12) | referensi |
| 10 | [10-phase2a-security-gating.md](10-phase2a-security-gating.md) | Router internal terpisah untuk tipe transaksi sistem, idempotency scope per-user, fee server-side, amount integral + cap, JWT/HSTS hardening | ✅ done |
| 11 | [11-phase2b-efficiency-locking.md](11-phase2b-efficiency-locking.md) | Redesign locking (akun sistem lepas dari FOR UPDATE), batch insert, caching resolve-account, UUIDv7, timeout & pool tuning | ✅ done |
| 12 | [12-phase2c-resilience-ops.md](12-phase2c-resilience-ops.md) | Redis opsional, outbox backoff + replay tooling, alert hook verifier, OTel opsional, chaos test | ✅ done |
| 13 | [13-p1-backlog-review.md](13-p1-backlog-review.md) | Analisa kode aktual atas sisa backlog 07 (H1–H8) & 08 (S1–S9) — temuan baru (race double-reversal, korelasi tidak dipersist, lifecycle tanpa guard) + keputusan desain terkunci K1–K9 & K-S (referensi, baca sebelum 14–16) | referensi |
| 14 | [14-phase2d-ledger-semantics-events.md](14-phase2d-ledger-semantics-events.md) | Semantic source/destination (H6), lifecycle guard atomik `closed_by_tx_id` (H7), kontrak event versioned `internal/ledger/events` (H1) | ✅ done |
| 15 | [15-phase2e-snapshots-statements.md](15-phase2e-snapshots-statements.md) | Daily balance snapshot + job + API `?as_of=` (H3), statement & export CSV (H4) | ✅ done |
| 16 | [16-phase2f-governance-recon-rls.md](16-phase2f-governance-recon-rls.md) | Maker-checker adjustment (H5), rekonsiliasi eksternal + persist korelasi (H2), RLS & DB roles (H8) | ✅ done |
| 17 | [17-phase3a-policy-recovery.md](17-phase3a-policy-recovery.md) | Limits & velocity policy layer `internal/policy` (S1), rebuild proyeksi + DR drill (S9) | ✅ done |
| 18 | [18-phase3b-multi-currency.md](18-phase3b-multi-currency.md) | Registry currency + lookup akun sistem per-currency + primitives FX (S2) | ✅ done |
| 19 | [19-phase3c-scheduled-accrual.md](19-phase3c-scheduled-accrual.md) | Scheduled transactions, batch disbursement (S3), interest accrual (S8) | ✅ done |
| 20 | [20-phase3d-aml-reporting.md](20-phase3d-aml-reporting.md) | PrePostHook screening AML/fraud (S6), regulatory reporting read-only (S7) | ✅ done |
| 21 | [21-service-topology-review.md](21-service-topology-review.md) | Peta jangka panjang monolith → 7 services (ledger/payin/payout/vendorgw/fraud/admin/user-facing) — keputusan terkunci K-T1–K-T7, extraction triggers, anti-goals | referensi |
| 22 | [22-phase4a-payin-vendorgw.md](22-phase4a-payin-vendorgw.md) | Modul payin + vendor gateway (interface, registry, mockvendor), webhook receiver `/webhooks/{vendor}`, admin replay | ✅ done |
| 23 | [23-phase4b-payout-orchestration.md](23-phase4b-payout-orchestration.md) | Modul payout: state machine, vendor outbound, hold→settle/cancel via guard K3, chaos crash-mid-flight | ✅ done |
| 24 | [24-extraction-playbook.md](24-extraction-playbook.md) | Playbook split modul → service: checklist per-fase, inventori kontrak internal API (admin-BFF), outline `internal/auth` — dieksekusi hanya saat extraction trigger terpenuhi | referensi |
| 25 | [25-phase5-business-shell.md](25-phase5-business-shell.md) | Business shell MVP: modul auth (register/login/refresh), topup intent, fee/revenue (P2P + withdraw-on-settle), notifikasi in-app (consumer RabbitMQ pertama), ops fixes, `business-e2e.sh` | ✅ done |
| 26 | [26-phase6a-foundations.md](26-phase6a-foundations.md) | **Master reference split microservices (baca dulu sebelum 27–35)** + foundations: lunasi hutang `pkg/→internal/config`, toolchain buf/gRPC, `pkg/grpcx`+`pkg/ledgererr`, restrukturisasi migrasi per-service | ⬜ todo |
| 27 | [27-phase6b-ledger-service.md](27-phase6b-ledger-service.md) | Ekstraksi ledger-service (+policy): `ledger.proto`, gRPC server + mapping error, `pkg/ledgerclient`, re-type interface konsumen, cutover `seev_ledger`, boundary v2 | ⬜ todo |
| 28 | [28-phase6c-auth-service.md](28-phase6c-auth-service.md) | Ekstraksi auth-service (PUBLIC `:8082`): DB `seev_auth`, ProvisionUser via gRPC, gateway melepas route auth | ⬜ todo |
| 29 | [29-phase6d-payin-service-routing.md](29-phase6d-payin-service-routing.md) | Ekstraksi payin-service (INTERNAL) + **routing topup DB-driven** (`payin_routing_rules`), webhook edge tetap di gateway (forward raw bytes via gRPC) | ⬜ todo |
| 30 | [30-phase6e-payout-service-routing.md](30-phase6e-payout-service-routing.md) | Ekstraksi payout-service (INTERNAL) + **routing payout DB-driven**, resume job ikut, chaos crash-mid-flight lintas service WAJIB | ⬜ todo |
| 31 | [31-phase6f-fraud-service.md](31-phase6f-fraud-service.md) | Ekstraksi fraud-service: modul `internal/fraud`, `screening_events`→`seev_fraud`, hook gRPC fail-open 500ms di seam PrePostHook, velocity via consumer event | ⬜ todo |
| 32 | [32-phase6g-gateway-service.md](32-phase6g-gateway-service.md) | Formalisasi gateway-service: rename `cmd/server`→`cmd/gateway`, notify DB→`seev_gateway`, boundary final, compose profile `app` enam service | ⬜ todo |
| 33 | [33-phase6h-fee-rules.md](33-phase6h-fee-rules.md) | **Fee DB-driven per-user-per-route**: tabel `fee_rules`, feepolicy DB-backed dengan spesifisitas user+route, admin CRUD, hapus env `FEE_*` | ⬜ todo |
| 34 | [34-phase6i-verification.md](34-phase6i-verification.md) | Verifikasi full-stack final: business-e2e enam service, chaos suite multi-service, smoke full-container, docs | ✅ done |
| 35 | [35-phase6j-kubernetes.md](35-phase6j-kubernetes.md) | OPSIONAL: Kubernetes lokal (kind) — manifest per service, Job migrasi, subset e2e via port-forward | ⬜ todo |
| 36 | [36-phase7a-request-tracing.md](36-phase7a-request-tracing.md) | **Master reference MVP product (baca dulu sebelum 37–41)** + request tracing end-to-end: `X-Request-Id` HTTP/gRPC/AMQP, persist di baris payin/payout/fraud + `ledger_transactions.request_id` (migrasi 000020, koreksi dari asumsi "tanpa migrasi"), rename `payout_vendor_calls.request_id` | ✅ done |
| 37 | [37-phase7b-fraud-seam.md](37-phase7b-fraud-seam.md) | Fraud keluar dari transaksi posting: `pkg/fraudcheck` bersama, screening di transport ledger pra-tx (P2P) / payin pra-posting / payout pra-hold, hapus seam PrePostHook | ✅ done |
| 38 | [38-phase7c-fee-quotes.md](38-phase7c-fee-quotes.md) | **Fee quote**: tabel `fee_quotes`, endpoint quote publik, posting/payout menghormati fee quoted persis atau 422 `QUOTE_EXPIRED`/`QUOTE_MISMATCH` — tidak pernah reprice diam-diam | ✅ done |
| 39 | [39-phase7d-kyc-tiers.md](39-phase7d-kyc-tiers.md) | **KYC bertingkat L0/L1/L2**: mock provider + review admin, JWT claim `kyc_level`, gate gateway, tier→`policy_limits` via gRPC `ApplyKycTier` + template `policy_tier_limits` | ✅ done |
| 40 | [40-phase7e-vendor-resilience.md](40-phase7e-vendor-resilience.md) | Resiliensi multi-vendor: circuit breaker per vendor, routing daftar kandidat, failover payout HANYA pra-konfirmasi (bukti `payout_vendor_calls`), mockvendor2 | ✅ done |
| 41 | [41-phase7f-mvp-acceptance.md](41-phase7f-mvp-acceptance.md) | Acceptance MVP final: journey register→KYC→quote→transaksi→payout+failover→trace penuh→admin ops, konsolidasi chaos, docs | ✅ done |
| 42 | [42-long-term-roadmap.md](42-long-term-roadmap.md) | Roadmap jangka panjang pasca-MVP: 19 track dalam 3 horizon (fondasi ops / kondisional-terukur / aspirasional) dengan trigger aktivasi — dokumen eksekusi 43+ lahir dari sini | referensi |
| 43 | [43-a1-observability.md](43-a1-observability.md) | **Track A1 — observability naik kelas**: compose profile Prometheus+Grafana+Loki+Tempo (ganti jaeger), OTel default-on enam service (otelhttp+otelgrpc, `pkg/tracing`), RED metrics + gauge breaker/payout-stuck, 7 dashboard as-code, 3 SLO + burn-rate alerting termapping ke runbooks, log terpusat berkorelasi `request_id` | ⬜ todo |

Selesai Phase 1 (dokumen 3–6) = **MVP ledger yang bisa dipakai**: posting engine double-entry yang aman, API HTTP, event reliable via outbox, dan job yang membuktikan integritas ledger setiap hari.

Dokumen 13–16 adalah hasil **backlog review** (2026-07-11, setelah 10–12 selesai) atas sisa task 07/08: dokumen 13 mengunci keputusan desain (termasuk tiga temuan baru yang mengubah bentuk task 07), dokumen 14–16 adalah plan eksekusinya — kerjakan berurutan 14 → 15 → 16. Task 07 H1–H8 kini dikerjakan **melalui 14–16**, jangan langsung dari 07 (detail di sana sudah usang terhadap temuan 13).

Dokumen 17–20 (ditulis 2026-07-12, setelah 14–16 selesai) adalah plan eksekusi S-track dari [08](08-phase-3-scale.md) berdasar keputusan [13 K-S](13-p1-backlog-review.md): S1+S9 → 17, S2 → 18, S3+S8 → 19, S6+S7 → 20. Kerjakan item 08 **melalui 17–20**, jangan langsung dari 08. Urutan yang disarankan: **17 → 18 → 19 → 20** (20-T1 butuh counter 17-T1; 19 memakai pola yang dimatangkan 16/17), tapi 17-T2 (S9) dan 18 saling independen — boleh paralel. **S4 (hot-account lanjutan) dan S5 (partisi/archival) SENGAJA tidak dibuatkan dokumen**: keduanya measurement-gated per K-S (S4 menunggu bukti lock-wait pada delta-apply; S5 menunggu `ledger_entries` mendekati ~50 juta row) — tulis dokumennya nanti HANYA setelah metrics membuktikan perlu, jangan dikerjakan spekulatif.

Dokumen 9–12 adalah hasil **hardening review** (2026-07-11) atas MVP yang sudah terverifikasi end-to-end — fokus pada resource terbatas, no-money-loss di bawah chaos/dependency failure, security gating, dan bottleneck locking. Kerjakan **09 dulu sebagai bacaan**, lalu 10 → 11 → 12 berurutan (masing-masing dijelaskan dependensinya di dalam dokumennya sendiri). Ini menggantikan/memperluas sebagian item di 07 (Task H9) dan 08 (S1, S4) — lihat catatan superseded di dokumen tersebut.

## Aturan Umum untuk Implementor

1. **Jangan menebak.** Semua keputusan desain yang ambigu sudah dikunci di `01-target-architecture.md` bagian "Keputusan yang Dikunci". Kalau menemukan ambiguitas baru yang tidak tercakup, berhenti dan tanyakan — jangan improvisasi pada kode yang memindahkan uang.
2. **Jalankan test setiap selesai satu task**: `make test` (sudah pakai `-race`). Task belum selesai kalau test merah.
3. **Ledger entries bersifat append-only.** Tidak pernah ada `UPDATE`/`DELETE` ke `ledger_entries` dari kode aplikasi. Koreksi = transaksi reversal baru.
4. **Uang bukan float.** Semua nominal adalah `decimal.Decimal` (github.com/shopspring/decimal) di Go dan `BIGINT` minor-unit di Postgres. Dilarang `float64` untuk nominal di jalur manapun.
5. **Satu commit per task** dengan pesan `feat(ledger): ...` / `refactor: ...` / `chore: ...` sesuai isi. Jangan menggabungkan task yang tidak berhubungan dalam satu commit.
6. **Jangan menghapus test yang gagal** untuk membuat build hijau. Perbaiki kodenya, atau kalau test-nya memang menguji perilaku lama yang sengaja diubah oleh plan ini, update test-nya dan sebutkan di pesan commit.
7. Ikuti konvensi kode yang sudah ada di repo: `log/slog` untuk logging, error wrapping dengan `fmt.Errorf("%w", ...)`, sentinel error di `internal/ledger/apperror`, mock via `go.uber.org/mock` (`//go:generate mockgen`).

## Definition of Done Global (MVP / Phase 1)

- [ ] `make lint` dan `make test` hijau.
- [ ] `docker compose up` + `make migrate-up` menghasilkan DB yang skemanya **persis** dipakai kode (tidak ada kolom/tabel yang dirujuk kode tapi tidak ada di migrasi, dan sebaliknya tidak ada tabel mati).
- [ ] End-to-end: register akun → top-up (money_in) → transfer P2P → withdraw — semua via HTTP API, saldo benar, entries seimbang.
- [ ] Idempotency terbukti dengan test: request sama dikirim 2× → satu transaksi, response kedua sukses tanpa double posting.
- [ ] Concurrency terbukti dengan test: N goroutine transfer bersamaan dari akun yang sama → tidak ada saldo negatif, tidak ada lost update, total ledger tetap seimbang.
- [ ] Outbox relay terbukti: event transaksi sampai ke RabbitMQ, dan tetap terkirim setelah worker di-restart di tengah jalan.
- [ ] `fn_verify_ledger_balance()` dan `fn_verify_account_balance()` mengembalikan 0 baris inkonsisten setelah test suite penuh dijalankan.
