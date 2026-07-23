# Documentation Style Guide

> [Documentation home](../README.md) · [Development](README.md)

> **Status: Current. Audience: documentation authors and reviewers.** These
> rules keep Seev understandable as its code and plans evolve.

## Write for a reader, not a repository

Start with the question a person is trying to answer. Explain the real-world
idea before naming a package, protocol, table, or environment variable.

Prefer this order:

1. What problem does this solve?
2. What happens in everyday language?
3. Why was this approach chosen?
4. What can fail, and what remains safe?
5. What are the exact technical interfaces?
6. Where is the implementation and proof?

Do not begin a beginner-facing explanation with a list of ports, acronyms, or
database tables.

## Use the correct information layer

| Layer | Reader | Content |
|---|---|---|
| README | First-time visitor | Purpose, limits, reading paths, setup |
| One-picture story | Young child with an adult or first-time visitor | Five spacious visual panels containing the whole core story and three promises |
| Read-aloud story | Young child with an adult | Pretend coins, one small diagram, three repeatable promises, no expectation of code knowledge |
| Five-minute tour | Reader from about age ten | One story, one arithmetic check, three safety rules, almost no assumed vocabulary |
| Visual story | Reader with no technical vocabulary | Ordinary objects, small diagrams, planned/reserved/final states |
| Beginner guide | Nontechnical reader | Stories, analogies, core safety ideas, common questions |
| Product tour | Product or curious reader | Complete user, operator, assurance, and recovery journeys |
| Rationale guide | Curious or reviewing reader | Benefit, prevented risk, and accepted cost of each major decision |
| Traceability map | Reviewer or contributor | Links from claims to owners, code, schema, and executable proof |
| Architecture | Product and technical reader | System reasons, ownership, topology, tradeoffs |
| Services and onboarding | Contributor | Exact responsibilities, interfaces, code paths |
| Operations, security, events, runbooks | Specialist | Precise operational contracts and procedures |
| Plans | Implementer or historian | Decisions, implementation work, acceptance evidence |

If a paragraph belongs to another layer, link to that layer rather than
copying all its detail.

Keep the five-minute tour below 900 whitespace-separated words. It should
teach only the complete core story; move optional detail into the next layer.
Keep the read-aloud story below 550 words and label toy amounts as separate
from the canonical worked example.

Treat the one-picture story as the primary overview. Keep it self-contained,
printable, free of external fonts or scripts, and understandable without
opening another document. Put nuance for engineers in the reference library
instead of shrinking more text into the image.

Maintain one fastest product route and one fastest engineering route in the
documentation index. Measure those routes by selected sections, not by the
size of the entire library. Plans and runbooks are lookup material and must
not silently become required onboarding steps.

## Label truth honestly

- **Current** describes behavior supported by executable code and tests.
- **Target** describes a design that is not fully implemented.
- **Historical** preserves an earlier decision or system shape.

Never redraw a target architecture as current before its acceptance criteria
pass. Never rewrite an old plan to pretend it predicted today's architecture;
update the plan index and link to the replacement.

When current code has a known limitation, state it near the otherwise ideal
explanation. A learning repository should teach the difference between a
desired rule and the code that currently enforces—or fails to enforce—it.

## Use plain English

- Prefer short sentences with one main idea.
- Use concrete verbs: “Payin stores the intent” is clearer than “intent
  persistence is performed.”
- Define an acronym or project-specific term on first use.
- Use the [shared glossary](../reference/glossary.md) for canonical definitions.
- Keep identifiers exact when they are contracts: route names, event names,
  status values, table names, and environment variables must not be paraphrased.
- Keep repeated names, amounts, and outcomes consistent with their canonical
  worked example. If an explanation intentionally uses different numbers,
  state that it is a separate example.
- Avoid calling something “simple,” “obvious,” or “just”; those words do not
  explain the missing step.
- Do not use a childish tone. Accessibility means removing hidden assumptions,
  not talking down to the reader.

### Keep language and terms consistent

- Use English (US) spelling: `behavior`, `practice`, `authorization`, and
  `organization`.
- Describe Seev as **open source under Apache-2.0**. Keep legal permission
  separate from technical readiness: the license permits production use, but
  the repository does not currently claim to be production-ready, certified,
  supported, or suitable for real money.
- Use **top-up** for the product journey and **Payin** only for the service
  that owns that journey.
- Use **withdrawal** for the product journey and **Payout** only for the
  service that owns that journey.
- Use **callback** as the primary term. Mention **webhook** only once as a
  familiar synonym when callback is first defined.
- Use **outside company** before technical names are introduced; use
  **vendor** after the term is defined.
- Use **big money book** in the beginner story and **Ledger** after the story
  explicitly maps the concept to the service name.
- Preserve exact capitalization for service names: Gateway, Auth, Payin,
  Payout, Ledger, Fraud, Admin BFF, Assurance, and VendorService.
- Do not alternate synonyms merely for variety. Repetition is helpful when a
  term has one precise meaning.

## Explain every diagram in words

A diagram supplements prose; it does not replace it. State what its arrows,
line styles, and important boundaries mean. Do not rely on color alone. Keep
current and target topologies in separate diagrams with visible labels.

For a sequence, explain at least:

- what starts it;
- where ownership is checked;
- when the operation becomes final;
- what is durable before a response is sent; and
- what a user may safely believe at each visible status; and
- what happens after a retry or partial failure.

## Service-section template

Use this order for a new service or a major rewrite:

1. **In plain English** — one short paragraph.
2. **Problem it solves** — why the service deserves to exist.
3. **Owns** — data and decisions for which it is authoritative.
4. **Must not do** — important boundary and safety exclusions.
5. **Inputs and outputs** — public/internal APIs and events.
6. **Happy path** — one representative journey.
7. **Failure behavior** — retries, degraded dependencies, and terminal states.
8. **Depends on / depended on by** — explicit runtime relationships.

## Runbook template

A runbook begins with status, audience, trigger, and a safety warning. Its
procedure then follows this order:

1. Confirm the symptom and affected scope.
2. Preserve evidence.
3. Perform the least destructive diagnosis.
4. Apply the documented recovery action.
5. Verify business state, not only process health.
6. Escalate when evidence is incomplete or an invariant is broken.

Commands must name their expected output and must not use unresolved broad
paths for destructive operations.

## Review checklist

- Can a new reader explain the feature without reading code?
- Does every technical term have a nearby explanation or glossary link?
- Does the document answer “why,” not only “what” and “how”?
- Are owner, source of truth, and forbidden responsibilities explicit?
- Are current, target, and historical statements separated?
- Does every diagram have a prose explanation?
- Are failure, retry, duplicate, and partial-success cases explained?
- Do links, anchors, commands, routes, event fields, and status values match the
  repository?
- Does `make docs-check` pass?
