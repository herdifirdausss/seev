# 40 — Phase 7e: Resiliensi Multi-Vendor — Circuit Breaker + Failover Pra-Konfirmasi

> Baca master reference di [36-phase7a-request-tracing.md](36-phase7a-request-tracing.md). Prasyarat: doc 39 selesai.

## Konteks

Hari ini routing memilih TEPAT SATU vendor (`ORDER BY (user_id IS NOT NULL) DESC, priority ASC LIMIT 1`), tidak ada konsep health/breaker, dan payout terpaku pada vendor awalnya selamanya — resume job hanya me-retry vendor yang sama. Vendor down = flow itu mati sampai vendor pulih. Fase ini menambahkan **circuit breaker per vendor** + **failover pra-konfirmasi**: vendor down tidak menghentikan bisnis, dan TIDAK PERNAH double-payout — aturan kerasnya berbasis bukti `payout_vendor_calls`, BUKAN state breaker (breaker hanya optimasi ketersediaan).

Aturan failover terkunci (master): pindah vendor DIIZINKAN ⟺ `status ∈ {created, held}` DAN `payout_vendor_calls` TIDAK punya row `accepted`/`uncertain` untuk payout itu. Timeout/unknown SETELAH Submit = `uncertain` = payout TERPAKU ke vendor itu selamanya (pemulihan = Query/retry vendor sama via resume job). Penolakan sinkron definitif pra-acceptance = `rejected` = boleh failover.

## T1 — Circuit breaker di vendorgw

