# 37 — Phase 7b: Fraud Keluar dari Transaksi Posting Ledger

> Baca master reference di [36-phase7a-request-tracing.md](36-phase7a-request-tracing.md). Prasyarat: doc 36 selesai.

## Konteks

Hari ini fraud gRPC call (`internal/ledger/screening/grpchook.go`, 500ms fail-open) dipanggil di `internal/ledger/service/handle/service.go` DI DALAM `WithTx` SETELAH `LockBalances` FOR UPDATE — round-trip jaringan menahan lock row user selama hingga 500ms per posting. Fase ini memindahkan screening ke level atas: **transport ledger pra-tx** (P2P — tetap satu pintu otoritatif), **payin-service pra-posting** (topup), **payout-service pra-hold** (payout). Ledger core menjadi murni tulis+validasi; seam `processors.PrePostHook` dan `internal/ledger/screening` DIHAPUS total (jangan sisakan seam kosong — seam mati membusuk). Velocity counting async (consumer `ledger.events.fraud` → Redis DB 1) TIDAK berubah. fraud-service tetap menulis `screening_events` untuk setiap Screen.

## T1 — Perkaya proto fraud (additive)

### Langkah
1. `api/proto/seev/fraud/v1/fraud.proto`: `ScreenRequest` + `string request_id = 5; string flow = 6;` (konvensi nilai flow: `p2p_transfer|topup|payout`, dokumentasikan di komentar proto). `make proto proto-lint proto-breaking`, commit `gen/`.
2. `internal/fraud/grpcserver` + `internal/fraud/fraud.go`: persist `request_id` dan `flow` ke `screening_events` (kolom sudah ada dari doc 36 T5). Rules engine mengabaikannya (threshold/velocity tidak berubah).

### Test wajib
- `make proto-breaking` hijau; grpcserver test: field baru terpersist; caller lama (tanpa field) tetap jalan (default proto3).

### DoD
- [x] Setiap screening event tercatat dengan request_id + flow asalnya.

### Hasil
Selesai. `ScreenRequest` proto mendapat field additive `request_id = 5` dan
`flow = 6` (komentar proto mendokumentasikan konvensi
`p2p_transfer|topup|payout`). `make proto` + `make proto-lint` bersih;
`make proto-breaking` tidak bisa dijalankan bermakna di environment ini
(bandingannya `.git#branch=main` yang belum pernah punya file proto ter-commit
sama sekali — repo ini seluruhnya masih uncommitted working tree pada `main`),
tapi perubahan murni penambahan dua field baru di message existing = additive
per definisi, tidak ada penghapusan/renumber field.

Kode: `model.ScreenInput`/`model.ScreeningEvent` mendapat field `RequestID`/
`Flow`; `grpcserver/server.go` Screen RPC meneruskan `request.GetRequestId()`/
`GetFlow()` ke `ScreenInput`; kedua rule (`amount_threshold`,
`velocity_anomaly`) meneruskannya ke `ScreeningEvent` saat `InsertEvent`;
`repository/screening_repository.go` INSERT/SELECT diperluas dengan kolom
`request_id`/`flow` (kolom sudah ada dari migrasi doc 36 T5, tidak perlu
migrasi baru). Test baru `TestScreenPropagatesRequestIDAndFlow` membuktikan
field baru sampai ke `ScreenInput`; test lama `TestScreenRoundTrip` (tanpa
field baru) tetap hijau — membuktikan default proto3 aman untuk caller lama.
Sanity-check manual INSERT/SELECT terhadap Postgres nyata (`seev_fraud`)
mengonfirmasi kolom benar. Seluruh test `internal/fraud/...` (unit +
integration) hijau.

## T2 — Client bersama `pkg/fraudcheck`

### Langkah
1. Paket baru `pkg/fraudcheck` (dipakai 3 service — HARUS di `pkg/`, tidak boleh `internal/<svc>` mana pun; `pkg/` tidak boleh import `internal/`): wrapper `fraudv1.FraudServiceClient` diangkat dari logika `internal/ledger/screening/grpchook.go` — timeout 500ms, fail-open pada error infra (log + metric `screening_client_errors_total{caller}`), fail-closed pada verdict `block=true`, isi `request_id` dari ctx dan `flow` dari caller.
2. Signature: `Check(ctx, flow, txType string, userID uuid.UUID, amount decimal.Decimal, currency string) (Verdict, error)` dengan `Verdict{Block bool; Reason string}`. Error return = error infra (caller memutuskan fail-open); `Block` = keputusan bisnis.

