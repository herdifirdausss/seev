# 21 — Service Topology Review: Peta Jangka Panjang Monolith → Services

Tanggal: 2026-07-12 (setelah seluruh 03–20 selesai dan diverifikasi).

Dokumen ini adalah **referensi arsitektur jangka panjang** — perannya seperti [13](13-p1-backlog-review.md) terhadap 14–16: mengunci keputusan desain supaya dokumen eksekusi ([22](22-phase4a-payin-vendorgw.md), [23](23-phase4b-payout-orchestration.md)) dan playbook ([24](24-extraction-playbook.md)) bisa dikerjakan tanpa re-litigasi.

**Visi yang diminta**: sistem terdekomposisi menjadi service — **ledger, payin, payout, vendor gateway adapters (middleware per vendor), fraud detection, internal admin, user-facing service** — di mana ledger, payin, payout, dan fraud **tidak terekspos publik**. **Keputusan strategis: TETAP modular monolith sekarang**, tapi setiap calon service dipetakan 1:1 ke modul monolith dengan boundary yang sudah bersih, sehingga split nanti adalah pekerjaan mekanis (facade → HTTP client, carve-out tabel), bukan refactor besar.

Prinsip yang dipegang (perluasan dari [01](01-target-architecture.md) D9): **"service atau modul" adalah keputusan deployment, bukan keputusan kode.** Kode yang boundary-nya benar bisa di-deploy sebagai satu binary hari ini dan tujuh service tahun depan tanpa menulis ulang logika bisnis.

---

## Kondisi Aktual Saat Ini (audit 2026-07-12)

Fakta yang menjadi dasar semua keputusan di bawah — diverifikasi terhadap kode, bukan asumsi:

| Aspek | Kondisi |
|---|---|
| Binary & DB | Satu binary (`cmd/server`), satu Postgres schema (`public`), satu timeline migrasi (000001–000018), RabbitMQ outbox **publish-only** (belum ada consumer), Redis opsional |
| Modul | `internal/ledger` (matang: posting engine, snapshot, statement, maker-checker, recon CSV, scheduled tx, disbursement, accrual, screening hooks, reporting views), `internal/policy` (limits/velocity, disuntik struktural sebagai `ledger.PolicyChecker`), `internal/handler` (composition root; route auth/users/admin masih placeholder 501) |
| Listener | Public `:8080` (tipe transaksi user allowlisted, rate limit, CORS, policy check) dan internal `127.0.0.1:8081` (semua tipe, `/metrics`, seluruh tooling admin, tanpa rate limit) — hasil [10 T1](10-phase2a-security-gating.md) |
| Integrasi vendor | **BELUM ADA SAMA SEKALI.** Tidak ada outbound call ke payment vendor; tidak ada webhook receiver; `money_in`/`money_out` hanya bisa diposting via router internal oleh "trusted caller" yang secara harfiah belum eksis. Recon = upload CSV. Disbursement = posting ledger saja, tidak pernah memanggil bank |
| Boundary | Bersih — tidak ada import subpackage lintas modul (diverifikasi grep); `internal/ledger/events` adalah satu-satunya subpackage yang boleh diimport konsumen (K4); policy↔ledger bertemu hanya lewat interface struktural |

Kesimpulan audit: **payin, payout, vendor adapter, dan fraud service belum eksis dalam bentuk apapun** — yang ada baru ledger core + surface API internal yang kelak mereka panggil. Ini justru posisi ideal: modul-modul baru bisa lahir langsung dengan boundary service-grade, tanpa memikul utang refactor.

---

## Peta Topologi Target: 7 Service ↔ Modul Monolith

Setiap baris = satu calon service. Kolom "Saat split" adalah SATU-SATUNYA hal yang berubah ketika service dipisah — kalau kolom itu terasa mahal, boundary-nya salah dan harus diperbaiki SEKARANG selagi masih monolith.

