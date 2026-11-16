---
parent_design_doc: ../../../../wiki/designs/sherwood.md
parent_part_slug: live-trading
---

# Plan: Live trading (from design sherwood)

**Status:** draft
**Created:** 2026-11-15
**Roadmap item:** part 6 of 6 of [Sherwood Design](../../../../wiki/designs/sherwood.md); part file [live-trading](../../../../wiki/designs/sherwood/parts/live-trading.md)
**Brief:** Add the broker helper's order calls and the Go Robinhood adapter, resting protective stops, unknown-state handling, session-expiry halts, the intraday loss-limit check and the live-mode switch, then finish the runbook.

## Goal

After the minimum paper period, you switch to live mode with a small amount, and sherwood trades your real account inside the same limits, halting on any broker doubt.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only.
- The Robinhood adapter is built only in live mode with the in-app confirmation.
- The operator runs the first live session; agents never do.

## Out of scope

- Raising limits beyond the small starting amount; that is your decision at the NAS.

## Tasks

### 1. Helper order calls
- **What:** Place, cancel, look up and list open orders in the broker helper, passing the idempotency key as the client reference where accepted.
- **Verification:** Tests against a fake library only; errors are reported, never retried.
- **Status:** [ ]

### 2. Go Robinhood adapter
- **What:** The adapter that passes each call to the helper and maps replies to order states.
- **Verification:** The contract test covers every call and every error path.
- **Status:** [ ]

### 3. Resting protective stops
- **What:** Place a stop when a holding opens and raise it after close for trailing rules.
- **Verification:** Fake-adapter tests place and move stops; the stop switch keeps them unless confirmed twice.
- **Status:** [ ]

### 4. Unknown state, session expiry and the intraday loss limit
- **What:** Settle unknown orders at reconciliation, halt on session expiry, and check the loss limit from the account value every 15 minutes.
- **Verification:** Tests throw each case and see trading halt.
- **Status:** [ ]

### 5. Live-mode switch
- **What:** The setting plus the in-app confirmation, and the paper record shown beside it.
- **Verification:** Without both, the Robinhood adapter is never built.
- **Status:** [ ]

### 6. Runbook
- **What:** Finish the runbook: halts, resets, reconciliation, re-login and restoring a backup.
- **Verification:** Each halt cause in code has a runbook entry.
- **Status:** [ ]

## Risks / open questions

- The unofficial route can break or be blocked at any time; trading halts and resting stops keep protecting.

## Verification strategy

CI gates from foundations; the operator runs the first live session.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md`. This plan is queued: before starting it, archive the finished active plan to `.harness/archive/` and move this file to `.harness/PLAN.md`. Refine the task list with `/plan` before `/work`.

## Locked design calls

See the final [Sherwood Design](../../../../wiki/designs/sherwood.md). In short: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.
