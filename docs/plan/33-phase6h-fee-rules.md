# 33 — Phase 6h: Fee DB-driven per-user-per-route

> Baca master reference di [26-phase6a-foundations.md](26-phase6a-foundations.md). Prasyarat: doc 32 selesai.

## Konteks

Fee hari ini statis dari env (`FEE_TRANSFER_P2P_*`, `FEE_WITHDRAW_*`) → `feepolicy.Policy` in-memory dengan key `<txType>:<gateway>:<currency>`, TANPA dimensi user. Fase ini menggantinya dengan tabel `fee_rules` di `seev_ledger`: fee configurable per (user, route/gateway) dengan resolusi spesifisitas **exact user+route > user default > route default > global default**. Mekanika hilir TIDAK berubah: fee tetap mengalir sebagai metadata `fee_amount`/`fee_gateway`, divalidasi `FeeAmountValidator`, deduct-from-amount, dan withdraw tetap fee-on-settle (payout `settle()` memanggil `ResolveFee`).

## T1 — Migrasi `fee_rules`

### Langkah
1. `migrations/ledger/000019_fee_rules.up/down.sql` (nomor berikutnya folder ledger — verifikasi saat implementasi):
```sql
CREATE TABLE fee_rules (
    id UUID PRIMARY KEY,
    tx_type TEXT NOT NULL,             -- 'transfer_p2p', 'withdraw_settle', ...
    gateway TEXT NOT NULL DEFAULT '',  -- '' = konvensi "tanpa gateway" feepolicy existing
    currency TEXT NOT NULL,
    user_id UUID,                      -- NULL = default semua user
    flat_minor_units BIGINT NOT NULL DEFAULT 0,
    percent_basis_pts BIGINT NOT NULL DEFAULT 0 CHECK (percent_basis_pts >= 0 AND percent_basis_pts < 10000),
    fee_gateway TEXT NOT NULL DEFAULT 'platform',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (tx_type, gateway, currency, user_id)
);
CREATE INDEX idx_fee_rules_lookup ON fee_rules(tx_type, currency) WHERE enabled;
```
   + RLS/grants pola `policy_limits`.

### Test wajib
- up→down→up bersih terhadap Postgres nyata.

### DoD
- [ ] Skema fee siap dengan uniqueness yang mencegah rule ganda ambigu.

### Hasil
_Belum dikerjakan._

## T2 — `feepolicy` DB-backed

### Langkah
1. Tulis ulang `internal/ledger/feepolicy`: `Resolve(ctx, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal) (fee, feeGateway, ok)` — SATU query: `WHERE enabled AND tx_type=$1 AND currency=$2 AND (user_id=$3 OR user_id IS NULL) AND gateway IN ($4,'') ORDER BY (user_id IS NOT NULL) DESC, (gateway <> '') DESC LIMIT 1`; hitung flat+bps (truncate) — pertahankan defensive clamp existing (`fee>0 && fee<amount` → else ok=false).
2. `transport/metadata.go` `buildMetadata`: pass `userID` (sudah in scope) ke Resolve.
3. HAPUS: `SetFeeRules`/`FeeRule` re-export di facade (beserta mekanisme rebuild router), `FeeConfig` + env `FEE_*` di `internal/config`, blok pembangunan rules di `cmd/ledger-service`.
4. Facade `ResolveFee` bertambah `ctx` + `userID`; proto `ResolveFeeRequest.user_id` (sudah dialokasikan doc 27) kini DIPAKAI; update `internal/ledger/grpcserver`, `pkg/ledgerclient`, `payout.Poster` + call site settle (pass `req.UserID`). Gotcha #1: vet kedua tag setelah perubahan signature.

### Test wajib
- Unit matriks spesifisitas: 4 level + disabled + no-match + clamp.
- Integration: transfer_p2p — user X kena F1 (rule user+route), user Y kena F2 (global) — fee leg di-assert di `ledger_entries`; withdraw settle dengan rule per-user.

### DoD
- [ ] Fee sepenuhnya dari DB; env `FEE_*` tidak ada lagi; per-user terbukti di level entries.

### Hasil
_Belum dikerjakan._

## T3 — Admin CRUD fee rules

### Langkah
1. Internal listener ledger: `GET/POST /api/v1/admin/ledger/fee-rules`, `PUT /api/v1/admin/ledger/fee-rules/{id}`, disable via `enabled=false` (JANGAN delete — audit trail, pola policy_limits). Admin-gated. Validasi: tx_type dikenal registry, gateway ∈ ValidGateways atau '', currency terdaftar, bps < 10000.

### Test wajib
- Unit handler (validasi, admin-gate 403, list/create/update/disable).

### DoD
- [ ] Operator mengelola fee tanpa deploy/SQL.

### Hasil
_Belum dikerjakan._

## T4 — E2E fee per-user

### Langkah
1. `business-e2e.sh`: ganti env `FEE_*` dengan seeding via admin API — buat fee P2P global + override per-user untuk user A; assert saldo A/B dan akun `fee[platform]` membuktikan A membayar tarif berbeda dari user default; withdraw dengan fee rule; cancel tetap tanpa fee.

### Test wajib
- business-e2e hijau end-to-end enam service.

### DoD
- [ ] "Fee configable berdasarkan user apa ke routing apa" terbukti dari API sampai entries.

### Hasil
_Belum dikerjakan._

---

## Verifikasi akhir dokumen
Gate standar master doc 26. Update README index → lanjut [34-phase6i-verification.md](34-phase6i-verification.md).
