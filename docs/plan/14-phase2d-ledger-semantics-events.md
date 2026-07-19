# 14 — Phase 2d: Ledger Semantics & Event Contract (H6, H7, H1)

Prasyarat: 10–12 selesai (sudah ✅). Baca [13-p1-backlog-review.md](13-p1-backlog-review.md) dulu — keputusan desain K2, K3, K4 dan temuan N1/N3 dikunci di sana. Kerjakan **berurutan T1 → T2 → T3** (T3 memakai hasil T1).

Aturan verifikasi 09 "Pelajaran Terverifikasi" berlaku penuh: setiap task di sini menyentuh SQL/urutan posting → **integration test + smoke test wajib**, unit hijau saja tidak cukup.

---

## T1 — Semantic source/destination (07 H6, keputusan K2)

**Masalah**: `execTransfer` menulis `source_account_id`/`destination_account_id` dari posisi slice (`generalutil.SafeIndex(cmd.AccountIDs, 0/1)`, `internal/ledger/service/handle/service.go:238-239`) — kontrak implisit yang tidak diaudit dan pernah melahirkan bug (09 E4).

### Langkah
1. Di `internal/ledger/processors/processors.go`, tambah tipe dan ubah interface:
   ```go
   type ResolvedAccounts struct {
       Ordered     []uuid.UUID
       Source      uuid.UUID // uuid.Nil bila tidak applicable
       Destination uuid.UUID // uuid.Nil bila tidak applicable
   }
   ```
   `ResolveAccounts(ctx, cmd) (ResolvedAccounts, string, error)` menggantikan return `([]uuid.UUID, string, error)`. `ResolvedCommand.AccountIDs` tetap ada (diisi dari `Ordered`) supaya `BuildEntries` per-index di 22 processor TIDAK berubah.
2. Update 22 processor: setiap `ResolveAccounts` mengisi `Source`/`Destination` sesuai semantik dananya. Panduan per kelompok (audit satu per satu, jangan pukul rata):
   - `money_in`: Source=settlement[gw], Destination=user cash/pocket. `money_out`: kebalikannya.
   - `transfer_p2p`/`transfer_pocket`: Source=akun pengirim, Destination=akun penerima.
   - `withdraw_*`, `escrow_*`: Source=akun yang didebit kaki utama, Destination=akun yang dikredit kaki utama (kaki fee BUKAN source/dest).
   - `adjustment_credit`: Source=akun adjustment sistem, Destination=akun user; `adjustment_debit` kebalikan.
   - `freeze_*`/`chargeback`/`reversal`: isi bila jelas; kalau ambigu (reversal multi-kaki) → `uuid.Nil` keduanya, JANGAN mengarang.
3. Di `service.go` `execTransfer`: isi kolom dari `cmd.Resolved.Source/Destination` (NULL bila `uuid.Nil`), hapus `SafeIndex`. Tambah assert: bila non-Nil, Source/Destination **harus** anggota `Ordered` — kalau tidak, return error internal (processor bug, jangan diam-diam).
4. Regenerate mocks (`go generate ./...` untuk package processors/handle) — jangan hand-edit.
5. Catat cutoff di komentar atas kolom di migrations/000001 (komentar saja, TIDAK ada migrasi data — kolom informatif, kebenaran di entries).

### Test wajib
- Unit per processor: table-driven, assert Source/Destination untuk tiap tipe (22 kasus — satu file test baru `processors/resolved_accounts_test.go`).
- Unit service: Source bukan anggota Ordered → error, tidak insert.
- Integration: posting `money_in` + `transfer_p2p` nyata → SELECT kolom source/destination = akun yang benar secara semantik (bukan sekadar non-NULL).

### DoD
- [x] Tidak ada lagi `SafeIndex(cmd.AccountIDs, ...)` di service.go.
- [x] 22 processor terisi + teraudit (tabel audit di PR description).
- [x] Integration + smoke test hijau.

