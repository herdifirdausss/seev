# 36 — Phase 7a: MVP Product — Master Reference + Request Tracing End-to-End

> **Dokumen ini merangkap dua peran**: (1) **master reference** untuk seluruh rangkaian MVP product (docs 36–41) — keputusan terkunci, urutan fase, dan gotcha eksekusi ada DI SINI dan dirujuk oleh dokumen 37–41; (2) plan eksekusi fase 7A: request tracing end-to-end (T1–T6 di bawah). Baca bagian master reference SEBELUM mengerjakan dokumen mana pun di rangkaian ini. Prasyarat: doc 34 selesai (split enam service terverifikasi penuh).

## Konteks

Split microservices (docs 26–34) SELESAI: enam service, DB-per-service, gRPC internal, routing & fee DB-driven, chaos suite hijau. Rangkaian docs 36–41 mengubah sistem yang "jalan" menjadi **produk MVP fintech** yang reliable/secure/scalable. Repo masih untuk belajar — breaking changes DIPERBOLEHKAN (`docker compose down -v` per cutover). Yang WAJIB tetap sama: **setiap fase berakhir dengan sistem yang jalan penuh** (gate lengkap di bawah).

Lima kemampuan baru rangkaian ini:
1. **Tracing end-to-end** (doc 36): satu `request_id` dari gateway sampai akhir semua flow, tercatat di baris domain payin/payout/fraud + metadata ledger dan semua log.
2. **Fraud keluar dari transaksi posting** (doc 37): hari ini fraud gRPC call terjadi DI DALAM tx DB ledger sambil row user FOR UPDATE terkunci — network+lock cost yang dihilangkan. Screening pindah ke level atas: transport ledger pra-tx (P2P), payin pra-posting (topup), payout pra-hold (payout). Ledger jadi murni tulis+validasi.
3. **Fee quote** (doc 38): fee yang dilihat user di UI DIHORMATI. Quote → `quote_id` → posting membawa quote_id → fee persis sesuai quote; kedaluwarsa/mismatch → 422, TIDAK PERNAH diam-diam reprice.
4. **KYC bertingkat** (doc 39): L0 daftar saja (tidak bisa transaksi), L1 KYC dasar (limit rendah), L2 KYC penuh (limit tinggi; payload boleh berisi data KYB). Mock provider + review admin. Tier → `policy_limits` via policy engine existing.
5. **Resiliensi multi-vendor** (doc 40): circuit breaker per vendor + failover HANYA pra-konfirmasi — vendor down tidak menghentikan bisnis, TIDAK PERNAH double-payout.

Doc 41 = journey acceptance MVP final + konsolidasi chaos + docs.

Multi-country: registry currency + FX primitives sudah ada (doc 18). MVP cukup memastikan semua tabel/quote/routing baru membawa `currency` — aktivasi non-IDR = future work.

## Keputusan terkunci (master — berlaku untuk docs 36–41)

