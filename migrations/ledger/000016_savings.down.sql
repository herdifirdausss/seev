DELETE FROM account_balances WHERE account_id IN (
    '00000000-0000-0000-0000-000000000029',
    '00000000-0000-0000-0000-000000000030'
);
DELETE FROM accounts WHERE id IN (
    '00000000-0000-0000-0000-000000000029',
    '00000000-0000-0000-0000-000000000030'
);

DROP TABLE IF EXISTS savings_config;

ALTER TABLE accounts DROP CONSTRAINT accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment','suspense',
     'fx_conversion'));
