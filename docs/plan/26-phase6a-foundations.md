# 26 — Phase 6a: Microservices Split — Master Reference + Foundations

> **Dokumen ini merangkap dua peran**: (1) **master reference** untuk seluruh rangkaian split microservices (docs 26–35) — arsitektur target, keputusan terkunci, kontrak gRPC, dan gotcha eksekusi ada DI SINI dan dirujuk oleh dokumen 27–35; (2) plan eksekusi fase foundations (T1–T4 di bawah). Baca bagian master reference SEBELUM mengerjakan dokumen mana pun di rangkaian ini.

## Konteks

Repo ini adalah modular monolith yang sudah matang (docs 03–25 ✅). Rangkaian docs 26–35 memecahnya menjadi **microservices untuk tujuan belajar**. Proyek belum dipakai bisnis — breaking changes DIPERBOLEHKAN: migrasi boleh di-renumber, data boleh di-wipe (`docker compose down -v`), URL boleh berubah. Playbook produksi doc 24 (dual-write, gradual cutover, pre-split gates) TIDAK berlaku — split big-bang per service. Yang WAJIB: **setiap fase berakhir dengan sistem yang jalan penuh** (`make test` + `./scripts/smoke-test.sh` + `./scripts/business-e2e.sh` hijau).

Dua fitur bisnis baru ikut dalam rangkaian ini:
1. **Routing DB-driven** (docs 29, 30): vendor untuk topup dan payout dipilih dari aturan routing di database (admin-configurable), menggantikan map hardcoded `payinGatewayMapping`/`payoutGatewayMapping` di `cmd/server/main.go`.
2. **Fee per-user-per-route** (doc 33): fee configurable di DB per (user, route/gateway), menggantikan env `FEE_*` statis.

## Keputusan terkunci (master — berlaku untuk docs 26–35)

| Keputusan | Pilihan |
|---|---|
| Database | **DB per service** — SATU container Postgres, ENAM database: `seev_ledger`, `seev_auth`, `seev_payin`, `seev_payout`, `seev_fraud`, `seev_gateway`, masing-masing dengan login role sendiri (`<svc>_app`). Satu container karena Docker Desktop dev ~3.9GB RAM; isolasi lintas-DB tetap nyata (tidak bisa JOIN antar database). |
| Komunikasi sync internal | **gRPC** (buf; proto di `api/proto/`, codegen di-commit di `gen/`). RabbitMQ tetap untuk event async. |
| Struktur repo | **Monorepo, satu go.mod** — tiap service = `cmd/<service>/` + package `internal/<module>` existing; `pkg/` tetap shared. |
| Runtime | **docker-compose** utama; doc 35 OPSIONAL: Kubernetes lokal (kind). |
| Service PUBLIC | `gateway-service` (user-facing/BFF, dihit frontend) dan `auth-service`. |
| Service INTERNAL | `ledger-service`, `payin-service`, `payout-service`, `fraud-service` — tidak pernah reachable end user. |
| Notify | Ikut **gateway-service** (API-nya user-facing; `notif_notifications` → `seev_gateway`; tetap consume `ledger.transaction.posted.v1`). |
| Policy engine | Tetap di **ledger-service** (`policy_limits` di `seev_ledger`; admin CRUD policy di internal listener ledger). |
| Auth → provisioning | **gRPC sync** `LedgerService.ProvisionUser` (pertahankan lazy-heal-on-login existing). |
| API ledger end-user | Gateway **reverse-proxy HTTP** `/api/v1/ledger/*` → user-HTTP listener ledger (`:8090`). Pengecualian sadar dari "gRPC untuk internal": request user yang lewat saja (~30 endpoint dengan JSON envelope yang harus stabil); semua panggilan *yang diinisiasi service* tetap gRPC. |
| Webhook edge | payin tetap INTERNAL. Gateway pegang `POST /webhooks/{vendor}` publik, forward vendor + headers + **raw body bytes** via gRPC (signature verification butuh byte mentah persis). |
| Tabel fee | `fee_rules` di **seev_ledger** (ledger yang posting fee leg). "Route" == string `gateway` ledger existing. |
| Tabel routing | `payin_routing_rules`+`payin_vendor_gateways` di seev_payin; `payout_routing_rules`+`payout_vendor_gateways` di seev_payout. `internal/ledger/service/disbursement` JANGAN disentuh (tidak punya konsep vendor). |
| Fraud | Modul baru `internal/fraud`; `screening_events` → `seev_fraud`; ledger pertahankan seam `processors.PrePostHook` + hook gRPC (timeout 500ms, **fail-open**). Velocity counter: Redis **DB index 1**, di-increment consumer event (menghitung transaksi posted, bukan attempt — upgrade semantik sadar). |
| Admin HTTP | Tiap service internal pegang internal admin HTTP listener sendiri. TIDAK ada admin BFF (future work). |
| JWT | `JWT_SECRET` sama di semua service; `pkg/middleware` verifikasi lokal; `cmd/gentoken` tetap untuk script. |
| Auth gRPC internal | Token statis `INTERNAL_GRPC_TOKEN` via metadata `authorization: Bearer` (interceptor di `pkg/grpcx`). mTLS = future work. |
| Outbox | **Hanya ledger**. Future work: event status payout butuh outbox payout sendiri. |
| Migrasi | `migrations/<service>/`, renumber dari 000001 per service; selama share satu DB pakai `x-migrations-table=schema_migrations_<service>`. |
| Urutan ekstraksi | **Ledger duluan** (27) → auth (28) → payin (29) → payout (30) → fraud (31) → gateway (32) → fee DB (33) → verifikasi (34) → k8s opsional (35). |

