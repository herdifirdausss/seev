# 00 — Audit Kondisi Repo Saat Ini

Snapshot per Juli 2026, branch `main`, commit `ac7d617`. Module path: `github.com/herdifirdausss/seev`, Go 1.25.

## Yang Sudah Ada dan Bagus (jangan ditulis ulang)

### Infrastruktur (`pkg/`)
- `pkg/database` — pgx (stdlib mode) pool + `WithTx` helper. Dipakai service ledger via interface `DatabaseSQL`.
- `pkg/cache` — Redis client + rate limiter (sliding window, fallback in-memory).
- `pkg/messaging` — RabbitMQ lengkap: broker, publisher, consumer, topology, pool, metrics, auto-reconnect, DLQ.
- `pkg/middleware` — RequestID, Logger, Recovery, CORS, RateLimit, SecurityHeaders, Timeout, JWT Auth (HS256) + `WithRole`.
- `pkg/logger` — slog terstruktur + masking data sensitif.
- `pkg/response` — JSON envelope standar.
- `pkg/generalutil`, `pkg/generalerror` — util SQL args, meta parsing, klasifikasi error Postgres (retryable, duplicate key).
- `internal/config` — env config dengan validasi ketat.
- `internal/server` — graceful shutdown.
- `internal/handler` — router `net/http` Go 1.22 pattern; health/ready probes.

### Modul Ledger (`internal/ledger/`) — inti yang serius
- **`service/handle/service.go`** — posting engine. Ini jantung sistem. Alurnya benar dan sudah melewati beberapa iterasi review:
  1. Idempotency gate via INSERT + SAVEPOINT (tahan unique-violation tanpa abort tx).
  2. Lock semua akun `FOR UPDATE` dengan urutan deterministik (ORDER BY account_id) → bebas deadlock.
  3. Validasi struktural (akun ada, aktif, currency cocok).
  4. Validasi bisnis per processor; gagal → commit header `status='failed'` (audit trail), bukan rollback.
  5. Build entries → validasi balanced (Σdebit = Σcredit) → hitung saldo baru satu kali (single source).
  6. Insert entries, update balance projection, mark posted, insert outbox — **satu transaksi DB**.
  7. Retry loop dengan jitter untuk error retryable (serialization, deadlock).
- **`processors/`** — 22 processor transaksi via interface `TxProcessor` + registry. Money in/out, withdraw lifecycle (6 tipe), transfer P2P/pocket, refund, fee, chargeback, escrow (3), freeze (3), adjustment (2), reversal. Mendukung inline fee atomik (3 entri dalam satu tx). Ada test untuk sebagian.
- **`repository/`** — interface + implementasi SQL untuk balance (lock/update), entry (insert), transaction (insert/mark/lookup idempotency), outbox (batch insert). Semua punya mock (`mockgen`).
- **`apperror/`** — sentinel errors (ErrAlreadyPosted, ErrInsufficientBalance, dll).
- **`constant/`** — Direction dan kode account type/status sebagai **string TEXT** (`"debit"`, `"cash"`, `"active"`).

## Masalah yang Harus Dibereskan

### M1 — KRITIS: Tidak ada skema DB yang cocok dengan kode
Kode menulis ke tabel-tabel ini (hasil grep SQL di repository + service):

| Tabel | Kolom yang dipakai kode |
|---|---|
| `ledger_transactions` | `id, idempotency_key, idempotency_scope, type, status, amount, currency, source_account_id, destination_account_id, error_message, created_at, updated_at` — status/type/currency **TEXT** |
| `ledger_entries` | `id, transaction_id, account_id, direction, amount, balance_after, note, created_at` — direction **TEXT** `'debit'/'credit'` |
| `account_balances` | `account_id, balance, currency` (LockBalances juga SELECT `a.status, a.type` dari JOIN `accounts`) |
| `accounts` | `id, status, type` sebagai **TEXT** codes |
| `outbox_events` | insert hanya `id, aggregate_type, aggregate_id, event_type, payload, created_at` — kolom lain harus punya DEFAULT |

