# 05 — Phase 1b: AccountRepository, Provisioning, HTTP API, Wiring

Prasyarat: 04 selesai (skema kanonik + contract test hijau).

## Task 1b.1 — Implementasi SQL `AccountRepository`

`internal/ledger/repository/account_repository.go` saat ini hanya interface + mock. Buat implementasi `pgAccountRepo` (menerima `*sql.DB` / interface query yang konsisten dengan repo lain di folder itu):

| Method | SQL |
|---|---|
| `GetAccountID(userID, accountType)` | `SELECT id FROM accounts WHERE owner_type='user' AND owner_id=$1 AND type=$2 AND pocket_code IS NULL AND status='active'` |
| `GetPocketAccountID(userID, pocketCode)` | `SELECT id FROM accounts WHERE owner_type='user' AND owner_id=$1 AND type='pocket' AND pocket_code=$2 AND status='active'` |
| `GetAccountCurrency(accountID)` | `SELECT currency FROM accounts WHERE id=$1` |
| `GetSystemAccountID(accountType, qualifier)` | `SELECT id FROM accounts WHERE owner_type='system' AND type=$1 AND COALESCE(system_qualifier,'')=$2` — qualifier `""` untuk adjustment/confiscated |

- `sql.ErrNoRows` → bungkus jadi `apperror.ErrAccountNotFound` dengan konteks (type + qualifier/user).
- MVP single-currency: lookup system account tidak memfilter currency (baru relevan di Phase 3 multi-currency — beri komentar TODO).
- Unit test dengan `go-sqlmock` (pola sudah ada di `pkg/database`), plus tambahkan skenario ke integration contract test.

## Task 1b.2 — Account Provisioning Service

Buat `internal/ledger/service/provision/provision.go`:

```go
// CreateUserAccounts membuat set akun standar untuk user baru — idempotent.
// Dipanggil modul auth/onboarding saat user terdaftar, atau lazily saat
// transaksi pertama. Satu transaksi DB.
func (s *Service) CreateUserAccounts(ctx context.Context, userID uuid.UUID, currency string) ([]Account, error)
// CreatePocket membuat akun pocket bernama untuk user.
func (s *Service) CreatePocket(ctx context.Context, userID uuid.UUID, currency, pocketCode string) (Account, error)
```

Perilaku:
1. Set standar: `cash`, `hold`, `pending`, `frozen` (currency sama). Insert `accounts` + `account_balances` (balance 0) dalam **satu tx** via `db.WithTx`.
2. Idempotent: `ON CONFLICT` pada unique index `uq_accounts_owner` → `DO NOTHING`, lalu SELECT hasil akhirnya. Dipanggil dua kali tidak boleh error.
3. `created_by` = `'service:ledger-provision'`.
4. Validasi: currency 3 huruf uppercase dan (MVP) harus `IDR`; pocket_code `[a-z0-9_]{1,32}`.
5. Unit + integration test.

## Task 1b.3 — Public API modul (`internal/ledger/ledger.go`)

Buat file root package `ledger` sebagai satu-satunya pintu masuk modul (aturan boundary 01):

```go
package ledger

// Module adalah facade publik modul ledger.
type Module struct { /* private: service handle, provision, repos, registry */ }

func NewModule(db *database.DB, logger *slog.Logger) *Module   // wiring internal lengkap
func (m *Module) Post(ctx, cmd Command) error                   // delegasi ke service/handle
func (m *Module) ProvisionUser(ctx, userID, currency) ...
func (m *Module) GetBalance(ctx, accountID) (Balance, error)
func (m *Module) GetTransaction(ctx, txID) (Transaction, error)
func (m *Module) ListEntries(ctx, accountID, cursor, limit) ([]Entry, string, error)
func (m *Module) Router() http.Handler                          // transport HTTP modul (1b.4)
func (m *Module) StartWorkers(ctx) / StopWorkers()              // diisi di 06
```

Read method (`GetBalance`, `GetTransaction`, `ListEntries`) butuh query repository baru — tambahkan di repository yang sudah ada, jangan bikin layer baru:
- Balance: `SELECT ab.balance, a.currency, a.status, a.type FROM account_balances ab JOIN accounts a ON a.id=ab.account_id WHERE ab.account_id=$1` (tanpa lock).
- ListEntries: keyset pagination — `WHERE account_id=$1 AND (created_at, id) < ($2, $3) ORDER BY created_at DESC, id DESC LIMIT $4`; cursor = base64 dari `created_at|id`.