## Arsitektur target

```
                    INTERNET
                       │
       ┌───────────────┴─────────────────┐
       │                                 │
  gateway-service :8080           auth-service :8082
  (PUBLIC, BFF frontend)          (PUBLIC register/login/
  cmd/gateway                      refresh /users/me)
  - /api/v1/ledger/* ── HTTP reverse proxy ──────────────┐
  - /topup /payout /notifications        │ gRPC          │
  - /webhooks/{vendor} (raw fwd)         │ ProvisionUser │
  - notify consumer (RabbitMQ)           ▼               ▼
  DB: seev_gateway (notif_*)   ┌──────────────────────────────────┐
       │ gRPC                  │ ledger-service                   │
       ├──────────────────────▶│ cmd/ledger-service               │
       │ CreateTopupIntent /   │ :9091 gRPC  :8090 user-HTTP      │
       ▼ HandleWebhook         │ :8091 internal admin HTTP        │
  payin-service                │ modules: internal/ledger,        │
  cmd/payin-service            │          internal/policy         │
  :9092 gRPC :8092 admin       │ DB: seev_ledger (+fee_rules)     │
  DB: seev_payin (+routing)    │ outbox ─▶ RabbitMQ ledger.events │
       │ gRPC Post/…           │      PrePostHook (gRPC 500ms,    │
       └──────────────────────▶│      fail-open) ─▶ fraud-service │
  payout-service ─────────────▶└──────────────────────────────────┘
  cmd/payout-service  gRPC              fraud-service
  :9093 gRPC :8093 admin                cmd/fraud-service
  DB: seev_payout (+routing)            :9094 gRPC :8094 admin
       ▲ gRPC CreatePayout              DB: seev_fraud, Redis DB 1
       └── gateway
  RabbitMQ exchange "ledger.events":
   ├─ ledger.events.audit (catch-all)
   ├─ ledger.events.notifications ─▶ gateway notify consumer
   └─ ledger.events.fraud ─────────▶ fraud consumer (velocity)
```

**Alokasi port** (compose container = angka di diagram; script lib.sh host-run pakai 1xxxx seperti biasa): gateway 8080/8081, auth 8082/8083, ledger 9091(gRPC)/8090(user-HTTP)/8091(admin), payin 9092/8092, payout 9093/8093, fraud 9094/8094.

