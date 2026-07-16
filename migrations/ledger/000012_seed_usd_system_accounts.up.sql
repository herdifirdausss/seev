-- docs/plan/18 Task T2 (S2): system account pool for USD, mirroring the IDR
-- pool seeded in 000002/000008. A qualifier like "bca" now names a FAMILY of
-- accounts, one per currency — GetSystemAccountID(type, qualifier, currency)
-- picks the member. Add a new currency = INSERT a new pool here, not a
-- schema change.
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by) VALUES
('00000000-0000-0000-0000-000000000013','system','settlement','USD','bca',      'migration'),
('00000000-0000-0000-0000-000000000014','system','settlement','USD','gopay',    'migration'),
('00000000-0000-0000-0000-000000000015','system','fee',       'USD','platform', 'migration'),
('00000000-0000-0000-0000-000000000016','system','fee',       'USD','bca',      'migration'),
('00000000-0000-0000-0000-000000000017','system','fee',       'USD','gopay',    'migration'),
('00000000-0000-0000-0000-000000000018','system','escrow',    'USD','USD',      'migration'),
('00000000-0000-0000-0000-000000000019','system','chargeback','USD','visa',     'migration'),
('00000000-0000-0000-0000-000000000020','system','adjustment','USD',NULL,       'migration'),
('00000000-0000-0000-0000-000000000021','system','confiscated','USD',NULL,      'migration'),
('00000000-0000-0000-0000-000000000022','system','suspense',  'USD','suspense:bca',      'migration'),
('00000000-0000-0000-0000-000000000023','system','suspense',  'USD','suspense:gopay',    'migration'),
('00000000-0000-0000-0000-000000000024','system','suspense',  'USD','suspense:platform', 'migration');

-- Same allow_negative rules as 000002/000008: settlement/adjustment/chargeback
-- can legitimately go negative (money owed to the outside world before it's
-- recorded); suspense always allow_negative (a recon gap can go either way).
INSERT INTO account_balances (account_id, allow_negative)
SELECT id, (type IN ('settlement','adjustment','chargeback','suspense'))
FROM accounts
WHERE id IN (
    '00000000-0000-0000-0000-000000000013',
    '00000000-0000-0000-0000-000000000014',
    '00000000-0000-0000-0000-000000000015',
    '00000000-0000-0000-0000-000000000016',
    '00000000-0000-0000-0000-000000000017',
    '00000000-0000-0000-0000-000000000018',
    '00000000-0000-0000-0000-000000000019',
    '00000000-0000-0000-0000-000000000020',
    '00000000-0000-0000-0000-000000000021',
    '00000000-0000-0000-0000-000000000022',
    '00000000-0000-0000-0000-000000000023',
    '00000000-0000-0000-0000-000000000024'
);
