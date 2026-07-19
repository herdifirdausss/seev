# 34 — Phase 6i: Verifikasi full-stack — e2e, chaos, docs

> Baca master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md). Prasyarat: doc 33 selesai.

## Konteks

Fase penutup wajib: membentuk ulang tiga script verifikasi live menjadi bentuk final multi-service, menjalankan seluruh chaos suite di topologi baru, dan merapikan dokumentasi. Setelah fase ini rangkaian split dinyatakan SELESAI (doc 35 opsional).

## T1 — `business-e2e.sh` bentuk final

### Langkah
1. Boot ENAM binary host via `scripts/lib.sh start_services` (ledger duluan).
2. Journey lengkap: register+login (auth-service) → setup routing rule topup & payout + fee rules global/per-user (admin API) → topup via webhook signed (gateway→payin→ledger) → transfer P2P dengan fee per-user → notifikasi sampai (gateway consumer) → payout ter-route + fee-on-settle → cancel refund penuh tanpa fee → admin surfaces hidup (recon list, outbox dead list, fraud events, fee rules) → tiga assertion integritas ledger (`assert_ledger_balanced`, `assert_no_inconsistent_projections`, `assert_no_stuck_pending_transactions`) terhadap `seev_ledger`.

### DoD
- [x] Satu perintah membuktikan seluruh bisnis jalan di topologi enam service.

### Hasil
Selesai. `business-e2e.sh` sudah dalam bentuk final sejak doc 33 T4 (enam
binary host, routing DB-driven, fee per-user, notifikasi, tiga assertion
integritas). Ditambahkan dua cek admin surface yang belum ada: `GET
/api/v1/admin/ledger/fee-rules` (membuktikan pricing yang di-seed section 1
terlihat operator tanpa SQL) dan `GET /api/v1/admin/fraud/events` di
fraud-service (reachable, admin-gated). `json_field`/`await_notification`
dipindah dari duplikat lokal di `business-e2e.sh` ke `scripts/lib.sh` —
single source of truth, dipakai juga oleh perbaikan T2 di bawah. Fresh-volume
run penuh: `FULL BUSINESS JOURNEY PASSED`, termasuk kedua cek admin surface
baru.

## T2 — `chaos-test.sh` multi-service

### Langkah
1. Skenario final (adaptasi existing + baru): (a) kill -9 ledger-service mid-payout → resume heal (dari doc 30 T6); (b) stop RabbitMQ → posting tetap 2xx, outbox menumpuk, restart → relay drain → notifikasi sampai; (c) fraud-service down → posting fail-open; (d) payin-service down → webhook 503 → restart → redelivery heal; (e) skenario Redis-down & Postgres-restart existing tetap dijalankan.
2. SEMUA skenario diakhiri `assert_ledger_balanced` = 0 baris.

### DoD
- [x] Tidak ada skenario kegagalan satu-service yang menghilangkan/menggandakan uang.

### Hasil
Selesai. `chaos-test.sh all` hijau bersih dua kali berturut-turut dari proses
yang benar-benar bersih. Skenario 5 (payout crash-mid-flight) diadaptasi ke
ledger-service (kill point tambahan setelah hold, sebelum settle); skenario 6
(payin down → 503 → redelivery) dan 7 (fraud down fail-open + block-mode)
baru ditulis untuk topologi enam service. Setiap skenario diakhiri
`assert_ledger_balanced`/`v_account_balance_audit`, dan keduanya HIJAU di
setiap run sepanjang investigasi ini — termasuk semua run yang gagal karena
bug harness di bawah. Empat bug nyata ditemukan dan diperbaiki:

1. **`stop_server_gracefully` hanya mematikan gateway** (`scripts/lib.sh`):
   setiap scenario memanggilnya mengharap shutdown enam proses penuh, tapi ia
   hanya membunuh gateway. Lima proses lain bocor ke scenario berikutnya,
   memicu `bind: address already in use`, dan `wait_for_service_up` yang
   hanya polling HTTP `/health` salah lapor proses lama yang masih hidup
   sebagai instance baru yang "up". Diperbaiki: `stop_gateway_only()`
   diekstrak, `stop_services()` memanggil keenamnya, `stop_server_gracefully`
   jadi alias penuh ke `stop_services`.
2. **`wait "$pid" 2>/dev/null || true` bukan wait sungguhan**: setiap proses
   dibuat via `nohup bin & echo $! >pidfile` di dalam subshell `( ... )` —
   begitu subshell keluar, pid itu BUKAN child dari shell pemanggil, jadi
   builtin `wait` bash langsung kembali tanpa benar-benar menunggu proses
   mati. Akibatnya `stop_services()` kembali SEBELUM proses benar-benar
   berhenti (graceful shutdown bisa sampai 30 detik), dan `start_services()`
   scenario berikutnya bisa balapan port dengan proses yang masih sekarat.
   Diperbaiki: `wait_for_pid_gone()` polling `kill -0` sungguhan, eskalasi ke
   SIGKILL jika 10 detik pertama belum cukup.
3. **Akar masalah sesungguhnya — scenario 1 me-restart ENAM proses padahal
   cuma satu yang mati**: `kill_server_hard` (peninggalan monolith) hanya
   membunuh gateway, tapi baris restart-nya memanggil `start_server` (alias
   `start_services`, start ENAM proses). Kelima proses lain (ledger, auth,
   payin, payout, fraud) masih hidup dari start awal scenario 1, jadi
   `start_ledger_service` dkk pada restart itu GAGAL bind port, langsung
   exit — TAPI tetap menimpa file PID dengan pid barunya yang sudah mati.
   Sejak titik itu, SETIAP stop/kill di scenario manapun sepanjang sisa run
   `all` menyasar pid yang salah (sudah mati), sementara proses asli
   scenario 1 terus hidup tanpa terlihat dan menjawab request scenario
   5/6/7 — persis menjelaskan kenapa payout tetap "settled" saat ledger
   "mati", payin tetap 200 saat "mati", dan fraud hook tidak pernah ter-log
   (proses fraud asli scenario 1, yang benar-benar mati kena kill, bukan yang
   dites). Diperbaiki: baris restart scenario 1 (`scripts/chaos-test.sh`)
   diganti dari `start_server` ke `start_gateway` — hanya proses yang benar
   benar mati yang di-restart.
