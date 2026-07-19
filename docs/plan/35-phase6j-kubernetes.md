# 35 — Phase 6j (OPSIONAL): Kubernetes lokal (kind)

> Baca master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md). Prasyarat: doc 34 selesai. Fase ini OPSIONAL — nilai belajar ops; sistem sudah lengkap tanpa fase ini.
>
> **Aturan RAM (gotcha #14 master): kind MENGGANTIKAN compose — JANGAN menjalankan keduanya bersamaan** di mesin dev 3.9GB.

## Konteks

Memindahkan topologi enam service dari docker-compose ke Kubernetes lokal (kind) sebagai latihan: manifest per service, StatefulSet Postgres dengan init multi-database, Job migrasi per service, Secret untuk JWT/token, NodePort untuk dua service publik.

## T1 — Manifest dasar

### Langkah
1. `deploy/k8s/`: `namespace.yaml` (`seev`); per app service (gateway, auth, ledger, payin, payout, fraud): `Deployment` (image dari Dockerfile `ARG SERVICE`, `resources.requests: 32Mi / limits: 128Mi`, liveness/readiness probe via flag `-healthcheck` atau HTTP `/health`) + `Service` (ClusterIP; gateway & auth juga NodePort).
2. Infra: `postgres` StatefulSet (volume 1Gi) + ConfigMap init script (buat enam database + role — adaptasi `scripts/postgres-init/`); `redis` + `rabbitmq` Deployment sederhana.
3. `Secret`: `JWT_SECRET`, `INTERNAL_GRPC_TOKEN`, password DB per service. ConfigMap env per service (alamat gRPC pakai DNS service k8s: `ledger-service.seev.svc:9091` dst).

### Test wajib
- `kubectl apply -k deploy/k8s` di cluster kind bersih → semua pod Ready.

### DoD
- [ ] Enam service + infra hidup di kind dari nol.

### Hasil
_Belum dikerjakan._

## T2 — Job migrasi + tooling

### Langkah
1. Job migrasi per service (image `migrate/migrate`, mount/copy folder `migrations/<svc>`, DSN ke DB masing-masing) — dijalankan sebelum Deployment ready (initContainer atau Job + wait).
2. Makefile: `kind-up` (create cluster + apply), `kind-load` (build image + `kind load docker-image`), `kind-down`.

### Test wajib
- `make kind-up` end-to-end di mesin bersih (compose dimatikan dulu).

### DoD
- [ ] Satu perintah menaikkan seluruh stack di kind.

### Hasil
_Belum dikerjakan._

## T3 — Verifikasi

### Langkah
1. Port-forward gateway + auth → jalankan subset `business-e2e.sh` (register→login→topup→transfer→notifikasi) terhadap port-forward.

### Test wajib
- Subset e2e hijau di kind.

### DoD
- [ ] Journey bisnis inti terbukti jalan di Kubernetes.

### Hasil
_Belum dikerjakan._

---

## Verifikasi akhir dokumen
T3 hijau. Update README index — rangkaian docs 26–35 selesai seluruhnya.
