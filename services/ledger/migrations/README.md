# Ledger migrations

Migrations are applied by golang-migrate in filename order. The tool does not
enforce sequential numbering, so gaps in the sequence are valid and harmless.

## Intentional gap at 000017

Migration 000017 was never created. The number was skipped during plan
reorganisation: plan-archive 19 (savings accrual) maps to 000016 and
plan-archive 20 (AML reporting views) maps to 000018. golang-migrate applies
000016 then 000018 without issue; the gap has no runtime effect.