## Kontrak gRPC (master — detail final di fase masing-masing)

Semua proto di `api/proto/seev/<svc>/v1/`, package Go hasil codegen `github.com/herdifirdausss/seev/gen/<svc>/v1`.

`ledger.proto` (doc 27):
```proto
service LedgerService {
  rpc Post(PostRequest) returns (PostResponse);
  rpc GetTransactionByIdempotencyKey(GetTxByIdemKeyRequest) returns (Transaction);
  rpc GetUserCurrency(GetUserCurrencyRequest) returns (GetUserCurrencyResponse);
  rpc ResolveFee(ResolveFeeRequest) returns (ResolveFeeResponse);   // + user_id di doc 33
  rpc ProvisionUser(ProvisionUserRequest) returns (ProvisionUserResponse);
}
message PostRequest {   // cermin processors.Command
  string idempotency_key = 1; string idempotency_scope = 2;
  string type = 3; string amount = 4;            // decimal string minor-unit — JANGAN tipe numerik proto
  string user_id = 5; string target_user_id = 6; // UUID string, "" = uuid.Nil
  string pocket_code = 7; string reference_id = 8;
  google.protobuf.Struct metadata = 9;
}
```

**Mapping error (LOAD-BEARING — klasifikasi payin/payout bergantung padanya):**
- `*apperror.LedgerError` → `codes.FailedPrecondition` + `errdetails.ErrorInfo{Reason: e.Code, Domain: "seev.ledger"}`
- `apperror.ErrAlreadyClosed` → `codes.Aborted` + `ErrorInfo{Reason:"ALREADY_CLOSED"}`
- not-found → `codes.NotFound`; sisanya → `codes.Internal`. Client: semua status tak ter-map (termasuk `Unavailable`) = infra error = retryable.
- Client side `pkg/ledgererr`: `FromStatus(err)` merekonstruksi `LedgerError{Code, Message, Retryable}` + sentinel `ErrAlreadyClosed` supaya `errors.Is/As` di payin/payout tetap bekerja.

`payin.proto` (doc 29): `HandleWebhook(vendor, headers map<string,string>, raw_body bytes) → Result{OK|BUSINESS_FAILURE}` (status gRPC: `NotFound`=vendor tak dikenal→HTTP 404, `Unauthenticated`=bad signature→401, `Internal`/`Unavailable`=infra→503; OK/BUSINESS_FAILURE→200), `CreateTopupIntent(user_id, amount)` — field vendor DIHAPUS, routing yang memutuskan — dan `GetTopupIntent(id, user_id)`.

`payout.proto` (doc 30): `CreatePayout(user_id, amount, destination bytes-JSON, created_by)` (vendor dihapus — routing), `GetPayout(id, user_id)`.

`fraud.proto` (doc 31): `Screen(tx_type, user_id, amount, currency) → {block bool, reason string}` (cermin `processors.Verdict`).

`ping.proto` (doc 26 T2): PingService trivial — hanya untuk test interceptor `pkg/grpcx`.

## Catatan penting untuk eksekutor (master — semua dari pengalaman nyata repo ini)

