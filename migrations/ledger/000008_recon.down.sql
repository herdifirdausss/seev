DROP TABLE IF EXISTS recon_items;
DROP TABLE IF EXISTS recon_batches;

DELETE FROM account_balances WHERE account_id IN (
    SELECT id FROM accounts WHERE type = 'suspense'
);
DELETE FROM accounts WHERE type = 'suspense';

ALTER TABLE accounts DROP CONSTRAINT accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment'));
