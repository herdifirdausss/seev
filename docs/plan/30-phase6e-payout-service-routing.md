# 30 — Phase 6e: Ekstraksi payout-service + routing payout DB-driven

> Baca master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md). Prasyarat: doc 29 selesai. Fase ini CERMIN doc 29 — ikuti pola yang sama; hanya perbedaan yang ditulis rinci di sini.

## Konteks

`internal/payout` pindah ke binary + DB sendiri (INTERNAL), dan pemilihan vendor payout menjadi DB-driven. Perbedaan penting dari payin: (a) payout punya **resume/polling job** (state machine `created→held→submitted→vendor_pending→settled|failed|cancelled`) yang butuh Redis DB 0 untuk distributed lock — job ini IKUT payout-service; (b) field `vendor` dihapus dari request client — routing memutuskan SAAT CREATE dan hasilnya disimpan di row `payout_requests.vendor` (step-step berikutnya sudah membaca vendor dari row, tidak berubah); (c) ini jalur uang keluar — chaos test crash-mid-flight WAJIB diulang lintas service.

## T1 — `payout.proto` + gRPC server
`PayoutService{CreatePayout(user_id, amount, destination bytes-JSON, created_by), GetPayout(id, user_id)}` (ownership ditegakkan server-side, non-owner = NotFound). `internal/payout/grpcserver` + `RegisterGRPC`. Bufconn test: create sukses, saldo kurang (business error existing → FailedPrecondition), get owner/non-owner.

- **DoD**: [ ] kontrak payout user-facing tereproduksi via gRPC.
- **Hasil**: _Belum dikerjakan._

## T2 — Routing DB-driven payout
Migrasi `migrations/payout/000002_routing.up/down.sql`: `payout_vendor_gateways` + `payout_routing_rules` — DDL identik doc 29 T2 dengan prefix `payout_` dan `flow` CHECK `('payout')`; seed mockvendor + fallback rule. Query resolusi + CRUD sama. `Create` (orchestrate.go): hapus argumen `vendor` dari signature dan cek registry/mapping di awal — ganti dengan `ResolvePayoutRoute(ctx, userID, currency, amount)`; vendor hasil resolusi divalidasi ada di registry lalu DISIMPAN di row (perilaku hilir: `submit`/`poll` membaca `req.Vendor` dari row — TIDAK berubah). Lookup gateway dari `payout_vendor_gateways`. Admin CRUD `/admin/payout/routing-rules` + `/vendor-gateways`. Tanpa match = create ditolak (business error `NO_ROUTE`).

Test wajib: unit matriks resolusi (sama doc 29) + integration create-terhadap-rule-nyata.
- **DoD**: [ ] vendor payout sepenuhnya dari DB.
- **Hasil**: _Belum dikerjakan._

## T3 — `cmd/payout-service/main.go`
DB `seev_payout` (role `payout_app`), **Redis DB 0** (lock resume job), ledgerclient, registry vendorgw dari env, gRPC `:9093`, admin `:8093` (AdminRouter payout existing + routing CRUD). `StartWorkers` (resume job) di sini. Flag `-healthcheck`.

- **DoD**: [ ] payout-service hidup sendiri termasuk resume job.
- **Hasil**: _Belum dikerjakan._

## T4 — Rewire gateway
Handler `/payout` (create/get) → gRPC; JSON envelope byte-identik existing; request body TIDAK lagi menerima field `vendor` (kalau dikirim → 400 unknown field, konsisten `response.Decode` DisallowUnknownFields). Drop konstruksi `payout.NewModule` + `StartWorkers` dari `cmd/server`.

- **DoD**: [ ] gateway bersih dari modul payout.
- **Hasil**: _Belum dikerjakan._

## T5 — Cutover DB + scripts + compose + boundary
`migrations/payout` → `seev_payout`; `payout_app`; `down -v`; lib.sh + payout-service (19093/18093); compose entry; boundary map `payout-service: {payout}` (catatan: `vendorgw` kini dipakai payin-service DAN payout-service — jadikan `internal/vendorgw` shared-library yang boleh diimport keduanya, tegaskan di rule). business-e2e: setup routing rule payout via admin API → withdraw ter-route → settle dengan fee → cancel refund penuh.

- **DoD**: [ ] data payout hidup di `seev_payout`; e2e payout jalan multi-service.
- **Hasil**: _Belum dikerjakan._

## T6 — Chaos crash-mid-flight lintas service (WAJIB)

Adaptasi skenario 5 `scripts/chaos-test.sh`: **kill -9 ledger-service** di antara hold (withdraw_initiate) dan settle → payout-service resume job re-drive setelah ledger-service restart → semua request mencapai terminal state, `fn_verify_ledger_balance` 0 baris, TIDAK ada double-settle — jalur `ErrAlreadyClosed` (guard K3) kini teruji LEWAT gRPC, bukan in-proc. Tambah juga: kill payout-service di keempat kill-point existing (created/held/submitted/vendor_pending) → resume menyembuhkan.

- **DoD**: [ ] crash-safety uang keluar terbukti di topologi multi-service.
- **Hasil**: _Belum dikerjakan._

---

## Verifikasi akhir dokumen
Gate standar master doc 26 + T6 chaos hijau. Update README index → lanjut [31-phase6f-fraud-service.md](31-phase6f-fraud-service.md).
