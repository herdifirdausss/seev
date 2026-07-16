-- Akun sistem awal. Tambah gateway/currency baru = INSERT baru di migrasi baru.
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by) VALUES
('00000000-0000-0000-0000-000000000001','system','settlement','IDR','bca',      'migration'),
('00000000-0000-0000-0000-000000000002','system','settlement','IDR','gopay',    'migration'),
('00000000-0000-0000-0000-000000000003','system','fee',       'IDR','platform', 'migration'),
('00000000-0000-0000-0000-000000000004','system','fee',       'IDR','bca',      'migration'),
('00000000-0000-0000-0000-000000000005','system','fee',       'IDR','gopay',    'migration'),
('00000000-0000-0000-0000-000000000006','system','escrow',    'IDR','IDR',      'migration'),
('00000000-0000-0000-0000-000000000007','system','chargeback','IDR','visa',     'migration'),
('00000000-0000-0000-0000-000000000008','system','adjustment','IDR',NULL,       'migration'),
('00000000-0000-0000-0000-000000000009','system','confiscated','IDR',NULL,      'migration');

-- settlement, adjustment dan chargeback secara desain bisa "berhutang" ke
-- dunia luar (uang masuk dari bank/gateway/dispute sebelum tercatat sebagai
-- aset ledger) — lihat account_balances.allow_negative di 000001.
INSERT INTO account_balances (account_id, allow_negative)
SELECT id, (type IN ('settlement','adjustment','chargeback'))
FROM accounts
WHERE owner_type = 'system';