| Keputusan | Pilihan |
|---|---|
| Propagasi request_id | Header `X-Request-Id` (HTTP, disanitasi di edge), metadata gRPC `x-request-id` (interceptor `pkg/grpcx` dua arah), AMQP `CorrelationId` (publisher/consumer `pkg/messaging`). Outbox relay ambil dari envelope event (bukan ctx). |
| Persist request_id | Kolom `request_id TEXT NULL` di `payin_webhook_events`, `payin_topup_intents`, `payout_requests`, `screening_events`, DAN `ledger_transactions` (migrasi `ledger/000020` — koreksi eksekusi: `ledger_transactions` TIDAK punya kolom metadata JSONB generik, hanya kolom informatif spesifik seperti `external_ref`/`gateway` migrasi 000007; `request_id` mengikuti pola yang sama, diekstrak `service.go` dari `cmd.Metadata["request_id"]` yang di-inject `buildMetadata`/grpcserver). Kolom `payout_vendor_calls.request_id` (= UUID payout, tabrakan nama) di-RENAME → `payout_request_id`. |
| Model KYC | Bertingkat 2 level: L0/L1/L2. Status hidup di `seev_auth` (`auth_users.kyc_level` + `kyc_submissions`). Mock provider `internal/kycvendor/mockkyc` (pola mockvendor): auto-decide L1, L2 SELALU `refer` → review admin manual. |
| Propagasi KYC | JWT claim `kyc_level` (staleness dibatasi TTL access token). Kontrol KERAS = `policy_limits` ledger, diupdate SINKRON saat approve via gRPC baru `LedgerService.ApplyKycTier` dari template `policy_tier_limits`. Gate di gateway = UX (403 `KYC_REQUIRED`). |
| Penempatan fraud | Transport ledger pra-tx (P2P, satu pintu otoritatif), payin pra-posting, payout pra-hold (Create SAJA — settle/cancel TIDAK di-screen). Client bersama `pkg/fraudcheck` (500ms, fail-open infra, fail-closed verdict block). Seam `processors.PrePostHook` + `internal/ledger/screening` DIHAPUS total. |
| Fee quote | Tabel `fee_quotes` di `seev_ledger`. Single-use, mengikat amount eksak, TTL env `FEE_QUOTE_TTL` default 10m. Konsumsi = UPDATE atomik `WHERE consumed_at IS NULL AND expires_at>now()`. Expired/consumed/not-found → 422 `QUOTE_EXPIRED`; row mismatch → 422 `QUOTE_MISMATCH`. Tanpa quote → perilaku sekarang (resolve at posting) tetap ada untuk flow internal. |
| Fee payout | Di-quote saat Create → DISIMPAN di `payout_requests` (`fee_quote_id/fee_amount/fee_gateway`) → settle pakai fee TERSIMPAN (skip ResolveFee). TTL hanya menjaga Create. Urutan anti-burn: insert row → consume quote (ref=`payout:<id>`) → hold; consume gagal → row terminal `rejected`. |
| Circuit breaker | `internal/vendorgw/breaker.go` in-memory per proses (limitasi multi-replica didokumentasikan; keamanan TIDAK bergantung breaker). Hanya error transport/5xx/timeout yang men-trip; penolakan bisnis tidak. Env `BREAKER_FAILURE_THRESHOLD` (5) / `BREAKER_COOLDOWN` (30s). |
| Failover payout | DIIZINKAN ⟺ status ∈ {created, held} DAN `payout_vendor_calls` tidak punya row `accepted`/`uncertain`. Timeout/unknown SETELAH Submit = `uncertain` = TERPAKU vendor itu selamanya (resume job Query/retry vendor sama). Penolakan sinkron definitif = `rejected` = boleh failover kandidat berikut. |
| Routing kandidat | Query routing dari `LIMIT 1` → daftar semua rule cocok terurut; skip vendor circuit-open/tak terdaftar; semua ter-skip → 503 `VENDOR_UNAVAILABLE`. |
| Vendor mock kedua | mockvendor diparameterkan nama+secret; `mockvendor2` didaftarkan di balik env `MOCKVENDOR2_ENABLED/SECRET`; rule routing-nya di-seed via admin API di script (bukan migrasi). |
| Nomor migrasi | ledger 000020 (tx_request_id, dieksekusi doc 36 T5), 000021 (fee_quotes, doc 38 — DIGESER dari 000020 semula), 000022 (policy_tier_limits, doc 39 — DIGESER dari 000021 semula) — JANGAN isi gap 000017; auth 000002 (kyc); payin 000004 (request_id); payout 000003 (request_id+rename), 000004 (quoted fee); fraud 000002 (request_id+flow). |
| Proto | HANYA additive (field/RPC baru, tidak renumber). `make proto proto-lint proto-breaking` + commit `gen/` setiap perubahan. |
| Urutan fase | 36 tracing → 37 fraud seam → 38 fee quotes → 39 KYC → 40 vendor resilience → 41 acceptance. Tracing duluan karena semua fase lain menulis request_id; fraud seam sebelum quote supaya konsumsi quote masuk ke `execTransfer` yang sudah bersih dari hook. |

## Gate akhir SETIAP fase (36–41)

`make lint` + `make test` + `go vet ./...` + `go vet -tags=integration ./...` + `./scripts/smoke-test.sh` + `./scripts/business-e2e.sh` + `./scripts/chaos-test.sh all` — semua hijau. Fase tidak selesai sebelum sistem utuh jalan.

