# 43 — Track A1: Observability Naik Kelas — Dashboards, SLO, Alerting, Log & Trace

> Lahir dari track **A1 ★** di [42-long-term-roadmap.md](42-long-term-roadmap.md) §4.
> Self-contained: eksekutor TIDAK perlu membaca dokumen lain untuk mengerjakan,
> tapi WAJIB mematuhi [PROJECT_GUIDE.md](../../PROJECT_GUIDE.md) (boundary rules, hard rules)
> dan bagian "Constraint untuk eksekutor" di bawah.

## Bukti trigger (syarat doc 42 §2 poin 1)

Trigger A1 bertipe campuran; jalur yang dipakai adalah **trigger belajar =
keputusan sadar + dependensi hijau** (bukan menunggu insiden debugging >30
menit terjadi dulu):

- **Keputusan sadar**: pemilik repo memilih A1 sebagai track pertama pasca-MVP
  (2026-07-17) — A1 adalah peringkat #4 nilai belajar di doc 42 §7 dan
  prasyarat praktis banyak track lain (A3, A7, B0 semuanya butuh dashboard
  untuk membaca hasil).
- **Dependensi hijau** (semua ✅):
  - [36](36-phase7a-request-tracing.md)–[41](41-phase7f-mvp-acceptance.md)
    selesai — `request_id` sudah jadi tulang punggung korelasi lintas
    HTTP/gRPC/AMQP/DB rows.
  - OTel opt-in dari 12-T5 sudah ada (`OTEL_EXPORTER_OTLP_ENDPOINT`,
    `TracingConfig` di `internal/config/config.go`).
  - `/metrics` Prometheus sudah diekspos keenam service (listener
    internal/admin, tidak pernah publik).
  - 5 runbooks operasional sudah ada di `docs/runbooks/`.

## Anti-scope (disalin dari track A1, doc 42 §4 — WAJIB dihormati)

- **Bukan APM berbayar.** Semua komponen open-source, jalan lokal di compose.
- **Tidak menambah metric cardinality per-user.** Dilarang label
  `user_id`/`account_id`/`idempotency_key`/path mentah pada metric apa pun.
- Turunan yang dikunci dokumen ini: **OTLP hanya untuk traces.** Metrics tetap
  Prometheus scrape pull-based; logs tetap promtail → Loki. TIDAK ada OTel
  metrics/logs pipeline, TIDAK ada otel-collector.
- **Tidak ada migrasi database** di track ini (butir migrasi checklist §10
  doc 42 = N/A; observability tidak menyentuh skema).

## Keputusan desain (dikunci — eksekutor DILARANG mengubah)

- **K1 — Stack 4 kontainer + 1 shipper, profile `observability`.**
  `prometheus`, `grafana`, `loki`, `tempo`, `promtail` — semuanya
  `profiles: ["observability"]` di `docker-compose.yml` (BUKAN default;
  budget RAM 4 GB Docker Desktop). Seluruh konfigurasi provisioning-as-code
  di direktori baru `deploy/observability/`:
  ```
  deploy/observability/
    prometheus/prometheus.yml
    tempo/tempo.yaml
    loki/loki.yaml
    promtail/promtail.yaml
    grafana/provisioning/datasources/datasources.yaml
    grafana/provisioning/dashboards/dashboards.yaml      (file provider)
    grafana/provisioning/alerting/slo-rules.yaml
    grafana/dashboards/*.json                            (7 dashboard)
  ```
  Constraint RAM tambahan: profile `observability` TIDAK boleh dijalankan
  bersamaan dengan suite testcontainers integration (perluasan aturan
  PROJECT_GUIDE.md yang sudah melarang app profile + Jaeger + testcontainers).
- **K2 — Jaeger DIHAPUS, Tempo menggantikan.** Blok service `jaeger` di
  `docker-compose.yml` (saat ini sekitar baris 326–343) dihapus. Tempo
  listen **OTLP gRPC `:4317`** (port host sama), sehingga env
  `OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317` yang existing TIDAK berubah.
  UI trace = Grafana Explore (datasource Tempo); UI Jaeger 16686 hilang.