## Task 1b.4 — HTTP transport (`internal/ledger/transport/http.go`)

Endpoint (mount di bawah `/api/v1/ledger`, semua lewat middleware auth yang ada):

| Route | Handler | Catatan |
|---|---|---|
| `POST /transactions` | post transaction | body ↓ ; `user_id` DIAMBIL DARI JWT CLAIM, bukan body. Tipe admin-only (`adjustment_*`, `freeze_*`, `reversal`, `escrow_*` kecuali dipicu sistem) → wajib `middleware.WithRole("admin")` |
| `GET /transactions/{id}` | detail tx + entries | 404 kalau bukan milik user (kecuali admin) |
| `GET /accounts` | daftar akun user (dari claim) | |
| `GET /accounts/{id}/balance` | saldo | cek kepemilikan |
| `GET /accounts/{id}/entries?cursor=&limit=` | statement | default limit 50, max 200 |
| `POST /accounts/pockets` | buat pocket | body: `{"pocket_code":"travel"}` |

Request body `POST /transactions`:
```json
{
  "idempotency_key": "client-generated-uuid",   // wajib, 8..128 char
  "type": "transfer_p2p",                        // harus terdaftar di registry
  "amount": "150000",                            // string desimal, integer minor unit, > 0
  "target_user_id": "…",                         // wajib utk transfer_p2p
  "pocket_code": "travel",                       // opsional
  "reference_id": "…",                           // opsional (uuid)
  "metadata": {"gateway": "bca"}                 // tergantung tipe (processor memvalidasi)
}
```

Response sukses `201`: `{ "status": "posted", "idempotency_key": "…" }` (pakai envelope `pkg/response`). Idempotent replay → `200` dengan body sama.

Mapping error → HTTP (satu fungsi `apperrToStatus`, unit-tested):

| apperror | HTTP |
|---|---|
| `ErrValidation`, `ErrEmptyIdempotencyKey`, `ErrUnknownProcessor` | 400 |
| `ErrInsufficientBalance` | 422 |
| `ErrAccountNotFound` | 404 |
| `ErrAccountSuspended/Closed`, `ErrCurrencyMismatch` | 422 |
| `ErrStillProcessing` | 409 |
| `ErrPreviousFailed` | 409 (body menyertakan pesan bahwa key sudah dipakai transaksi gagal — client harus pakai key baru) |
| lainnya | 500 (tanpa detail internal di body; detail ke log) |

Semua handler: decode dengan limit body (middleware sudah ada), validasi field, konversi ke `processors.Command`. Test handler dengan `httptest` + mock Module.

## Task 1b.5 — Wiring di composition root

1. `internal/handler/dependencies.go`: tambah field `Ledger *ledger.Module`.
2. `cmd/server/main.go`: konstruksi `ledger.NewModule(db, log)` → masukkan deps.
3. `internal/handler/router.go`: mount `apiMux.Handle("/ledger/", http.StripPrefix("/ledger", authed(deps.Ledger.Router())))`. Hapus placeholder yang tidak dipakai ATAU biarkan `/auth/*` (akan diisi modul auth nanti) — jangan hapus health/ready.
4. Tambah route `GET /metrics` (promhttp) di root mux, tanpa middleware auth (tapi jangan diekspos publik di production — cukup catat di README).

## Task 1b.6 — Metrics & tracing minimal

Di `service/handle`: counter `ledger_transactions_total{type,status}` + histogram `ledger_post_duration_seconds{type}`; span OTel `ledger.Handle` dengan attribute type & idempotency scope (JANGAN log idempotency key penuh & nominal di span publik). Registrasi collector di `NewModule`.

## Definition of Done 05

- [ ] `curl` flow lengkap terhadap server lokal (`make dev`, `docker compose up`): provision (via register-stub atau endpoint provision) → money_in (admin/simulasi gateway) → transfer_p2p antara 2 user → cek balance & entries via API. Dokumentasikan urutan curl-nya di `docs/plan/verify-mvp.md`.
- [ ] Test idempotency & concurrency level API: 2 request paralel idempotency key sama → satu 201 + satu 200/409, tidak pernah double posting (integration test).
- [ ] `make lint`, `make test`, contract test integration hijau.
- [ ] Boundary: `grep -rn "internal/ledger/" --include="*.go" cmd/ internal/handler/` hanya menemukan import `internal/ledger` (root) — tidak ada import subpackage ledger dari luar modul.