### Test wajib
- Unit dengan mock client: verdict block diteruskan; error infra → error surfaced (untuk fail-open caller); timeout 500ms dihormati; request_id + flow terinjeksi ke request.

### DoD
- [x] Satu implementasi kontrak screening (timeout/fail-open) dipakai ketiga caller, tidak ada duplikasi.

### Hasil
Selesai. `pkg/fraudcheck/fraudcheck.go` — logika diangkat dari
`internal/ledger/screening/grpchook.go` (timeout 500ms via
`context.WithTimeout`, request_id dari `middleware.RequestIDFromCtx(ctx)`).
`Client.Check(ctx, flow, txType string, userID uuid.UUID, amount
decimal.Decimal, currency string) (Verdict, error)` — kontrak: error non-nil
HANYA untuk kegagalan infra (caller memutuskan fail-open); `Verdict.Block`
tanpa error = keputusan bisnis definitif (fail-closed, caller WAJIB
menghormati). Metric `screening_client_errors_total{caller}` (namespace
`screening`) menghitung setiap error infra per caller (ledger/payin/payout)
— mengganti `screening_hook_errors_total{hook}` lama yang scope-nya
ledger-only (dihapus di T3). `Client` dikonstruksi sekali per service saat
startup (bukan per-request) dengan label `caller` tetap. 5 test unit dengan
fake `fraudv1.FraudServiceClient`: verdict block diteruskan; error infra
di-surface (bukan disembunyikan); timeout 500ms benar-benar dihormati
(diukur elapsed time, bukan cuma context deadline); request_id + flow
terinjeksi ke request; verdict allow (Block=false) tidak salah dianggap
block. `go vet` dua tag bersih, `boundary_test.go` hijau (paket ada di
`pkg/`, tidak import `internal/` mana pun, hanya `gen/fraud/v1` dan
`pkg/middleware`).

## T3 — Ledger: hapus hook dari dalam tx, screen di transport

### Langkah
1. `internal/ledger/service/handle/service.go`: hapus loop hook (step 4c), field `hooks`, dan parameter variadic konstruktor. Hapus `processors.PrePostHook` + `processors.Verdict`. Hapus seluruh `internal/ledger/screening/`. PERTAHANKAN `apperror.ErrScreeningBlocked` (kontrak error HTTP stabil).
2. `internal/ledger/ledger.go` facade + `cmd/ledger-service/main.go`: lepas wiring hook lama; ganti dengan client fraud (`pkg/fraudcheck`) yang di-inject ke transport via option router baru. `FRAUD_GRPC_ADDR` env ledger sudah ada.
3. `internal/ledger/transport/http.go`: di handler posting PUBLIC — layer yang sama dengan cek `PolicyChecker`, SEBELUM `svc.Handle` dan SEBELUM tx DB mana pun — panggil `fraudcheck.Check(ctx, "p2p_transfer", type, userID, amount, currency)`. Verdict block → error `ErrScreeningBlocked` (respons 422 sama seperti sekarang). Error infra → fail-open: log ERROR (pesan tetap kompatibel dengan asersi chaos existing bila memungkinkan) + lanjut posting.
4. Router INTERNAL (disbursement/adjustment/system) TIDAK di-screen — screening adalah kontrol flow user; dokumentasikan di komentar router.
5. Update `boundary_test.go` untuk paket yang dihapus.

### Test wajib
- Test yang membuktikan `execTransfer` tidak melakukan panggilan fraud (tanpa client fraud terkonfigurasi → tetap posting).
- Transport: block → 422 + TIDAK ada tx dibuat; fraud down → posting tetap jalan (fail-open) + ERROR ter-log.
- `go vet` dua tag (signature konstruktor berubah — banyak test perlu update).

### DoD
- [x] Tidak ada network call di dalam transaksi posting; kontrak HTTP (422 SCREENING_BLOCKED, fail-open) tidak berubah dari sudut pandang user.

### Hasil
Selesai. Dihapus total: `internal/ledger/processors/hooks.go`
(`PrePostHook`, `Verdict`), seluruh direktori `internal/ledger/screening/`,
field `hooks`/parameter variadic di `service/handle/service.go` dan
`ledger.go` facade, alias `ledger.PrePostHook`, metric
`screening_hook_errors_total` di `service/handle/metrics.go` (digantikan
`screening_client_errors_total{caller}` di `pkg/fraudcheck` dari T2).
`apperror.ErrScreeningBlocked` DIPERTAHANKAN persis seperti diminta —
komentarnya diperbarui untuk mencerminkan bahwa error ini sekarang muncul
SEBELUM transaksi apa pun dibuka (bukan lagi `status='failed'` yang
commit).