- **K3 — Satu implementasi tracing di `pkg/tracing`.**
  `cmd/gateway/tracing.go` dan `cmd/ledger-service/tracing.go` (duplikat)
  dihapus, isinya dipindah ke package baru `pkg/tracing` dengan signature:
  `tracing.Setup(ctx, serviceName, otlpEndpoint string) (shutdown func(context.Context) error, err error)`.
  Keenam `cmd/*` main memanggilnya dengan `serviceName` benar per service:
  `gateway`, `auth-service`, `ledger-service`, `payin-service`,
  `payout-service`, `fraud-service` — sekaligus MEMPERBAIKI bug existing:
  gateway hari ini salah memakai `ServiceName("seev")`
  (`cmd/gateway/tracing.go:47`). Perilaku 12-T5 dipertahankan: endpoint
  kosong = provider no-op, zero cost; kegagalan setup non-fatal (log warn,
  service tetap start). "Default-on" artinya keenam service ter-wire, bukan
  exporter selalu aktif.
- **K4 — Instrumentasi otomatis HTTP + gRPC; AMQP JANGAN disentuh.**
  - HTTP: middleware baru `pkg/middleware.WithTracing()` membungkus
    `otelhttp` (`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`),
    dipasang TEPAT SETELAH `WithRequestID()` di setiap chain router publik &
    internal (gateway `internal/handler/router.go`, keenam `cmd/*/router.go`
    / `main.go` yang merakit `middleware.Chain(...)`). Span name = pattern
    route, bukan raw path.
  - gRPC: `otelgrpc` (`go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc`)
    sebagai `grpc.StatsHandler` di `pkg/grpcx/grpcx.go` — server di
    `NewServer`, client di `Dial`/`DialLazy`.
  - AMQP: SUDAH selesai penuh sejak 36-T4 (`pkg/messaging/publisher.go`
    inject W3C traceparent + span `rabbitmq.publish`; `consumer.go` extract +
    span `rabbitmq.consume`; CorrelationId = request_id) — DILARANG diubah.
- **K5 — RED metrics + 2 gauge bisnis, aturan cardinality keras.**
  - HTTP: middleware baru `pkg/middleware.WithHTTPMetrics(service string)` →
    histogram `http_request_duration_seconds{service, method, route, code}`.
    Label `route` WAJIB pattern hasil `r.Pattern` (Go 1.22 net/http) — kalau
    kosong (404/no match) pakai literal `"unmatched"`. DILARANG raw
    `r.URL.Path`.
  - gRPC: interceptor di `pkg/grpcx` → histogram
    `grpc_server_handling_seconds{service, grpc_method, grpc_code}`.
  - Gauge bisnis baru:
    - `vendorgw_breaker_state{vendor}` — 0=closed, 1=half-open, 2=open;
      di-set dari `internal/vendorgw/breaker.go` (`HealthTracker` sudah punya
      state per vendor + `Snapshot()`).
    - `payout_stuck_total{status}` — jumlah payout berumur > threshold resume
      di status `held|submitted|vendor_pending`; di-refresh setiap tick
      `internal/payout/worker/resume.go` (`ResumeJob`).
  - Register memakai pola repo existing: `promauto` package-level (contoh:
    `internal/ledger/worker/metrics.go`).
- **K6 — Tiga SLO + burn-rate multi-window.** Definisi dikunci:
  | SLO | SLI (PromQL basis) | Target |
  |---|---|---|
  | Availability posting | rasio `ledger_transactions_total{status="posted"}` terhadap total | 99.5% / 30 hari |
  | Latensi webhook→posting | p95 `http_request_duration_seconds{service="gateway", route="POST /webhooks/{vendor}"}` (route pattern persis dari `internal/handler/router.go:101`) | p95 < 2s |
  | Lag notifikasi | `ledger_outbox_pending` + p95 `rabbitmq_consume_handler_duration_seconds` | outbox_pending < 100 selama 5m; p95 consume < 1s |
  Burn-rate alerting dua pasang window pola Google SRE Workbook:
  **fast burn** 5m & 1h (burn rate > 14.4 → page) dan **slow burn** 30m & 6h
  (burn rate > 6 → ticket). Recording rules ditaruh di
  `deploy/observability/prometheus/prometheus.yml` (blok `rule_files` +
  file rules terpisah `deploy/observability/prometheus/rules/slo.yml`).
