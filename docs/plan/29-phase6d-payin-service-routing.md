# 29 — Phase 6d: Ekstraksi payin-service + routing topup DB-driven

> Baca master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md). Prasyarat: doc 28 selesai.

## Konteks

Dua hal sekaligus di fase ini: (1) `internal/payin` pindah ke binary + DB sendiri sebagai service INTERNAL (webhook edge publik tetap di gateway, di-forward via gRPC dengan raw body); (2) **fitur baru routing DB-driven** — vendor untuk topup tidak lagi hardcoded (`payinGatewayMapping` di main.go dan field `vendor` dari client), melainkan diputuskan aturan di tabel `payin_routing_rules` (admin-configurable): match currency/amount-range/user-override, priority, enabled, fallback.

## T1 — `payin.proto` + gRPC server

### Langkah
1. `api/proto/seev/payin/v1/payin.proto`: `PayinService{HandleWebhook, CreateTopupIntent, GetTopupIntent}` sesuai master doc 26 — `HandleWebhookRequest{vendor, headers map<string,string>, raw_body bytes}` (byte mentah — signature diverifikasi atas bytes persis); `CreateTopupIntentRequest{user_id, amount}` TANPA vendor.
2. `internal/payin/grpcserver` + facade `RegisterGRPC`. Mapping hasil→status: sukses/ignored/business-failure → OK dengan `Result`; unknown vendor → `NotFound`; bad signature → `Unauthenticated`; infra → `Internal`.

### Test wajib
- Bufconn test SEMUA outcome: ok, ignored (event non-settled), unknown vendor, bad signature, business failure, infra error.

### DoD
- [ ] Kontrak webhook 200/401/404/503 existing tereproduksi penuh lewat gRPC.

### Hasil
_Belum dikerjakan._

## T2 — Routing DB-driven

### Langkah
1. Migrasi `migrations/payin/000003_routing.up/down.sql`:
```sql
CREATE TABLE payin_vendor_gateways (
    vendor  TEXT PRIMARY KEY,      -- nama vendorgw registry, mis. 'mockvendor'
    gateway TEXT NOT NULL          -- nilai metadata gateway ledger, mis. 'bca'
);
CREATE TABLE payin_routing_rules (
    id UUID PRIMARY KEY,
    flow TEXT NOT NULL DEFAULT 'topup' CHECK (flow IN ('topup')),
    priority INT NOT NULL,                  -- kecil menang
    enabled BOOLEAN NOT NULL DEFAULT true,
    currency TEXT,                          -- NULL = semua
    min_amount BIGINT, max_amount BIGINT,   -- minor unit inklusif; NULL = tanpa batas
    user_id UUID,                           -- NULL = semua user; non-NULL = override per-user
    vendor TEXT NOT NULL REFERENCES payin_vendor_gateways(vendor),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (flow, priority)
);
```
   + RLS/grants pola tabel payin lain + seed `('mockvendor','bca')` + satu fallback rule all-NULL priority 1000.
2. Repository payin: query resolusi SATU statement — `WHERE enabled AND flow=$1 AND (user_id=$2 OR user_id IS NULL) AND (currency=$3 OR currency IS NULL) AND (min_amount IS NULL OR $4>=min_amount) AND (max_amount IS NULL OR $4<=max_amount) ORDER BY (user_id IS NOT NULL) DESC, priority ASC LIMIT 1` + CRUD rules + CRUD vendor_gateways.
3. `internal/payin/routing.go`: `ResolveTopupRoute(ctx, userID, currency, amount) (vendor, gateway string, err error)`; tanpa match = business error baru `ErrNoRoute` (HTTP 422 `NO_ROUTE`).
4. `CreateTopupIntent` drop argumen `vendor` (routing yang memutuskan; field `Vendor` tetap tersimpan di intent + response). `NewModule` drop param `gatewayMapping`; lookup gateway di `HandleWebhook` kini dari `payin_vendor_gateways` (vendor terdaftar tanpa row mapping = config error, sama seperti perilaku existing terhadap mapping kosong).
5. Admin CRUD di AdminRouter payin: `GET/POST/PUT /admin/payin/routing-rules`, `GET/PUT /admin/payin/vendor-gateways/{vendor}` — validasi tulis: vendor ∈ registry, gateway ∈ `constant.ValidGateways`. CATATAN: `constant` adalah subpackage `internal/ledger` — JANGAN import; duplikasi daftar gateway valid sebagai validasi payin sendiri ATAU validasi via `ledgerclient` bila tersedia; putuskan saat implementasi dan catat di Hasil.