### Langkah
1. `internal/vendorgw/breaker.go`: `HealthTracker` per-vendor — state machine closed → open (setelah N kegagalan beruntun; env `BREAKER_FAILURE_THRESHOLD` default 5) → half-open (setelah cooldown; env `BREAKER_COOLDOWN` default 30s; TEPAT SATU probe) → closed saat probe sukses / open lagi saat gagal. In-memory per proses — limitasi multi-replica DIDOKUMENTASIKAN di doc comment (tiap replica trip independen; aman, hanya konvergensi lebih lambat).
2. API: `Allow(vendor) bool`, `RecordSuccess(vendor)`, `RecordFailure(vendor)`, `Snapshot() []VendorHealth{Vendor, State, ConsecutiveFailures, OpenedAt, LastProbeAt}`.
3. Klasifikasi WAJIB: hanya error transport/5xx/timeout yang dihitung kegagalan; penolakan BISNIS (rekening invalid, saldo vendor kurang) TIDAK men-trip (gotcha #13 master).
4. Konfigurasi env di `internal/config` + wire di main payin/payout.

### Test wajib
- Unit `-race`: threshold men-trip; half-open probe single-flight (dua goroutine → satu probe); business error tidak dihitung; recovery menutup circuit; Snapshot akurat.

### DoD
- [x] Breaker teruji di bawah race, dengan semantik klasifikasi error yang benar.

### Hasil
- `internal/vendorgw/breaker.go` (baru): `HealthTracker` per-vendor, in-memory per proses, mutex per-vendor (bukan satu lock global) supaya vendor berbeda tidak saling memblokir. State machine PERSIS spek: `closed` → `open` (setelah `failureThreshold` kegagalan beruntun) → `half_open` (SATU caller yang menang lock transisi ke half-open dan SEKALIGUS menjadi probe — caller lain yang datang di jendela yang sama melihat state sudah `half_open` dan ditolak, tanpa perlu flag `probing` terpisah) → `closed` (probe sukses) / `open` lagi (probe gagal, TANPA perlu re-akumulasi threshold — satu probe gagal sudah cukup bukti vendor masih turun).
- API: `Allow(vendor) bool`, `RecordSuccess(vendor)`, `RecordFailure(vendor)`, `Snapshot() []VendorHealth` (terurut nama vendor, untuk output deterministik di admin endpoint T5).
- Klasifikasi bisnis-vs-infra (gotcha #13) SENGAJA bukan logika di dalam `HealthTracker` — itu tanggung jawab PEMANGGIL (lihat Hasil T3): breaker hanya punya `RecordSuccess`/`RecordFailure`, dan kontrak dok-comment-nya eksplisit menyatakan `RecordSuccess` dipanggil bahkan untuk penolakan bisnis sinkron (vendor tetap terjangkau, hanya menolak). `TestHealthTracker_RecordSuccess_NeverTrips` membuktikan sisi tracker dari kontrak ini: memanggil `RecordSuccess` berulang (mensimulasikan banyak penolakan bisnis) tidak pernah membuka circuit.
- `internal/config`: `BreakerConfig{FailureThreshold, Cooldown}` + env `BREAKER_FAILURE_THRESHOLD` (default 5) / `BREAKER_COOLDOWN` (default 30s), pola identik `FeeQuoteTTL`. Di-wire di `cmd/payin-service/main.go` dan `cmd/payout-service/main.go`: `breaker := vendorgw.NewHealthTracker(cfg.Breaker.FailureThreshold, cfg.Breaker.Cooldown, log)`, diteruskan ke `payin.NewModule`/`payout.NewModule` (parameter baru trailing, nil = breaker dimatikan sepenuhnya — byte-identical dengan sebelum fitur ini ada).
- Test (`internal/vendorgw/breaker_test.go`, semua PASS di bawah `-race`): threshold men-trip tepat pada hitungan ke-N; `TestHealthTracker_HalfOpenSingleProbe_RaceSafe` — 20 goroutine memanggil `Allow` bersamaan tepat saat cooldown habis, TEPAT SATU yang menerima `true` (DoD "single probe" eksplisit); probe sukses menutup circuit dan mereset counter; probe gagal membuka lagi TANPA menunggu threshold; `Snapshot` akurat dan terurut multi-vendor; default `NewHealthTracker(0,0,nil)` jatuh ke 5/30s.
- Verifikasi: `go build`/`go vet` (default) bersih; `gofmt -l` bersih untuk file baru.

## T2 — Routing daftar kandidat

### Langkah
1. `internal/payout/repository/routing_repository.go` (+ mirror `internal/payin/repository`): query `Resolve` dari `LIMIT 1` → kembalikan SEMUA rule cocok terurut (user-specific DESC, priority ASC).
2. `internal/payout/routing.go` / `internal/payin/routing.go`: iterasi kandidat — skip vendor yang `!breaker.Allow(vendor)` atau tidak terdaftar di registry; kandidat pertama yang lolos menang; semua ter-skip → typed `ErrNoVendorAvailable` → payout create 503 `VENDOR_UNAVAILABLE`, topup intent create 503 (mapping di gateway handler).
3. Terima parameter exclusion list (dipakai T3 failover: vendor yang sudah dicoba tidak dipilih lagi).

### Test wajib
- Unit routing: urutan prioritas dihormati; breaker open → lompat ke kandidat berikut; semua open → error; rule user-specific tetap menang atas global; exclusion list bekerja.

### DoD
- [x] Vendor down otomatis ter-skip untuk request BARU tanpa intervensi manual.

### Hasil
- `internal/payout/repository/routing_repository.go` + mirror `internal/payin/repository/routing_repository.go`: `Resolve` (LIMIT 1) diganti `ResolveCandidates` — query SQL sama persis (`ORDER BY (user_id IS NOT NULL) DESC, priority ASC`) minus `LIMIT 1`, mengembalikan `[]model.RoutingCandidate{Vendor, Gateway}` (tipe baru, minimal — hanya field yang benar-benar dipakai pemanggil). `model.RoutingCandidate` ditambahkan di kedua paket `model`.
- `internal/payout/routing.go` `ResolvePayoutRoute` / `internal/payin/routing.go` `ResolveTopupRoute`: iterasi kandidat terurut, skip yang ada di `exclude` (map lookup), skip yang tidak terdaftar di registry, skip yang `!breaker.Allow(vendor)` (breaker `nil` = tidak pernah skip, byte-identical ke sebelum fitur T1 ada) — kandidat pertama yang lolos menang. Nol kandidat cocok sama sekali → `ErrNoRoute` (existing, tak berubah — config belum diset). SEMUA kandidat ter-skip → sentinel BARU `ErrNoVendorAvailable`.
- Paritas gRPC: `internal/payout/grpcserver.New`/`internal/payin/grpcserver.New` menambah parameter `noVendorAvailable error` (pola sentinel-sebagai-parameter yang SUDAH ada untuk `noRoute`/`notFound`, bukan import langsung — grpcserver tetap tidak bergantung pada tipe konkret modul); dipetakan ke `codes.Unavailable` (BEDA dari `codes.FailedPrecondition` milik `ErrNoRoute` — satu problem KONFIGURASI permanen, satu TRANSIEN). Gateway (`internal/handler/payout.go`, `internal/handler/topup.go`) menambah cabang `codes.Unavailable` → HTTP 503 `{"code":"VENDOR_UNAVAILABLE"}`, terpisah dari `codes.FailedPrecondition` → 422 `NO_ROUTE`.
- `ResolvePayoutRoute` sekarang menerima parameter `exclude []string` (dipakai T3's failover — lihat Hasil T3); `ResolveTopupRoute` TIDAK menerima exclusion list (payin tidak pernah failover — hanya membuat intent, tidak pernah submit sinkron ke vendor).
- Test BARU: `internal/payout/routing_test.go` — `TestResolvePayoutRoute_BreakerOpen_SkipsToNextCandidate`, `TestResolvePayoutRoute_AllCandidatesOpen_ErrNoVendorAvailable`, `TestResolvePayoutRoute_ExclusionList_SkipsAlreadyTried`; `internal/payin/routing_test.go` mirror (`TestResolveTopupRoute_BreakerOpen_SkipsToNextCandidate`, `TestResolveTopupRoute_AllCandidatesOpen_ErrNoVendorAvailable`). `TestResolvePayoutRouteMatrix`/`TestResolveTopupRoute_Matrix` existing diperbarui ke `ResolveCandidates` tanpa mengubah skenario (urutan prioritas, user-specific menang, currency, range, fallback tetap PASS).
- Verifikasi: `go build`/`go vet` (default + `-tags=integration`) bersih; `go test ./internal/payin/... ./internal/payout/... ./internal/vendorgw/... ./internal/handler/...` semua PASS, nol regresi.

## T3 — Aturan failover payout (inti anti-double-payout)

### Langkah
1. `internal/payout/repository`: kolom klasifikasi outcome di `payout_vendor_calls` — perluas `recordVendorCall` untuk menulis `outcome ∈ accepted|rejected|uncertain` (migrasi kolom baru bila belum ada — cek skema; bila resp_status/error existing cukup diklasifikasi, tambah satu kolom `outcome TEXT` via migrasi payout berikutnya — nomor menyesuaikan setelah doc 38 memakai 000004, berarti 000005).
2. `internal/payout/orchestrate.go` `submit`: (a) hasil Submit sukses/diterima → `accepted`; (b) penolakan sinkron definitif → `rejected` + `RecordFailure` TIDAK dipanggil (bisnis) + boleh failover: re-run routing dengan exclusion list vendor yang sudah dicoba, update kolom vendor di row, submit ke kandidat berikut; (c) timeout/unknown → `uncertain` + `RecordFailure` + status tetap untuk resume job (VENDOR SAMA — pinned selamanya).
3. Guard failover di satu fungsi bernama jelas (mis. `mayFailover(req, calls) bool`) yang mengimplementasikan aturan terkunci persis; resume job (`ResumeStuck`) TIDAK berubah — tetap re-drive vendor yang tersimpan di row.
4. Cap percobaan failover = jumlah kandidat (hindari loop).

### Test wajib
- Table-driven orchestration: (a) vendor A reject instan → B sukses → TEPAT SATU settle, ledger balanced; (b) A timeout saat Submit → TIDAK failover, pinned, resume re-query A; (c) circuit A open sebelum panggilan apa pun → langsung B; (d) race resume-job vs failover → tidak pernah dobel submit (pola `race_integration_test.go` existing).
- Verifikasi idempotency key vendor per (payout, attempt) aman terhadap dedup mockvendor (Submit di-cache per key — pastikan failover ke vendor BARU memakai registry berbeda sehingga cache tidak bocor antar vendor).

### DoD
- [x] Tidak ada jalur kode yang bisa menghasilkan dua settle untuk satu payout, dibuktikan test race.

### Hasil
**Skema outcome** — migrasi `migrations/payout/000005_vendor_call_outcome.up.sql` menambah
kolom `payout_vendor_calls.outcome TEXT NOT NULL CHECK (outcome IN ('accepted','rejected','uncertain'))`.
Ditambahkan via `DEFAULT 'uncertain'` lalu `DROP DEFAULT` — baris lama backfill ke asumsi
paling konservatif (pinned, tidak pernah failover), insert baru wajib eksplisit. Diverifikasi
naik-turun-naik terhadap `seev-postgres-1` + `\d payout_vendor_calls`.

**Klasifikasi outcome** (`internal/payout/orchestrate.go`):
- `classifySubmitOutcome(result, callErr)`: `callErr != nil` → `uncertain`; `result.Status ==
  PayoutFailed` dengan `callErr == nil` → `rejected` (penolakan sinkron definitif); selain itu
  (Settled/Pending) → `accepted`.
- `classifyQueryOutcome(callErr)`: `callErr != nil` → `uncertain`; selain itu → `accepted` (Query
  hanya pernah terjadi atas request yang SUDAH `accepted` dari Submit sebelumnya, jadi tidak
  pernah bisa jadi `rejected` baru).
- Klasifikasi bisnis-vs-infra ini hidup di CALLER (`orchestrate.go`), bukan di breaker — hanya
  kegagalan transport/5xx/timeout yang memanggil `RecordFailure`; penolakan bisnis sinkron adalah
  `RecordSuccess` dari sudut pandang breaker (vendor terjangkau, cuma menolak).

**`mayFailover(calls []model.PayoutVendorCall) bool`** — fungsi murni atas hasil
`ListVendorCalls` (bukan query boolean di DB, supaya unit-testable tanpa mock DB): `false` bila
ADA call dengan outcome `accepted` atau `uncertain`; `true` bila semua (atau tidak ada) call
berstatus `rejected`. Ini mengimplementasikan aturan terkunci persis: switching vendor aman
HANYA SELAMA belum pernah ada call yang mendarat `accepted`/`uncertain`.

**Loop failover di `submit()`** — setelah hasil `PayoutFailed`, mengambil `ListVendorCalls` segar,
cek `mayFailover`; bila `true`, panggil `ResolvePayoutRoute` dengan exclusion list berisi semua
vendor yang sudah dicoba pada loop ini, persist vendor baru via `SetVendor`, lanjut loop
(re-attempt Submit ke vendor baru); bila `false` atau tidak ada kandidat lagi, jatuh ke `cancel()`
seperti semula. Dibatasi `maxFailoverAttempts = 20` sebagai pengaman tambahan (exclusion list
sendiri sudah menjamin terminasi dalam maksimal `len(candidates)` iterasi).

**Parameterisasi nama mockvendor ditarik maju dari T4** — dibutuhkan oleh Test Wajib T3 sendiri
("failover ke vendor BARU memakai registry berbeda sehingga cache tidak bocor antar vendor"):
`mockvendor.New(name, secret string)`, `mockvendor.NewPayoutProvider(name string)`, keduanya
`Vendor()` mengembalikan `name` yang disimpan. Semua caller existing diperbarui memakai
`mockvendor.VendorName` eksplisit (perilaku lama identik byte-per-byte). T4 sekarang tinggal:
wiring `mockvendor2` di `cmd/*-service/main.go` + admin force-fail switch + seed rule routing.

**Saklar force-fail vendor** (`mockvendor.PayoutProvider.SetForceFail(bool)`, juga ditarik maju
dari T4 karena dipakai desain di atas sebagai referensi) — mengembalikan `error` transport-style
asli (BUKAN penolakan bisnis terstruktur), supaya benar-benar men-trip breaker sesuai klasifikasi
gotcha #13; dicek PERTAMA di `Submit`, sebelum cache lookup, dan TIDAK PERNAH di-cache (sama
seperti `ModeTimeout` — vendor yang benar-benar down tidak bisa mengingat apa yang tidak pernah
diterimanya).

**Test wajib — semua ditulis dan hijau:**
- `internal/payout/failover_test.go` (unit, mock repo):
  - `TestSubmit_VendorRejectsSynchronously_FailsOverToNextCandidate` — skenario (a): A menolak
    sinkron → B settle → tepat satu settle, `SetVendor` dipanggil sekali ke vendor pemenang.
  - `TestSubmit_VendorTimesOut_NeverFailsOver_PinnedForResume` — skenario (b): error transport →
    `uncertain` → TIDAK ada `ListVendorCalls`/`SetVendor` yang dipanggil sama sekali (pinned,
    resume job yang meng-query ulang vendor yang SAMA).
  - `TestSubmit_CircuitAlreadyOpen_GoesStraightToSecondCandidate` — skenario (c): breaker vendor A
    sudah open sebelum `submit()` berjalan; `Submit` A memanggil `t.Fatal` bila tersentuh — proses
    langsung ke B (perilaku exclusion sudah dijamin oleh routing T2; test ini membuktikan
    `submit()` sendiri menghormati vendor yang sudah diroutekan Create()).
  - `TestMayFailover_TableDriven` — 6 kasus tabel (tanpa call, hanya rejected, campur
    rejected+accepted, dll).
- `internal/payout/failover_integration_test.go` (integration, Postgres asli via testcontainers)
  — skenario (d): `TestFailover_ConcurrentSubmit_RaceResumeJobVsFailover`, 10 goroutine memanggil
  `m.submit()` konkuren untuk request yang sama (simulasi resume job vs percobaan submit lain yang
  masih in-flight). Karena routing deterministik + setiap panggilan vendor per-goroutine
  konsisten, semua goroutine konvergen ke vendor pemenang yang SAMA dan idempotency key settle
  yang SAMA — log membuktikan tepat 1 `posted` + 9 `idempotent: transaction already posted`.
  Diverifikasi: status akhir `settled`, `vendor` akhir konsisten, tepat 1 baris
  `ledger_transactions` untuk `settleIdempotencyKey`, saldo cash tepat mencerminkan satu settle,
  `fn_verify_ledger_balance` nol baris, tidak ada call `uncertain` (semua sinkron di skenario ini).
- `internal/vendorgw/mockvendor/payout_test.go` —
  `TestPayoutProvider_IdempotencyCache_IsolatedAcrossVendorInstances`: idempotency key yang SAMA
  (payout ID) di-Submit ke `vendorA` (ModeFail) lalu ke instance `vendorB` (ModeInstantSettle) yang
  BERBEDA — membuktikan `vendorB` memproses key itu segar (bukan membaca cache `Failed` milik A),
  dan cache `vendorA` sendiri tidak berubah setelah aktivitas `vendorB`.

**Regresi yang diperbaiki**: `TestSubmit_VendorFailed_CancelsAndReturnsHold` (test lama,
single-vendor) perlu tambahan `repo.EXPECT().ListVendorCalls(...).Return(nil, nil)` — skenario
tanpa call sebelumnya SECARA TEKNIS mengizinkan failover (`mayFailover` = true), tapi
`ResolvePayoutRoute` dengan exclusion vendor tunggal yang ada tidak menemukan kandidat lain,
sehingga tetap jatuh ke `cancel()` seperti semula. `TestInsertVendorCall_And_ListStuck`
(repository integration test lama) diperbarui menambahkan `Outcome: model.VendorCallAccepted`
eksplisit — CHECK constraint kolom baru menolak insert tanpa outcome valid.

**Verifikasi akhir**: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`,
`gofmt -l .` (bersih di luar `internal/policy/alert_test.go` yang pre-existing, tidak disentuh),
`make lint` bersih, `go test ./...` seluruh repo hijau, `go test -tags=integration
./internal/payout/...` seluruh paket (termasuk `repository`, `grpcserver`) hijau termasuk race
test baru, `go test -race` pada test T1-T3 yang relevan hijau.

## T4 — Vendor mock kedua

### Langkah
1. Parameterkan konstruktor `internal/vendorgw/mockvendor` dengan nama + secret (`New(name, secret)` / `NewPayoutProvider(name)`) — pertahankan default `mockvendor` supaya test existing tidak berubah.
2. Daftarkan `mockvendor2` di `cmd/payin-service/main.go` + `cmd/payout-service/main.go` di balik env `MOCKVENDOR2_ENABLED` / `MOCKVENDOR2_SECRET`; compose + `scripts/lib.sh` export env-nya.
3. Saklar force-fail level-VENDOR pada provider payout mock (admin endpoint kecil di admin port payout, atau atomic yang di-set via endpoint test-only) — `mock_mode` per-payload TIDAK cukup untuk men-trip breaker dari trafik nyata (perlu vendor yang gagal untuk SEMUA request).
4. Rule routing mockvendor2 (priority 2, global) di-seed via admin API DI DALAM script (gotcha #15 — test data bukan schema).

### Test wajib
- Registry dengan dua vendor: verifikasi signature webhook per-vendor tetap terisolasi (secret beda); force-fail switch bekerja.

### DoD
- [x] Failover bisa didemonstrasikan nyata dengan dua vendor terdaftar. _(mekanisme lengkap dan
  teruji di bawah; demonstrasi end-to-end nyata dibuktikan oleh `scripts/chaos-test.sh 8`, lihat
  Hasil T6.)_

### Hasil
**Parameterisasi konstruktor** — sudah selesai, ditarik maju ke T3 karena Test Wajib T3 sendiri
membutuhkannya (lihat Hasil T3). Bug ditemukan & diperbaiki sebagai bagian T4: `mockvendor.Verifier.VerifyAndParse`
sebelumnya menulis `Vendor: VendorName` (konstanta package, hardcoded) alih-alih `Vendor: v.name`
— artinya instance kedua ("mockvendor2") akan salah menandai setiap event yang diverifikasinya
sebagai `"mockvendor"`, padahal `PayinEvent.Vendor` inilah yang menjadi idempotency scope DAN
kunci lookup vendor-gateway di `internal/payin`. Diperbaiki di
`internal/vendorgw/mockvendor/mockvendor.go`; dibuktikan oleh test baru
`TestVerifyAndParse_SecondNamedInstance_TagsEventWithOwnNameAndSecret` — instance kedua menandai
event dengan namanya sendiri, dan secretnya benar-benar terisolasi dari instance pertama (kedua
arah: secret A tidak memverifikasi signature B dan sebaliknya).

**Registrasi `mockvendor2`** — `internal/config` sudah punya `VendorConfig.Mockvendor2Enabled`/
`Mockvendor2Secret` (env `MOCKVENDOR2_ENABLED`/`MOCKVENDOR2_SECRET`); ditambahkan validasi pasangan
wajib (`validate()`, sama seperti mockvendor pertama). `cmd/payin-service/main.go` dan
`cmd/payout-service/main.go` mendaftarkan `mockvendor2` ke registry masing-masing bila enabled.
`docker-compose.yml` (blok `payin-service` dan `payout-service`) serta `scripts/lib.sh`
(`start_payin_service`, `start_payout_service`) meng-export env-nya — purely additive, mockvendor2
hanya benar-benar dipakai sekali ada routing rule yang mengarah ke sana.

**Saklar force-fail level-vendor** — mekanisme (`mockvendor.PayoutProvider.SetForceFail`) sudah
ada dari T3. Ditambahkan admin endpoint di T4: `POST /admin/payout/vendors/{vendor}/force-fail`
(`internal/payout/http.go`) dengan body `{"fail": bool}`. Type-assert ke interface lokal sempit
`forceFailSwitch` (bukan import `mockvendor` langsung — modul produksi tetap vendor-agnostic);
vendor yang tidak mengimplementasikannya (bukan mockvendor) melapor 400.

**Test wajib — semua ditulis dan hijau:**
- `internal/vendorgw/mockvendor/mockvendor_test.go`:
  `TestVerifyAndParse_SecondNamedInstance_TagsEventWithOwnNameAndSecret` — isolasi signature dua
  arah + `PayinEvent.Vendor` benar (lihat bug fix di atas).
- `internal/payout/http_test.go` — 4 test untuk endpoint force-fail:
  `TestAdminRouter_ForceFail_NonAdmin_403`, `TestAdminRouter_ForceFail_UnregisteredVendor_404`,
  `TestAdminRouter_ForceFail_VendorWithoutSwitch_400` (vendor tanpa kapabilitas force-fail),
  `TestAdminRouter_ForceFail_Success_TripsSubsequentSubmit` (memakai
  `mockvendor.NewPayoutProvider` sungguhan — membuktikan switch ON membuat SETIAP Submit
  berikutnya gagal terlepas dari isi destination, dan switch OFF memulihkan perilaku normal).

**Verifikasi**: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l .`
bersih (di luar `internal/policy/alert_test.go` pre-existing), `make lint` bersih, `go test ./...`
hijau.

## T5 — Surface admin vendor health

### Langkah
1. `GET /admin/vendors/health` di admin payin (:8092) dan payout (:8093): JSON `breaker.Snapshot()`.

### Test wajib
- Handler test dengan tracker ter-seed (closed/open/half-open tampil benar).

### DoD
- [x] Operator melihat kesehatan vendor tanpa SQL/log-diving.

### Hasil
**Endpoint** — diimplementasikan sebagai `GET /admin/payin/vendors/health` (:8092) dan
`GET /admin/payout/vendors/health` (:8093), BUKAN literal `/admin/vendors/health` seperti tertulis
di Langkah: kedua service ini hanya me-mount sub-tree `/admin/payin/` atau `/admin/payout/` di
`cmd/*/router.go` (`root.Handle("/admin/payin/", authed(handlers.AdminRouter()))`), jadi path tanpa
prefix modul tidak akan pernah sampai ke `AdminRouter()`. Path baru mengikuti konvensi SEMUA route
admin lain di kedua modul (namespaced per modul), bukan pengecualian baru.

Handler (`internal/payin/http.go`, `internal/payout/http.go`, masing-masing
`vendorHealthHandler`): admin-gated seperti semua handler admin lain; `nil` breaker (BREAKER_*
tidak dikonfigurasi) melapor `{"vendors":[]}` — kontrak "byte-identical ketika fitur off" yang
sama dipakai di seluruh doc 40. Response dibungkus `response.Envelope` standar (`{"success":true,
"data":{"vendors":[...]}}"`), sama seperti semua endpoint admin lain di codebase ini.

**Test wajib — semua ditulis dan hijau:**
- `internal/payin/http_test.go` / `internal/payout/http_test.go`, masing-masing 3 test:
  `TestAdminRouter_VendorHealth_NonAdmin_403`, `TestAdminRouter_VendorHealth_NilBreaker_EmptyList`,
  `TestAdminRouter_VendorHealth_ReportsAllThreeStates` — satu tracker di-seed dengan vendor
  `closed` (tidak pernah disentuh sama sekali — karena itu TIDAK muncul di `Snapshot()`, closed
  adalah default implisit bukan baris snapshot), `open` (`RecordFailure` sampai threshold), dan
  `half_open` (cooldown 1ns lalu `Allow()` dipanggil, mempromosikan ke half-open) — dibuktikan
  ketiganya tampil benar dalam SATU snapshot yang sama.

**Verifikasi**: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l .`
bersih (di luar `internal/policy/alert_test.go` pre-existing), `make lint` bersih, `go test ./...`
hijau, `go test .` (boundary check) hijau.

## T6 — Chaos drill + index README

### Langkah
1. `scripts/chaos-test.sh` skenario baru `vendor-failover`: force-fail mockvendor → payout BARU ter-route ke mockvendor2 (assert via admin health + `payout_vendor_calls`) → satu payout `uncertain` in-flight TETAP pinned ke mockvendor (assert kolom vendor tidak berubah) → pulihkan mockvendor → resume job menyelesaikan payout pinned → `fn_verify_ledger_balance()` 0 baris + assert tidak ada payout dengan dua settle (idempotency key settle unik per payout).
2. Update `docs/plan/README.md`.

### Test wajib
- `chaos-test.sh all` hijau penuh termasuk skenario baru.

### DoD
- [x] Gangguan vendor terbukti tidak menghilangkan/menggandakan uang dan tidak menghentikan bisnis.

### Hasil
**Skenario baru `scripts/chaos-test.sh 8`** ("vendor failover"), ditambahkan sebagai `scenario_8`
mengikuti pola scenario 1–7 persis (`ensure_deps_up` → `build_server` → `start_services`, assert
via `ok`/`fail`, `assert_ledger_balanced` + `assert_no_inconsistent_projections` di akhir). Alur:

1. `BREAKER_FAILURE_THRESHOLD=1` di-export sebelum `start_services` — membuat breaker trip pada
   kegagalan force-fail PERTAMA (deterministik, tidak perlu menunggu threshold default 5).
2. Seed `mockvendor2`: `PUT /admin/payout/vendor-gateways/mockvendor2` (gateway `gopay` — daftar
   gateway payout yang diizinkan hanya `bca`/`gopay`/`platform`, `bri` dari draft awal ditolak
   dengan `BAD_REQUEST "gateway is not allowed"`, ditemukan & diperbaiki saat menjalankan skenario
   sungguhan) lalu `POST /admin/payout/routing-rules` priority **1001** (bukan literal "priority 2"
   dari Langkah dokumen — lihat catatan desain di bawah).
3. Force-fail mockvendor via endpoint T4 (`{"fail":true}`).
4. Payout #1 dibuat — masih ter-route ke mockvendor (kandidat prioritas tertinggi, breaker belum
   trip saat routing dievaluasi) → Submit gagal dengan error transport asli → outcome `uncertain`
   → status tetap `submitted`, PINNED ke mockvendor → breaker mockvendor `RecordFailure` → trip
   `open` (threshold=1).
5. Admin health (`GET /admin/payout/vendors/health`) dikonfirmasi melaporkan mockvendor `open`.
6. Payout #2 dibuat — routing (Task T2) melihat mockvendor `open` (`!breaker.Allow`), langsung
   skip ke mockvendor2 (priority 1001) → settle instan.
7. Mockvendor dipulihkan (`{"fail":false}`); payout #1 di-backdate lewat SQL (pola
   `backdate_payout` scenario 5) supaya cron tick resume job berikutnya (≤60 detik) langsung
   memprosesnya.
8. Tunggu 65 detik → resume job me-retry payout #1 terhadap vendor yang SAMA (mockvendor, TIDAK
   pernah failover meski secara teknis breaker sudah kembali bisa di-`Allow` — resume job tidak
   pernah mengonsultasikan breaker sama sekali, ia hanya mengulang `submit()` dengan vendor yang
   tersimpan di baris) → kali ini sukses → settled.
9. Assert akhir: `payout_requests.vendor` payout #1 TETAP `mockvendor` (tidak pernah berubah),
   tepat SATU baris `ledger_transactions` untuk idempotency key settle masing-masing payout,
   saldo cash mencerminkan tepat dua settle, `fn_verify_ledger_balance()` 0 baris,
   `v_account_balance_audit` konsisten.

**Keputusan desain: mockvendor2 di priority 1001, bukan "priority 2" seperti tertulis di
Langkah** — `ResolveCandidates` (Task T2) mengurutkan `ORDER BY ... priority ASC`, jadi ANGKA
LEBIH KECIL = dicoba LEBIH DULU. Seed migrasi mockvendor sendiri (`000002_routing.up.sql`) memakai
priority **1000**. Bila mockvendor2 diberi priority 2 (lebih kecil dari 1000), ia justru akan
dicoba LEBIH DULU dari mockvendor — terbalik dari maksud skenario (mockvendor2 sebagai FALLBACK,
bukan pengganti). Priority 1001 (lebih besar dari 1000) membuatnya benar-benar sebuah fallback.

**Pengujian nyata terhadap proses berjalan** (bukan cuma unit/integration test Go): skenario ini
dijalankan berkali-kali secara manual melawan enam service sungguhan sebelum + sesudah setiap
perbaikan (gateway allowlist, dll.) hingga hijau bersih, membuktikan seluruh rantai
HTTP→gRPC→Postgres→resume-job-cron benar-benar bekerja, bukan cuma logika orchestrate.go dalam
isolasi mock.

**`docs/plan/README.md`** — baris index doc 40 diubah status `⬜ todo` → `✅ done`.

**Verifikasi gate penuh (dari clean Postgres volume, `docker compose down -v`)**:
- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l .` bersih (di luar
  `internal/policy/alert_test.go` pre-existing, tidak disentuh), `make lint` bersih.
- `make test` (`go test -race -cover ./...`) — SELURUH paket hijau termasuk `boundary_test.go`.
- `./scripts/smoke-test.sh` — hijau penuh.
- `./scripts/business-e2e.sh` — hijau penuh (8 section, termasuk journey KYC/fee-quote/tracing
  dari doc 36–39).
- `./scripts/chaos-test.sh all` — kedelapan skenario hijau (1–7 existing + 8 baru), termasuk
  `fn_verify_ledger_balance()` dan `v_account_balance_audit` bersih di setiap skenario.

## Verifikasi akhir dokumen
Gate standar master doc 36 hijau semua (lint/test/vet-both-tags/smoke/business-e2e/chaos-all dari
clean Postgres volume — lihat Hasil T6 di atas) → lanjut
[41-phase7f-mvp-acceptance.md](41-phase7f-mvp-acceptance.md).
