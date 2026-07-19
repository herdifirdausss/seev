# 28 — Phase 6c: Ekstraksi auth-service

> Baca master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md). Prasyarat: doc 27 selesai (ledger-service jalan; `auth.Provisioner` sudah re-typed ke ledgerclient).

## Konteks

`internal/auth` sudah mandiri secara boundary (satu-satunya dependensi keluar = `Provisioner`, kini `pkg/ledgerclient`). Fase ini memindahkannya ke binary + database sendiri sebagai service PUBLIC kedua: `auth-service` di `:8082` (public: register/login/refresh/users-me) + `:8083` (internal: metrics/health). JWT yang diterbitkan tetap diverifikasi lokal oleh semua service lain lewat `JWT_SECRET` bersama — tidak ada introspeksi runtime ke auth-service.

## T1 — `cmd/auth-service/main.go`

### Langkah
1. Main baru: config → logger → DB `seev_auth` (role `auth_app`) → ledgerclient (`LEDGER_GRPC_ADDR`) → `auth.NewModule` → `EnsureBootstrapAdmin` (PINDAH ke sini dari `cmd/server`).
2. TANPA RabbitMQ (auth tidak publish/consume apa pun).
3. Listener public `:8082`: pindahkan registrasi route auth dari `internal/handler/router.go` (blok `deps.Auth != nil`: `/api/v1/auth/register|login|refresh` unauth + rate-limited, `/api/v1/users/me` GET/PUT authed) ke `cmd/auth-service/router.go` — pertahankan chain middleware persis (request-id, logger, recovery, security headers, CORS, rate limit Redis DB 0, RequireJSON, WithAuth untuk /users/me).
4. Listener internal `:8083`: `/metrics`, `/health`. Flag `-healthcheck`.

### Test wajib
- Unit router auth-service: register 201, login 200, refresh rotation, /users/me 200/401 — reuse test existing modul auth (yang HTTP-level dipindah/diadaptasi).

### DoD
- [ ] auth-service hidup sendiri; register memprovision akun ledger via gRPC (dibuktikan integration/e2e T2).

### Hasil
_Belum dikerjakan._

## T2 — Cutover DB + gateway melepas auth

### Langkah
1. Terapkan `migrations/auth` → `seev_auth` (Makefile map + lib.sh); `ensure_app_role` untuk `auth_app` di `seev_auth`; `docker compose down -v`.
2. `cmd/server` (gateway): hapus dependency `Auth` + konstruksi `auth.NewModule` + `EnsureBootstrapAdmin` + route `/api/v1/auth/*` dan `/users/me` dari `internal/handler`.
3. `scripts/lib.sh`: `start_services` menambah auth-service (18082/18083); `business-e2e.sh` panggil register/login di `:18082` (endpoint lain tetap gateway `:18080`).

### Test wajib
- `./scripts/business-e2e.sh` hijau: register di auth-service → JWT dipakai ke gateway (topup/transfer) dan tembus proxy ke ledger — membuktikan shared-secret JWT lintas tiga proses.

### DoD
- [ ] Data auth hidup di `seev_auth`; gateway tidak tahu apa-apa soal password/refresh token.

### Hasil
_Belum dikerjakan._

## T3 — Boundary + compose

### Langkah
1. `boundary_test.go` `serviceModules`: `auth-service: {auth}`; `cmd/auth-service` hanya boleh import `internal/auth` + config + pkg + gen.
2. Compose: service `auth-service` (profile `app`, port 8082/8083, healthcheck `-healthcheck`, depends_on postgres + ledger-service).

### Test wajib
- `make test` hijau (termasuk boundary).

### DoD
- [ ] Peta service di boundary test mencerminkan topologi nyata tiga-service.

### Hasil
_Belum dikerjakan._

---

## Verifikasi akhir dokumen

Gate standar master doc 26 + gate tambahan: journey e2e penuh register→login→topup→transfer membuktikan JWT terbitan auth-service diterima gateway & ledger-service. Update README index → lanjut [29-phase6d-payin-service-routing.md](29-phase6d-payin-service-routing.md).