| # | Future service | Ekspos | Modul monolith | Tabel (prefix) | Komunikasi ke ledger | Saat split |
|---|---|---|---|---|---|---|
| 1 | **Ledger** | internal only | `internal/ledger` (ada, matang) | unprefixed (grandfathered, [01](01-target-architecture.md) rule 4) | — | Facade jadi internal HTTP API — **router internal `:8081` SUDAH merupakan API itu**; konsumen in-proc pindah ke HTTP client |
| 2 | **Payin** | internal only (receiver webhook = edge tipis, lihat K-T1) | `internal/payin` (baru — [22](22-phase4a-payin-vendorgw.md)) | `payin_webhook_events` | Facade call `ledger.Post` (money_in), `idempotency_key`=vendor ref, `idempotency_scope`=`payin:<vendor>`, metadata `gateway`+`external_ref` (→ recon CSV existing langsung kompatibel, kolom sudah dipersist per K5) | `ledger.Post` → HTTP client ke internal API ledger; tabel `payin_*` carve-out; webhook receiver jadi edge service-nya |
| 3 | **Payout** | internal only | `internal/payout` (baru — [23](23-phase4b-payout-orchestration.md)) | `payout_requests`, `payout_vendor_calls` | Facade calls: `withdraw_initiate` (hold) → vendor → `withdraw_settle`/`withdraw_cancel` dengan `ReferenceID` — guard atomik `closed_by_tx_id` (K3) **sudah** membuat double-settle/settle-after-cancel mustahil; payout adalah persis "orchestrator dengan bug retry" yang diantisipasi temuan N3 | Sama seperti payin |
| 4 | **Vendor adapters** | — (library, bukan service) | `internal/vendorgw` (interface + registry) + subpackage per vendor: `internal/vendorgw/mockvendor` dulu | tidak ada (adapter stateless; state milik payin/payout) | **Tidak pernah menyentuh ledger** — hanya dipanggil payin/payout | Ikut pindah bersama payin/payout sebagai library; jadi sidecar per-vendor hanya kalau isolasi menuntut — keputusan deployment, cermin D9 |
| 5 | **Fraud** | internal only | **TETAP `internal/ledger/screening`** (K-T4) | `screening_events` tetap milik ledger sampai split | Seam sinkron = `processors.PrePostHook` (sudah ada, fail-open); seam asinkron = konsumsi `ledger.transaction.posted.v1` dari `internal/ledger/events` | Implementasi hook → HTTP client (timeout ketat, fail-open); fraud dapat DB sendiri; `screening_events` bermigrasi saat itu |
| 6 | **Internal admin** | internal only | **TIDAK ada modul baru** (K-T7) — listener internal `:8081` ADALAH kontrak API admin service masa depan | — | — | Admin service = BFF yang memanggil internal API tiap service; kontraknya = inventori route internal hari ini (dibekukan di [24](24-extraction-playbook.md)) |
| 7 | **User-facing** | **PUBLIC** | Public router `:8080` + `internal/auth` masa depan ([01](01-target-architecture.md) D12, tidak berubah) | `auth_*` nanti | via public router (sudah policy-checked + rate-limited) | Public router → user-facing/BFF service; auth module ikut ke sana |

Diagram alur uang setelah 22+23 selesai (masih satu binary):

```
                      internet
                         │
      ┌──────────────────┼──────────────────────┐
      │ :8080 public     │                      │
      │  /api/v1/…       │  /webhooks/{vendor}  │   ← K-T1: signature auth, bukan JWT
      ▼                  ▼                      │
 [user-facing surface]  [payin edge]            │
      │                  │                      │
      │            internal/payin ──────────────┤
      │                  │ ledger.Post(money_in)│
      ▼                  ▼                      │
 ┌──────────────── internal/ledger ─────────────┤   ← satu-satunya pemegang uang
 │  posting engine + PrePostHook(screening)     │
 └──────────────────────┬───────────────────────┘
                        │ withdraw_initiate/settle/cancel
                  internal/payout ──► internal/vendorgw/<vendor> ──► vendor API (outbound)
                        ▲                                              │
                        └── callback via /webhooks/{vendor} ◄──────────┘

 127.0.0.1:8081 internal: seluruh admin tooling (= kontrak admin service masa depan)
```

