# 24 — Extraction Playbook: Cara Memisahkan Modul Menjadi Service

Referensi — **JANGAN dieksekusi sekarang.** Dokumen ini dibaca saat salah satu [extraction trigger di 21](21-service-topology-review.md) benar-benar terpenuhi (diukur, bukan dirasa). Perannya: saat trigger datang, split adalah eksekusi checklist yang membosankan — bukan proyek riset.

Prasyarat membaca: [21](21-service-topology-review.md) (peta modul↔service + K-T), [01](01-target-architecture.md) (aturan boundary yang membuat playbook ini mungkin).

---

## Gate: JANGAN split sebelum semua ini benar

Centang SEMUA sebelum memulai split modul apa pun:

- [ ] Salah satu extraction trigger [21](21-service-topology-review.md) terpenuhi **dengan bukti** (metrics deploy cadence / incident blast radius / beban asimetris / struktur tim) — tulis buktinya di PR split.
- [ ] `boundary_test.go` hijau minimal 3 bulan terakhir tanpa pengecualian baru (boundary yang sering dilanggar = modul belum siap pisah).
- [ ] Modul yang mau displit TIDAK berbagi tabel dengan modul lain (audit `grep` nama tabelnya di luar `internal/<mod>/`).
- [ ] Observability dasar siap: request ID propagation (sudah ada), OTel exporter aktif (12-T5 — dari opsional jadi wajib saat ada network hop di jalur uang), alert webhook terpasang.
- [ ] Ada environment staging yang bisa menjalankan dua topologi (monolith & split) berdampingan untuk perbandingan.

---

## Checklist Split per-Service (generik)

Urutan yang aman — setiap langkah bisa di-rollback sebelum langkah berikutnya:

### Fase A — persiapan (masih monolith, zero downtime)
1. **Bekukan kontrak**: inventarisasi semua titik sentuh modul (facade methods yang dipakai siapa, route internal, event yang dikonsumsi/diterbitkan). Untuk route internal, inventori di bagian bawah dokumen ini adalah baseline-nya.
2. **HTTP client shim**: tulis implementasi kedua dari interface facade yang berbicara HTTP ke internal API (route internal `:8081` modul tersebut). Konsumen memilih implementasi via config (`<MOD>_MODE=inproc|http`). Uji di staging: monolith memanggil dirinya sendiri via HTTP — fungsional identik, latensi terukur.
3. **Role DB per-service**: buat login role baru (`seev_<mod>`) dengan grant HANYA ke tabel prefix modul itu (+ SELECT ke tabel referensi yang sah — idealnya nol; kalau ada, itu boundary leak yang harus dibereskan dulu). RLS existing (16-T3) sudah FORCE — tinggal grant-minimal.
4. **Konsumen event pindah kontrak**: pastikan semua konsumsi data modul lain lewat `internal/<mod>/events` payload, bukan query tabel (aturan 01 rule 2 — harusnya sudah, verifikasi lagi).

### Fase B — pisah proses (masih satu DB)
5. **Binary kedua**: `cmd/<mod>d/main.go` — wiring subset: config → DB (role `seev_<mod>`) → modul + worker-nya sendiri. Modul di monolith DIMATIKAN via config, konsumen di monolith pindah ke HTTP shim (langkah 2). D9 terbukti: worker sudah package sendiri, ini murni komposisi.
6. **Cutover bertahap**: staging dulu; produksi dengan monolith sebagai fallback (flip `<MOD>_MODE` kembali = rollback instan).
7. Amati minimal satu siklus bisnis penuh (termasuk recon harian + verifier + snapshot) sebelum lanjut.

### Fase C — pisah data (titik tanpa jalan pulang murah)
8. **Carve-out tabel by prefix**: DB baru untuk service; migrasi data tabel `<mod>_*`; dual-write ATAU cutover window singkat (untuk volume kecil, cutover window lebih sederhana dan lebih aman daripada kompleksitas dual-write).
9. **Timeline migrasi terpisah**: folder `migrations/` baru di repo service (atau subfolder per-service) — nomor migrasi monolith berhenti mencakup tabel yang pindah.
10. **Outbox per-service**: bila service menerbitkan event, ia butuh tabel outbox + relay sendiri (pola 06/12 disalin, jangan berbagi `outbox_events` lintas DB).

### Fase D — rapikan
11. Hapus kode modul dari monolith (facade tinggal HTTP shim), hapus grant lama, update `boundary_test.go`, update peta [21](21-service-topology-review.md) dan dokumen ini.

---

## Catatan Spesifik per-Service