### Test wajib
- Unit matriks resolusi: user-override menang atas priority; currency/amount filter; disabled dilewati; fallback kena; no-match → ErrNoRoute.
- Integration (testcontainers): rule di DB nyata → CreateTopupIntent memilih vendor sesuai rule.

### DoD
- [ ] Tidak ada lagi vendor/gateway hardcoded di jalur topup; semua dari DB dan bisa diubah admin tanpa deploy.

### Hasil
_Belum dikerjakan._

## T3 — `cmd/payin-service/main.go`

### Langkah
1. Main: DB `seev_payin` (role `payin_app`), registry vendorgw dari env `VENDOR_*` (copy pola `cmd/server`), ledgerclient, gRPC `:9092` (grpcx), admin HTTP `:8092` (AdminRouter payin + routing CRUD + `/metrics` `/health`). TANPA RabbitMQ/Redis. Flag `-healthcheck`.

### Test wajib
- Boot manual: gRPC health + admin listener up.

### DoD
- [ ] payin-service hidup sendiri.

### Hasil
_Belum dikerjakan._

## T4 — Rewire gateway

### Langkah
1. `internal/handler/webhook.go`: forward `{vendor, headers, raw body}` ke `PayinService.HandleWebhook`; mapping status gRPC→HTTP (NotFound→404, Unauthenticated→401, OK/BUSINESS_FAILURE→200, lainnya→503) — kontrak vendor existing TIDAK berubah.
2. Handler `/topup` (create/get) di gateway memanggil gRPC; JSON envelope response byte-identik `topup_http.go` existing (business-e2e mengasserinya). Hapus HTTP handler user dari modul payin.
3. `Dependencies.Payin` menjadi klien gRPC; drop konstruksi `payin.NewModule` dari `cmd/server`.

### Test wajib
- Unit handler gateway dengan fake PayinService: kelima mapping status; body raw sampai byte-identik.

### DoD
- [ ] Gateway = satu-satunya pintu publik; payin tidak punya permukaan publik.

### Hasil
_Belum dikerjakan._

## T5 — Cutover DB + scripts + compose + boundary

### Langkah
1. `migrations/payin` → `seev_payin`; `ensure_app_role` `payin_app`; `docker compose down -v`.
2. lib.sh `start_services` + payin-service (19092/18092); compose entry; boundary map `payin-service: {payin, vendorgw}`.
3. `business-e2e.sh`: sebelum topup, buat routing rule via admin API payin; assert topup ter-route ke mockvendor → webhook settle → saldo masuk; skenario negatif: disable semua rule → topup 422 `NO_ROUTE`.

### Test wajib
- smoke + business-e2e hijau (4 proses).

### DoD
- [ ] Data payin hidup di `seev_payin`; routing terbukti dari DB end-to-end.

### Hasil
_Belum dikerjakan._

---

## Verifikasi akhir dokumen

Gate standar master doc 26 + chaos payin: kill payin-service → webhook balas 503 (vendor akan redeliver) → restart → redelivery menyembuhkan, saldo benar, `fn_verify_ledger_balance` 0 baris. Update README index → lanjut [30-phase6e-payout-service-routing.md](30-phase6e-payout-service-routing.md).
