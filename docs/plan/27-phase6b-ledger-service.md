# 27 — Phase 6b: Ekstraksi ledger-service (+ policy)

> Baca dulu master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md) (arsitektur, kontrak gRPC, mapping error, gotcha). Prasyarat: doc 26 selesai.

## Konteks

Ledger diekstraksi PERTAMA karena semua modul lain bergantung padanya — fase ini memaksa seluruh mesin split dibangun sekali (proto kontrak, gRPC server, client shim, orkestrasi multi-proses di scripts, boundary rules v2); fase 28–31 tinggal mengulang polanya. Setelah fase ini: `cmd/ledger-service` berjalan sendiri terhadap database `seev_ledger` dengan gRPC `:9091`, user-HTTP `:8090`, internal admin `:8091`; `cmd/server` (monolith yang menyusut) memegang sisanya dan memanggil ledger HANYA via gRPC / reverse proxy.

Modul `internal/policy` ikut ledger-service (di-inject ke transport ledger; tabelnya `policy_limits` memang di skema ledger).

## T1 — `ledger.proto`

### Langkah
1. Tulis `api/proto/seev/ledger/v1/ledger.proto` persis sketsa master doc 26: `LedgerService{Post, GetTransactionByIdempotencyKey, GetUserCurrency, ResolveFee, ProvisionUser}`; `PostRequest` cermin `processors.Command` (amount = decimal string, UUID = string, metadata = `google.protobuf.Struct`).
2. `make proto && make proto-lint`; commit `gen/ledger/v1`.

### Test wajib
- `make proto && git diff --exit-code gen/` bersih.

### DoD
- [ ] Kontrak wire LedgerService final untuk fase ini (perubahan berikutnya lewat `make proto-breaking`).

### Hasil
_Belum dikerjakan._

## T2 — gRPC server di modul ledger

### Langkah
1. Subpackage baru `internal/ledger/grpcserver`: implement `ledgerv1.LedgerServiceServer`; konversi proto↔tipe facade (`ledger.Command` = `processors.Command`); TABEL MAPPING ERROR master doc 26 diimplementasikan DI SINI (boleh import `internal/ledger/apperror` — masih dalam modul).
2. Konversi `google.protobuf.Struct` ↔ `map[string]any` untuk metadata; `""` ↔ `uuid.Nil`; amount via `decimal.NewFromString` (tolak non-integral dengan `InvalidArgument`).
3. Method facade `(*ledger.Module) RegisterGRPC(s *grpc.Server)` di `internal/ledger/ledger.go`.

### Test wajib
- Unit bufconn: tiap RPC happy path + tiap cabang mapping error (LedgerError→FailedPrecondition+ErrorInfo, ErrAlreadyClosed→Aborted, not-found→NotFound, error lain→Internal).
- SATU integration test testcontainers: post `money_in` end-to-end via gRPC → transaksi posted, `fn_verify_ledger_balance` 0 baris.

### DoD
- [ ] Semua semantik error facade tereproduksi lewat wire gRPC (dibuktikan test round-trip).

### Hasil
_Belum dikerjakan._

## T3 — Client shim `pkg/ledgerclient`

### Langkah
1. Package `pkg/ledgerclient`: struct `Command`/`Transaction` MILIK SENDIRI (field `decimal.Decimal` + `uuid.UUID` — JANGAN import `internal/ledger`); `New(conn *grpc.ClientConn) *Client`; method `Post(ctx, Command) error`, `GetTransactionByIdempotencyKey(ctx, key, scope) (Transaction, error)`, `GetUserCurrency(ctx, userID, pocketCode) (string, error)`, `ResolveFee(ctx, txType, gateway, currency string, amount decimal.Decimal) (fee decimal.Decimal, feeGateway string, ok bool, err error)`, `ProvisionUser(ctx, userID uuid.UUID, currency string) error`.
2. Semua error dilewatkan `ledgererr.FromStatus` sebelum dikembalikan.

### Test wajib
- Round-trip wire format terhadap fake `LedgerServiceServer` hand-written di test (bufconn) — BUKAN terhadap `internal/ledger` (boundary: pkg tidak boleh import internal). Termasuk kasus error: verifikasi `errors.As(err, *ledgererr.LedgerError)` dan `errors.Is(err, ledgererr.ErrAlreadyClosed)` bekerja.

### DoD
- [ ] `pkg/ledgerclient` + `pkg/ledgererr` mereplikasi kontrak klasifikasi error yang payin/payout andalkan hari ini.

### Hasil
_Belum dikerjakan._

## T4 — Re-type interface konsumen (putus import `internal/ledger`)

### Langkah
1. `internal/payin`: `Poster` (payin.go) pindah ke tipe `ledgerclient.Command`; klasifikasi error `ledger.LedgerError`→`ledgererr.LedgerError` (cek call site sekitar `payin.go:261`).
2. `internal/payout`: `Poster` sama; `ResolveFee` BERTAMBAH `ctx` (update site `orchestrate.go` settle); `GetTransactionByIdempotencyKey` return `ledgerclient.Transaction`; `ledger.ErrAlreadyClosed`→`ledgererr.ErrAlreadyClosed`.
3. `internal/auth`: `Provisioner` jadi `ProvisionUser(ctx context.Context, userID uuid.UUID, currency string) error` — drop return `[]ledger.Account` (caller mengabaikannya, cek `auth.go:287`).
4. Update SEMUA mock/stub unit test terdampak (stubPoster payin/payout, stub Provisioner auth). Ingat gotcha #1: `go vet ./...` + `go vet -tags=integration ./...` setelah semua perubahan signature.