- **K7 — Grafana unified alerting via file provisioning; setiap alert
  menunjuk runbook.** TIDAK ada Alertmanager. Alert rules di
  `deploy/observability/grafana/provisioning/alerting/slo-rules.yaml`,
  masing-masing WAJIB punya annotation `runbook` berisi path repo. Mapping
  minimum (tabel ini disalin ke dashboard/alert):
  | Alert | Runbook |
  |---|---|
  | Burn-rate availability posting (fast/slow) | `docs/runbooks/ledger-integrity-alert.md` |
  | `ledger_verification_discrepancies_total` naik | `docs/runbooks/ledger-integrity-alert.md` |
  | Outbox lag / pending tinggi | `docs/runbooks/ledger-integrity-alert.md` |
  | Payout stuck > 0 melewati threshold waktu | `docs/runbooks/reconciliation.md` |
  | Breaker open berkepanjangan (`vendorgw_breaker_state == 2` selama 10m) | `docs/runbooks/reconciliation.md` |
  | Latensi webhook→posting melanggar SLO | `docs/runbooks/reconciliation.md` |
  (fx-position.md, dr-restore-drill.md, regulatory-reporting.md tidak punya
  alert otomatis di fase ini — dicatat sebagai gap sadar, bukan kelalaian.)
- **K8 — Log terpusat: promtail docker_sd → Loki.** Promtail scrape stdout
  kontainer via Docker socket (`docker_sd_configs`), label minimum:
  `compose_service`, `container`. Keenam service app profile di compose
  di-set `LOG_FORMAT: json` (pipeline stage `json` di promtail mengangkat
  `level`, `service`, `request_id`). Grafana datasource Loki diberi derived
  fields: `request_id` (link ke query Loki lintas service) dan `trace_id`
  (link ke Tempo). Log binari host-run (smoke/chaos `WORK_DIR/*.log`) DI
  LUAR scope — tetap file lokal.

## Task

Setiap task: kerjakan Langkah berurutan, tulis Test wajib, penuhi DoD, isi
bagian `### Hasil` setelah selesai (pola doc 36–41).

### T1 — Compose profile `observability` + provisioning skeleton

**Langkah**
1. Hapus blok service `jaeger` dari `docker-compose.yml`.
2. Tambah lima service baru ber-profile `["observability"]`:
   `prometheus` (host 9090), `grafana` (host 3000), `loki` (host 3100),
   `tempo` (host 3200 untuk API + 4317 OTLP gRPC), `promtail` (tanpa port
   host; mount `/var/run/docker.sock` read-only + `/var/lib/docker/containers`
   read-only). Pin versi image eksplisit (JANGAN `:latest` — konvensi repo
   hanya mengizinkan `:latest` untuk dev-viewer, dan alasan itu ikut hilang
   bersama jaeger).
3. Buat seluruh file konfigurasi di `deploy/observability/` sesuai K1:
   - `prometheus/prometheus.yml`: scrape 6 target — `gateway-service:8081`,
     `auth-service:8083`, `ledger-service:8091`, `payin-service:8092`,
     `payout-service:8093`, `fraud-service:8094` (nama service compose +
     port listener internal/admin DI DALAM network compose), interval 15s,
     `metrics_path: /metrics`; blok `rule_files` menunjuk `rules/slo.yml`
     (file rules boleh berisi placeholder kosong sampai T5).
   - `tempo/tempo.yaml`: receiver OTLP gRPC :4317, storage lokal
     (`/var/tempo`), retention pendek (48h).
   - `loki/loki.yaml`: single-binary mode, filesystem storage, retention 48h.
   - `promtail/promtail.yaml`: `docker_sd_configs` + relabel `compose_service`
     dari label container compose; pipeline stage `json` (lihat K8).
   - `grafana/provisioning/datasources/datasources.yaml`: Prometheus
     (default), Loki (dengan derived fields K8), Tempo (dengan
     `tracesToLogs`/link balik ke Loki).
   - `grafana/provisioning/dashboards/dashboards.yaml`: file provider yang
     memuat `deploy/observability/grafana/dashboards/*.json`.
4. Tambah named volume untuk data prometheus/loki/tempo/grafana (ikuti pola
   `seev_*_data` existing di bagian `volumes:`).
5. Update komentar header compose + `README.md` root (satu paragraf cara
   menjalankan profile observability).

**Test wajib**
- `docker compose --profile app --profile observability up --build -d` lalu:
  Prometheus `http://localhost:9090/targets` → 6/6 target UP;
  Grafana `http://localhost:3000` login default → ketiga datasource
  berstatus OK (test via UI atau `GET /api/datasources` + health);
  `logcli`/Grafana Explore query `{compose_service="ledger-service"}`
  mengembalikan baris log.