Ledger inject fraud: `NewRouterWithFraud(svc, policy, feePolicy,
fraudClient, logger)` konstruktor baru (`NewRouterWithOptions` tetap ada,
delegasi dengan `fraudClient=nil` — 100% backward compatible). `handler`
struct mendapat field `fraudClient *fraudcheck.Client` + `logger
*slog.Logger`. Screening dipanggil di `postTransaction`, layer yang SAMA
dengan cek `PolicyChecker` (setelah policy, sebelum `svc.Post`), HANYA pada
`h.allowedTypes != nil` (public router) — internal router (disbursement/
adjustment/system) tidak pernah menerima `fraudClient` sama sekali secara
struktural. Flow di-hardcode `"p2p_transfer"` untuk seluruh
`publicUserTypes` (`transfer_p2p`, `transfer_pocket`, `withdraw_initiate`,
`escrow_hold`) sesuai teks plan. Verdict block → `apperror.ErrScreeningBlocked`
→ `writeError` yang SUDAH memetakan ke 422 (tidak ada perubahan mapping);
error infra → log ERROR `"screening check error, failing open"` + lanjut
`svc.Post` (fail-open).

`cmd/ledger-service/main.go`: wiring `screening.NewGRPCHook` diganti
`fraudcheck.New(fraudv1.NewFraudServiceClient(fraudConn), "ledger")`,
diteruskan sebagai parameter terakhir `ledger.NewModule` (bukan lagi
variadic hooks) — `FRAUD_GRPC_ADDR` env tidak berubah.

Test: unit baru `TestPostTransaction_PublicRouter_FraudBlock_Rejects422NoPosting`
(block → 422 + `svc.Post` `Times(0)`), `TestPostTransaction_PublicRouter_FraudInfraError_FailsOpen`
(error infra → tetap 201 + `svc.Post` terpanggil), `TestPostTransaction_InternalRouter_NotScreened`
(router internal tidak pernah screening). Integration
`TestSchemaContract_ExecTransfer_PostsWithoutAnyFraudClientConfigured`
menggantikan test hook lama (`spyHook`/`newServiceWithHooks` dihapus) —
membuktikan `execTransfer` posting normal tanpa APA PUN fraud client
dikonfigurasi (tidak ada lagi seam untuk dikonfigurasi sama sekali). Semua
unit test (`go test ./...`) dan integration test (`go test -tags=integration
./internal/ledger/...`, real Postgres) hijau; `go vet` dua tag bersih;
`boundary_test.go` hijau tanpa perubahan (walk direktori otomatis);
`gofmt -l` bersih.

## T4 — Payin: screen pra-posting

### Langkah
1. `internal/payin/payin.go` (jalur `postAndFinalize`): SEBELUM `poster.Post`, panggil `fraudcheck.Check(ctx, "topup", "money_in", userID, amount, currency)`.
2. Block → status event baru `blocked` + reason di `payin_webhook_events` (perluas CHECK constraint status bila ada), webhook DIBALAS 200 (uang sudah diterima vendor; keputusan bisnis non-retriable — vendor tidak boleh redeliver selamanya); admin replay = jalur pemulihan, dan replay RE-SCREEN (deliberate).
3. Error infra → fail-open (jangan strand deposit riil; velocity event tetap mengalir untuk deteksi post-hoc).
4. `cmd/payin-service/main.go`: dial fraud via `grpcx.DialLazy(FRAUD_GRPC_ADDR, INTERNAL_GRPC_TOKEN)`; compose: env `FRAUD_GRPC_ADDR: fraud-service:9094` + `depends_on` payin-service; `scripts/lib.sh` `start_payin_service` export env yang sama (port test 19094).

### Test wajib
- Unit: block → event `blocked`, `poster.Post` TIDAK terpanggil, webhook 200; fraud error → tetap posting; replay event `blocked` melakukan screening ulang.

### DoD
- [x] Topup di-screen sebelum menyentuh ledger; deposit tidak pernah hilang karena fraud-service down.