### Test wajib
- `make test` hijau; `grep -rn '"github.com/herdifirdausss/seev/internal/ledger"' internal/payin internal/payout internal/auth` hanya boleh menyisakan import `internal/ledger/events` (kalau ada) — TIDAK ada import root facade.

### DoD
- [ ] `internal/{payin,payout,auth}` tidak lagi bergantung compile-time pada `internal/ledger`.

### Hasil
_Belum dikerjakan._

## T5 — `cmd/ledger-service/main.go`

### Langkah
1. Copy wiring ledger+policy dari `cmd/server/main.go` (blok config→logger→tracing→DB→redis→rabbitmq→policy engine→`ledger.NewModule`→`LoadCurrencies`→`SetFeeRules`(masih env, sampai doc 33)→`StartWorkers`) — DB env `POSTGRES_DB=seev_ledger`, user `ledger_app`.
2. Tiga listener: gRPC `:9091` (`GRPC_PORT`; `grpcx.NewServer` + `module.RegisterGRPC`), user-HTTP `:8090` (`/health`, `/ready`, `/api/v1/ledger/*` = middleware JWT + StripPrefix + `Module.Router()`), internal `:8091` (`/metrics`, internal router ledger, `/api/v1/admin/policy/*`).
3. Flag `-healthcheck`: mode self-probe (hit `/health` sendiri lalu exit 0/1) untuk healthcheck compose distroless (gotcha #4).
4. Graceful shutdown meniru urutan cleanup `cmd/server` (workers berhenti sebelum koneksi ditutup).

### Test wajib
- Boot manual terhadap compose infra: gRPC health serving, `/health` 200, `/metrics` up.

### DoD
- [ ] ledger-service bisa hidup sendirian (tanpa monolith) dan melayani ketiga permukaannya.

### Hasil
_Belum dikerjakan._

## T6 — Rewire monolith jadi klien

### Langkah
1. `cmd/server/main.go`: hapus konstruksi ledger/policy; buat `ledgerclient.New(grpcx.Dial(cfg.LedgerGRPCAddr, cfg.InternalGRPCToken))`; pass ke payin/payout/auth.
2. `internal/handler/dependencies.go`: hapus `Ledger`/`Policy`; tambah `LedgerProxy *httputil.ReverseProxy` (target `LEDGER_USER_API_URL`, path-preserving) di-mount di titik `router.go` tempat `deps.Ledger.Router()` sekarang; `NewInternalRouter` kehilangan mount ledger/policy.
3. Config baru: `LEDGER_GRPC_ADDR`, `LEDGER_USER_API_URL`, `INTERNAL_GRPC_TOKEN` (+ `.env.example`).

### Test wajib
- Unit router: `/api/v1/ledger/*` ter-forward ke backend stub (httptest server sebagai target proxy).

### DoD
- [ ] Monolith tidak lagi meng-instantiate modul ledger/policy; semua jalur uang lewat gRPC/proxy.

### Hasil
_Belum dikerjakan._

## T7 — Cutover DB + scripts + compose

### Langkah
1. Makefile: map SERVICE→DB (`ledger→seev_ledger`, lainnya masih `seev`); terapkan `migrations/ledger` ke `seev_ledger`.
2. `scripts/lib.sh`: `psql_exec` menerima argumen database; `ensure_app_role` per-DB (GRANT `app_service` ke `ledger_app` di `seev_ledger`); ketiga assertion integritas (`assert_ledger_balanced` dkk) target `seev_ledger`; `start_server` → `start_services` (build + launch `ledger-service` di 19091/18090/18091 dan `server` di 18080/18081, env saling menunjuk); `fund_user` target internal port ledger (18091).
3. docker-compose: service `ledger-service` (profile `app`, satu Dockerfile dengan `ARG SERVICE`), depends_on postgres/redis/rabbitmq healthy.
4. `docker compose down -v` (cutover).

### Test wajib
- `./scripts/smoke-test.sh` dan `./scripts/business-e2e.sh` hijau dengan DUA proses.

### DoD
- [ ] Data ledger hidup di `seev_ledger`; seluruh script verifikasi bekerja multi-proses.

### Hasil
_Belum dikerjakan._

## T8 — Boundary test v2

### Langkah
1. Tambah map `serviceModules` di `boundary_test.go`: `ledger-service: {ledger, policy}`; sisanya untuk sementara milik monolith `server`.
2. Rule baru: `cmd/<svc>` hanya boleh import modul miliknya + `internal/config` + `pkg/*` + `gen/*`; kode produksi `internal/{payin,payout,auth,notify,handler}` DILARANG import `internal/ledger` (kecuali `internal/ledger/events`).

### Test wajib
- `make test` hijau; test negatif (tambahkan import ilegal sementara → boundary test merah → revert).

### DoD
- [ ] Pemisahan ledger ditegakkan otomatis oleh CI, bukan konvensi.

### Hasil
_Belum dikerjakan._

---

## Verifikasi akhir dokumen

Gate standar master doc 26 + gate tambahan fase ini: **kill ledger-service** → `/ready` monolith degradasi + `/api/v1/ledger/*` balas 502 → restart ledger-service → pulih tanpa intervensi. Lalu update README index → lanjut [28-phase6c-auth-service.md](28-phase6c-auth-service.md).