- `docker compose config` valid (exit 0) dengan dan tanpa profile.

**DoD**
- Enam target Prometheus UP; Grafana hidup dengan 3 datasource provisioned;
  Loki menerima log semua kontainer app profile; Tempo siap menerima OTLP di
  :4317; jaeger hilang dari compose; tidak ada perubahan kode Go di task ini.

### T2 — `pkg/tracing` + OTel default-on enam service + otelhttp/otelgrpc

**Langkah**
1. Buat `pkg/tracing/tracing.go` — pindahkan isi `cmd/ledger-service/tracing.go`
   (versi yang service.name-nya benar) menjadi
   `Setup(ctx, serviceName, endpoint)`; hapus kedua file `tracing.go` lama.
2. Wire keenam main (`cmd/gateway`, `cmd/auth-service`, `cmd/ledger-service`,
   `cmd/payin-service`, `cmd/payout-service`, `cmd/fraud-service`):
   panggil `tracing.Setup` dengan nama service masing-masing (K3), simpan
   shutdown func ke jalur cleanup existing masing-masing main. Config:
   pakai `cfg.Tracing.OTLPEndpoint` yang sudah ada — per-service loader
   (`LoadAuthService` dll di `internal/config/config.go`) harus ikut
   membaca `OTEL_EXPORTER_OTLP_ENDPOINT` bila belum.
3. Buat `pkg/middleware.WithTracing()` (otelhttp) dan pasang tepat setelah
   `WithRequestID()` di semua perakitan `middleware.Chain(...)` (router
   publik + internal keenam service).
4. Tambah `otelgrpc.NewServerHandler()` sebagai `grpc.StatsHandler` di
   `pkg/grpcx.NewServer`, dan `otelgrpc.NewClientHandler()` di
   `dial`/`DialLazy`.
5. `go mod tidy` — dependensi baru hanya dua modul contrib otelhttp/otelgrpc.
6. Set `OTEL_EXPORTER_OTLP_ENDPOINT: tempo:4317` di keenam service app
   profile compose (di dalam network compose; host tetap `localhost:4317`).

**Test wajib**
- Unit: test middleware `WithTracing` menghasilkan span dengan nama pattern
  route (pakai `sdktrace` in-memory exporter); test grpcx interceptor chain
  masih lolos test existing.
- E2E trace: stack compose penuh (app+observability) → jalankan satu journey
  `scripts/business-e2e.sh` ATAU manual topup→transfer → di Grafana Explore
  (Tempo) tampak SATU trace menyambung minimal
  `gateway → grpc payin → rabbitmq.publish → rabbitmq.consume (notify)`,
  dan span ledger `ledger.Handle` existing menempel di trace HTTP-nya.
- `make test` + `go vet ./...` + `go vet -tags=integration ./...` hijau.

**DoD**
- Keenam binari punya TracerProvider dengan service.name benar (bug "seev"
  gateway fix); endpoint kosong tetap no-op (test: start tanpa env, tidak
  ada koneksi keluar); duplikasi `tracing.go` per-cmd hilang; HTTP+gRPC+AMQP
  tersambung dalam satu trace end-to-end.

**GATE fase 1 (setelah T2)**: `make verify-full` hijau penuh dari volume
bersih. Catatan: gate ini SEKALIGUS melunasi run `verify-full` refactor
repo-layer 2026-07-17 yang terputus sebelum selesai.

### T3 — RED metrics + gauge bisnis

**Langkah**
1. `pkg/middleware/metrics.go`: `WithHTTPMetrics(service string)` per K5;
   pasang di chain SETELAH `WithTracing()` di keenam service.
2. `pkg/grpcx`: histogram `grpc_server_handling_seconds` di
   `loggingInterceptor` atau interceptor baru (satu tempat, jangan dua).
3. `internal/vendorgw/breaker.go`: gauge `vendorgw_breaker_state{vendor}`
   di-update di setiap transisi state (atau di-refresh dari `Snapshot()`
   oleh pemanggil admin health existing — pilih satu, dokumentasikan).
4. `internal/payout/worker/resume.go`: di setiap tick, set
   `payout_stuck_total{status}` dari count query yang SUDAH dipakai resume
   job (jangan tambah query baru bila count sudah tersedia; kalau perlu
   query baru, lewat repository payout — bukan SQL inline di worker,
   konsisten dengan refactor repo-layer 2026-07-17).
