# Seev Documentation

> **Status: Current index.** This is the only documentation page a new reader
> needs to choose where to begin. Plans have their own Current, Target, or
> Historical status.

> **License and readiness:** Seev is open source under
> [Apache-2.0](../LICENSE). The license permits production use, but the
> repository is still under development and makes no production-readiness,
> certification, or support claim.

Do not read this library from top to bottom. Choose one goal from the table,
open that page, and return here only when your question changes.

## Start here

| Your goal | Open this | Stop when you can… |
|---|---|---|
| Understand Seev without technical knowledge | [Interactive story](index.html) | Retell why plans are not money, repeats happen once, and uncertainty waits for proof |
| Read with a young child | [Read-aloud story](learn/read-aloud-story.md) | Repeat the three safety promises together |
| Get a short independent introduction | [Five-minute tour](learn/five-minute-tour.md) | Follow Mia's money from top-up to withdrawal |
| Understand every product journey | [Product tour](learn/product-tour.md) | Explain identity, top-up, transfer, withdrawal, operators, and recovery |
| Find code and make a change | [Developer onboarding](development/onboarding.md) | Trace one request through its owner, data, and proof |
| Run or troubleshoot the stack | [Operations](operations/README.md) | Choose the correct command, signal, or runbook |
| Review the system design | [Architecture](reference/architecture.md) | Explain ownership, boundaries, and tradeoffs |
| See future work or decision history | [Roadmap](roadmap/README.md) | Distinguish an active plan from archived history |

The [interactive story](index.html) is the primary learning experience. It is
one offline file with six chapters, 208 one-screen panels, illustrations, and
ten quizzes. The [one-picture story](seev-story.svg) is its static fallback.

## Library map

Each directory has one purpose and its own index.

| Directory | Use it for | Do not use it for |
|---|---|---|
| [`learn/`](learn/README.md) | Plain-language stories and product journeys | Exact runtime contracts |
| [`reference/`](reference/README.md) | Current architecture, services, packages, events, glossary, and evidence | Step-by-step incident response |
| [`development/`](development/README.md) | Onboarding, engineering rules, and documentation conventions | Product introductions |
| [`operations/`](operations/README.md) | Runtime tooling, verification, observability, and runbooks | Future architecture claims |
| [`security/`](security/README.md) | Threat model and trust boundaries | Private vulnerability reports |
| [`roadmap/`](roadmap/README.md) | Strategy, active plans, and archived decisions | Current runtime truth |

The root of the repository intentionally keeps only public-project entry files:
`README.md`, `LICENSE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, and
`SECURITY.md`.

## What end-to-end means

“Understand the whole repository” has several useful levels:

| Level | Recommended route |
|---|---|
| Core product story | Interactive story, Product chapter |
| Product end to end | [Fastest product end-to-end route](#fastest-product-end-to-end-route) |
| Engineering end to end | [Fastest engineering end-to-end route](#fastest-engineering-end-to-end-route) |
| Specialist detail | Open one relevant reference, runbook, or plan |

Reading every historical plan is not an onboarding requirement. The archive
exists so a reviewer can recover old reasoning when a specific question needs
it.

## Fastest product end-to-end route

1. Open [Seev in one picture](seev-story.svg).
2. Read the Product chapter in the [interactive story](index.html#chapter-product).
3. Use these Product Tour sections only if you need more detail:
   [journey map](learn/product-tour.md#one-map-of-every-journey),
   [worked balances](learn/product-tour.md#a-worked-example-with-visible-balances),
   [fact ownership](learn/product-tour.md#who-owns-which-fact),
   [proof](learn/product-tour.md#how-the-repository-proves-these-stories), and
   [limits](learn/product-tour.md#what-seev-deliberately-does-not-claim).

Stop when you can explain what begins each journey, who owns it, when Ledger
makes money final, and what happens after a duplicate, timeout, or crash.

## Fastest engineering end-to-end route

1. Complete the product route above.
2. Read Onboarding's [60-second model](development/onboarding.md#60-second-mental-model),
   [service map](development/onboarding.md#service-map-name--code--data), and
   [representative request trace](development/onboarding.md#recommended-first-read-trace-one-request-end-to-end).
3. Use the [traceability map](reference/traceability.md) to connect the claim
   to its owner, interface, schema, focused test, and end-to-end proof.
4. Read Operations' [tooling overview](operations/README.md#the-general-problem-this-tooling-layer-solves)
   and [verification scripts](operations/README.md#3-scripts--verification-operations-and-bootstrap).

Stop when you can trace one top-up or withdrawal from entry point to domain
owner, Ledger posting, durable data, recovery behavior, and executable proof.

## Realistic time by age

Age alone does not create technical experience. The practical estimates and
expectations for ages 5 through 30 live in the [learning index](learn/README.md#realistic-time-by-age)
so this top-level chooser stays short.

## Status labels

- **Current** must agree with executable code and tests.
- **Target** is a reviewed design that is not fully implemented.
- **Historical** preserves an earlier decision or system shape.

If documents disagree, current code, tests, Compose, and the Makefile outrank a
historical plan. The [roadmap index](roadmap/README.md) is authoritative for
plan status.

## Complete document list

- Learning: [read-aloud story](learn/read-aloud-story.md),
  [five-minute tour](learn/five-minute-tour.md),
  [visual story](learn/visual-story.md),
  [beginner guide](learn/beginner-guide.md), and
  [product tour](learn/product-tour.md).
- Reference: [rationale](reference/rationale.md),
  [glossary](reference/glossary.md),
  [architecture](reference/architecture.md),
  [services](reference/services.md),
  [shared packages](reference/shared-packages.md),
  [event contract](reference/events.md), and
  [traceability](reference/traceability.md).
- Development: [onboarding](development/onboarding.md),
  [project guide](development/project-guide.md), and
  [documentation style](development/documentation-style.md).
- Operations and security: [operations](operations/README.md),
  [runbooks](operations/runbooks/README.md), and
  [threat model](security/threat-model.md).

## Maintenance

Use clear English, define terms before using them, link to one canonical
explanation instead of copying it, and keep Current, Target, and Historical
claims separate. Follow the [documentation style](development/documentation-style.md)
and run `make docs-check` before merging.