4. **`cmd/payout-service/main.go` tidak menghormati `REDIS_ENABLED=false`**
   (bug produk asli, bukan test harness): tidak seperti ledger-service, ia
   memanggil `cache.New` tanpa syarat, jadi restart operator dengan
   `REDIS_ENABLED=false` (mitigasi resmi docs/plan/12 T1) tetap gagal keras
   kalau Redis benar-benar down — padahal `payout.NewModule` sudah mendukung
   `redisClient == nil` (fallback in-memory lock). Diperbaiki: main.go
   payout-service sekarang mem-bungkus `cache.New` dengan `if
   cfg.Redis.Enabled` persis pola ledger-service. `fraud-service` SENGAJA
   TIDAK diberi jalur ini — velocity counter fraud memang Redis-only by
   design (tidak ada fallback in-memory), jadi scenario 4's restart-with-
   REDIS_ENABLED=false sekarang sengaja mengecualikan fraud-service.
5. **Lock resume-job payout ber-TTL 5 menit** (`internal/payout/worker/resume.go`):
   TTL lock Redis job = `job timeout + buffer`, dan scheduler generik
   default 5 menit. Kalau instance yang memegang lock mati (persis skenario
   chaos ini — `kill -9` payout-service berulang), lock basi itu memblokir
   SEMUA instance lain mengambil giliran resume sampai 5 menit — jauh lebih
   lama dari jendela `sleep 65` di scenario 5, membuat resume job tidak
   pernah jalan sama sekali untuk seluruh sisa scenario. Diperbaiki:
   `ResumeJob.Start` memberi `scheduler.WithJobTimeout(30*time.Second)`
   khusus untuk job "payout-resume" (tidak mengubah default scheduler lain)
   — cukup longgar untuk resume pass normal, tapi window self-heal jauh
   lebih dekat ke kadensi cron 1-menitnya sendiri.

Kelima bug di atas diverifikasi dengan `go build ./...`, `go vet ./...`, `go
vet -tags=integration ./...`, `make lint`, `make test` (semua hijau), lalu
`chaos-test.sh all` dijalankan lima kali berturut-turut dari proses bersih:
4/5 hijau bersih, 1/5 gagal pada assertion timing non-uang (bukan
`assert_ledger_balanced`/`v_account_balance_audit`, keduanya tetap hijau
di run itu juga) yang tidak reproduce pada percobaan berikutnya — sejalan
dengan sifat lingkungan (build+start enam binary + migrasi berulang di
laptop dev, bukan CI terisolasi) yang sudah dicatat di header script sejak
awal. Tidak ada indikasi kehilangan/penggandaan uang di run manapun.
`business-e2e.sh` juga hijau dari volume Docker benar-benar baru.

## T3 — Smoke full-container

### Langkah
1. `docker compose --profile app up -d` → tunggu enam container healthy → satu round-trip webhook topup end-to-end → `down`. (Ingat gotcha #14: jangan bersamaan testcontainers.)

### DoD
- [x] Mode containerized terbukti, bukan cuma binary host.

### Hasil
Selesai. `docker compose --profile app up --build -d` — sembilan container
(tiga infra + enam service) semuanya `healthy`. Round-trip dibuktikan lewat
gateway publik (`:8080`), bukan port internal binary-host: register + login
user baru via auth-service (`:8082`) → buat topup intent via gateway
(routing DB-driven memakai rule fallback yang sudah di-seed migrasi) →
webhook mockvendor bertanda tangan HMAC ke `POST /webhooks/mockvendor`
gateway → `200 {"received":true}`. Diverifikasi langsung di Postgres
container: `payin_topup_intents.status='settled'` dan akun cash ledger
user tersebut naik dari 0 ke 75000 — bukti byte-exact bahwa jalur gateway→
payin→ledger yang sama dengan mode binary-host benar-benar berjalan di
topologi container. `docker compose --profile app down` bersih setelahnya.

## T4 — Dokumentasi final

### Langkah
1. Finalisasi PROJECT_GUIDE.md/README/docs/plan/README (status ✅ untuk 26–34), runbook singkat "service X down — apa yang terjadi & cara pulih" per service.
2. Daftar future work resmi: admin BFF, mTLS antar service, outbox payout untuk event status, caching fee-rule/routing-rule, real vendor adapter.

### DoD
- [x] Rangkaian docs 26–34 tertutup rapi; future work terdokumentasi.

### Hasil
Selesai. `docs/plan/README.md` — doc 34 ditandai ✅ done. `PROJECT_GUIDE.md` — bagian
baru "Runbook: one service down" (satu paragraf per service: apa yang gagal
saat mati, apa yang tetap aman, cara pulih) ditambahkan sebelum "Known future
work"; daftar future work diperluas dengan "real vendor adapter" (pengganti
`internal/vendorgw/mockvendor`) dan caching routing-rule digabung ke entri
fee-rule yang sudah ada. `README.md` sudah mencerminkan arsitektur enam
service sejak fase sebelumnya, tidak perlu perubahan.

---

## Verifikasi akhir dokumen
T1+T2+T3 hijau + gate standar master doc 26 = rangkaian split SELESAI. [35-phase6j-kubernetes.md](35-phase6j-kubernetes.md) opsional.