5. Audit label: `grep` semua metric baru — pastikan tidak ada label
   user/account/path mentah (anti-scope).

**Test wajib**
- Unit: `WithHTTPMetrics` menghasilkan seri dengan `route` = pattern dan
  `"unmatched"` untuk 404; breaker gauge berubah saat state transisi
  (extend `internal/vendorgw/breaker_test.go`); resume job men-set gauge
  (extend test resume existing).
- `curl` `/metrics` tiap service di stack compose → seri baru muncul.
- `make test` hijau (termasuk `-race`).

**DoD**
- RED metrics tersedia di keenam service; dua gauge bisnis hidup; audit
  cardinality tercatat di bagian Hasil.

### T4 — Dashboards provisioning (7 dashboard)

**Langkah**
1. Buat 6 dashboard per-service (`gateway.json`, `auth.json`, `ledger.json`,
   `payin.json`, `payout.json`, `fraud.json`) — panel minimum: request rate,
   error rate, p50/p95/p99 latency (dari `http_request_duration_seconds` +
   `grpc_server_handling_seconds`), goroutines/mem (metric Go default),
   metric domain service itu (mis. `fraud_screening_findings_total` di
   fraud, `rabbitmq_*` di notify/gateway).
2. Buat `money.json` — "dashboard uang": posting rate & error
   (`ledger_transactions_total`), `ledger_post_duration_seconds`, outbox
   (`ledger_outbox_pending`, `ledger_outbox_dead_total`), verifier
   (`ledger_verification_discrepancies_total`), payout stuck
   (`payout_stuck_total`), breaker (`vendorgw_breaker_state`).
3. Semua JSON di-commit di `deploy/observability/grafana/dashboards/`;
   dashboard memuat link ke runbook terkait di panel description (mapping K7).

**Test wajib**
- Restart grafana kontainer → ketujuh dashboard muncul ter-provision (bukan
  dibuat manual via UI); setiap panel menampilkan data saat business-e2e
  dijalankan; tidak ada panel "No data" permanen (kecuali dicatat alasannya).

**DoD**
- 7 dashboard as-code, reproducible dari clone bersih; dashboard uang
  menampilkan kelima area sketsa A1.

**GATE fase 2 (setelah T4)**: `make verify-full` hijau penuh.

### T5 — SLO recording rules + burn-rate alerts + mapping runbook

**Langkah**
1. `deploy/observability/prometheus/rules/slo.yml`: recording rules SLI K6
   (error ratio posting, latency webhook, lag notifikasi) + burn-rate pada
   window 5m/30m/1h/6h.
2. `deploy/observability/grafana/provisioning/alerting/slo-rules.yaml`:
   alert rules Grafana unified alerting per K6/K7 — fast burn (page) & slow
   burn (ticket) per SLO + alert non-SLO (verifier discrepancies, outbox
   lag, payout stuck, breaker open) — SEMUA dengan annotation `runbook`.
3. Salin tabel mapping alert→runbook (K7) ke bagian Hasil + tambahkan
   catatan singkat di `docs/runbooks/ledger-integrity-alert.md` dan
   `docs/runbooks/reconciliation.md` bahwa alert kini datang dari Grafana
   (satu kalimat + nama alert, jangan tulis ulang runbook).

**Test wajib**
- `promtool check rules deploy/observability/prometheus/rules/slo.yml` lulus.
- Uji sintetis minimal DUA alert: (a) stop `fraud-service` + jalankan
  traffic → SLO availability tidak boleh false-positive (fail-open by
  design, dicatat); (b) suntik row discrepancy di DB dev (pola
  `chaos-test.sh` scenario verifier) ATAU set threshold sementara sangat
  rendah → alert firing di Grafana dengan annotation runbook benar, lalu
  kembalikan threshold.
- Screenshot/state alert dicatat di Hasil.

**DoD**
- Recording+alert rules as-code; setiap alert punya runbook annotation;
  minimal 2 alert terbukti firing & resolve di uji sintetis.

### T6 — Log terpusat berkorelasi request_id

**Langkah**
1. Set `LOG_FORMAT: json` untuk keenam service di compose app profile
   (`pkg/logger` sudah mendukung; tidak ada perubahan kode Go kecuali
   ternyata ada service yang belum meneruskan `LOG_FORMAT` ke
   `LoggerConfig` — cek `internal/config`).