1. **`go build ./...` TIDAK meng-compile file `_test.go`** — setiap perubahan signature interface WAJIB diikuti `go vet ./...` DAN `go vet -tags=integration ./...`.
2. **Zero-value `RabbitMQConfig`/`BrokerConfig` deadlock (bug nyata)**: `MaxConcurrentPublish: 0` → publish semaphore kapasitas 0 → `PublishTo` block selamanya tanpa error apa pun. Semua `main.go` baru WAJIB lewat jalur normalisasi default, jangan hand-build config parsial.
3. **Port Postgres host = 5433** (bukan 5432); `scripts/lib.sh` auto-detect via `docker port` — pertahankan pola itu.
4. **Distroless tak punya shell/curl** → healthcheck compose pakai flag `-healthcheck` (mode self-probe) binary itu sendiri.
5. **golang-migrate multi-folder satu DB** wajib `&x-migrations-table=schema_migrations_<service>` di DSN atau versi bentrok. Renumber aman HANYA karena tiap cutover `docker compose down -v`.
6. **RLS/role split**: app konek sebagai login role terbatas (`<svc>_app`), migrasi sebagai schema owner. Tiap DB baru butuh `<svc>_app LOGIN` + `GRANT app_service` (lib.sh `ensure_app_role` per DB — postgres-init hanya jalan di first boot volume).
7. **Semantik error lintas gRPC itu load-bearing**: kontrak webhook 200-vs-503 payin dan rekonsiliasi `ErrAlreadyClosed` payout bergantung pada mapping status/ErrorInfo doc 27. Test round-trip-nya eksplisit.
8. **Uang tidak pernah jadi angka proto** — amount = decimal string minor-unit; UUID = string dengan `""` ↔ `uuid.Nil`.
9. **Typed-nil interface gotcha**: tiru `handler.CacheOrNil` (`internal/handler/dependencies.go`) saat assign pointer konkret nilable ke field interface di main baru.
10. **Publish ke exchange yang belum dideclare mematikan channel AMQP** — `DeclareTopology` declare exchange+queue+DLX; ledger declare `ledger.events.audit` saat start, tiap consumer declare queue-nya sendiri → urutan start antar-service fleksibel.
11. **`SetFeeRules` me-rebuild router publik ledger** — sampai doc 33 menghapusnya, harus dipanggil sebelum serving, persis seperti sekarang.
12. **`JWT_SECRET` harus identik di semua service** atau semuanya 401.
13. **Rule boundary_test skip `_test.go`** — integration test lintas modul tetap legal; jangan "diperbaiki".
14. **Budget RAM 3.9GB (Docker Desktop dev)**: jalur test utama = binary host vs infra compose; `make test` pakai testcontainers — jangan bersamaan dengan compose profile `app` penuh + jaeger.
15. **JANGAN sentuh `internal/ledger/service/disbursement`** untuk routing — batch posting internal ledger tanpa konsep vendor.
16. **Known issue pre-existing di luar scope**: `TestSchemaContract_Accrual_BasisIsSnapshotNotLiveBalance` gagal reproducible (bug accrual docs/plan/19, di-flag terpisah) — BUKAN disebabkan pekerjaan split, tidak memblokir gate fase mana pun.

## Aturan gate per fase (master)

Fase dinyatakan selesai HANYA jika semuanya hijau:
```bash
make lint && make test
go vet ./... && go vet -tags=integration ./...
./scripts/smoke-test.sh
./scripts/business-e2e.sh
```
Cutover DB selalu `docker compose down -v` (data boleh hilang). Setiap task selesai → centang DoD + isi Hasil di dokumen fasenya; setiap fase selesai → update status di [README.md](README.md).

---

---

# Fase eksekusi dokumen ini: Foundations

**Goal**: perilaku monolith TIDAK berubah sama sekali; pondasi split disiapkan — hutang boundary `pkg/ → internal/config` dilunasi, toolchain buf/gRPC berdiri, plumbing gRPC shared tersedia, migrasi direstrukturisasi per-service.

## T1 — Per-pkg config structs (bunuh grandfathered violation)

`boundary_test.go` punya SATU pelanggaran yang di-grandfather sejak awal proyek: beberapa package `pkg/*` (database, cache, messaging, logger) mengimport `internal/config` untuk struct config-nya — melanggar aturan `pkg/` tidak boleh import `internal/`. Docs 21/24 menandai ini WAJIB dilunasi sebelum ekstraksi pertama.

