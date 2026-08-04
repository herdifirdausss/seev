DELETE FROM account_balances WHERE account_id IN (
    '00000000-0000-0000-0000-000000000025',
    '00000000-0000-0000-0000-000000000026'
);

DELETE FROM accounts WHERE id IN (
    '00000000-0000-0000-0000-000000000025',
    '00000000-0000-0000-0000-000000000026'
);

ALTER TABLE accounts DROP CONSTRAINT accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment','suspense'));