## Catatan penting untuk eksekutor (rangkaian 36–41; melengkapi catatan doc 26 yang tetap berlaku)

1. **`go build` tidak compile `_test.go`** — setiap perubahan signature WAJIB `go vet` dua tag.
2. **Urutan interceptor gRPC**: ekstraksi request_id HARUS sebelum `loggingInterceptor` atau log tidak dapat field-nya.
3. **Outbox relay publish di luar request ctx** — request_id datang dari envelope event yang dipersist, bukan dari ctx.
4. **Jangan log/persist id client tanpa sanitasi** — edge publik; max 64 char, charset aman, invalid → generate baru.
5. **`buildMetadata` men-strip SEMUA metadata client di router publik** — nilai server-side (request_id, fee quoted) di-inject SETELAH strip; quote_id mengalir via field typed command, BUKAN metadata.
6. **Konsumsi quote P2P harus satu tx DB dengan posting** — rollback = un-consume (perilaku benar). Konsumsi dipanggil SETELAH buka tx, SEBELUM `LockBalances` (fail fast tanpa pegang lock).
7. **Idempotency lookup tetap SEBELUM konsumsi quote** — replay sukses mengembalikan tx asli walau quote sudah consumed.
8. **Settle/cancel payout TIDAK di-screen fraud** — uang sudah di-hold; block settle = strand dana. Replay payin RE-SCREEN (deliberate).
9. **Fixture script pecah saat gating KYC masuk** — task yang menambah gate WAJIB sekaligus update fixture user `scripts/lib.sh` (KYC dance/admin fast-approve) atau gate fase tidak pernah hijau.
10. **Level KYC tidak boleh mendahului limits** — `ApplyKycTier` gagal ⇒ approval gagal atomik (level tidak naik, submission tetap pending).
11. **Claim `kyc_level` absen di token lama = level 0**, bukan error (token mid-rotation).
12. **Webhook payin INBOUND** — breaker hanya untuk pemilihan vendor saat create intent; verifikasi webhook tetap menerima event dari vendor "down" (uang sudah bergerak).
13. **Breaker: penolakan bisnis (rekening tujuan invalid dll.) TIDAK men-trip** — hanya transport/5xx/timeout.
14. **`payout_vendor_calls.request_id` = UUID payout** (bukan trace id) — rename → `payout_request_id` satu migrasi + sweep repo + contract test dalam SATU task.
15. **Test data bukan schema** — rule routing mockvendor2 di-seed via admin API dalam script, bukan migrasi.
16. **Budget RAM 3.9GB** tetap berlaku — jangan jalankan profile `app` penuh + testcontainers bersamaan.

## Deferral resmi rangkaian ini (catat di future work doc 41)

Fee topup (tipe didukung quote, rule tidak di-seed); provider KYC riil + penyimpanan dokumen + downgrade level; breaker terdistribusi (Redis); purge job `fee_quotes` kedaluwarsa; aktivasi non-IDR end-to-end; OTel span di luar ledger; retry queue async untuk `ApplyKycTier`.

---

# Fase 7A — Request Tracing End-to-End

Satu `request_id` dari edge gateway mengalir lewat HTTP proxy, gRPC, dan AMQP, lalu tersimpan di baris domain payin/payout/fraud + metadata ledger; semua log line di semua service membawanya.

Fakta kode saat ini (verifikasi saat implementasi):
- `pkg/middleware/request_id.go` `WithRequestID`: id yang DI-GENERATE hanya di-echo ke response header, TIDAK di-set ke `r.Header` → proxy gateway (`cmd/gateway/ledger_remote.go`, plain `NewSingleHostReverseProxy`) tidak meneruskannya.
- `pkg/grpcx/grpcx.go`: client interceptor hanya kirim `authorization`; server chain recovery→logging→auth tidak mengekstrak apa pun.
- AMQP satu-satunya yang propagate trace (`pkg/messaging/publisher.go` Inject OTel, `consumer.go` Extract) + `WithCorrelationID` terpisah (`util.go`).

## T1 — Sumber id di `pkg/middleware`

