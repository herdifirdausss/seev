-- docs/plan/16 Task T2 (K5): kolom korelasi eksternal — prasyarat rekonsiliasi.
-- Kolom INFORMATIF (sama seperti source/destination_account_id di 000001) —
-- sumber kebenaran tetap ledger_entries. TIDAK ada backfill data lama; hanya
-- baris yang di-posting SETELAH migrasi ini diisi oleh service.go dari
-- metadata tervalidasi (external_ref max 128 char, gateway ter-allowlist
-- constant.ValidGateways — lihat transport/metadata.go).
ALTER TABLE ledger_transactions
    ADD COLUMN external_ref TEXT NULL,
    ADD COLUMN gateway      TEXT NULL;

CREATE INDEX idx_ltx_external_ref ON ledger_transactions (gateway, external_ref)
    WHERE external_ref IS NOT NULL;