### Langkah
1. Buat `pkg/database/config.go`: `type Config struct` mirror semua field `internal/config.PostgresConfig` (termasuk `StatementTimeout`/`LockTimeout`/`IdleInTxTimeout` yang dipakai DSN builder). Ubah `pkg/database/postgres.go` (`database.New`, `DSN()`) memakai tipe lokal ini.
2. Buat `pkg/cache/config.go` dan `pkg/logger/config.go` dengan pola sama; ubah `pkg/cache/redis.go`, `pkg/logger/logger.go`.
3. `pkg/messaging`: `messaging.New` (`broker.go:75`) saat ini menerima `config.RabbitMQConfig` padahal `messaging.BrokerConfig` (config.go:35) SUDAH ADA lengkap dengan `withDefaults()` — ganti signature `New`/`NewWithRegistry`/`newWithDial` ke `BrokerConfig`, hapus import `internal/config`. PERHATIAN gotcha #2: pastikan jalur `withDefaults()` selalu terpanggil.
4. Bersihkan juga file `_test.go` di bawah `pkg/` yang import `internal/config` (`pkg/database/postgres_test.go`, `postgres_integration_test.go`, `pkg/logger/logger_test.go`, `pkg/middleware/logger_test.go` — audit dengan grep).
5. Tambah mapper di `internal/config`: `func (c PostgresConfig) Pkg() database.Config`, `func (c RedisConfig) Pkg() cache.Config`, `func (c LoggerConfig) Pkg() logger.Config`, `func (c RabbitMQConfig) Broker() messaging.BrokerConfig`. Pakai di `cmd/server/main.go` (satu-satunya composition root saat ini).
6. Hapus entry `"pkg -> internal/config"` dari map `grandfathered` di `boundary_test.go` — biarkan map-nya kosong, jangan hapus mekanismenya.

### Test wajib
- `make test` hijau (semua test pkg/database, pkg/cache, pkg/messaging, pkg/logger ter-update).
- Boundary test hijau dengan map grandfathered KOSONG.

### DoD
- [ ] `grep -rn "internal/config" pkg/` tidak mengembalikan apa pun; perilaku runtime monolith byte-identical (env yang sama menghasilkan config efektif yang sama).

### Hasil
_Belum dikerjakan._

## T2 — Toolchain buf + proto pertama

### Langkah
1. Tambah `buf.yaml` (module root `api/proto`) dan `buf.gen.yaml` (managed mode, `go_package_prefix: github.com/herdifirdausss/seev/gen`; plugin `protoc-gen-go` + `protoc-gen-go-grpc`, out `gen/`).
2. Proto pertama: `api/proto/seev/ping/v1/ping.proto` — `service PingService { rpc Ping(PingRequest) returns (PingResponse); }` (dipakai HANYA oleh test interceptor T3).
3. Makefile target baru: `tools` (go install versi ter-pin protoc-gen-go/protoc-gen-go-grpc + buf), `proto` (`buf generate`), `proto-lint` (`buf lint`), `proto-breaking` (`buf breaking --against '.git#branch=main'`).
4. Commit hasil codegen `gen/` (kebijakan repo: generated code di-commit supaya `go build` tidak butuh toolchain proto).
5. `boundary_test.go`: tambah `api` dan `gen` ke `skipDirs`; tambahkan rule eksplisit bahwa package mana pun boleh import `gen/*` (kontrak wire shared, analog `internal/ledger/events`).

### Test wajib
- `make proto && git diff --exit-code gen/` bersih (codegen deterministik).
- `make proto-lint` hijau.

### DoD
- [ ] Toolchain reproducible: fresh clone + `make tools proto` menghasilkan `gen/` identik dengan yang di-commit.

### Hasil
_Belum dikerjakan._

## T3 — `pkg/grpcx` + `pkg/ledgererr`