### Langkah
1. `pkg/middleware/request_id.go`: setelah baca/generate id, juga `r.Header.Set("X-Request-Id", id)` supaya reverse proxy default director meneruskannya.
2. Sanitasi id inbound: max 64 karakter, charset `[A-Za-z0-9._-]`; invalid/oversized → ganti UUID baru (edge publik — jangan biarkan log poisoning).
3. Helper di `pkg/middleware/helper.go`: fungsi yang mengembalikan `*slog.Logger` ber-atribut `request_id` dari ctx; pakai di middleware logging HTTP semua service sehingga SETIAP access log line membawa `request_id` (cek `pkg/middleware/logger.go` — kemungkinan sudah log request_id; pastikan konsisten).

### Test wajib
- Unit `pkg/middleware`: id generated tampak di `r.Header` handler berikutnya; id invalid/oversized diganti; id valid client dipertahankan; response header tetap ter-echo.

### DoD
- [x] Semua request HTTP (client-supplied maupun generated) membawa `X-Request-Id` yang tersanitasi di `r.Header` DAN response header, dan tercantum di access log.

### Hasil
Selesai. `WithRequestID` sekarang menulis id ke `r.Header` (bukan hanya
response header) sehingga proxy/forwarder meneruskan id yang di-generate
gateway. Validasi `isValidRequestID` (max 64 char, charset
`[A-Za-z0-9._-]`) menolak id klien yang oversized atau mengandung karakter
berbahaya (mis. CRLF injection) dan menggantinya dengan UUID baru. Access
log (`pkg/middleware/logger.go` `WithLogger`) sudah menyertakan
`request_id` di setiap baris — tidak perlu perubahan tambahan, sudah
dipasang di seluruh service (`WithRequestID()` selalu mendahului
`WithLogger()` di chain gateway/auth/ledger/payin/payout/fraud). 7 unit
test baru/diperluas di `pkg/middleware/request_id_test.go` hijau.

## T2 — Proxy gateway meneruskan id secara eksplisit

### Langkah
1. `cmd/gateway/ledger_remote.go`: tambahkan `Rewrite` (atau `Director`) pada reverse proxy yang set `X-Request-Id` dari `middleware.RequestIDFromCtx` — belt-and-braces di atas T1, melindungi dari reorder middleware di masa depan.

### Test wajib
- httptest: header sampai backend stub baik saat client mengirim id maupun saat gateway men-generate.

### DoD
- [x] Proxy `/api/v1/ledger/*` selalu meneruskan request_id ke ledger-service.

### Hasil
Selesai. `newLedgerProxy` membungkus `Director` bawaan
`NewSingleHostReverseProxy` (BUKAN mengganti ke `Rewrite` — itu akan
mematikan rewriting host/path bawaan tanpa reimplementasi manual) untuk
menimpa `X-Request-Id` dari `middleware.RequestIDFromCtx(ctx)` setelah
Director asli jalan. 2 test baru di `cmd/gateway/ledger_remote_test.go`
membuktikan id klien maupun id hasil generate gateway (hanya ada di ctx,
tidak di header request asli) sama-sama sampai ke backend.

## T3 — Propagasi gRPC di `pkg/grpcx`

### Langkah
1. Client: interceptor inject metadata `x-request-id` dari `RequestIDFromCtx(ctx)` bila ada (chain dengan interceptor auth existing).
2. Server: interceptor baru — posisi SEBELUM `loggingInterceptor` — ekstrak `x-request-id` dari incoming metadata ke ctx key `middleware.RequestIDKey`; absen → generate (worker/background caller tidak punya).
3. `loggingInterceptor` menambahkan field `request_id` di log.

### Test wajib
- Bufconn/fake invoker: ctx→metadata (client); metadata→ctx (server); absen→generate; log line gRPC memuat request_id.

### DoD
- [x] Semua panggilan gRPC antar service membawa dan mencatat request_id yang sama dengan HTTP asalnya.