### Hasil
- `internal/payin/repository/repository.go`: `Repository` interface + `repo`
  impl mendapat `MarkBlocked(ctx, id, reason)` — `UPDATE ... SET status =
  'blocked', error_message = $1` — terpisah dari `MarkFailed` agar operator
  bisa membedakan "fraud menolak deposit ini" dari "posting ke ledger sendiri
  yang gagal" sekilas pandang saat melihat `payin_webhook_events`. Mock
  diregenerasi via `mockgen -source=repository.go
  -destination=repository_mock.go -package=repository`
  ([repository_mock.go](../../internal/payin/repository/repository_mock.go)).
- `migrations/payin/000005_webhook_blocked_status.{up,down}.sql`: DROP+ADD
  CHECK constraint `payin_webhook_events_status_check` menambahkan
  `'blocked'` di samping `'received','posted','failed'` (Postgres tidak bisa
  `ALTER CHECK` in place). Diverifikasi up→down→up terhadap
  `seev-postgres-1` yang sedang berjalan — constraint kembali persis ke 3
  nilai lama setelah down, dan ke 4 nilai setelah up lagi.
- `internal/payin/payin.go`: `Module` mendapat field `fraudClient
  *fraudcheck.Client` (nil = valid, tidak ada screening); `NewModule`
  mendapat parameter terakhir baru `fraudClient *fraudcheck.Client`. Di
  dalam `postAndFinalize`, SEBELUM `poster.Post` dipanggil
  `fraudClient.Check(ctx, "topup", "money_in", ev.UserID, ev.Amount,
  ev.Currency)` (currency diambil langsung dari `WebhookEvent`, sudah
  tersimpan di baris webhook — tidak perlu panggilan tambahan). Karena
  `postAndFinalize` adalah satu-satunya jalur yang dipakai baik oleh
  `HandleWebhook` (delivery baru) maupun `ReplayEvent` (admin retry),
  requirement "replay RE-SCREEN" terpenuhi tanpa kode khusus apa pun.
  - Verdict `Block` (fail-closed): `MarkBlocked` dipanggil dengan
    `verdict.Reason`, lalu dikembalikan `&businessError{...}` — tipe yang
    sama dipakai jalur business-failure lain, sehingga webhook receiver
    tetap ACK 200 (uang sudah diterima vendor; redelivery tidak akan pernah
    menyembuhkan keputusan bisnis ini) tanpa perlu perubahan apa pun di
    lapisan HTTP.
  - Error infra (fail-open): dicatat `logger.Error("payin: screening check
    error, failing open", ...)`, lalu proses lanjut ke `poster.Post` seperti
    biasa — deposit riil tidak pernah tersendat karena fraud-service down.
- `cmd/payin-service/main.go`: menambahkan dial fraud
  (`grpcx.DialLazy(cfg.FraudGRPCAddr, cfg.InternalGRPCToken)`) persis pola
  yang sudah dipakai `cmd/ledger-service/main.go` di T3 —
  `FRAUD_GRPC_ADDR` kosong (default) ⇒ `fraudClient` tetap `nil` ⇒ tidak ada
  screening, 100% backward compatible untuk siapa pun yang belum
  mengonfigurasi fraud-service.
- `docker-compose.yml`: menambahkan env `FRAUD_GRPC_ADDR: fraud-service:9094`
  ke `payin-service`. **Deviasi dari Langkah #4 di atas**: instruksi Langkah
  menyebut juga menambah `depends_on: fraud-service`, tapi diperiksa ulang
  bahwa `ledger-service` (T3) — komponen yang jadi pola rujukan langsung —
  justru TIDAK diberi `depends_on: fraud-service` sama sekali; dial fraud di
  sana memakai `grpcx.DialLazy` yang non-blocking by design (persis untuk
  mendukung fail-open — fraud-service boleh belum siap saat caller start).
  Menambah `depends_on` keras akan bertentangan dengan tujuan
  availability-over-strictness itu sendiri (fraud down seharusnya tidak
  mencegah payin-service start sama sekali). `payin-service` mengikuti pola
  T3 yang sudah berjalan (env saja, tanpa `depends_on`) demi konsistensi.
- `scripts/lib.sh`: `start_payin_service` menambahkan
  `export FRAUD_GRPC_ADDR=localhost:$FRAUD_GRPC_PORT` (port test `19094`,
  variabel sudah ada dari T3), persis pola `start_ledger_service`.