| Service | Catatan split |
|---|---|
| **Payin** | Kandidat pertama paling masuk akal (trigger #1: tambah vendor = deploy sering). Bawa `internal/vendorgw` ikut sebagai library. Webhook receiver pindah menjadi edge service-nya sendiri; route `/webhooks/{vendor}` di monolith tinggal 301/proxy selama transisi. `ledger.Post` → HTTP `POST /api/v1/ledger/transactions` di router internal ledger — kontraknya sudah persis itu. |
| **Payout** | Sama seperti payin + worker resume/polling ikut pindah (sudah package sendiri). Perhatikan: guard K3 tetap di ledger — payout service TIDAK menyalin logika itu. |
| **Fraud** | Implementasi `PrePostHook` di ledger diganti HTTP client fail-open ber-timeout ketat (bujet latensi: sub-50ms atau skip) → memanggil fraud service. `screening_events` + rule engine pindah. Konsumsi async `ledger.transaction.posted.v1` untuk enrichment. JANGAN split fraud sebelum ada model/rule yang benar-benar butuh resource terpisah. |
| **Ledger** | Displit TERAKHIR (kalau pernah) — semua service lain adalah kliennya; ledger yang pindah berarti semua shim berubah bersamaan. Lebih mungkin: ledger adalah "yang tersisa di monolith" setelah yang lain keluar. |
| **Internal admin** | Bukan split — service BARU (BFF) yang memanggil internal API tiap service. Kontraknya = inventori di bawah. Bisa dibangun kapan saja tanpa menyentuh monolith. |
| **User-facing** | Butuh modul `internal/auth` lahir dulu (outline di bawah), lalu public router + auth pindah bersama sebagai BFF. |

---

## Inventori Kontrak Internal API (baseline 2026-07-12 — kontrak admin-BFF masa depan)

Semua di listener internal `:8081`, prefix `/api/v1`, JWT + admin-gated per handler. **Perubahan pada daftar ini setelah 2026-07-12 = perubahan kontrak API** — sadar-kompatibilitas, catat di dokumen ini.

| Area | Route |
|---|---|
| Posting (semua tipe) | `POST /ledger/transactions`, `GET /ledger/transactions/{id}` |
| Accounts | `GET /ledger/accounts`, `GET /ledger/accounts/{id}/balance`, `/entries`, `/statement`, `POST /ledger/accounts/pockets` |
| Outbox ops | `POST /ledger/admin/outbox/dead/{id}/replay`, `POST /ledger/admin/outbox/dead/replay-all` |
| Maker-checker | `POST/GET /ledger/admin/adjustments`, `POST .../{id}/approve`, `.../{id}/reject`, `GET .../{id}` |
| Recon | `POST /ledger/admin/recon/batches`, `GET .../batches/{id}`, `POST .../items/{id}/resolve` |
| Schedules | `POST /ledger/admin/schedules/run` (+ CRUD user di kedua router) |
| Disbursement | `POST /ledger/admin/disbursements`, `POST .../{id}/run`, `GET .../{id}` |
| Savings | `PUT /ledger/admin/savings/{account_id}`, `GET /ledger/admin/savings` |
| Screening | `GET /ledger/admin/screening/events` |
| Reports | `GET /ledger/admin/reports/{position\|mutation\|recon}` |
| Policy | `/admin/policy/limits...` (modul policy) |
| (setelah 22) Payin | `GET /admin/payin/events`, `POST /admin/payin/events/{id}/replay` |
| (setelah 23) Payout | `GET /admin/payout/requests`, `POST .../{id}/cancel`, `.../{id}/retry` |
| Ops | `GET /metrics` |

---

## Outline Modul `internal/auth` (D12 — untuk user-facing service kelak)

Ringkas, untuk dijadikan dokumen eksekusi sendiri saat dibutuhkan:
- Tabel: `auth_users`, `auth_credentials`, `auth_refresh_tokens` (prefix per K-T5).
- Facade: `auth.Module` — `Register`, `Login`, `Refresh`, `Me`; menerbitkan JWT dengan kontrak claims yang SUDAH dipakai middleware existing (`pkg/middleware` — UserID, Role, iss) supaya ledger/policy tidak berubah.
- Integrasi: `Register` sukses → panggil `ledger.ProvisionUser` (facade) → event `auth.user.registered.v1` via outbox-nya sendiri.
- Route placeholder 501 di public router (`/auth/login`, `/auth/register`, `/users/me`) diganti implementasi nyata — router publik tidak berubah bentuk.
- Saat user-facing service displit: auth + public router pindah bersama; monolith internal tetap memverifikasi JWT dengan secret/issuer yang sama.
