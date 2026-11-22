---
parent_design_doc: ../wiki/designs/sherwood.md
parent_part_slug: live-trading
---

# Plan: Live trading (from design sherwood)

**Status:** done
**Created:** 2026-11-15 (refined 2026-11-21 against the merged paper-trading and research-feed code)
**Roadmap item:** part 6 of 6 of [Sherwood Design](../wiki/designs/sherwood.md); part file [live-trading](../wiki/designs/sherwood/parts/live-trading.md)
**Brief:** Add the broker helper's order calls and the Go Robinhood adapter, wire the execution layer and reconciliation into live mode, add the 15-minute checks with the intraday loss limit and session-expiry handling, gate live mode behind the setting, an in-app confirmation and the minimum paper period, then finish the runbook.

## Goal

After the minimum paper period, you switch to live mode with a small amount, and sherwood trades your real account inside the same limits, halting on any broker doubt.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes and recorded-shape fixtures with made-up values; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only (next free number: `0009`).
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
- **Status:** [x] Merged as #141 (d328ca1), Python only, no migration. The helper now offers exactly eight ops: `ping`, `holdings`, `cash`, `account`, `lookup`, `open_orders`, `place` and `cancel`. The order routes are written out in `library.py` so the safety test can pin each one, and `_post` is the only POST. It allows exactly the orders route and `orders/<uuid>/cancel/`, keeps `_get`'s host lock, 10s timeout, no redirects and no retry, and succeeds on 200 or 201 (201 is accepted on purpose). `place` strictly checks its arguments and never rounds a price: a price off the broker's steps is `bad_request`. It answers `rejected` with nothing sent when the symbol is not tradeable, and checks that the reply's `ref_id` and account match. The client reference is a UUIDv5 of the idempotency key under a fixed namespace, and a test vector pins it. `cancel` finds the order by reference, sends nothing when the order has already finished, and posts once otherwise. `open_orders` reports `ref: null` with `client_ref`, so task 2 must map client references back to keys. `account` adds the value from the portfolio's `equity`. New error code: `order_not_found`. All seven helper error codes halt; none is retried. The wire contract, the design calls and seven operator checks are in the hand-over note `live-task-1.md`. Those checks cover the reply shapes, `ref_id` on each order type, quote-less JSON bodies, stop size and fractional limits, the `equity` field, history length and gtc expiry.

### 2. Go Robinhood adapter and the contract test
- **What:** A `broker.Adapter` over the helper client (for example `internal/broker/robinhood`) with typed calls, implementing `Health` from new supervisor health state and `CashReader`, and passing every call's outcome through `LoginAlert.Seen`. A reusable adapter conformance suite (in `brokertest`) run against the fake, the paper broker and this adapter. The Go-to-Python contract test grows to run every new op against the real helper with its library faked.
- **Verification:** The conformance suite covers every call and every error path for all three adapters; the contract test covers every op; a `login_needed` reply sends one login alert per streak.
- **Status:** [x] Merged as #146 (90e3ce2). It is Go only, inside `internal/broker/**`, with no migration. `robinhood.New(Config{Client, Helper, Login, Keys, Logger})` implements `Adapter`, `Health` and `CashReader` over the new `Client.CallFor`, with its own time limits: 60s for the order calls, 120s for holdings and 30s for cash and account. Every helper error is an error and is never retried. A refusal is a `rejected` report, and a malformed order is refused locally. Answers are decoded strictly, and `ref` and `client_ref` must match. `FillPrice` is filled, rounded to the cent. `ClientRef` is a hand-written UUIDv5, pinned to the helper's vector. Open orders are mapped back to keys in the adapter: `Keys` is `OrderKeys(store.Reader())`, every sent order. An order that matches no key comes back as `foreign-order:<broker id>`, and two open orders under one key are an error. Health is the supervisor's new `Healthy()` and no running login streak. Every outcome goes through `LoginAlert.Seen`. `brokertest.Run` is a reusable conformance suite, run against the fake (new `Track` mode), the paper broker and the adapter over a fake helper. The contract test runs all eight ops through the adapter against the real helper with its library faked, plus a logged-out helper that gives one alert. For task 4: set `execution.Config.CallTimeout` to at least 120s in live mode; share one `LoginAlert`; an immediate fill at place time currently halts as `unknown_order`. The operator checks are in `live-task-2.md`.

