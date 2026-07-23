# Runbook: FX Position (fx_out / fx_in)

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: financial operators and maintainers.** Confirm
> both sides from durable evidence before retrying or compensating either leg.

Covers the FX orchestration primitives (docs/roadmap/archive/18 Task T3, decision K-S S2): `fx_out` and `fx_in` are ordinary ledger transaction types, each debiting/crediting one currency, wired together by an external orchestrator (quote engine, rate feed — out of scope for this repo). The ledger enforces nothing about the pair as a whole; this runbook is what to do when the pair doesn't complete cleanly.

## The model

A conversion is two **separate** ledger transactions, each with its own idempotency key:

```
fx_out: user.cash[ccy1]           -> fx_conversion[pair][ccy1]     key: fx:<quote_id>:out
fx_in:  fx_conversion[pair][ccy2] -> user.cash[ccy2]                key: fx:<quote_id>:in
```

`pair` (e.g. `"IDRUSD"`) selects the account family; `fx_conversion` has one account per currency member of the pair, both `allow_negative=true` — the platform's FX position can run either direction depending on order flow across many conversions.

**Invariant**: one ledger transaction is always one currency. `fn_verify_ledger_balance` stays meaningful per-transaction. Nothing in the ledger enforces that a `fx_out`/`fx_in` pair nets to zero across currencies — that "balance" is an FX position, watched here, not by the ledger's own integrity checks.

## Normal operation

Both legs succeed independently and idempotently (retry-safe via their own idempotency keys). No operator action needed. The `fx_conversion` account balances drift up and down as orders flow through — that's expected, not an incident by itself.

## When a pair diverges

`fx_out` posted but `fx_in` failed permanently (e.g. destination cash account suspended, target currency's system account missing) — or the reverse. The tell: a query for the specific `quote_id`'s two transactions shows one `posted`, the other never posted (no transaction row, since it never got recorded at all — permanent failures never write a row here).

### Step 1 — Confirm the open position

```sql
SELECT lt.id, lt.type, lt.status, lt.amount, lt.currency, lt.idempotency_key
FROM ledger_transactions lt
WHERE lt.idempotency_key IN ('fx:<quote_id>:out', 'fx:<quote_id>:in');
```

If only one row comes back, the other leg never posted — this is the open-position case.

```sql
SELECT a.currency, a.system_qualifier, b.balance
FROM accounts a JOIN account_balances b ON b.account_id = a.id
WHERE a.type = 'fx_conversion' AND a.system_qualifier = '<pair>';
```

A non-zero balance on the currency member that DID post is the visible proof of the open position — that's by design (docs/roadmap/archive/18 K-S S2 decision): FX positions are meant to be visible in the account balance, not hidden.

### Step 2 — Decide: retry the missing leg, or reverse the posted one

This is a human decision — the ledger has no automatic resolution for a cross-currency FX position, deliberately (K-S S2: FX orchestration lives outside the ledger).

- **Retry the missing leg** (preferred when the failure was transient or has since been fixed — e.g. the destination account is no longer suspended): re-submit the same `fx_out`/`fx_in` command with the SAME idempotency key. If the underlying cause is now resolved, it posts and the position closes.
- **Reverse the posted leg** (when the missing leg cannot be completed — e.g. the user's account was permanently closed): use the standard `reversal` transaction type against the transaction that DID post. This returns the `fx_conversion` account back toward zero for that quote and restores the user's original-currency cash balance, as if the conversion never started.

### Step 3 — Verify closure

Re-run the Step 1 balance query — the position for that specific movement should be gone (though the account's overall balance may still be non-zero from other, unrelated conversions in flight — that's normal, not a leftover from this incident).

## What NOT to do

- Do not manually `UPDATE account_balances` to "fix" a position — always post a real transaction (retry or reversal) so the entry trail stays complete and `fn_verify_ledger_balance`/projection-rebuild integrity checks stay meaningful.
- Do not treat every non-zero `fx_conversion` balance as an incident — a healthy FX desk has open positions in flight constantly. Only investigate a SPECIFIC `quote_id` once you have a concrete reason to suspect its pair diverged (an alert, a support ticket, a stuck-order report from the orchestrator).
