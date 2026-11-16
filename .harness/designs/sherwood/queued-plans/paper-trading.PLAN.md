---
parent_design_doc: ../../../../wiki/designs/sherwood.md
parent_part_slug: paper-trading
---

# Plan: Paper trading with the safety rails (from design sherwood)

**Status:** draft
**Created:** 2026-11-15
**Roadmap item:** part 4 of 6 of [Sherwood Design](../../../../wiki/designs/sherwood.md); part file [paper-trading](../../../../wiki/designs/sherwood/parts/paper-trading.md)
**Brief:** Turn signals into tracked orders that trade on paper, with the mandatory risk check, approvals, the protective-sell path, the stop switch, automatic halts, reconciliation, notifications and the nightly backup.

## Goal

Sherwood trades on paper every day by its rules. Buys wait for your tap; protective sells go through on their own; every order passes the risk check; and any doubt halts trading until you reset it.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only.
- The execution layer cannot be constructed without a risk check.
- A failed send is never retried.

## Out of scope

- The Robinhood order calls and resting stops at the real broker (live-trading).

## Tasks

### 1. Order record, state machine and history
- **What:** The orders table, the append-only order history, the allowed transitions, and the idempotency key with its unique constraint.
- **Verification:** Every allowed transition is tested; an illegal one returns an error that halts trading; a duplicate key finds the existing order.
- **Status:** [ ]

### 2. Risk check
- **What:** Every limit in the design, the shorter list for protective sells, and the verdict shown before approval.
- **Verification:** Each limit has a passing and a refusing test; the execution layer will not build without a risk check.
- **Status:** [ ]

### 3. Paper broker and execution layer
- **What:** The paper adapter's fill model, the 09:35 ET send window, day limit orders for buys, and the protective-sell path.
- **Verification:** Fill-model tests cover gaps, limits and stops; a failed fake call moves the order to unknown and sends no retry.
- **Status:** [ ]

### 4. Approvals and Orders views
- **What:** Approval cards with reason, risk verdict and one tap per order; expiry at end of day; the Orders view with full history.
- **Verification:** Handler tests approve, decline and expire; approvals obey the device allowlist.
- **Status:** [ ]

### 5. Stop switch, automatic halts and reset
- **What:** The switch and its second confirmation for protective stops, every automatic halt cause, and the recorded reset.
- **Verification:** Tests throw each halt cause and see sends stop while signals are still recorded.
- **Status:** [ ]

### 6. Limits from the app, reconciliation and notifications
- **What:** Tightening limits in the app, the 09:00 ET reconciliation, and notifications for waiting buys and halts.
- **Verification:** A loosening attempt from the app is refused; a reconciliation difference halts trading.
- **Status:** [ ]

### 7. Nightly backup and runbook start
- **What:** The 02:00 online backup with its open-and-count check, and the first sections of the operational runbook.
- **Verification:** A test restores a backup and matches row counts.
- **Status:** [ ]

## Risks / open questions

- Paper fills are approximate at end-of-day resolution; compare with live fills later.

## Verification strategy

CI gates from foundations, plus the idempotency, no-retry and halt tests named above.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md`. This plan is queued: before starting it, archive the finished active plan to `.harness/archive/` and move this file to `.harness/PLAN.md`. Refine the task list with `/plan` before `/work`.

## Locked design calls

See the final [Sherwood Design](../../../../wiki/designs/sherwood.md). In short: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.
