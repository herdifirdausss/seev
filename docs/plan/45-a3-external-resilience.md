# 45 — Track A3: Resiliensi Dependensi Eksternal

> **Status: SIAP DIEKSEKUSI BERTAHAP (2026-07-17).**
>
> Dokumen ini adalah indeks Plan 45. Seluruh isi plan sebelum review tetap
> disimpan utuh; hasil review keamanan dan efisiensi dipisahkan agar keputusan
> lama tidak hilang dan perbedaan semantics dapat dilihat dengan jelas.

## Dokumen

1. [45-1 — Scope asli lengkap](45-1-a3-original-complete.md)

   Salinan utuh Plan 45 sebelum review: baseline repository, K1–K7, outbox,
   breaker, hot-swap Redis, adapter Xendit, chaos scenario 9–11,
   observability, task T1–T5, constraint, dan Definition of Done.

2. [45-2 — Core execution hasil review](45-2-a3-core-execution-reviewed.md)

   Baseline implementasi yang telah dikoreksi agar lebih aman, efisien,
   vendor-neutral, serta dapat divalidasi sepenuhnya dengan komponen
   gratis/open-source lokal.

Plan ini lahir dari track **A3 ★** di
[42-long-term-roadmap.md](42-long-term-roadmap.md) dan melanjutkan invarian
anti-double-payout [Plan 40](40-phase7e-vendor-resilience.md).

## Aturan prioritas

- `45-1` adalah sumber inventaris requirement dan histori keputusan. Tidak ada
  requirement lama yang dihapus.
- `45-2` adalah sumber kebenaran untuk implementasi core dan mengungguli
  `45-1` hanya pada konflik semantics keamanan, durability, atau gate.
- Detail `45-1` yang tidak bertentangan dengan koreksi `45-2` tetap berlaku
  dan boleh diadopsi pada task terkait.
- Perubahan terhadap keputusan terkunci harus dicatat di dokumen terkait
  sebelum kode diubah.

## Koreksi yang mengungguli scope asli

| Area | Scope asli `45-1` | Keputusan eksekusi `45-2` | Alasan |
|---|---|---|---|
| Dispatch vendor | Klaim dispatch tepat sekali pada beberapa test | At-least-once + idempotency key yang sama + satu efek ledger | Timeout jaringan tidak dapat membuktikan exactly-once |
| Fraud saat Redis mati | `MemoryVelocityStore` per replika | Fail-closed `DEPENDENCY_UNAVAILABLE` sebelum money movement | Memory per replika dapat melemahkan fraud threshold secara diam-diam |
| Xendit | T4 dan sandbox nyata bagian DoD global | Tetap terdokumentasi, tetapi follow-up opsional setelah core | Membutuhkan akun/kredensial eksternal dan tidak boleh memblokir gate gratis/open-source |
| Breaker Redis | Default aktif bila Redis tersedia | Opt-in sampai integration dan chaos lintas replika hijau | Rollout aman dan rollback sederhana |
| Final gate | `docker compose down -v` generik | Project Compose terisolasi dengan nama container eksplisit | Volume development tidak ikut terhapus |
| Observability | Dashboard dan alert blocker track | Metric low-cardinality wajib; dashboard/alert setelah core stabil | Mengurangi coupling dengan Plan 43 tanpa kehilangan requirement |

## Urutan eksekusi

### Fase A — Core wajib dan sepenuhnya open-source

Ikuti `45-2` secara berurutan:

1. T0 — contract, schema, constraint, RLS, dan atomic enqueue.
2. T1 — relay sebagai satu-satunya pemilik `provider.Submit`.
3. T2 — distributed breaker Redis dengan fallback lokal.
4. T3 — degradasi Redis selektif dan fraud fail-closed.
5. T4 — chaos, regression suite, dan final gate terisolasi.

Fase A hanya memakai PostgreSQL, Redis, Go, Docker Compose, Prometheus,
`httptest`, dan testcontainers. Tidak diperlukan akun, kredensial, atau
service berbayar.

### Fase B — Requirement lanjutan yang tetap dipertahankan

Setelah Fase A hijau, ambil detail berikut dari `45-1` sebagai task/PR
terpisah:

1. Adapter Xendit config-gated dan sandbox manual dari K4/T4. Default tetap
   disabled dan tidak masuk CI/`verify-full`.
2. Dashboard, panel, alert provisioning, dan pembuktian firing/resolve dari
   K7/T5 setelah stack Plan 43 stabil.
3. Penyempurnaan chaos scenario 9–11 yang belum tercakup test matrix Fase A.
4. Update README, runbook, dan future-work hanya setelah kode terkait benar
   selesai dan terverifikasi.

Fase B bukan requirement yang dibuang. Pemisahan ini mencegah dependency
eksternal/proprietary menghambat merge core resilience.

## Definition of Done agregat

- [ ] Seluruh DoD core di `45-2` terpenuhi.
- [ ] Semua detail `45-1` dipetakan ke implementasi, dinyatakan superseded
      oleh tabel koreksi, atau dicatat sebagai Fase B; tidak boleh hilang
      tanpa keputusan eksplisit.
- [ ] Tidak ada klaim exactly-once untuk network dispatch.
- [ ] Fraud velocity tidak berubah menjadi bypass saat Redis unavailable.
- [ ] Gate core dapat berjalan tanpa kredensial dan tanpa service berbayar.
- [ ] Xendit dan observability lanjutan tetap dapat ditelusuri melalui
      `45-1`, walaupun tidak memblokir core.
- [ ] Bagian `### Hasil` di dokumen task yang dieksekusi berisi command,
      output ringkas, dan commit terkait.
