---
parent_design_doc: ../../../../wiki/designs/sherwood.md
parent_part_slug: live-trading
---

# Plan: Live trading (from design sherwood)

**Status:** draft
**Created:** 2026-11-15 (refined 2026-11-21 against the merged paper-trading and research-feed code)
**Roadmap item:** part 6 of 6 of [Sherwood Design](../../../../wiki/designs/sherwood.md); part file [live-trading](../../../../wiki/designs/sherwood/parts/live-trading.md)
**Brief:** Add the broker helper's order calls and the Go Robinhood adapter, wire the execution layer and reconciliation into live mode, add the 15-minute checks with the intraday loss limit and session-expiry handling, gate live mode behind the setting, an in-app confirmation and the minimum paper period, then finish the runbook.

## Goal

After the minimum paper period, you switch to live mode with a small amount, and sherwood trades your real account inside the same limits, halting on any broker doubt.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes and recorded-shape fixtures with made-up values; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only (the next free number is set when this plan is promoted).
- The Robinhood adapter is built only in live mode with the in-app confirmation and after the minimum paper period.
- The operator runs the first live session; agents never do.

## Out of scope

- Raising limits beyond the small starting amount; that is your decision at the NAS.

## What already exists

- **Helper:** one JSON request and reply per call over the Unix socket, with error codes including `login_needed` and `broker_error`; ops today are `ping`, `holdings` and `cash` only, over a `Library` protocol that wraps the pinned `robin-stocks` with its own GET requests (no redirects, no retry). Tests fake the library and block importing the real one. `tests/test_safety.py` deliberately forbids order ops, order-like method names, POST and DELETE, and pins the library attributes used; task 1 rewrites it on purpose. Login is the `python -m brokerhelper login` command, not a socket call. `cash` returns cash and buying power but no account value.
- **Go broker package:** `Client.Call(op, args, result)` with `ErrUnavailable`, `ErrNoReply`, `ErrBadReply`, `HelperError` and `LoginNeeded`; the `Adapter` interface (Place, Cancel, Lookup, OpenOrders, Holdings, Account) where any error means the outcome is unknown; optional `Health` and `CashReader`; `Order.Ref` is the idempotency key, documented as the client reference; `Report.FillPrice` exists. `LoginAlert.Seen` sends the login alert once per streak and is wired nowhere yet. The supervisor restarts the helper and reports failures, but has no health state. The only Go-to-Python contract test runs ping and error cases against the real helper.
- **Execution:** adapter-agnostic already. `Send` re-checks, sends once, keeps one never-lowered resting stop per holding (raised at the next 09:35 from the last close's suggestion), and resizes it before a trim; a send error goes to `unknown` and halts. `Reconcile` settles `submitted` and `unknown` orders by one lookup each and compares holdings, cash and open orders. The stop switch's two confirmations exist.
- **Live mode today:** `mode: live` starts the supervisor (its errors throw `broker_error`), but `registerTrading` registers nothing, the executor is nil, `web.Config.Orders` is nil and broker health is always true. Settings shows the mode and the paper record but has no confirmation. `min_paper_weeks` is parsed and shown, never enforced.
- **Daily loss:** the risk check refuses at send when the loss from the previous close reaches the limit, using `Account().Value`. Nothing checks it during the day, and `halt.DailyLoss` is thrown nowhere. The scheduler has daily jobs only.
- **Runbook:** has a row for every halt cause, but several sections still say live trading is not built.

## Tasks

### 1. Helper order and account calls
- **What:** New helper ops `place`, `cancel`, `lookup`, `open_orders` and `account` (cash, buying power and the account's total value), through new `Library` methods with a POST sibling of the existing GET (same host lock, timeout, no redirects, no retry). `place` passes the order's idempotency key as the client reference wherever the route accepts one (derive the format the route needs deterministically from the key, and document it). Replies map to the adapter's statuses with filled shares and the fill price. Every failure is reported with its code; nothing is retried. Rewrite `tests/test_safety.py` on purpose: the op set is exactly the new list, the library attributes are pinned to the new list, and POST and DELETE are allowed only on the order endpoints.
- **Verification:** Tests against a fake library and recorded-shape fixtures with made-up values cover every op and every error path (timeout, non-200, 401/403 as `login_needed`, a malformed reply); a call is made exactly once; the safety test fails on an extra op, attribute or endpoint.
- **Status:** [ ]

### 2. Go Robinhood adapter and the contract test
- **What:** A `broker.Adapter` over the helper client (for example `internal/broker/robinhood`) with typed calls, implementing `Health` from new supervisor health state and `CashReader`, and passing every call's outcome through `LoginAlert.Seen`. A reusable adapter conformance suite (in `brokertest`) run against the fake, the paper broker and this adapter. The Go-to-Python contract test grows to run every new op against the real helper with its library faked.
- **Verification:** The conformance suite covers every call and every error path for all three adapters; the contract test covers every op; a `login_needed` reply sends one login alert per streak.
- **Status:** [ ]

### 3. Interval jobs and the 15-minute checks
- **What:** An interval job kind in the scheduler (every N minutes inside market hours on trading days, with no make-up runs for missed slots and failure alerts grouped as today). In live mode, every 15 minutes: check open orders at the broker through reconciliation's comparison, and check the daily loss from `Account().Value` against the effective limit, throwing `halt.DailyLoss` with the values as evidence. Session expiry: a `login_needed` error halts trading as a broker error and sends the login alert; a crashing helper gives one halt and one push, not one per restart.
- **Verification:** Under the fake clock the interval job runs only in market hours; a loss past the limit halts trading; an expired session halts and alerts once; a helper that restarts repeatedly gives one halt.
- **Status:** [ ]

### 4. Live wiring and the live-mode gate
- **What:** In live mode, `registerTrading` builds the Robinhood adapter and the executor, registers `send-orders`, `reconcile` and the 15-minute checks, passes the executor to `web.Config.Orders` and the adapter's health to everything that reads it — but only when all three hold: the setting is `live`, the operator has confirmed live mode in the app, and the paper record is at least `min_paper_weeks` old. Settings gains the confirmation (approval devices only, anti-forgery token), shown beside the paper record and the weeks so far, recorded in `settings_changes`; until all three hold, sherwood runs as today's live mode does (no adapter, nothing sent) and says why on Settings and Today. Settle which broker cash field the snapshot uses, from the operator's answer in the holdings how-to.
- **Verification:** Without any one of the three, the Robinhood adapter is never built and no order job is registered; with all three (against a fake helper) the jobs are registered; the confirmation needs an approval device and the token.
- **Status:** [ ]

### 5. Runbook and the first live session
- **What:** Finish the runbook for live mode: halts and resets with the live causes, reconciliation differences against the real broker, re-login, the 15-minute checks, restoring a backup when orders may rest at the broker, and rolling back. Add a how-to for the first live session: the minimum paper period, the small starting amount, the confirmation, what to watch at 09:00, 09:35 and after the close, and how to stop.
- **Verification:** Each halt cause in code has a runbook entry that matches live behaviour; placeholders only.
- **Status:** [ ]

## Order and parallelism

Task 1 first. Then 2 (after 1) and 3 (needs only the scheduler and the adapter interface) side by side. Then 4 (after 2 and 3). Then 5 (after 4). The plan's own decisions on a migration are made when it is promoted.

## Risks / open questions

- The unofficial route can break or be blocked at any time; trading halts and resting stops keep protecting.
- Whether the route accepts a client reference on every order type; where it does not, reconciliation's lookup by the broker's own id is the only protection against a double send, and the send is still never retried.
- The broker session lasts what the broker grants (about a day); a daily re-login may be needed. Decide after the operator sees the real expiry.
- Whether the broker refuses stop orders above some size (re-audit trigger in the design).

## Verification strategy

CI gates from foundations, plus the helper safety test, the adapter conformance suite and the contract test; the operator runs the first live session.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md`. This plan is queued: before starting it, archive the finished active plan and move this file to `.harness/PLAN.md`, changing its links from `../../../../wiki/` to `../wiki/`.

## Locked design calls

- The execution layer, reconciliation and the stop switch are reused as they are; live trading adds an adapter and wiring, not a second send path.
- The intraday loss check runs every 15 minutes in market hours, from the broker's account value, and halts rather than declines.
- Live mode needs the setting, the in-app confirmation and the minimum paper period, all three, before the adapter is built.
- See the final [Sherwood Design](../../../../wiki/designs/sherwood.md) for the rest: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.