Sementara file skema yang ada **semuanya tidak cocok**:
- `migrations/001.sql` — draft awal + potongan pseudo-SQL yang bahkan tidak valid (baris `external_ref UNIQUE` lepas).
- `migrations/002.sql` — paling dekat dengan kode tapi pakai `transaction_id` bukan `idempotency_key`, tanpa banyak kolom.
- `migrations/auth.sql` — **kosong (0 byte)**.
- `migrations/ledger.sql` + `migrations/ledgernew.sql` — desain berbeda total: tabel `balance_transactions`, lookup table SMALLINT FK (`transaction_types`, `entry_directions`, dll), kolom fee, `balance_before`. Kode TIDAK menulis ke bentuk ini. Tapi file ini berisi guard bagus yang harus di-port: trigger immutability, lifecycle outbox (`pending/processing/published/failed/dead` + auto dead-letter), fungsi verifikasi `fn_verify_ledger_balance` / `fn_verify_account_balance`, view audit, RLS.
- `internal/ledger/001.sql` — salinan lain lagi, salah tempat (bukan di `migrations/`), dan view-nya merujuk kolom yang tidak ada (`a.user_id`, `a.is_system`).

**Keputusan (dikunci, lihat 01):** bentuk kode menang. Tulis satu set migrasi kanonik mengikuti tabel/kolom yang dipakai kode, port guard dari `ledgernew.sql`, arsipkan semua file skema lama.

### M2 — KRITIS: `AccountRepository` belum ada implementasinya
`internal/ledger/repository/account_repository.go` hanya berisi **interface** (`GetAccountID`, `GetPocketAccountID`, `GetAccountCurrency`, `GetSystemAccountID`) + mock. Semua processor bergantung padanya. Tanpa implementasi SQL, tidak ada satupun processor yang bisa jalan di luar unit test.

### M3 — Modul ledger tidak ter-wire ke aplikasi
- `cmd/server/main.go` hanya wiring DB/Redis/RabbitMQ → router. Tidak ada konstruksi `ledger.Service`, registry processor, atau repository.
- `internal/handler/router.go` hanya berisi placeholder 501 (`/auth/login`, `/users/me`, dll). Tidak ada endpoint ledger sama sekali.
- Tidak ada mekanisme membuat akun user (account provisioning). Comment di skema bilang "account dibuat eksplisit oleh service" — service itu belum ada.

### M4 — Salah tempat / file mati
- `cmd/scheduler/scheduler_final.go` — 1.221 baris library cron lengkap (extended cron syntax, distributed lock Redis, heap scheduler) sebagai `package main` di `cmd/`. Ini library bagus yang salah tempat → pindah ke `pkg/scheduler`. Test-nya (582 + 727 baris) ikut pindah.
- `cmd/rabbitmq/rabbitmq.go` — 204 baris contoh/demo; duplikat fungsi `pkg/messaging` → hapus.
- `internal/ledger/service/migration.go` — mendefinisikan enum SMALLINT (`AccountCash = 1`, `CurrencyIDR = 1`) milik desain `ledgernew.sql` yang tidak dipakai kode → hapus (atau tulis ulang jika ada logic provisioning yang mau diselamatkan — periksa isinya dulu).
- `internal/ledger/service/transfer/transfer_service.go` — periksa: kemungkinan versi lama yang sudah digantikan `service/handle`. Kalau tidak direferensikan siapapun (`grep -rn "service/transfer"`), hapus.
- `internal/ledger/001.sql` — hapus setelah migrasi kanonik dibuat.
- `internal/handler/dependencties.go` — typo nama file → rename `dependencies.go`.

### M5 — README bohong
README mendeskripsikan struktur yang tidak ada (`internal/database`, `internal/middleware`, `docker-compose.yml`, `.air.toml`, `.env.example` — sebagian di `pkg/`, sebagian tidak ada sama sekali). Tulis ulang setelah Phase 0.

### M6 — Duplikasi tipe `Command`
`processors.Command` dan `model.Command` identik dan dua-duanya ada. Satu sumber saja: pakai `processors.Command`, hapus `model.Command` (cek pemakai dengan `grep -rn "model.Command"`).

