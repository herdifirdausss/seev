# Seev in Five Minutes

> [Documentation home](../README.md) · [Learn](README.md)

> **Status: Current concept guide. Audience: anyone from about age ten, with
> no technology or finance knowledge required.** This page explains the main
> idea. It leaves details for later.

The [one-picture story](../seev-story.svg) gives the same core explanation with
less text.

For a young child listening with an adult, begin with the shorter
[story to read together](read-aloud-story.md).

## Seev in one sentence

Seev is a set of computer instructions showing how a digital wallet can
remember money correctly, even when messages repeat or a computer stops.

It is the invisible work behind a possible wallet app. This repository does
not contain the phone app, contact a real bank, or move real money.

## Imagine a small office

When Mia taps a button in a wallet app, an invisible team does the work:

| Office role | Its one job | Seev name |
|---|---|---|
| Front desk | Sends Mia's request to the right desk | Gateway |
| Identity desk | Checks who Mia is | Auth |
| Money-in desk | Remembers Mia's plan to add money | Payin |
| Money-out desk | Manages Mia's withdrawal | Payout |
| Accounting book | Records every real money change | Ledger |
| Safety officer | Checks for risky actions | Fraud |
| Staff workspace | Lets approved staff handle problems | Admin BFF |
| Independent inspector | Finds records that disagree | Assurance |

The roles are separate so responsibility is clear and a problem is easier to
contain.

## Follow Mia's money

The identity desk checks Mia. The accounting book then creates an empty wallet
record without creating free money. Mia starts with **0**. Noah also has an
empty wallet.

### 1. Mia adds 100,000

First, the money-in desk creates a ticket saying:

> Mia plans to add 100,000 through this payment company.

The ticket does not change her balance. After the outside payment company
reports success, Seev checks two things:

1. Did the message really come from that company?
2. Does it match Mia's ticket?

Only then does the accounting book record 100,000. Mia's balance is now
**100,000**.

Why use a ticket? The outside company may report a payment, but it must not be
allowed to choose whose wallet receives it.

### 2. Mia sends 25,000 to Noah

Suppose Seev promised a fee of 500 before Mia agreed.

- Mia loses 25,000 and has **75,000** left.
- Noah receives **24,500**.
- The platform's fee box receives **500**.

All three records are saved together. If one cannot be saved, none of them are
saved.

Why? Mia must not lose money while Noah receives nothing.

### 3. Mia withdraws 20,000

Seev first puts 20,000 in a hold box. Mia cannot spend it while the outside
company is working.

- If success is confirmed, 18,000 goes toward the bank and 2,000 becomes the
  withdrawal fee. Mia has **55,000** left.
- If failure is confirmed, the full 20,000 returns to Mia and there is no fee.
- If nobody knows the result yet, Seev keeps waiting instead of guessing.

Why not try another company immediately? The first company may already have
sent the money. Trying again could pay twice.

## Check that no value vanished

After all three successful actions:

```text
Mia 55,000 + Noah 24,500 + fees 2,500 + bank path 18,000 = 100,000
```

The places changed, but the total is still explained. This is the main job of
the accounting book.

## Three rules to remember

1. **A plan is not money.** A top-up ticket says what should happen. Only the
   accounting book makes the wallet balance real.
2. **One request must happen once.** If the same message arrives twice, Seev
   recognizes it and does not move the money twice.
3. **Do not guess.** When an outside result is unknown, Seev keeps that
   uncertainty visible and looks for proof.

## What happens later?

A messenger can notify Mia, a safety officer can look for risky patterns, and
an inspector can compare records. They cannot secretly rewrite Mia's balance.
A confirmed mistake gets a visible correction instead of erased history.

## What is this repository made of?

You do not need to read the code yet:

| Part | Everyday meaning |
|---|---|
| `docs/` and the root guides | Explanations, decisions, and emergency instructions |
| `cmd/` and `internal/` | Instructions that start the offices and perform their work |
| `migrations/` | Instructions for building each office's filing cabinet |
| `scripts/` and tests | Rehearsals that check normal work and failures |

## One honest warning

The current learning code has an old top-up path that may continue without
finding the matching ticket. That is a known limitation. The planned fix
requires a matching money-in or money-out record before telling the user that
the process is complete.

## A quick check

You understand the main idea if you know that a ticket is only a plan, all
parts of a transfer must succeed together, a hold prevents spending the same
value twice, no answer is not proof of failure, and only the Ledger accounting
book makes a balance change real.

Next, read the [visual story](visual-story.md) for more pictures or the
[beginner guide](beginner-guide.md) for the complete explanation. Look up any
unfamiliar word in the [glossary](../reference/glossary.md).