- Test wajib — unit (`internal/payin/payin_test.go`, semua via mock
  `Repository` + `fakeFraudGRPCClient` yang dibungkus `fraudcheck.Client`
  asli, meniru pola yang sudah dipakai
  `internal/ledger/transport/http_test.go` di T3):
  - `TestHandleWebhook_FraudBlock_MarkedBlocked_PostNotCalled_Acked200` —
    verdict Block → `MarkBlocked` dipanggil dengan reason yang benar,
    `poster.Post` NOL kali, error diklasifikasi `IsBusinessFailure` (⇒ 200).
  - `TestHandleWebhook_FraudInfraError_FailsOpen_StillPosts` — error infra
    dari fraud client → posting tetap jalan (`poster.Post` 1 kali), tidak
    ada error yang mem-block webhook.
  - `TestReplayEvent_BlockedEvent_ReScreens` — event berstatus `blocked`
    di-replay dengan verdict fraud yang sekarang `Allow` → posting berhasil,
    membuktikan replay memanggil ulang fraud check (bukan sekadar mengulang
    keputusan lama).
- Regresi: seluruh 8 unit test `internal/payin` lama (happy path, duplicate,
  infra error, business failure, replay) tetap hijau tanpa perubahan
  perilaku — `fraudClient` nil di semua test lama berarti tidak ada
  screening yang berjalan, persis seperti sebelum T4.
- Verifikasi dijalankan: `go build ./...` bersih; `go vet ./...` dan
  `go vet -tags=integration ./...` bersih (2 call site
  `payin.NewModule` di `internal/payin/payin_integration_test.go` dan 1 di
  `internal/handler/webhook_integration_test.go` diberi argumen `nil`
  tambahan); `gofmt -l .` tidak menunjukkan file baru yang belum diformat;
  `make lint` bersih; `make test` (termasuk `boundary_test.go` — nol
  perubahan diperlukan) hijau semua; integration test testcontainers untuk
  `internal/payin` (4 test, termasuk journey topup penuh) dan
  `internal/handler` (webhook route lewat router publik penuh) hijau
  terhadap Postgres real; migrasi `000005_webhook_blocked_status`
  diverifikasi up→down→up terhadap `seev-postgres-1`.

## T5 — Payout: screen pra-hold

