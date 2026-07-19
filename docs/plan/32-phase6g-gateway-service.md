# 32 — Phase 6g: Formalisasi gateway-service

> Baca master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md). Prasyarat: doc 31 selesai.

## Konteks

Setelah docs 27–31, `cmd/server` yang tersisa TINGGAL: router publik (proxy ledger, forward gRPC payin/payout, webhook edge) + modul notify (consumer + API notifications). Fase ini meresmikannya sebagai **gateway-service**: rename binary, carve-out DB notify ke `seev_gateway`, boundary rules final, compose profile `app` lengkap enam service.

## T1 — Rename + bersih-bersih

### Langkah
1. `git mv cmd/server cmd/gateway`; nama binary `gateway`; sesuaikan Makefile/scripts/Dockerfile default.
2. `internal/handler/dependencies.go`: hapus field mati sisa ekstraksi (pertahankan `DB` untuk repo notify dan `MQ` untuk consumer notify; `Cache` untuk rate limiter).
3. Hapus placeholder `GET /admin/users` (501) + fungsi `placeholder()` di `router.go`.

### Test wajib
- `make test` hijau; smoke hijau.

### DoD
- [ ] Tidak ada kode mati sisa monolith di gateway.

### Hasil
_Belum dikerjakan._

## T2 — Carve-out DB notify

### Langkah
1. Pool DB gateway → `seev_gateway` (role `gateway_app`); `migrations/gateway` diterapkan ke sana; `down -v`.
2. Consumer notify TIDAK berubah (queue `ledger.events.notifications` tetap; hanya koneksi DB tujuan insert yang pindah).

### Test wajib
- Integration notify existing tetap hijau dengan DB baru; business-e2e: notifikasi tetap sampai.

### DoD
- [ ] `notif_notifications` hidup di `seev_gateway`.

### Hasil
_Belum dikerjakan._

## T3 — Boundary final + build-all + compose lengkap

### Langkah
1. `boundary_test.go` peta final: `gateway: {handler, notify}`, `auth-service: {auth}`, `ledger-service: {ledger, policy}`, `payin-service: {payin}`, `payout-service: {payout}`, `fraud-service: {fraud}`; `vendorgw` = shared library payin+payout; SATU-SATUNYA cross-module import yang tersisa boleh: `internal/ledger/events` (kontrak event) dan `gen/*` (kontrak wire).
2. Makefile `build-all` (enam binary); lib.sh `start_services` start ENAM proses dengan urutan ledger duluan.
3. Compose profile `app` final (semua service, healthcheck `-healthcheck`, depends_on: infra healthy + ledger started). Estimasi RAM total ≈ 0.9–1.2GB (lihat master gotcha #14). `.env.example` ditulis ulang per-service (section per binary).

### Test wajib
- `make test` (boundary final); `docker compose --profile app up` → enam container healthy.

### DoD
- [ ] Topologi enam service ditegakkan CI dan bisa dinaikkan penuh via compose.

### Hasil
_Belum dikerjakan._

## T4 — Dokumentasi

### Langkah
1. Update `PROJECT_GUIDE.md`: section arsitektur (enam service, port, DB, alur panggilan), aturan boundary baru, cara menjalankan.
2. Update `README.md` repo + `docs/plan/README.md` index; catat future work: admin BFF, mTLS antar service, outbox payout, caching fee-rule.

### DoD
- [ ] Kontributor baru bisa memahami topologi dari dokumentasi tanpa konteks percakapan sebelumnya.

### Hasil
_Belum dikerjakan._

---

## Verifikasi akhir dokumen
Gate standar master doc 26 (kini enam proses) + compose profile `app` smoke. Update README index → lanjut [33-phase6h-fee-rules.md](33-phase6h-fee-rules.md).
