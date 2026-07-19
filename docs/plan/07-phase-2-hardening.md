# 07 — Phase 2: Hardening (P1 Features)

> **⚠️ SUPERSEDED sebagai dokumen eksekusi (2026-07-11).** Rincian desain per-task yang diminta paragraf di bawah SUDAH ditulis: analisa + keputusan terkunci di [13-p1-backlog-review.md](13-p1-backlog-review.md), plan eksekusi di [14](14-phase2d-ledger-semantics-events.md) (H6, H7, H1), [15](15-phase2e-snapshots-statements.md) (H3, H4), [16](16-phase2f-governance-recon-rls.md) (H5, H2, H8). H9 superseded oleh [12 T7](12-phase2c-resilience-ops.md) (sudah ✅). **Kerjakan dari 14–16, bukan dari sini** — audit 13 menemukan hal yang mengubah bentuk beberapa task (race double-reversal; `external_ref` tidak dipersist sehingga H2 butuh prasyarat skema; lifecycle settle/cancel tanpa guard). Dokumen ini dipertahankan sebagai konteks asal usul task.

Prasyarat: MVP (03–06) selesai dan terverifikasi. Fase ini boleh dikerjakan per-task secara independen kecuali disebut lain. Detail per task sengaja lebih ringkas daripada Phase 1 — sebelum mengerjakan satu task, tulis dulu rincian desainnya sebagai `docs/plan/07x-<task>.md` dan minta review.

## Task H1 — Kontrak event versioned
- Definisikan skema payload untuk `ledger.transaction.posted.v1` (dan `failed.v1`): txID, type, amount (string), currency, entries ringkas (account_id, direction, amount), occurred_at, `schema_version`.
- Satu package `internal/ledger/events` berisi tipe payload + konstanta event type — modul konsumen import dari sini (satu-satunya subpackage ledger yang boleh diimport modul lain, atau naikkan ke root `ledger`).
- Processor `OutboxEvents()` diseragamkan memakai tipe ini (sekarang tiap processor bebas menyusun payload sendiri — audit satu per satu).

## Task H2 — Rekonsiliasi eksternal
- Tabel `recon_batches` + `recon_items`: import settlement report gateway (CSV) → match ke `ledger_transactions` via `reference_id`/metadata → status `matched / missing_internal / missing_external / amount_mismatch`.
- Akun sistem `suspense` per gateway untuk parkir selisih; penyelesaian selisih via `adjustment_*` (yang di H5 diberi maker-checker).
- CLI/endpoint admin untuk upload report + laporan hasil match.

## Task H3 — Daily balance snapshot
- Tabel `account_balance_snapshots (account_id, as_of_date, closing_balance, entry_count)` diisi job harian (00:15 WIB) dari entries hari itu + snapshot kemarin.
- Verifikasi silang: snapshot vs `fn_verify_account_balance` — selisih = ERROR.
- API `GET /accounts/{id}/balance?as_of=2026-07-01` membaca snapshot + delta entries hari berjalan.
- Ini juga membuat verifikasi projection (06 Task 1c.2) tidak perlu full scan untuk akun lama.

## Task H4 — Statement & export
- `GET /accounts/{id}/statement?from=&to=&format=json|csv` — pakai snapshot (H3) untuk opening balance + entries periode.

## Task H5 — Maker-checker untuk adjustment
- Tabel `pending_adjustments (id, requested_by, approved_by, cmd_payload, status)`.
- `adjustment_credit/debit` via API tidak langsung post: buat pending → user admin **lain** approve → baru `Handle()` dieksekusi dengan `created_by` kedua identitas.
- Freeze/confiscate tetap langsung (kebutuhan compliance bisa mendesak) tapi wajib `reason` di metadata + audit log.

## Task H6 — Perbaikan semantik source/destination
- `ledger_transactions.source_account_id/destination_account_id` saat ini diisi `AccountIDs[0..1]` hasil sort (tidak semantik, lihat catatan di skema 04).
- Ubah: `TxProcessor.ResolveAccounts` mengembalikan struct `{Source, Destination, Fee uuid.UUID}` alih-alih slice polos, ATAU tambahkan method baru; service menulis kolom dari situ. Migrasi data lama tidak perlu (kolom informatif, kebenaran tetap di entries) — cukup catat cutoff date di CHANGELOG.

## Task H7 — Guard lifecycle & reversal
- Test + guard: reversal atas transaksi yang sudah direversal → tolak; reversal atas reversal → tolak; withdraw_settle atas hold yang sudah cancel → tolak. Pattern: kolom `reversed_by_tx_id UUID NULL` di `ledger_transactions` (unique) — sekaligus jadi audit link.
- Escrow/withdraw state machine: dokumentasikan transisi valid + tolak transisi ilegal di processor `Validate` dengan query status transaksi asal (dalam tx DB yang sama, sudah didukung signature `Validate(ctx, tx, ...)`).

## Task H8 — RLS & DB roles (ditunda dari D11)
- Port bagian RLS dari `docs/design/legacy-schemas/ledgernew.sql`: role `app_service` & `app_readonly`, grant minimal, RLS + FORCE, policy. Sesuaikan nama tabel dengan skema kanonik.
- Koneksi aplikasi pindah ke `app_service`; verifikasi test suite tetap hijau.

## Task H9 — Load & chaos test
> **Superseded oleh [12-phase2c-resilience-ops.md](12-phase2c-resilience-ops.md) Task T7.** Hasil hardening review 2026-07-11 menulis skenario chaos yang lebih rinci (kill -9 mid-posting, broker/Postgres/Redis down) dengan assertion konkret via `fn_verify_ledger_balance`. Kerjakan T7 di dokumen 12, bukan item ini. k6/vegeta load test (500 rps) yang disebut di sini masih relevan sebagai pelengkap opsional, tapi bukan blocker Phase 2.
- ~~k6/vegeta: 500 rps transfer campuran pada pool 1.000 akun, 10 menit → p99 latency dicatat, verifier bersih setelahnya.~~
- ~~Chaos: putus koneksi Postgres/RabbitMQ di tengah beban → sistem pulih, tidak ada inkonsistensi (jalankan verifier).~~

## Definition of Done Phase 2
- [ ] Semua task H1–H9 punya test; verifier tetap bersih setelah full suite + load test.
- [ ] Runbook singkat `docs/runbook.md`: apa yang harus dilakukan kalau verifier menemukan selisih, kalau outbox `dead` menumpuk, kalau rekonsiliasi mismatch.