### 3. Interval jobs and the 15-minute checks
- **What:** An interval job kind in the scheduler (every N minutes inside market hours on trading days, with no make-up runs for missed slots and failure alerts grouped as today). In live mode, every 15 minutes: check open orders at the broker through reconciliation's comparison, and check the daily loss from `Account().Value` against the effective limit, throwing `halt.DailyLoss` with the values as evidence. Session expiry: a `login_needed` error halts trading as a broker error and sends the login alert; a crashing helper gives one halt and one push, not one per restart.
- **Verification:** Under the fake clock the interval job runs only in market hours; a loss past the limit halts trading; an expired session halts and alerts once; a helper that restarts repeatedly gives one halt.
- **Status:** [x] Merged as #145 (cbd905e), with no migration and nothing under `internal/broker/**`. **Scheduler.** `Job.Every` makes an interval job. Its slots fall at the open plus k times the interval, before the close, on trading days. Only the current slot is a candidate, so a missed slot is never made up. There is one `job_runs` row per job and date, rewritten by each slot. A failed slot is not retried, and failure alerts go once per group and date. **Halts.** `halt.ThrowOnce` dedupes by cause. It is used for `broker_error` from failed calls everywhere in execution, for the supervisor's `OnError` hook (one halt for a restarting helper), and for `daily_loss`. **`CheckOpenOrders`.** It settles `submitted` and `unknown` orders, and open orders the broker no longer lists, by one lookup each and never a resend, through the shared `settleOrders`. It then runs reconciliation's open-order comparison, now `compareOpen`. **`CheckDailyLoss`.** It reads `Account().Value` against the previous close's value and the effective limit, and throws `daily_loss` with the values as evidence. An unmeasurable loss fails the job instead of halting. A `login_needed` error halts with session-expiry wording. `registerChecks(sched, ex)` registers `open-orders-check` and `daily-loss-check` every 15 minutes; task 4 calls it in live mode only. **Gaps left for task 4.** The full `Reconcile` does not yet settle orders that have left the broker's book. Fills recorded by lookup do not update `holdings`. The operator checks are in `live-task-3.md`.

