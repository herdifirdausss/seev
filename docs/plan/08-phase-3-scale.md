# 08 — Phase 3: Scale & Compliance (P2–P3 Features)

> **SUPERSEDED (2026-07-12) — kerjakan lewat dokumen 17–20, bukan langsung dari sini.** Dokumen eksekusi detail sudah ditulis berdasar keputusan [13 K-S](13-p1-backlog-review.md): S1+S9 → [17](17-phase3a-policy-recovery.md), S2 → [18](18-phase3b-multi-currency.md), S3+S8 → [19](19-phase3c-scheduled-accrual.md), S6+S7 → [20](20-phase3d-aml-reporting.md). **S4 dan S5 tetap TANPA dokumen** — measurement-gated (S4: tunggu bukti metrics lock-wait pada delta-apply; S5: tunggu `ledger_entries` mendekati ~50 juta row); tulis dokumennya hanya setelah pengukuran membuktikan perlu.

> **Keputusan desain per-item (S1–S9) sudah dikunci di [13-p1-backlog-review.md bagian K-S](13-p1-backlog-review.md)** (2026-07-11): pendekatan, prasyarat antar-item, dan pola yang wajib diikuti (mis. fallback Redis-opsional 12-T1 untuk S1, idempotency key deterministik untuk S3/S8). Prasyarat keras: S5 butuh H3 ([15 T1](15-phase2e-snapshots-statements.md)); S7 butuh H2+H3+H8 ([15](15-phase2e-snapshots-statements.md)/[16](16-phase2f-governance-recon-rls.md)); S9 tanpa prasyarat (kandidat quick-win).

Garis besar saja — detail eksekusi di dokumen 17–20. Prioritas urut dari atas.

## S1 — Limits & velocity (policy layer)
> **Amount cap dasar (safety ceiling global) sudah dipindah ke [10-phase2a-security-gating.md](10-phase2a-security-gating.md) Task T5** — dikerjakan lebih awal karena "tidak ada cap sama sekali" adalah temuan kritis, bukan sekadar fitur skala. Item S1 di sini TETAP relevan sebagai lapisan lanjutannya: limit **per-user, per-tipe, dan velocity (harian/bulanan)** yang jauh lebih granular — kerjakan setelah T5 selesai.

Modul baru `internal/policy`: limit per-tx / harian / bulanan per user & per tipe, dievaluasi **sebelum** `ledger.Post` di transport layer. Counter di Redis (sliding window per user+tipe) — atau in-memory kalau `REDIS_ENABLED=false` (lihat [12-phase2c-resilience-ops.md](12-phase2c-resilience-ops.md) Task T1, pola fallback yang sama harus dipakai di sini), konfigurasi limit di tabel. Ledger sendiri tetap tidak tahu-menahu (sesuai catatan di processors.go).

## S2 — Multi-currency
- Lepas asumsi `IDR` di provisioning & validator; tabel `currencies` (kode, minor_unit) jadi rujukan validasi.
- FX: bukan fitur ledger — orchestration `money_out(IDR)` + `money_in(USD)` via akun konversi per pasangan, rate & quote id disimpan di metadata kedua transaksi. Akun sistem `fx_conversion` per currency pair.
- Lookup `GetSystemAccountID` mulai memfilter currency (TODO yang ditinggal di 05 Task 1b.1).

## S3 — Scheduled & batch posting
- Scheduled: tabel `scheduled_transactions` + job `pkg/scheduler`; eksekusi = `ledger.Post` biasa dengan idempotency key deterministik (`sched:<id>:<run_date>`).
- Batch disbursement: satu file/manifest → banyak `Post` dengan progress tracking + resume; laporan hasil per item.

## S4 — Hot account lanjutan
> **Prasyarat: [11-phase2b-efficiency-locking.md](11-phase2b-efficiency-locking.md) Task T1 (pemisahan lock user vs sistem, delta-apply atomik) harus sudah selesai duluan.** T1 menghilangkan `FOR UPDATE` pada akun sistem sepenuhnya — itu sendiri sudah menyelesaikan sebagian besar masalah hot-row untuk skala MVP. Item S4 di sini adalah langkah LANJUTAN kalau setelah T1 pun satu akun sistem spesifik masih jadi bottleneck di skala jauh lebih besar (ukur dari metrics lock-wait / row-level contention pada `UPDATE ... balance = balance + $delta`, bukan lagi dari `FOR UPDATE` karena itu sudah dihilangkan).

Sharding per gateway sudah ada (desain di processors.go). Kalau satu akun sistem tetap jadi bottleneck (ukur dulu dari metrics lock-wait!): sub-shard `fee['gopay#0'..'#7']` + view agregat untuk finance, atau async balance projection untuk akun sistem (entries tetap sinkron, projection eventual).

## S5 — Partisi & archival
Ikuti panduan 6 fase di `docs/design/legacy-schemas/ledgernew.sql` bagian PARTITIONING (dual-write → backfill → rename): `ledger_entries` & `ledger_transactions` partisi bulanan by `created_at`. Prasyarat: H3 snapshot jalan (query saldo tidak butuh scan partisi lama). Archival: partisi > N bulan pindah cold storage, tetap immutable & auditable.

## S6 — AML / fraud hooks
Interface `PrePostHook` di pipeline `Handle()` (setelah validasi bisnis, sebelum build entries): implementasi awal = rule sederhana (velocity anomali, amount threshold) + mode `monitor` vs `block`. Integrasi vendor screening menyusul di belakang interface yang sama.

## S7 — Regulatory reporting
Laporan posisi dana & mutasi periodik (format menyesuaikan kebutuhan BI/OJK saat entitas legalnya jelas). Sumber data: snapshots (H3) + recon (H2). Read-only role `app_readonly` (H8) dipakai di sini.

## S8 — Interest / yield accrual
Job harian menghitung accrual per akun produk saving → posting `interest_accrue` (tipe transaksi baru + processor baru — registry pattern membuat ini murah). Kapitalisasi periodik.

## S9 — Point-in-time rebuild & DR drill
Script rebuild `account_balances` penuh dari `ledger_entries` (truncate projection → replay) + drill terjadwal di staging: restore backup → rebuild → verifier bersih → catat RTO.