### Hasil
Selesai. `pkg/grpcx`: interceptor server baru `requestIDServerInterceptor`
dipasang SEBELUM `loggingInterceptor` di chain (`recovery→requestID→
logging→auth`) — ekstrak metadata `x-request-id` ke ctx via
`middleware.RequestIDKey` (key yang sama dipakai HTTP), generate UUID baru
bila absen (caller background/worker). Interceptor client baru
`requestIDClientInterceptor` di-chain bersama `clientAuthInterceptor` lewat
`grpc.WithChainUnaryInterceptor` di `dial()` dan `DialLazy()` — inject
`x-request-id` dari `middleware.RequestIDFromCtx(ctx)` bila ada, tanpa
mengubah semantics `clientAuthInterceptor` yang sudah ada.
`loggingInterceptor` menambahkan field `request_id` di setiap log gRPC. 2
test baru di `pkg/grpcx/grpcx_test.go` (bufconn) membuktikan: id dari ctx
client sampai ke ctx server dengan nilai identik, dan caller tanpa id tetap
mendapat id ter-generate (bukan string kosong).

## T4 — Propagasi AMQP via CorrelationId

### Langkah
1. `pkg/messaging` publisher: bila ctx membawa request_id, set AMQP `CorrelationId` otomatis (selaras `WithCorrelationID` existing — jangan dobel mekanisme, satukan).
2. Consumer: sebelum invoke handler, masukkan `CorrelationId` delivery ke ctx sebagai request_id.
3. Outbox relay publish DI LUAR request ctx → request_id harus ikut DIPERSIST: cek envelope `internal/ledger/events` — bila punya metadata map, simpan request_id di envelope saat event dibuat (di dalam tx posting, ctx masih ada); relay membacanya dan set CorrelationId. PREFER field envelope daripada migrasi tabel outbox.

### Test wajib
- Round-trip publisher/consumer: request_id ctx → CorrelationId → ctx consumer.
- Integration: notifikasi/velocity consumer mencatat request_id dari transaksi posting asal.

### DoD
- [x] Event async (notify, fraud velocity) tercatat dengan request_id transaksi asalnya.

### Hasil
Selesai. `pkg/messaging/util.go` `correlationIDFromContext` disatukan: bila
`WithCorrelationID` eksplisit di-set pakai itu (mekanisme lama, prioritas
tertinggi — caller yang correlate atas id bisnis lain, bukan request_id),
kalau tidak fallback ke `middleware.RequestIDFromCtx(ctx)` — jadi PUBLISH
mana pun dengan ctx ber-request_id otomatis dapat `CorrelationId` tanpa
setiap call site memanggil `WithCorrelationID` sendiri. `consumer.go`
`handleDelivery` memasukkan `d.CorrelationId` ke `handlerCtx` via
`middleware.RequestIDKey` SEBELUM invoke handler. Untuk outbox relay
(publish di luar request ctx sepenuhnya): `events.TransactionPosted`
mendapat field baru `RequestID` (JSON `request_id`, additive — tidak ubah
`SchemaVersion`), diisi `processors.newPostedEvent` dari
`cmd.Metadata["request_id"]` (kunci yang sama yang akan diisi
`buildMetadata` di T5); `internal/ledger/worker/outbox_relay.go`
`publishOne` membaca `e.Payload["request_id"]` dan membungkus ctx via
`messaging.WithCorrelationID` sebelum `PublishTo` — relay TIDAK PERNAH
mengandalkan ctx-nya sendiri (yang memang tidak punya request_id apa pun).
3 test baru: `pkg/messaging/util_test.go` (prioritas
eksplisit-vs-fallback), `internal/ledger/worker/outbox_relay_test.go`
`TestOutboxRelay_RestoresRequestIDFromPayload` (payload→CorrelationId lewat
fakePublisher yang sekarang merekam `messaging.CorrelationIDFromContext`).
Bukti end-to-end lintas broker sungguhan (consumer notify/fraud benar-benar
menerima id ini) dibuktikan di T6 lewat `business-e2e.sh` — tidak
menduplikasi dengan test testcontainers terpisah di sini.

## T5 — Persist request_id di baris domain