2. Lengkapi pipeline promtail (T1) bila perlu: pastikan `request_id` dan
   `trace_id` terangkat sebagai field terindeks-query (bukan label
   high-cardinality — biarkan sebagai field JSON, cukup derived field
   Grafana).
3. Verifikasi derived fields Loki→Tempo dan Tempo→Loki dua arah.

**Test wajib**
- Jalankan `scripts/business-e2e.sh` terhadap stack compose penuh; ambil
  satu `request_id` dari respons; di Grafana Explore query
  `{job=~".+"} |= "<request_id>"` → baris log muncul dari MINIMAL 3 service
  berbeda dalam satu view; klik derived field → pindah ke trace Tempo yang
  sama.

**DoD**
- Satu request_id bisa diikuti lintas service di satu layar tanpa
  `docker logs` manual; klik-through log↔trace bekerja dua arah.

**GATE fase 3 (setelah T6, final)**: `make verify-full` hijau penuh +
checklist §10 doc 42 dicentang + update README/doc 42 (lihat Penutup).

## Constraint untuk eksekutor (baca sebelum mulai)

**Boleh**: memecah task jadi sub-langkah sendiri; memilih versi image pinned;
menyesuaikan detail konfigurasi YAML selama perilaku K1–K8 terpenuhi.
**Dilarang**: mengubah keputusan K1–K8; menyentuh hal-hal di daftar berikut.

Do-not-touch / aturan keras:
1. JANGAN reorder `execTransfer` di `internal/ledger/service/handle/service.go`
   (PROJECT_GUIDE.md hard rule #5). Instrumentasi HTTP/gRPC hidup di middleware,
   bukan di dalam service ini; span `ledger.Handle` existing dibiarkan.
2. `/metrics` TETAP hanya di listener internal/admin — jangan pernah
   menaruhnya di router publik (regresi keamanan doc 10-T6).
3. JANGAN tambah label metric ber-cardinality user (anti-scope). Route =
   pattern, bukan path.
4. JANGAN ubah `pkg/messaging` (AMQP tracing + CorrelationId sudah final
   sejak 36-T4).
5. Boundary rules `boundary_test.go` tetap berlaku; `pkg/` DILARANG import
   `internal/` (jadi `pkg/middleware`/`pkg/grpcx`/`pkg/tracing` tidak boleh
   menyentuh apa pun di `internal/`).
6. Port host yang SUDAH terpakai (jangan bentrok): 5433 (postgres), 6380
   (redis), 5672/15672 (rabbitmq), 8080/8081 (gateway), 8082/8083 (auth),
   8090/8091/9091 (ledger), 8092/9092 (payin), 8093/9093 (payout),
   8094/9094 (fraud). Port baru dokumen ini: 9090 (prometheus), 3000
   (grafana), 3100 (loki), 3200+4317 (tempo).
7. Budget RAM 4 GB Docker Desktop: profile observability terpisah; JANGAN
   jalankan bersamaan dengan suite testcontainers; dokumentasikan ini di
   README root.
8. Debugging script gate: SELALU `docker compose down -v` sebelum
   mempercayai run smoke/chaos (lihat PROJECT_GUIDE.md bagian Debugging).
9. Env var baru/berubah — tabel lengkap:
   | Env | Service | Nilai di compose | Catatan |
   |---|---|---|---|
   | `OTEL_EXPORTER_OTLP_ENDPOINT` | keenam service | `tempo:4317` | sudah ada di config loader; kosong = tracing off |
   | `LOG_FORMAT` | keenam service | `json` | sudah didukung `pkg/logger` |
10. File yang boleh dibuat/diubah per task sudah dieksplisitkan di Langkah
    masing-masing — di luar itu, berhenti dan tanyakan (Aturan Umum README
    #1: jangan improvisasi).

## Penutup (dikerjakan setelah GATE fase 3)

- [ ] Update `docs/plan/README.md`: status baris 43 → `✅ done`.
- [ ] Update `docs/plan/42-long-term-roadmap.md`: status track A1 →
      selesai via dokumen ini.
- [ ] Isi semua bagian `### Hasil` per task.
- [ ] Checklist §10 doc 42 lengkap dicentang di PR/commit terakhir.