---

## Keputusan Desain yang Dikunci (K-T — jangan re-litigasi)

### K-T1. Webhook receiver di public listener, `POST /webhooks/{vendor}`
Route group baru di listener publik (`internal/handler/router.go`), **di LUAR chain JWT/CORS/RequireJSON**. Auth-nya adalah **verifikasi signature per-vendor** (via `vendorgw.PayinVerifier`), plus: raw-body capture (signature dihitung atas bytes mentah — JANGAN decode JSON dulu), body cap `http.MaxBytesReader` (~64KB — webhook payment kecil), dan rate limit per-vendor.

Rekonsiliasi dengan "payin tidak terekspos publik": yang tidak publik adalah **core API payin** (list/replay/query — semuanya di router internal). Webhook receiver mau tidak mau harus internet-reachable karena vendor yang memanggil; ia sengaja dibuat **edge setipis mungkin** — verify signature → dedup → post → status code, tanpa logika bisnis lain. Saat split, route group ini persis menjadi route edge/gateway yang mem-forward ke payin service internal.

**Listener ketiga khusus webhook DITOLAK**: di satu box tidak menambah isolasi nyata (tetap harus internet-reachable), hanya menambah beban ops. Keputusan pengguna 2026-07-12.

### K-T2. Payin memproses inline — vendor retry machinery ADALAH antriannya
Alur: verify signature → persist raw event ke `payin_webhook_events` (dedup `UNIQUE(vendor, vendor_event_id)`) → `ledger.Post` money_in → `200`. Bila posting gagal karena infra → **5xx** agar vendor melakukan redelivery (semua payment vendor punya retry-with-backoff); duplicate delivery aman dua lapis (dedup tabel payin + idempotency ledger). Event yang mati permanen ditangani **admin replay endpoint** (pola 12-T3 outbox replay).

**Tanpa worker/queue baru** — alasan yang sama dengan K5 (matcher recon sinkron): volume webhook = volume transaksi, satu INSERT + satu Post per event, box kecil tidak butuh buffer tambahan; antrian justru menambah state yang bisa hilang.

### K-T3. Disbursement TETAP di ledger; payout adalah hal yang berbeda
`internal/ledger/service/disbursement` (19-T2) adalah **primitive bulk-posting** milik ledger: CSV manifest → loop `Post`. Payout service adalah **orkestrasi per-item terhadap vendor** dengan state machine (`created→held→submitted→vendor_pending→settled|failed|cancelled`). Jangan dipindah atau di-merge: memindah disbursement ke payout memaksa payout mengimport internal ledger (boundary rusak), dan payout kelak bisa menawarkan batch dengan meng-iterate state machine-nya sendiri. Memindahkan sekarang = churn tanpa vendor yang didapat.

### K-T4. Fraud: dokumentasikan seam-nya, JANGAN pindahkan apa pun
`internal/ledger/screening` TETAP di tempatnya. Seam ekstraksi sudah eksis dan sudah benar:
- **Sinkron**: `processors.PrePostHook` — saat fraud jadi service, implementasi hook diganti HTTP client ber-timeout ketat; kebijakan fail-open pipeline (20-T1) tidak berubah, dan vendor-grade check yang butuh fail-closed mengembalikan `Block:true` sendiri (sudah didokumentasikan di doc comment interface).
- **Asinkron**: konsumsi `ledger.transaction.posted.v1` via `internal/ledger/events` — kontrak versioned yang memang dibuat untuk konsumen eksternal (K4).

