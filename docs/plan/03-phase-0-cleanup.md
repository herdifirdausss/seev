# 03 — Phase 0: Bersih-Bersih Repo

Tujuan: repo yang jujur (README = kenyataan), struktur yang benar (`cmd/` tipis, library di `pkg/`), tidak ada file mati, siap menerima Phase 1. **Tidak ada perubahan perilaku runtime di fase ini** kecuali yang disebut eksplisit.

Kerjakan task berurutan. Setiap task: kerjakan → `make test` → commit.

## Task 0.1 — Pindahkan scheduler ke `pkg/scheduler`

1. Buat `pkg/scheduler/scheduler.go` dari `cmd/scheduler/scheduler_final.go`:
   - Ganti `package main` → `package scheduler`.
   - Hapus fungsi `main()` dan blok signal-handling/demo di dalamnya (kalau ada) — library tidak menangani SIGTERM; itu urusan pemanggil.
   - Export tipe/fungsi yang dipakai dari luar: `NewScheduler`, `Cron`, `Stop`, `NewMemoryLock`, lock Redis, opsi `WithLocation`, dan interface metrics. Biarkan helper internal tetap unexported.
2. Pindahkan `cmd/scheduler/scheduler_test.go` dan `scheduler_unit_test.go` ke `pkg/scheduler/`, sesuaikan package name. Semua test harus tetap hijau.
3. Pindahkan `cmd/scheduler/README.md` → `pkg/scheduler/README.md`.
4. Hapus folder `cmd/scheduler/`.

Acceptance: `go build ./...` hijau; `go test ./pkg/scheduler/ -race` hijau; tidak ada lagi `package main` selain `cmd/server`.

## Task 0.2 — Hapus `cmd/rabbitmq`

`cmd/rabbitmq/rabbitmq.go` adalah kode contoh yang fungsinya sudah dicakup `pkg/messaging`. Verifikasi dulu: `grep -rn "cmd/rabbitmq" --include="*.go" .` harus kosong. Lalu hapus folder.

## Task 0.3 — Konsolidasi file skema

1. Buat folder `docs/design/legacy-schemas/`.
2. Pindahkan ke sana: `migrations/001.sql`, `migrations/002.sql`, `migrations/ledger.sql`, `migrations/ledgernew.sql`, `internal/ledger/001.sql`.
3. Hapus `migrations/auth.sql` (file kosong 0 byte).
4. Tambahkan `docs/design/legacy-schemas/README.md` satu paragraf: "Arsip draft skema sebelum migrasi kanonik (lihat docs/plan/04). `ledgernew.sql` adalah draft paling matang dan sumber guard yang di-port. JANGAN dieksekusi ke database."

Folder `migrations/` menjadi kosong — akan diisi Phase 1 (dokumen 04). Pastikan target `make migrate-up`/`migrate-down` di Makefile memakai golang-migrate dengan pola nama `NNNNNN_nama.up.sql` / `.down.sql`; perbaiki kalau belum.

## Task 0.4 — Hapus kode mati di modul ledger

1. `internal/ledger/service/migration.go` — berisi enum SMALLINT desain lama. Cek pemakai: `grep -rn "AccountCash\|CurrencyIDR\|AccountType(" --include="*.go" internal/ cmd/`. Kalau hanya dipakai file itu sendiri / tidak dipakai, hapus.
2. `internal/ledger/service/transfer/transfer_service.go` — cek pemakai: `grep -rn "service/transfer" --include="*.go" .`. Kalau tidak direferensikan, hapus foldernya. Kalau direferensikan, baca dan laporkan sebelum memutuskan.
3. `internal/ledger/model/ledger_transaction.go` berisi `model.Command` yang duplikat dengan `processors.Command`. Cek pemakai `grep -rn "model.Command" --include="*.go" .`; migrasi pemakainya ke `processors.Command`, lalu hapus `model.Command`.
4. Perbaiki komentar salah-tempel di `internal/ledger/model/ledger_entry.go` (komentar `LedgerEntryRecord` menempel di `EntryInstruction`).

## Task 0.5 — Rename typo

`internal/handler/dependencties.go` → `internal/handler/dependencies.go` (isi tidak berubah).

## Task 0.6 — Infrastruktur dev yang dijanjikan README

Cek keberadaan: `docker-compose.yml`, `.env.example`, `.golangci.yml`, `.air.toml`, `Dockerfile`. Yang belum ada, buat minimal:
- `docker-compose.yml`: postgres:16 (port 5432), redis:7, rabbitmq:3-management — dengan healthcheck.
- `.env.example`: semua env var yang divalidasi `internal/config/config.go` (baca file itu sebagai sumber daftar), nilai contoh development.
- `.golangci.yml`: minimal `errcheck, govet, staticcheck, ineffassign, unused, misspell`.
- `Dockerfile`: multi-stage, distroless, non-root (sesuai klaim README).

## Task 0.7 — CI

Buat `.github/workflows/ci.yml`: trigger push/PR ke main; job: setup Go (versi dari go.mod), `make lint`, `make test`. Integration test (build tag `integration`) di job terpisah yang boleh `continue-on-error: false` dengan service container Postgres.

## Task 0.8 — Tulis ulang README.md

Sesuai kenyataan pasca Phase 0: struktur folder aktual, arsitektur modular monolith (ringkas, link ke `docs/plan/01`), quick start, make targets. Hapus semua klaim fitur yang tidak ada.

## Task 0.9 — Dokumentasi boundary

Buat `PROJECT_GUIDE.md` di root repo untuk kontributor berikutnya, berisi: aturan boundary modul (dari docs/plan/01), perintah build/test/lint, konvensi error & logging, dan larangan keras: "ledger_entries append-only; nominal tidak boleh float; jangan ubah urutan langkah di service/handle/service.go tanpa membaca docs/plan/04".

## Definition of Done Phase 0

- [ ] `go build ./...`, `make lint`, `make test` hijau.
- [ ] `cmd/` hanya berisi `server/`.
- [ ] `migrations/` kosong (siap diisi), skema lama terarsip di `docs/design/legacy-schemas/`.
- [ ] Tidak ada file yang teridentifikasi mati di 00-current-state.md M4/M6 yang tersisa.
- [ ] CI jalan hijau di GitHub.
- [ ] README akurat.