### Langkah
1. `internal/payout/orchestrate.go` `Create`: screen SEBELUM insert row `payout_requests` dan SEBELUM hold. Block → typed error → gateway handler (`internal/handler/payout.go`) memetakan ke 422 `SCREENING_BLOCKED`; TIDAK ada row payout (audit = `screening_events` di seev_fraud). Error infra → fail-open.
2. PENTING: settle/cancel TIDAK di-screen — uang sudah di-hold, block settle = strand dana (gotcha #8 master).
3. `cmd/payout-service/main.go` + compose + `scripts/lib.sh`: dial fraud + env sama seperti T4.

### Test wajib
- Unit: block → tidak ada row, tidak ada posting hold; gateway memetakan 422; settle path terbukti tanpa screening.
- Smoke terarah: fraud mode block (`SCREENING_AMOUNT_THRESHOLD` kecil) menolak payout besar pra-hold.

### DoD
- [x] Payout di-screen sebelum uang di-hold; jalur settle/cancel bebas screening.

### Hasil
- `internal/payout/errors.go`: `ErrScreeningBlocked` sentinel baru — dibungkus
  via `fmt.Errorf("%w: %s", ErrScreeningBlocked, verdict.Reason)`, mengikuti
  pola `internal/payin`'s `ErrScreeningBlocked`/`ErrTopupIntentMismatch`
  (bukan tipe struct, sentinel + wrap cukup karena hanya perlu `errors.Is`).
- `internal/payout/payout.go`: `Module` mendapat field `fraudClient
  *fraudcheck.Client` (nil = valid, tanpa screening); `NewModule` mendapat
  parameter terakhir baru `fraudClient *fraudcheck.Client`;
  `RegisterGRPC`'s `grpcserver.New(...)` call diberi argumen ke-4
  `ErrScreeningBlocked`.
- `internal/payout/orchestrate.go` `Create`: SEBELUM `ResolvePayoutRoute`
  DAN sebelum `repo.Insert`/`hold` — dipanggil
  `fraudClient.Check(ctx, "payout", "withdraw_initiate", userID, amount,
  currency)` (currency sudah diresolve satu baris sebelumnya via
  `GetUserCurrency`, tidak perlu panggilan tambahan).
  - Verdict Block (fail-closed): `Create` mengembalikan
    `uuid.Nil, fmt.Errorf("%w: %s", ErrScreeningBlocked, verdict.Reason)`
    TANPA pernah memanggil `repo.Insert` — nol baris `payout_requests`
    tercipta untuk percobaan yang diblokir; audit trail satu-satunya ada di
    `screening_events` milik fraud-service (persis seperti T3/T4).
  - Error infra (fail-open): dicatat
    `logger.Error("payout: screening check error, failing open", ...)`, lalu
    lanjut ke `ResolvePayoutRoute`/insert/hold seperti biasa — payout
    legitimate tidak pernah tersendat karena fraud-service down.
  - `settle()` dan `cancel()` SENGAJA TIDAK disentuh sama sekali — tidak ada
    referensi `fraudClient` di dalamnya, memenuhi requirement "settle/cancel
    TIDAK di-screen" (uang sudah di-hold; memblokir settle akan men-strand
    dana, gotcha #8 master doc).
- `internal/payout/grpcserver/server.go`: `Server` mendapat field
  `screeningBlocked error`; `New(service, notFound, noRoute,
  screeningBlocked error)` — parameter baru; `CreatePayout` menambahkan
  cabang `errors.Is(callErr, s.screeningBlocked)` → `status.Error(FailedPrecondition,
  callErr.Error())`, DITEMPATKAN sebelum cek generic
  `*ledgererr.LedgerError` supaya klasifikasinya eksplisit, bukan kebetulan
  jatuh ke situ.
- `internal/handler/payout.go` (`createPayoutHandler`): cabang
  `FailedPrecondition` diperluas dari if/else 2 arah menjadi `switch` 3
  arah — pesan `"no payout route available"` tetap → `NO_ROUTE`; pesan
  dengan prefix `"payout: screening blocked"` (string sentinel payout
  sendiri, dicocokkan via `strings.HasPrefix`, BUKAN import
  `internal/payout` — gateway hanya bicara ke payout-service lewat gRPC,
  tidak pernah lintas-modul) → 422 `SCREENING_BLOCKED`, kontrak HTTP identik
  dengan ledger's sendiri (T3); selainnya tetap fallback generic
  `UNPROCESSABLE_ENTITY`.
- `cmd/payout-service/main.go`: menambahkan dial fraud
  (`grpcx.DialLazy(cfg.FraudGRPCAddr, cfg.InternalGRPCToken)`), pola identik
  T4/T3 — `FRAUD_GRPC_ADDR` kosong ⇒ `fraudClient` nil ⇒ tanpa screening,
  100% backward compatible.
- `docker-compose.yml`: env `FRAUD_GRPC_ADDR: fraud-service:9094` ditambahkan
  ke `payout-service`, TANPA `depends_on: fraud-service` — deviasi yang
  SAMA dan dengan alasan yang SAMA seperti didokumentasikan di T4 Hasil
  (mengikuti pola nyata `ledger-service` di T3: `DialLazy` non-blocking by
  design, `depends_on` keras bertentangan dengan availability-over-
  strictness).
- `scripts/lib.sh`: `start_payout_service` menambahkan
  `export FRAUD_GRPC_ADDR=localhost:$FRAUD_GRPC_PORT` (port test `19094`,
  variabel sudah ada dari T3), pola identik `start_ledger_service`/
  `start_payin_service`.
- Test wajib — unit (`internal/payout/payout_test.go`, via
  `fakeFraudGRPCClient` dibungkus `fraudcheck.Client` asli, pola identik
  T3/T4):
  - `TestCreate_FraudBlock_NoRowInserted_NoHold` — mock `Repository` TANPA
    ekspektasi call apa pun (bukti `repo.Insert` nol kali), `Create`
    mengembalikan error yang `errors.Is(err, ErrScreeningBlocked)`.
  - `TestCreate_FraudInfraError_FailsOpen_StillCreates` — reuse skenario
    happy-path instant-settle, fraud client mengembalikan infra error →
    `Create` tetap sukses penuh (insert→hold→submit→settle).
  - `TestSettle_NeverScreened_EvenWithBlockingFraudClient` — memanggil
    `m.settle()` langsung dengan `fraudClient` yang SELALU mem-block →
    settle tetap sukses, membuktikan settle() tidak pernah memanggil
    fraudClient sama sekali.
  - `internal/payout/grpcserver/server_test.go`: `New(...)` call site diberi
    argumen ke-4 (`errors.New("screening blocked")`) — no behavior test baru
    diperlukan di layer ini (klasifikasi errors.Is sudah dites di atas).
  - `internal/handler/payout_test.go`:
    `TestPayoutGatewayScreeningBlocked` — gRPC `FailedPrecondition` dengan
    pesan `"payout: screening blocked: ..."` → gateway membalas 422 dengan
    body mengandung `"code":"SCREENING_BLOCKED"`.
- Smoke terarah (dijalankan manual terhadap stack lokal nyata — 6 binary +
  Postgres/Redis/RabbitMQ dari `scripts/lib.sh`, BUKAN skrip permanen baru;
  kodifikasi permanen ke `chaos-test.sh` adalah scope T6):
  `SCREENING_MODE=block SCREENING_AMOUNT_THRESHOLD=10000`, lalu
  `POST /api/v1/payout` amount=500000 → **422
  `{"code":"SCREENING_BLOCKED","message":"payout: screening blocked: amount
  500000 >= threshold 10000"}`**, diverifikasi `SELECT count(*) FROM
  payout_requests WHERE user_id=...` = **0** (baris memang tidak pernah
  dibuat); lalu sanity-check amount=5000 (di bawah threshold) → **201,
  status settled** — membuktikan mode block tidak overblock semua payout,
  hanya yang melewati threshold.
- **Bug pra-existing ditemukan & diperbaiki (di luar scope fraud, ditemukan
  saat menjalankan gate integrasi penuh)**:
  `internal/testutil/ledger.go`'s `LedgerHarness.Post` mengembalikan error
  MENTAH dari `ledger.Module.Post` (yakni `internal/ledger/apperror`'s
  sentinel, lewat re-export publik `ledger.ErrAlreadyClosed`/
  `ledger.LedgerError`) — BUKAN hasil translasi gRPC-wire yang dilakukan
  `pkg/ledgererr.FromStatus` untuk caller produksi yang benar-benar lewat
  jaringan. Akibatnya `TestSettleAfterCancel_LedgerRejectsViaK3_ReconciledNoMoneyMoved`
  (test T4 doc 23, sudah ada sebelum sesi ini) GAGAL deterministik ketika
  dijalankan (`errors.Is(postErr, ledgererr.ErrAlreadyClosed)` di
  `orchestrate.go`'s `settle()`/`cancel()` — TIDAK disentuh sesi ini — tidak
  pernah cocok karena beda sentinel package). Diperbaiki dengan menambahkan
  `translateLedgerErr` di `LedgerHarness.Post` yang menerjemahkan
  `ledger.ErrAlreadyClosed`/`*ledger.LedgerError` ke `ledgererr.ErrAlreadyClosed`/
  `*ledgererr.LedgerError` — sehingga harness in-process berperilaku identik
  dengan client gRPC nyata. Diverifikasi tidak meregresi konsumen
  `LedgerHarness` lain (`internal/payin`, `internal/auth`,
  `internal/handler` — full integration suite ketiganya tetap hijau).
- Verifikasi: `go build ./...` bersih; `go vet ./...` &
  `go vet -tags=integration ./...` bersih (call site
  `grpcserver.New(...)` di `server_test.go` dan `payout.NewModule(...)` di
  `payout_integration_test.go` diberi argumen tambahan); `gofmt -l .` tanpa
  file baru; `make lint` bersih; `make test` (termasuk `boundary_test.go` —
  `internal/testutil` tetap lolos boundary check via re-export publik
  `ledger.go`, bukan import langsung `internal/ledger/apperror`) hijau
  semua; integration test testcontainers `internal/payout` (semua file:
  `payout_test.go`, `payout_integration_test.go`, `race_integration_test.go`,
  `grpcserver`, `repository`) hijau — termasuk test K3 yang sempat gagal,
  sekarang hijau setelah fix `testutil`; `internal/payin`/`internal/auth`/
  `internal/handler` integration suite re-run hijau (regresi check untuk fix
  `testutil`).

## T6 — Chaos + index README

### Langkah
1. `scripts/chaos-test.sh`: perbarui skenario 7 (fraud down fail-open + block mode) agar mencakup KETIGA flow: P2P transfer, topup webhook, payout create — fraud down → semuanya sukses fail-open + `assert_ledger_balanced`; fraud block mode → ketiganya ditolak SEBELUM posting apa pun (ledger tidak berubah). Perhatikan asersi log existing (`screening hook error, failing open`) — sesuaikan dengan pesan log baru di transport/fraudcheck.
2. Update `docs/plan/README.md`.

### Test wajib
- `chaos-test.sh all` hijau penuh.

### DoD
- [x] Kegagalan fraud-service terbukti tidak menghilangkan/menahan uang di ketiga flow; block mode terbukti menahan SEBELUM penulisan.

### Hasil
- `scripts/chaos-test.sh` `scenario_7()` ditulis ulang untuk mencakup KETIGA
  flow (sebelumnya hanya P2P transfer):
  - **Bug ditemukan & diperbaiki**: asersi log lama meng-grep string
    `'screening hook error, failing open'` — string PENINGGALAN in-tx
    `PrePostHook` yang sudah dihapus total di T3, sehingga assertion ini
    SUDAH GAGAL SENYAP (chaos-test.sh 7 tidak pernah dijalankan ulang sejak
    T3) sebelum sesi T6 memulai perbaikan. Diperbaiki ke string baru yang
    benar-benar dipakai `internal/ledger/transport/http.go`:
    `'screening check error, failing open'` — persis peringatan yang
    diberikan Langkah #1 dokumen ini.
  - **Fraud-service DOWN (fail-open), ketiga flow**: P2P transfer (existing,
    dipertahankan), + **topup webhook baru** (signed mockvendor payload,
    verifikasi saldo naik tepat sejumlah amount + log
    `PAYIN_LOG` berisi `'payin: screening check error, failing open'`), +
    **payout create baru** (verifikasi response settled + log `PAYOUT_LOG`
    berisi `'payout: screening check error, failing open'`).
  - **Fraud-service UP, mode block (`SCREENING_AMOUNT_THRESHOLD=100`),
    ketiga flow ditolak SEBELUM penulisan apa pun**: P2P transfer (existing,
    422 + saldo tak berubah + baris `screening_events` verdict=blocked) +
    **topup webhook baru** (200 — ack non-retriable sesuai kontrak
    `internal/handler/webhook.go`'s business-failure mapping, saldo TIDAK
    berubah, `payin_webhook_events.status='blocked'` diverifikasi via
    `psql_exec`) + **payout create baru** (422 `SCREENING_BLOCKED`,
    `count(*) FROM payout_requests WHERE user_id=...` diverifikasi TIDAK
    berubah sebelum vs sesudah — bukti nol baris tercipta).
  - **Bug ditemukan & diperbaiki (saat menulis T6, sebelum test hijau)**:
    draf pertama membandingkan saldo setelah percobaan block P2P terhadap
    baseline `topup_after` yang sudah basi (diambil SEBELUM payout create
    memindahkan uang lewat hold) — false positive FAIL. Diperbaiki dengan
    mengambil baseline `before_block` tepat sebelum setiap percobaan block,
    bukan reuse variabel dari langkah sebelumnya.
  - `assert_ledger_balanced` + `assert_no_inconsistent_projections` tetap di
    akhir seperti semua skenario lain.
- `docs/plan/README.md`: baris index dokumen 37 diubah dari `⬜ todo` menjadi
  `✅ done`.
- Verifikasi (semua dijalankan dari VOLUME POSTGRES BERSIH —
  `docker compose down -v` lalu fresh init — karena volume lama yang dipakai
  sepanjang sesi T4/T5/T6 sudah terkontaminasi state dari banyak run manual
  dengan UUID tetap `smoke-test.sh`/`business-e2e.sh` pakai, menyebabkan 2
  false-positive FAIL saldo pada `smoke-test.sh` sebelum reset):
  - `./scripts/chaos-test.sh 7` hijau sendirian (setelah 2 iterasi
    memperbaiki bug asersi di atas).
  - `./scripts/chaos-test.sh all` — ketujuh skenario hijau penuh.
  - `./scripts/smoke-test.sh` — hijau penuh dari volume bersih.
  - `./scripts/business-e2e.sh` — hijau penuh (journey onboarding → topup →
    transfer → withdraw+cancel → daily ops → tracing end-to-end).
  - `gofmt -l .`, `go vet ./...`, `go vet -tags=integration ./...`,
    `make lint`, `make test` (termasuk `boundary_test.go`) — semua bersih/
    hijau.

---

## Verifikasi akhir dokumen
Gate standar master doc 36 hijau semua → lanjut [38-phase7c-fee-quotes.md](38-phase7c-fee-quotes.md).