### Langkah
1. `pkg/grpcx`: `NewServer(logger *slog.Logger, token string, opts ...grpc.ServerOption) *grpc.Server` — interceptor chain (unary): recovery (panic → `codes.Internal` + log) → logging (method, duration, code) → token-auth (metadata `authorization: Bearer <token>`; mismatch → `codes.Unauthenticated`; token kosong = auth dimatikan, untuk test). Register `grpc_health_v1` health server. `Dial(ctx, addr, token string) (*grpc.ClientConn, error)` — client interceptor menyisipkan metadata authorization + keepalive/timeout wajar.
2. `pkg/ledgererr`: `type LedgerError struct{ Code, Message string; Retryable bool }` (+`Error()`), sentinel `ErrAlreadyClosed`, `FromStatus(err error) error` (parse `status.Status` + `errdetails.ErrorInfo` sesuai tabel mapping master di atas), konstanta reason (`"ALREADY_CLOSED"`, domain `"seev.ledger"`).
3. Unit test via bufconn memakai PingService (dari T2): token benar diterima, token salah → `Unauthenticated`, handler panic → `Internal` + server tetap hidup, `FromStatus` round-trip semua cabang mapping.
4. Kedua package DILARANG import `internal/*` (boundary rule 2) — hanya `gen/ping/v1` untuk test.

### Test wajib
- `go test ./pkg/grpcx/... ./pkg/ledgererr/... -race` hijau.

### DoD
- [ ] Plumbing gRPC siap dipakai semua service tanpa satu pun dependensi ke `internal/`.

### Hasil
_Belum dikerjakan._

## T4 — Restrukturisasi migrasi + groundwork multi-DB

### Langkah
1. Pindahkan file migrasi (git mv, SQL TIDAK diedit):
   - `migrations/000001..000018` → `migrations/ledger/` (nomor tetap 000001..000018)
   - `000019_payin` → `migrations/payin/000001`, `000022_payin_topup_intents` → `migrations/payin/000002`
   - `000020_payout` → `migrations/payout/000001`
   - `000021_auth` → `migrations/auth/000001`
   - `000023_notify` → `migrations/gateway/000001`
2. Makefile: `migrate-up SERVICE=ledger` (dst) dengan DSN `...&x-migrations-table=schema_migrations_$(SERVICE)`; `migrate-up-all` loop semua folder — **masih ke database `seev` yang sama** di fase ini (cutover per-DB terjadi di fase masing-masing service).
3. Script baru `scripts/postgres-init/02-service-dbs.sh` (jalan di first boot container): CREATE DATABASE `seev_ledger`, `seev_auth`, `seev_payin`, `seev_payout`, `seev_fraud`, `seev_gateway`; CREATE ROLE `<svc>_app LOGIN PASSWORD '<svc>_app'` per service. (GRANT `app_service` per-DB dilakukan lib.sh saat DB-nya mulai dipakai, karena role group dibuat oleh migrasi.)
4. `scripts/lib.sh`: fungsi idempoten `ensure_service_dbs` (CREATE DATABASE IF NOT EXISTS via psql loop — aman untuk volume lama); `apply_migrations` di-loop per folder (masih ke `seev`).
5. Update `.env.example` (dokumentasikan struktur baru), `README.md` repo root bila menyebut path migrasi.
6. Wajib satu kali `docker compose down -v && docker compose up -d` untuk memicu init script baru.

### Test wajib
- Fresh volume: `docker compose down -v && up -d postgres`, `make migrate-up-all` bersih; keenam database ada (`\l`).
- `./scripts/smoke-test.sh` dan `./scripts/business-e2e.sh` hijau (monolith masih jalan normal di DB `seev`).

### DoD
- [ ] Migrasi tertata per service dengan tabel version terpisah; enam database + enam login role ter-provision di fresh boot; perilaku monolith tidak berubah.

### Hasil
_Belum dikerjakan._

---

## Verifikasi akhir dokumen

Gate standar master (lihat atas): `make lint && make test`, `go vet ./...` + `-tags=integration`, `./scripts/smoke-test.sh`, `./scripts/business-e2e.sh` — semuanya hijau, lalu update README index → lanjut [27-phase6b-ledger-service.md](27-phase6b-ledger-service.md).
