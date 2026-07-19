# 31 — Phase 6f: Ekstraksi fraud-service

> Baca master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md). Prasyarat: doc 30 selesai.

## Konteks

Screening AML/fraud (docs/plan/20) hari ini hidup DI DALAM ledger sebagai `internal/ledger/screening` yang mengimplementasikan `processors.PrePostHook` (sync, fail-open, mode off/monitor/block). Fase ini memindahkannya menjadi service INTERNAL sendiri sesuai seam yang memang sudah didesain untuk ini (doc 21 K-T4): ledger mempertahankan interface `PrePostHook`, implementasinya diganti klien gRPC ber-timeout ketat yang fail-open; rules + tabel `screening_events` pindah ke `seev_fraud`; velocity counting menjadi event-driven (consumer `transaction.posted` → Redis DB 1 — menghitung transaksi yang BENAR-BENAR posted, bukan attempt; upgrade semantik sadar, catat di Hasil).

## T1 — Modul `internal/fraud`

### Langkah
1. Buat modul baru pola standar repo: `internal/fraud/{fraud.go, errors.go, rules/, repository/, model/}`.
2. PINDAHKAN logika `internal/ledger/screening/{amount_threshold,velocity_anomaly,mode,metrics}.go` → `internal/fraud/rules` (copy, sesuaikan import; file lama dihapus di T4 setelah hook baru terpasang).
3. Repository `screening_events` (insert + list, pola repo payin); facade `Screen(ctx, ScreenInput{TxType, UserID, Amount, Currency}) (Verdict{Block, Reason}, error)` menjalankan rules sesuai mode.
4. Velocity rule membaca counter Redis `fraud:velocity:<user_id>:<hour>` (yang di-increment consumer T3 — bukan lagi increment saat Screen).

### Test wajib
- Unit rules (adaptasi test screening existing): threshold flag/block, velocity di atas ambang, mode off/monitor/block.

### DoD
- [ ] Modul fraud mandiri, tidak import `internal/ledger` (kecuali `internal/ledger/events` untuk consumer).

### Hasil
_Belum dikerjakan._

## T2 — `fraud.proto` + migrasi + ledger melepas screening

### Langkah
1. `api/proto/seev/fraud/v1/fraud.proto`: `FraudService{Screen}` sesuai master doc 26; `internal/fraud/grpcserver` + `RegisterGRPC`.
2. `migrations/fraud/000001_screening_events.up/down.sql`: copy DDL `migrations/ledger/000017` + kolom `currency` + RLS/grants; HAPUS `migrations/ledger/000017_*` (reset breaking OK — `down -v`).
3. Ledger melepas kepemilikan: hapus `screeningRepo`, blok konstruksi rules dari `ScreeningConfig` di `ledger.NewModule`, method `ListScreeningEvents` + route internalnya. `ScreeningConfig` env pindah jadi config fraud-service.

### Test wajib
- `make test` hijau setelah pencabutan (test screening di ledger dipindah/dihapus sesuai kepemilikan baru).

### DoD
- [ ] `screening_events` milik fraud; ledger tidak punya sisa kode screening kecuali seam hook.

### Hasil
_Belum dikerjakan._

## T3 — Consumer velocity (event-driven)

### Langkah
1. fraud-service declare + consume queue `ledger.events.fraud` (routing key `ledger.transaction.posted.v1`) — pola persis `internal/notify/notify.go` (DeclareTopology + Consume, prefetch 10, max delivery 5).
2. Handler: decode `events.TransactionPosted` → increment `fraud:velocity:<user_id>:<hour>` di Redis **DB 1** (env `REDIS_DB=1`), TTL ≥ 2 jam.

### Test wajib
- Unit handler decode/increment (mock counter); integration real-RabbitMQ (pola test notify): post → counter naik.

### DoD
- [ ] Velocity terisi dari event posted, bukan dari attempt Screen.

### Hasil
_Belum dikerjakan._

## T4 — Hook gRPC di ledger (fail-open)

### Langkah
1. `internal/ledger/screening/grpchook.go` (satu-satunya isi package screening yang tersisa): implement `processors.PrePostHook` — panggil `FraudService.Screen` dengan `context.WithTimeout(ctx, 500*time.Millisecond)`; error APA PUN (deadline, Unavailable, dll) → return `(processors.Verdict{}, err)` → jalur fail-open existing di pipeline (`hooks.go`) yang menangani.
2. `ledger.NewModule` menerima `hooks ...processors.PrePostHook` (re-export `ledger.PrePostHook`); `cmd/ledger-service` wire hook HANYA bila `FRAUD_GRPC_ADDR` diset — kosong = zero hooks = byte-identical tanpa fraud.

### Test wajib
- Unit hook: server merespon block → Verdict.Block sampai; server mati/timeout → error dikembalikan (pipeline fail-open); bufconn.

### DoD
- [ ] Posting ledger TIDAK PERNAH gagal karena fraud-service down (fail-open terjaga).

### Hasil
_Belum dikerjakan._

## T5 — `cmd/fraud-service` + scripts + compose + boundary

### Langkah
1. Main: DB `seev_fraud` (role `fraud_app`), Redis DB 1, RabbitMQ (consumer), gRPC `:9094`, admin `:8094` (`GET /api/v1/admin/fraud/events` pindahan ListScreeningEvents + `/metrics` `/health`). Flag `-healthcheck`.
2. `migrations/fraud` → `seev_fraud`; `down -v`; lib.sh + fraud-service (19094/18094); compose entry; boundary map `fraud-service: {fraud}`.

### Test wajib
- Chaos: stop fraud-service → transfer tetap posting (fail-open; ERROR ter-log di ledger).
- E2E mode block: `SCREENING_MODE=block` + amount di atas threshold → transfer gagal business error, row muncul di `screening_events` `seev_fraud`.

### DoD
- [ ] Fraud berdiri sendiri; dua jalur (sync Screen, async velocity) terbukti end-to-end.

### Hasil
_Belum dikerjakan._

---

## Verifikasi akhir dokumen
Gate standar master doc 26 + kedua test T5. Update README index → lanjut [32-phase6g-gateway-service.md](32-phase6g-gateway-service.md).
