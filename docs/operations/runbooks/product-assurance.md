# Product assurance and intake control

> [Documentation home](../../README.md) · [Operations](../README.md) · [Runbooks](README.md)

> **Status: Current. Audience: product and incident operators.** Assurance may
> pause intake through its governed workflow, but it never moves or repairs
> money.

Assurance-service (`8096`/`18096`) only reads payin, payout, and ledger via
gRPC. It never mutates a domain transaction. New critical/high findings,
reopens, or severity escalations go on the durable alert queue.

## Routine checks

```bash
ASSURANCE_TOKEN="$TOKEN" scripts/product-assurance.sh summary
ASSURANCE_TOKEN="$TOKEN" scripts/product-assurance.sh list 'status=open&severity=critical'
ASSURANCE_TOKEN="$TOKEN" scripts/product-assurance.sh run
```

If a run fails because a dependency is unavailable, the cursor must not
advance. Check `/admin/assurance/runs` and the
`assurance_run_failures_total` metric; once the dependency recovers, run a
manual run or wait for the 60-second interval.

## Handling a finding

1. Acknowledge with an investigation reason; acknowledging does not clear
   the money-at-risk.
2. Fix the root cause in the owning service through its existing domain
   procedure.
3. Resolve only after the next proof run is healthy. The finding stays
   stored and will reopen if the same mismatch appears again.

```bash
scripts/product-assurance.sh acknowledge <finding-id> "investigating webhook lag"
scripts/product-assurance.sh resolve <finding-id> "ledger proof restored"
```

## Emergency intake pause

Pause only rejects the creation of new topup intents/payouts. Already-paid
webhooks, the payout worker, settle, cancel, replay, reconciliation, and
reversal all keep running.

```bash
scripts/product-assurance.sh pause payin <uuid> <revision> "PA01 money mismatch"
scripts/product-assurance.sh pause payout <uuid> <revision> "PO05 vendor backlog"
```

If assurance-service is down, an `admin` principal can use the owning
service's direct-pause endpoint, `/admin/payin/intake/pause` or
`/admin/payout/intake/pause`. There is no direct-resume.

Resume requires a second principal:

```bash
scripts/product-assurance.sh resume-request payout <uuid> <revision> "request resume after review"
scripts/product-assurance.sh resume-approve payout <uuid>
```

The same requester and approver are always rejected. The command UUID and
revision keep retries idempotent; a change is only considered successful
once the owning service confirms persistence.