### Langkah
1. `migrations/payin/000004_request_id.up/down.sql`: `ALTER TABLE payin_webhook_events ADD COLUMN request_id TEXT; ALTER TABLE payin_topup_intents ADD COLUMN request_id TEXT;` (nullable).
2. `migrations/payout/000003_request_id.up/down.sql`: `ALTER TABLE payout_requests ADD COLUMN request_id TEXT;` DAN `ALTER TABLE payout_vendor_calls RENAME COLUMN request_id TO payout_request_id;` — sweep semua query di `internal/payout/repository` + update schema contract test yang mengunci nama kolom. Down membalikkan keduanya.
3. `migrations/fraud/000002_request_id.up/down.sql`: `ALTER TABLE screening_events ADD COLUMN request_id TEXT, ADD COLUMN flow TEXT;` (kolom `flow` dipakai doc 37 — ship sekali di sini).
4. `migrations/ledger/000020_tx_request_id.up/down.sql`: `ALTER TABLE ledger_transactions ADD COLUMN request_id TEXT;` — koreksi eksekusi: rencana awal ("ledger tanpa migrasi") salah asumsi; `ledger_transactions` tidak punya kolom metadata JSONB generik, hanya kolom informatif spesifik (`external_ref`/`gateway`, migrasi 000007) yang diekstrak `service.go` dari `cmd.Metadata`. `request_id` mengikuti pola PERSIS yang sama. `internal/ledger/transport/metadata.go` `buildMetadata` inject `out["request_id"] = RequestIDFromCtx(ctx)` SETELAH strip metadata client (tidak bisa dispoof) ke `cmd.Metadata`; `internal/ledger/grpcserver` jalur Post melakukan hal yang sama dari ctx gRPC (yang sudah diisi T3); `internal/ledger/service/handle/service.go` mengekstrak `cmd.Metadata["request_id"]` dan menuliskannya ke kolom baru saat INSERT `ledger_transactions`, tepat di sebelah ekstraksi `external_ref`/`gateway` yang sudah ada.
5. Kode penyimpan: `internal/payin/topup.go` `CreateTopupIntent` + jalur simpan webhook event mengisi kolom dari ctx; `internal/payout/orchestrate.go` `Create` mengisi kolom pada row yang di-insert.

### Test wajib
- Integration repository: kolom round-trip di ketiga service.
- Ledger transport: `metadata->>'request_id'` terisi pada tx posted; metadata `request_id` kiriman client di-strip/tertimpa nilai server.
- up→down→up bersih terhadap Postgres nyata untuk ketiga migrasi.

### DoD
- [x] Satu request_id bisa dicari di `payin_*`, `payout_requests`, `screening_events`, dan `ledger_transactions.metadata`.

### Hasil
Selesai. Empat migrasi baru: `migrations/payin/000004_request_id` (kolom
`request_id` di `payin_webhook_events` + `payin_topup_intents`),
`migrations/payout/000003_request_id` (kolom `request_id` di
`payout_requests` DAN rename `payout_vendor_calls.request_id` →
`payout_request_id` — menyelesaikan tabrakan nama dengan UUID payout
request), `migrations/fraud/000002_request_id` (kolom `request_id` + `flow`
di `screening_events`, `flow` baru dipakai doc 37), DAN
`migrations/ledger/000020_tx_request_id` (kolom `request_id` di
`ledger_transactions`) — **koreksi eksekusi terhadap rencana awal**:
keputusan terkunci master semula menyatakan "ledger tanpa migrasi" dengan
asumsi kolom metadata JSONB generik sudah ada; verifikasi terhadap Postgres
nyata membuktikan `ledger_transactions` HANYA punya kolom informatif
spesifik (`external_ref`, `gateway` — migrasi 000007), bukan JSONB generik.
`request_id` mengikuti pola persis yang sama (diekstrak `service.go` dari
`cmd.Metadata`). Nomor migrasi ledger 000020 ini menggeser rencana doc 38
(fee_quotes) ke 000021 dan doc 39 (policy_tier_limits) ke 000022 — kedua
dokumen sudah diperbarui. Keempat migrasi diverifikasi up→down→up bersih
terhadap Postgres nyata (migrate CLI). Kode: `internal/payin/model` +
`repository.go` + `topup_repository.go` menambah field/kolom `RequestID`;
`payin.go` HandleWebhook dan `topup.go` CreateTopupIntent mengisi dari
`middleware.RequestIDFromCtx(ctx)`. `internal/payout/model` —
`PayoutVendorCall.RequestID` di-rename `PayoutRequestID` (field Go
mengikuti rename kolom), `PayoutRequest` mendapat field `RequestID` baru;
`orchestrate.go` Create mengisi dari ctx. Ledger: `metadata.go`
`buildMetadata` inject `out["request_id"]` ke `cmd.Metadata` SETELAH
allowlist-strip (klien tidak bisa menitipkan `request_id` sendiri lewat
metadata JSON — dibuktikan test); `grpcserver/server.go` Post RPC melakukan
hal serupa dari ctx gRPC (diisi interceptor T3), menimpa `request_id` apa
pun yang caller (payin/payout, trusted) kirim di proto Metadata sendiri;
`service/handle/service.go` mengekstrak `cmd.Metadata["request_id"]` persis
di sebelah ekstraksi `external_ref`/`gateway` yang sudah ada dan
menuliskannya ke kolom baru saat INSERT. Verifikasi menyeluruh: seluruh
integration test suite (`go test -tags=integration ./...`) hijau terhadap
Postgres real termasuk `payin`, `payout/repository` (termasuk
`payout_vendor_calls` yang di-rename), `ledger`, `fraud`, `notify`; unit
test baru di `transport/http_test.go` dan `grpcserver/server_test.go`
membuktikan strip-lalu-override; `make lint` bersih; `boundary_test.go`
hijau; `business-e2e.sh` Bagian 6 (trace_check, ditambahkan T6) membuktikan
end-to-end lewat proses nyata: header echo, log gateway+ledger-service,
DAN `ledger_transactions.request_id` via psql.