### Hasil (2026-07-11)
Audit 22 processor mengonfirmasi kontrak yang sudah ada secara konsisten (`BuildEntries` selalu `AccountIDs[0]=Debit, AccountIDs[1]=Credit`) — jadi `twoLeg(source, destination, extra...)` helper cukup untuk 21 processor, hanya `Reversal` yang `Source`/`Destination`-nya sengaja `uuid.Nil` (multi-akun, keputusan K2). Ditambahkan: `internal/ledger/processors/resolved_accounts_test.go` (22 test — satu per tipe, mengunci Source/Destination masing-masing), assertion membership di `service.go` (`accountIDIn`), test `TestHandle_SourceNotInOrdered_RejectedAsProcessorBug`, dan integration test `TestSchemaContract_EndToEndFlow` (diperluas dengan assertion source/dest per posting) + `TestSchemaContract_ReversalSourceDestAlwaysNull` — keduanya lulus terhadap Postgres asli (testcontainers). `go build`, `go vet ./...` (termasuk `-tags=integration`), `go test ./...`, dan `go test -tags=integration -race ./...` semua hijau.

---

## T2 — Lifecycle guard atomik (07 H7, temuan N1+N3, keputusan K3)

**Masalah**: double-reversal race (N1) dan settle-atas-cancel tanpa guard (N3) — dua-duanya jalur money-loss/creation nyata.

### Langkah
1. Migrasi `000004_lifecycle_guard.up.sql` (+down):
   ```sql
   ALTER TABLE ledger_transactions
     ADD COLUMN closed_by_tx_id UUID NULL UNIQUE REFERENCES ledger_transactions(id),
     ADD COLUMN closed_reason   TEXT NULL CHECK (closed_reason IN ('reversed','settled','cancelled','released','refunded')),
     ADD CONSTRAINT chk_closed_pair CHECK ((closed_by_tx_id IS NULL) = (closed_reason IS NULL));
   ```
2. `TransactionRepository`: tambah `CloseOriginal(ctx, tx, originalID, byTxID uuid.UUID, reason string) error` — implementasi **satu UPDATE bersyarat** `WHERE id=$1 AND closed_by_tx_id IS NULL`, cek `RowsAffected==1`, kalau 0 → `apperror.ErrAlreadyClosed` (sentinel baru, map ke HTTP 409). Untuk `reason='reversed'` set juga `status='reversed'` di statement yang sama (kompatibel `GetStatus` existing). Tambah `GetHeader(ctx, tx, id) (type, status string, amount decimal, closedBy *uuid.UUID, err error)` untuk validasi (GetStatus saja tidak cukup — butuh type & amount).
3. Wajibkan `ReferenceID` di `ValidateCommand` untuk: `withdraw_settle`, `withdraw_cancel`, `withdraw_pending_settle`, `withdraw_pending_cancel`, `escrow_release`, `escrow_refund` (reversal sudah). Error jelas: `"reference_id (original transaction) is required for <type>"`.
4. Di `Validate` masing-masing processor lifecycle (dalam tx DB yang sama — signature sudah mendukung): `GetHeader(cmd.ReferenceID)` → cek tipe asal sesuai pasangan (settle→`withdraw_initiate`, pending_settle→`withdraw_pending`, release/refund→`escrow_hold`), status asal `posted`, belum closed, dan `cmd.Amount == amount asal` (full-amount only, error menyebut nilai keduanya). Reversal: tolak bila tipe asal `reversal`.
5. Di `BuildEntries` (atau titik setelah validasi di jalur yang sama — konsisten dengan pola reversal.go:82 yang sudah update-status di BuildEntries): panggil `CloseOriginal` dengan reason sesuai tipe. Reversal MIGRASI dari `UpdateStatus` polos ke `CloseOriginal` — inilah yang menutup N1.
6. `withdraw_initiate`/`withdraw_pending`/`escrow_hold` TIDAK berubah (mereka pembuka lifecycle, bukan penutup).

### Test wajib
- Integration **race**: 2 goroutine reversal konkuren atas original sama (idempotency key beda) → tepat satu sukses, satu `ErrAlreadyClosed`, `fn_verify_ledger_balance()` kosong, saldo = seperti satu reversal. (Pola test konkurensi 50-goroutine di `schema_contract_test.go` bisa dicontoh.)
- Integration lifecycle: initiate → cancel → settle ditolak 409; initiate → settle → settle kedua ditolak; settle amount ≠ amount asal ditolak; release atas escrow_hold yang sudah refund ditolak.
- Unit: ValidateCommand tanpa ReferenceID → error per tipe.
- Jalankan ulang `./scripts/chaos-test.sh 1` (jalur posting berubah).

### DoD
- [x] Kedua race (N1, N3) punya integration test yang GAGAL di kode lama dan LULUS di kode baru (buktikan dengan menjalankan test terhadap commit sebelum fix bila memungkinkan, atau reasoning eksplisit di PR).
- [x] Migrasi up+down teruji. Verifier bersih setelah full suite.