Me-rename/memindah screening ke `internal/fraud` sekarang justru **memperburuk** boundary: modul fraud terpaksa import `internal/ledger/repository` (screeningRepo) dan wiring hook — pelanggaran rule 1. Tabel `screening_events` bermigrasi ke DB fraud **saat split**, bukan sekarang.

### K-T5. Boundary enforcement dapat gigi: CI check + konvensi prefix tabel
1. **`boundary_test.go`** (root repo, test Go murni, jalan via `make test` — tanpa tooling/linter plugin baru) menegakkan: (a) tidak ada package di luar `internal/<mod>` yang import `internal/<mod>/<sub>` — kecuali `internal/<mod>/events` (generalisasi K4); (b) `pkg/*` tidak pernah import `internal/*`; (c) `internal/payin` dan `internal/payout` tidak saling import (komunikasi antar keduanya, bila kelak perlu, lewat event — bukan facade, supaya split tidak menciptakan rantai dependency runtime).
2. **Konvensi prefix tabel per modul** (penegasan [01] rule 4): `payin_*`, `payout_*`, `auth_*`, dst. Tabel ledger tetap unprefixed (grandfathered). Prefix = unit carve-out saat split DB.
3. **Role DB per-service DITUNDA** ke saat extraction ([24](24-extraction-playbook.md)) — `app_service`/`app_readonly` existing cukup selama satu binary; menambah role per modul sekarang = kompleksitas grant tanpa penegakan nyata (semua modul tetap satu proses, satu pool).

**Temuan saat check pertama kali dijalankan (2026-07-12)**: pelanggaran pre-existing — enam package `pkg/*` (database, cache, messaging, logger, middleware) mengimport `internal/config` untuk tipe struct config-nya, melanggar rule 3 doc 01 sejak awal proyek. Di-grandfather eksplisit di `boundary_test.go` (entry tunggal, dilarang menambah) supaya check bisa mendarat tanpa refactor menumpang. **Tech debt**: perbaikannya = tiap `pkg/<x>` mendefinisikan struct config-nya sendiri, `cmd/server/main.go` memetakan dari `internal/config` — WAJIB dibereskan sebelum extraction service pertama (pkg adalah lapisan library bersama yang di-vendor setiap service).

### K-T6. Kontrak vendor adapter: generic-first, mock dulu
- `vendorgw.PayinVerifier`: `Verify(headers, rawBody) error` + parse ke **`PayinEvent` ternormalisasi** (vendor, vendorEventID, externalRef, amount minor-unit, currency, userID/VA mapping, occurredAt) — payin module tidak pernah melihat format mentah vendor.
- `vendorgw.PayoutProvider`: outbound call dengan **idempotency key = payout_request ID**, timeout eksplisit, bounded retry; hasil ternormalisasi `PayoutResult`.
- **`Registry` config-driven**: vendor terdaftar via env/config; menambah vendor riil = satu subpackage baru + satu entry registry, **nol perubahan** di payin/payout.
- **`mockvendor` dulu** (keputusan pengguna 2026-07-12 — belum ada akun vendor riil): HMAC signature sederhana, mode uji instant-settle / delayed / fail / duplicate-delivery / bad-signature. Vendor Indonesia riil (Midtrans SHA512-concat, Xendit callback-token, dst.) menyusul sebagai subpackage saat akunnya ada — interface sudah menampung variasi skema signature karena `Verify` menerima headers+rawBody mentah.

### K-T7. Internal admin: bekukan kontrak, jangan bikin modul
Router internal `:8081` **adalah** API yang kelak dipanggil admin service (BFF). Yang dilakukan sekarang bukan membuat `internal/admin`, melainkan **menginventarisasi dan membekukan kontrak route internal** di [24](24-extraction-playbook.md) — perubahan route internal setelah itu diperlakukan seperti perubahan kontrak API (sadar-kompatibilitas), bukan refactor bebas.