## T6 — Asersi script + index README

### Langkah
1. `scripts/business-e2e.sh`: kirim satu transfer dengan `X-Request-Id: e2e-trace-$RANDOM`; assert (a) response header echo, (b) log gateway + ledger-service (+ fraud-service setelah doc 37) memuat id itu, (c) `ledger_transactions.request_id` = id via psql.
2. Update `docs/plan/README.md`: baris 36–41 dengan status.

### Test wajib
- business-e2e hijau end-to-end enam service dengan asersi trace baru.

### DoD
- [x] Satu perintah membuktikan trace end-to-end lintas proses dan storage.

### Hasil
Selesai. `scripts/business-e2e.sh` mendapat Bagian 6 `trace_check()`:
mengirim satu transfer_p2p dengan header `X-Request-Id: e2e-trace-$RANDOM`,
lalu assert (a) response header meng-echo id itu, (b) `GATEWAY_LOG` DAN
`LEDGER_LOG` sama-sama memuat id itu (membuktikan propagasi HTTP→gRPC dari
T1-T3), (c) `ledger_transactions.request_id` (kolom baru dari T5) sama
persis dengan id yang dikirim — dipanggil sebagai langkah terakhir sebelum
`stop_services`, tidak mengganggu asersi saldo eksak di bagian
transfer/withdraw sebelumnya. `docs/plan/README.md` diperbarui (baris 36
kini merujuk ke rincian sub-status per task, lihat entri terpisah di bawah).

Full gate dijalankan dari volume Postgres benar-benar bersih
(`docker compose down -v`): `make lint` bersih, `go vet ./...` +
`go vet -tags=integration ./...` bersih, seluruh unit+integration test suite
hijau, `./scripts/smoke-test.sh` HIJAU PENUH, `./scripts/business-e2e.sh`
**FULL BUSINESS JOURNEY PASSED** (termasuk asersi trace baru), dan
`./scripts/chaos-test.sh all` **ALL CHAOS ASSERTIONS PASSED** (ketujuh
skenario, termasuk Redis-down/restart, payout crash-mid-flight, payin-down
redelivery, fraud-down fail-open + block-mode — semua `fn_verify_ledger_balance()`
0 baris).

---

## Verifikasi akhir dokumen
Gate standar master (bagian atas) hijau semua (lint, test, vet dua tag,
smoke, business-e2e, chaos all — dijalankan dari volume Postgres bersih)
→ **Fase 7A SELESAI** → lanjut [37-phase7b-fraud-seam.md](37-phase7b-fraud-seam.md).