### Hasil (2026-07-11)
Implementasi persis mengikuti K3 dengan satu penyesuaian arsitektur: `CloseOriginal` dipanggil terpusat di `service.go` `execTransfer` (step 4b, via `lifecycleCloseReason` map dari `cmd.Type`), BUKAN di dalam `BuildEntries` masing-masing processor — ini menghindari perubahan signature `TxProcessor.BuildEntries` (yang akan menyentuh 22 file) sekaligus menjaga satu titik guard untuk semua tipe lifecycle. Reversal dimigrasi dari `UpdateStatus` polos ke jalur ini (menutup N1); `WithdrawSettle/Cancel/PendingSettle/PendingCancel/EscrowRelease/EscrowRefund` mendapat `txRepo` + `validateOriginalForClose` helper bersama (cek tipe asal, status, belum closed, amount full-match) di `Validate()`, dan `ReferenceID` wajib di `ValidateCommand` via `requireReferenceID` (menutup N3).

Migrasi `000004_lifecycle_guard` (kolom `closed_by_tx_id` UNIQUE + `closed_reason` + `chk_closed_pair`) diverifikasi up DAN down bekerja bersih terhadap Postgres asli (throwaway container, migrate CLI).

Bukti integration test terhadap Postgres asli (testcontainers), semua lulus:
- `TestSchemaContract_ConcurrentReversal_NoDoubleClose` — 10 reversal konkuren atas transaksi original yang sama → tepat 1 sukses, sisanya `ErrAlreadyClosed`/`ErrAlreadyReversed`, saldo akhir = satu reversal (bukan berlipat), `fn_verify_ledger_balance()` kosong. Ini bukti langsung fix N1.
- `TestSchemaContract_LifecycleGuard_SettleAfterCancel_Rejected` — settle setelah cancel ditolak `ErrAlreadyClosed`, dana tetap di cash (bukti fix N3).
- `TestSchemaContract_LifecycleGuard_DoubleSettle_Rejected` — settle kedua atas initiate yang sama ditolak.
- `TestSchemaContract_LifecycleGuard_AmountMismatch_Rejected` — settle amount ≠ amount asal ditolak `ErrLifecycleAmountMismatch`.

Unit test tambahan: `internal/ledger/processors/lifecycle_guard_test.go` (ValidateCommand per 6 tipe, `validateOriginalForClose` semua cabang, Reversal reversal-of-reversal + already-closed). `./scripts/chaos-test.sh all` dijalankan ulang penuh (bukan cuma skenario 1) setelah perubahan jalur posting — 4/4 skenario tetap lulus. `go build`, `go vet ./...` (+ `-tags=integration`), `go test ./...`, `go test -tags=integration -race -count=1 ./...` semua hijau.

---

## T3 — Kontrak event versioned (07 H1, keputusan K4)

**Masalah**: payload outbox ad-hoc per processor tanpa `schema_version` (contoh `money_in.go:106-125`); kontrak dedup konsumen belum terdokumentasi formal. Kerjakan SETELAH T1 (payload butuh source/dest semantik).

### Langkah
1. Package baru `internal/ledger/events` — hanya tipe + konstanta, **tanpa import subpackage ledger lain**:
   ```go
   const (
       TypeTransactionPosted   = "ledger.transaction.posted.v1"
       TypeTransactionReversed = "ledger.transaction.reversed.v1"
   )
   type EntrySummary struct { AccountID uuid.UUID; Direction string; Amount string }
   type TransactionPosted struct {
       SchemaVersion   int            `json:"schema_version"` // = 1
       TxID            uuid.UUID      `json:"tx_id"`
       TransactionType string         `json:"transaction_type"`
       Amount          string         `json:"amount"`   // minor units, string
       Currency        string         `json:"currency"`
       SourceAccountID *uuid.UUID     `json:"source_account_id"`
       DestinationAccountID *uuid.UUID `json:"destination_account_id"`
       Entries         []EntrySummary `json:"entries"`
       ExternalRef     string         `json:"external_ref,omitempty"`
       OccurredAt      time.Time      `json:"occurred_at"`
   }
   ```
   (+ `TransactionReversed` dengan `original_tx_id`.) Amount SELALU string — jangan biarkan `decimal.Decimal` ter-marshal sebagai angka JSON.