---

## Extraction Triggers — kapan benar-benar split (ukur, jangan feeling)

Split sebuah modul menjadi service HANYA ketika minimal satu terpenuhi:

1. **Deploy cadence menyakitkan**: modul X butuh rilis jauh lebih sering dari ledger, dan setiap rilis me-restart posting pipeline (mis. tambah vendor baru tiap minggu → payin/vendorgw split duluan — kandidat pertama yang paling masuk akal).
2. **Blast radius**: insiden di modul X (mis. vendor SDK memory leak) mengganggu posting ledger — bukti nyata dari metrics/incident, bukan kekhawatiran.
3. **Skala asimetris**: beban webhook/vendor call butuh replika lebih banyak daripada ledger (ledger dibatasi Postgres, payin dibatasi network I/O).
4. **Tim >1**: kepemilikan kode per tim menuntut siklus rilis independen.
5. **Compliance/isolasi**: fraud service butuh data/model yang aksesnya harus terpisah secara organisasi.

Sebelum salah satu terpenuhi: split hanya menambah biaya (network hop di jalur uang, dua deploy, dua on-call surface, distributed tracing wajib) tanpa keuntungan. Playbook teknisnya siap di [24](24-extraction-playbook.md) supaya saat trigger terpenuhi, eksekusinya tinggal mengikuti checklist.

---

## Anti-Goals — yang SENGAJA tidak dilakukan sekarang

Tulis sekali di sini supaya tidak diperdebatkan ulang di 22/23:

- **Tanpa gRPC/protobuf** — komunikasi antar modul = facade call (in-proc) atau event; saat split, internal HTTP + JSON (kontrak sudah ada di router internal) lebih dari cukup untuk skala ini.
- **Tanpa DB/schema terpisah** — satu Postgres, satu schema, satu timeline migrasi; prefix tabel adalah persiapan carve-out yang cukup.
- **Tanpa split binary** — D9 tetap; worker & modul hidup sebagai goroutine dalam satu proses.
- **Tanpa k8s/service mesh/API gateway** — satu box kecil, docker-compose.
- **Tanpa integrasi vendor riil sebelum akun vendor ada** — mockvendor di belakang interface yang sama; vendor riil pertama = "satu entry registry baru".
- **Tanpa queue/worker baru untuk webhook** — K-T2; redelivery vendor + admin replay endpoint sudah cukup.
- **Tanpa modul `internal/admin`** — K-T7.
- **Tanpa rename/pindah screening** — K-T4.
- **Tanpa role DB per-service** — K-T5 poin 3.
- **Tanpa payment intents (VA/QRIS pending flow) di iterasi pertama payin** — settled-webhook-only dulu (keputusan pengguna 2026-07-12); intents adalah iterasi berikutnya di atas fondasi yang sama (`payin_intents` menyusul, `payin_webhook_events` tidak berubah).

---

## Urutan Eksekusi

1. **[22](22-phase4a-payin-vendorgw.md)** — vendorgw (interface+registry+mockvendor) + payin module + webhook receiver + admin endpoints. Prasyarat: dokumen ini.
2. **[23](23-phase4b-payout-orchestration.md)** — payout state machine + PayoutProvider outbound + orkestrasi settle/cancel. Prasyarat: 22 (reuse webhook receiver & registry).
3. **[24](24-extraction-playbook.md)** — referensi; boleh dibaca kapan saja, dieksekusi hanya saat extraction trigger terpenuhi.

CI boundary check (K-T5 poin 1) dikerjakan **bersamaan dengan dokumen ini** (sudah ada sebelum modul baru lahir, sehingga payin/payout lahir di bawah penegakan).

Aturan verifikasi [09](09-hardening-review.md) berlaku penuh untuk 22/23: semua jalur yang menyentuh posting → integration test wajib (testcontainers), smoke test curl, dan chaos test bila pipeline berubah.
