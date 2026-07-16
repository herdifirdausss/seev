-- docs/plan/18 Task T3 (S2 butir 2): FX orchestration primitives. FX is NOT
-- a ledger feature — a conversion is orchestration of two ordinary ledger
-- transactions (fx_out debits the user's source currency into a platform
-- position account, fx_in credits the user's target currency out of it).
-- This migration only adds the account type + seeds the position accounts.

ALTER TABLE accounts DROP CONSTRAINT accounts_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN
    ('cash','hold','pending','frozen','pocket','fee',
     'settlement','escrow','chargeback','confiscated','adjustment','suspense',
     'fx_conversion'));

-- One account PER CURRENCY per pair, both sharing the pair's qualifier —
-- "IDRUSD" names the pair, not a currency; GetSystemAccountID's currency
-- parameter picks the IDR or USD leg. allow_negative=true on both: the
-- platform's FX position can run either direction depending on order flow
-- (docs/plan/18 Task T3 decision).
INSERT INTO accounts (id, owner_type, type, currency, system_qualifier, created_by) VALUES
('00000000-0000-0000-0000-000000000025','system','fx_conversion','IDR','IDRUSD','migration'),
('00000000-0000-0000-0000-000000000026','system','fx_conversion','USD','IDRUSD','migration');

INSERT INTO account_balances (account_id, allow_negative)
VALUES
('00000000-0000-0000-0000-000000000025', true),
('00000000-0000-0000-0000-000000000026', true);