### 4. Live wiring and the live-mode gate
- **What:** In live mode, `registerTrading` builds the Robinhood adapter and the executor, registers `send-orders`, `reconcile` and the 15-minute checks, passes the executor to `web.Config.Orders` and the adapter's health to everything that reads it — but only when all three hold: the setting is `live`, the operator has confirmed live mode in the app, and the paper record is at least `min_paper_weeks` old. Settings gains the confirmation (approval devices only, anti-forgery token), shown beside the paper record and the weeks so far, recorded in `settings_changes`; until all three hold, sherwood runs as today's live mode does (no adapter, nothing sent) and says why on Settings and Today. Settle which broker cash field the snapshot uses, from the operator's answer in the holdings how-to.
- **Verification:** Without any one of the three, the Robinhood adapter is never built and no order job is registered; with all three (against a fake helper) the jobs are registered; the confirmation needs an approval device and the token.
- **Status:** [x] Merged in two PRs with no migration (`0009` is still free).
  - **Part 1, the gate and the live wiring (#148, 73c054b).** `limits.ReadGate` checks the three conditions. `RecordMode` writes a `mode` row when the mode changes, and `ConfirmLive` writes `live.confirmed` with the strategy file's hash. The confirmation lapses on any new strategy file or change of mode. Paper weeks count only paper time. The gate is read at start-up, so confirming takes a restart. With the gate open, `registerTrading` builds the adapter once, with one shared `LoginAlert` and `CallTimeout` 150s. It registers `reconcile`, `send-orders` and the two 15-minute checks (the checks in live mode only), and passes the executor and the adapter's health to the web app and the after-close preview. With the gate shut, it logs and builds nothing. Settings gains a Live mode card (approval devices only, anti-forgery token, `POST /settings/live`), and Settings and Today say why live trading is off. The design is amended.
  - **Part 2, the execution gaps (#149, c9e1432).** `holdings.Book` takes over the paper settlement's arithmetic. Every fill recorded by lookup or at place time writes a `fills` holdings and cash snapshot in the same transaction; a sell the holdings can't take, or an overfill, is a reconciliation difference. The full `Reconcile` settles orders that have left the broker's book, through `settleListed`, which it shares with the 15-minute check. An immediate fill at place time is recorded instead of halting, unless it can't be recorded. A resting stop the broker cancelled or expired halts with `reconciliation`, and the 09:35 send re-places it at no less than the lost price. The snapshot uses the broker's `cash`, not buying power, and that is an operator question. A partial fill that grows later halts on purpose.
  - **Hand-over.** `live-task-4.md` holds the halt-cause table and ten behaviours for task 5's runbook.

### 5. Runbook and the first live session
- **What:** Finish the runbook for live mode: halts and resets with the live causes, reconciliation differences against the real broker, re-login, the 15-minute checks, restoring a backup when orders may rest at the broker, and rolling back. Add a how-to for the first live session: the minimum paper period, the small starting amount, the confirmation, what to watch at 09:00, 09:35 and after the close, and how to stop.
- **Verification:** Each halt cause in code has a runbook entry that matches live behaviour; placeholders only.
- **Status:** [x] Merged as #151 (eabd536). It is documentation only, with no migration, so `0009` is still free.
  - **`wiki/operational/runbook.md`** no longer says that live trading is not built.
    - Its halt table has a row for each of the 8 causes in `internal/halt/halt.go`, with live behaviour. Two open orders under one reference are a `broker_error`, from the adapter's bad reply.
    - The by-cause folding is explained.
    - Reconciliation covers gone orders, how live fills reach the records, every live difference message, lost stops and a partial fill that grows.
    - New sections cover the live jobs and the 15-minute checks, logging in again, restoring a backup in live mode (cancel the orders the copy doesn't know before the next 09:35), and going back to paper mode.
  - **The new `wiki/how-to/run-the-first-live-session.md`** covers the small starting amount, setting `mode: live`, confirming, the second restart, and what to watch the evening before, at 09:00, at 09:35, every 15 minutes and after the close. It also covers how to stop. Its section 7 is the one checklist of every operator check from tasks 1 to 4.
  - **Links** were added from Home, the settings reference, the broker-login how-to and the strategy how-to.
  - **Open gap.** A partial fill that grows can't be cleared by a reset, because no command settles an order by hand, so each later check halts again. The runbook says so. A hand-settle command is a follow-up before partial fills happen live.

## Order and parallelism

Task 1 first. Then 2 (after 1) and 3 (needs only the scheduler and the adapter interface) side by side. Then 4 (after 2 and 3). Then 5 (after 4). No task is expected to need a migration; one that does takes `0009`.

## Risks / open questions

- The unofficial route can break or be blocked at any time; trading halts and resting stops keep protecting.
- Whether the route accepts a client reference on every order type; where it does not, reconciliation's lookup by the broker's own id is the only protection against a double send, and the send is still never retried.
- The broker session lasts what the broker grants (about a day); a daily re-login may be needed. Decide after the operator sees the real expiry.
- Whether the broker refuses stop orders above some size (re-audit trigger in the design).

## Verification strategy

CI gates from foundations, plus the helper safety test, the adapter conformance suite and the contract test; the operator runs the first live session.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md` and the hand-over notes for live-trading tasks. When the last task lands, close it out: status done, a summary in `progress.md`, and archive this file to `.harness/PLAN.archive.YYYYMMDD-live-trading.md`.

## Locked design calls

- The execution layer, reconciliation and the stop switch are reused as they are; live trading adds an adapter and wiring, not a second send path.
- The intraday loss check runs every 15 minutes in market hours, from the broker's account value, and halts rather than declines.
- Live mode needs the setting, the in-app confirmation and the minimum paper period, all three, before the adapter is built.
- See the final [Sherwood Design](../wiki/designs/sherwood.md) for the rest: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.