2. Satu constructor `events.NewTransactionPosted(cmd, txID, entries, occurredAt)` dipakai SEMUA processor — `OutboxEvents` per-processor menyusut jadi satu-dua baris. Reversal memakai `NewTransactionReversed`. Audit: tidak boleh ada `map[string]any` payload tersisa di processors/.
3. Routing key AMQP = event type baru. Hapus event type lama (`money_in.completed` dst.) — belum ada konsumen, breaking sekarang gratis (K4).
4. Update PROJECT_GUIDE.md bagian Module Boundaries: `internal/ledger/events` adalah SATU-SATUNYA subpackage ledger yang boleh diimport modul lain.
5. Dokumen kontrak `docs/events.md`: skema kedua event, aturan versioning (field baru = optional di v-sama; field berubah/hilang = vN+1 + dual-publish selama transisi), dan kontrak delivery: **at-least-once; konsumen WAJIB dedup by AMQP `message_id` (= `outbox_events.id`); urutan antar-event TIDAK dijamin**.

### Test wajib
- Unit: setiap processor menghasilkan event dengan `schema_version=1`, type benar, amount string.
- Golden test JSON: marshal `TransactionPosted` dibandingkan file golden — melindungi kontrak dari perubahan tak sengaja (tag json berubah = test merah).
- Integration end-to-end: posting nyata → baca payload di `outbox_events` → unmarshal ke struct events → field cocok dengan transaksi (termasuk source/dest dari T1).

### DoD
- [x] Nol payload ad-hoc tersisa; golden test mengunci wire format.
- [x] `docs/events.md` ada dan PROJECT_GUIDE.md terbarui.
- [x] Outbox relay tetap jalan end-to-end ke RabbitMQ (smoke: `docker compose up` + lihat event terpublish dengan routing key baru).

### Hasil (2026-07-11)
Satu penyesuaian terhadap desain awal: `events.NewTransactionPosted` TIDAK bisa menerima `cmd ResolvedCommand` langsung sebagai parameter (itu akan membuat `internal/ledger/events` bergantung ke `internal/ledger/processors`, melanggar aturan "no cross-import" yang didokumentasikan di paket yang sama). Constructor di `events` hanya menerima nilai primitif/`uuid.UUID`; adapter `newPostedEvent(cmd, txID, entries)` yang menjembatani `ResolvedCommand` → `events.TransactionPosted` hidup di `internal/ledger/processors/processors.go` (arah dependensi tetap satu jalur: processors → events).

`TxProcessor.OutboxEvents` diperluas menerima `entries []model.EntryInstruction` (dipanggil dengan `entries` yang sudah dibangun `BuildEntries` di `service.go` step 11) — perubahan interface kecil ini menghindari merekonstruksi entries dari `cmd` saja (yang akan kehilangan detail fee leg). 21 dari 22 processor kini punya `OutboxEvents` satu baris (`return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}`); Reversal mengemit dua event (posted + reversed, seperti didesain).

Field bernama seperti `commission`/`net_to_merchant` yang dulu ada di beberapa payload ad-hoc (mis. escrow_release) SENGAJA tidak dipertahankan — konsumen menurunkannya dari array `entries` (leg fee punya account_id/amount sendiri). Ini konsekuensi eksplisit dari keputusan K4 (dua skema event generik, bukan 22 skema per-tipe).

Bukti: golden JSON test (`internal/ledger/events/events_test.go`) mengunci wire format persis; integration test `TestSchemaContract_OutboxEventContract` posting nyata → baca `outbox_events.payload` mentah → unmarshal ke `events.TransactionPosted` → semua field cocok. Smoke test manual penuh (docker compose real, bukan testcontainers): post `money_in` via router internal → `outbox_events` berisi `event_type='ledger.transaction.posted.v1'` → pesan diambil langsung dari RabbitMQ (`rabbitmqadmin get`) dengan `routing_key='ledger.transaction.posted.v1'` dan payload JSON persis sesuai skema (amount string, external_ref lolos, entries lengkap). `go build`, `go vet ./...` (+`-tags=integration`), `go test ./...`, `go test -tags=integration -race -count=1 ./...` semua hijau.

---

## Verifikasi Akhir Fase 2d
```bash
go build ./... && make test && go test -tags=integration -race ./...
./scripts/chaos-test.sh all   # jalur posting berubah di T2 — bukti ulang no-money-loss
```