## Addendum — Bug Build yang Ditemukan & Diperbaiki Selama Phase 0

Audit awal di atas tidak menjalankan `go build ./...` (hanya membaca kode). Saat eksekusi Phase 0, repo ternyata **tidak compile** karena beberapa bug tambahan. Semua sudah diperbaiki; dicatat di sini supaya Phase 1a/1b tidak berasumsi salah tentang bentuk interface:

1. **`TxProcessor.ValidateCommand` dideklarasikan di interface tapi tidak ada satupun dari 22 processor yang mengimplementasikannya**, dan `Service.Handle()` juga tidak pernah memanggilnya (dead interface surface). Diperbaiki: ditambahkan `ValidateCommand(_ context.Context, _ Command) error { return nil }` no-op ke semua 22 processor, dan dipanggil di `Handle()` tepat setelah `registry.Get()`, sebelum `ResolveAccounts()`. Jika sebuah processor butuh validasi metadata pre-DB nyata, implementasikan di method ini.
2. **`internal/ledger/processors/reversal.go` memanggil method `TransactionRepository` yang salah signature atau tidak ada**: `GetAccountIDs(ctx, txID)` (interface lama mensyaratkan `*sql.Tx` padahal dipanggil dari `ResolveAccounts` yang tidak punya tx), `GetStatus` (dikomentari di interface), `MarkReversed` (tidak pernah ada). Diperbaiki:
   - `TransactionRepository.GetAccountIDs` sekarang `(ctx, transactionID) ([]uuid.UUID, error)` — read-only, dibaca lewat `database.DatabaseSQL` yang disimpan di `transactionRepo`, dipanggil sebelum posting tx dimulai (pola sama seperti `AccountRepository`).
   - `TransactionRepository.GetStatus(ctx, tx, transactionID) (string, error)` ditambahkan (dipakai dari dalam `Validate`, yang punya tx).
   - `reversal.go` memakai `UpdateStatus(ctx, tx, id, "reversed", nil)` alih-alih `MarkReversed`.
   - **Konsekuensi untuk skema (04)**: status `ledger_transactions` HARUS mencakup `'reversed'` selain `'pending'/'posted'/'failed'` — update CHECK constraint di 04 saat implementasi.
   - **Konsekuensi untuk constructor**: `NewTransactionRepository()` sekarang `NewTransactionRepository(db database.DatabaseSQL)` — perbarui pemanggilnya di Phase 1b (wiring).
   - Bug SQL nyata juga ditemukan & diperbaiki: query lama `GetAccountIDs` tidak mem-passing `transactionID` sebagai parameter query (`$1` tanpa arg) — akan gagal di Postgres asli meski lolos di beberapa mock.
   - Mock `TransactionRepository` diregenerasi via `mockgen`.
3. **`internal/ledger/service/transfer/transfer_service.go` dihapus** — prototipe posting-engine yang sudah digantikan `service/handle/service.go`, tidak dipakai siapapun, dan tidak compile (`domain` package tidak ada, memanggil method repo yang tidak ada: `InsertPending`, `MarkFailed`, `MarkPosted`).
4. Test `internal/ledger/service/handle/service_test.go` diperbarui: `TestHandle_ResolveError_Propagated` menambah ekspektasi mock `ValidateCommand`, dan test baru `TestHandle_ValidateCommandError_Propagated` memverifikasi bahwa `ResolveAccounts` tidak dipanggil kalau `ValidateCommand` menolak command.

Status setelah addendum ini: `go build ./...`, `go vet ./...`, dan `go test ./... -race` semua hijau.

## Perintah Verifikasi Cepat

```bash
# Tabel apa yang benar-benar dipakai kode:
grep -rhoE 'FROM [a-z_]+|INTO [a-z_]+|UPDATE [a-z_]+' internal/ --include="*.go" | sort -u

# Siapa yang memakai file yang dicurigai mati:
grep -rn "service/transfer\|model.Command\|cmd/rabbitmq" --include="*.go" .

# Status test saat ini:
make test
```
