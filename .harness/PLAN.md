---
parent_design_doc: ../wiki/designs/sherwood.md
parent_part_slug: paper-trading
---

# Plan: Paper trading with the safety rails (from design sherwood)

**Status:** in progress
**Created:** 2026-11-15 (refined 2026-11-18 against the merged phone-app and prices-and-rules code)
**Roadmap item:** part 4 of 6 of [Sherwood Design](../wiki/designs/sherwood.md); part file [paper-trading](../wiki/designs/sherwood/parts/paper-trading.md)
**Brief:** Turn signals into tracked orders that trade on paper, with the mandatory risk check, approvals, the protective-sell path, the stop switch, automatic halts, reconciliation, notifications and the nightly backup.

## Goal

Sherwood trades on paper every day by its rules. Buys wait for your tap; protective sells go through on their own; every order passes the risk check; and any doubt halts trading until you reset it.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only. Only one session at a time may add a migration, and the coordinator names its number (next free: `0006`).
- The execution layer cannot be constructed without a risk check.
- A failed send is never retried.
- `cmd/sherwood/main.go`, `internal/logging/allowlist.go` and `go.mod` are shared by parallel tasks: keep edits there small and rebase (never back-merge) when `main` moves.

## Out of scope

- The Robinhood order calls and resting stops at the real broker (live-trading).
- The research export and the Feed view (research-feed).

## What the merged code already gives this part

- `internal/signals`: signals and per-holding suggestions per strategy and date (migration `0004`), with `signals.Key(strategy, symbol, side, kind, date)` as the idempotency key, and the `stop_sell` kind for protective sells. Signals for a date may arrive at 16:30, 18:00 or 21:00 ET, so read them by date.
- `internal/rules`: `Set.Combine`, `Set.Sizing` and `Set.InitialStop` — the sizing and initial-stop logic the back-tester already uses.
- `internal/fill`: the paper fill model as a pure function (`BuyLimit`, `SellStop`, `SellAtOpen`).
- `internal/holdings`: add-only snapshots with sources `import`, `fills` and `reconcile`; `Current`, `AsOf` and `LatestCash`.
- `internal/notify`: a `Notifier` with fixed-wording alerts (`BuysWaiting`, `TradingHalted`, `Test`) and a fake.
- `internal/web`: `HandleRead`, `HandleChange`, `ApprovalDevicesOnly`, the views table, and Today and Settings views with placeholder cards for the stop switch, limits and paper record.
- `settings_changes` (migration `0001`) for recording limit changes. Its column comment names `limits.max_daily_loss`; the real key is `limits.daily_loss_limit`. Never edit an applied migration: record the correct key and say so in the runbook.
- Known gaps this part closes: a broker helper error is only logged; `day.Halt()` from the after-close job is only an Error log line; the scheduler has only trading-day jobs (the 02:00 backup needs an every-day job).

## Tasks

### 1. Orders, order history and fills
- **What:** Migration **`0006`**: an `orders` table (strategy, stock, side, signal kind, order type — day limit buy, stop, sell at open — quantity, limit or stop price, trading date it was made for, current state, the reason, the risk verdict text, the strategy hash, and the idempotency key from `signals.Key` under a UNIQUE constraint), an append-only `order_history` (every state change with time, actor and reason), and an append-only `fills` table. An `internal/orders` package with the design's states and the allowed transitions as one table, `Create` that finds the existing order on a duplicate key, and `Move` that appends history and updates the row in one transaction. An illegal transition returns a distinct error that callers turn into a halt (task 3).
- **Verification:** Every allowed transition is tested and every other pair is refused; a duplicate key returns the existing order; an order's state can be rebuilt from its history; history and fills refuse UPDATE and DELETE.
- **Status:** [x] Shipped in [#113](https://github.com/alexherrero/sherwood/pull/113) (2026-11-18). Migration `0006` adds `orders` (with the idempotency key from `signals.Key` under a unique constraint), an append-only `order_history` and an append-only `fills` table. `internal/orders` holds the design's states and the only allowed transitions in one table; `Create` returns the existing order on a duplicate key, `Move` appends history and updates the row in one transaction and refuses anything else with `ErrIllegalTransition` (writing nothing), and `RecordFill` records fills. Actors are free text with constants for the proposer, risk, operator, stop switch, execution, broker, reconciliation and expiry. An order's state can be rebuilt from its history, and a no-float guard covers the package.

### 2. Risk check
- **What:** An `internal/risk` package: a pure `Check(Order, Snapshot) Verdict` over every limit in the design's table, with the shorter list for protective sells (shares held, no more than held, market open or resting stop, max order value). `Snapshot` carries holdings, cash, the day's orders so far, the previous close's portfolio value, the last closes, the watchlist, the switch state and the clock. The verdict lists each check with pass or the refusal reason in plain words, so the approval card can show it. When `max_sector_share` is set, the strategy file must give each stock a sector (add a `sectors:` map to `internal/config`, refused at start-up if a limited stock has none). The execution layer's constructor (task 5) takes a `risk.Checker` and refuses nil.
- **Verification:** Each limit has a passing and a refusing table test; protective sells pass on a day the loss limit, order count or switch would block a buy; a sell of more shares than held is refused; a no-float guard covers the package.
- **Status:** [x] Shipped in [#114](https://github.com/alexherrero/sherwood/pull/114) (2026-11-18). `internal/risk` is a pure check over every limit in the design, in a fixed order, with the shorter list for protective sells (order value, shares held, market open or a resting stop); its verdict prints one plain line per check for the approval card. The limit-price distance refuses a limit too far either side of the close, and an opposite-side order on the same stock the same day is refused. The strategy file gains a `sectors:` map, required for every watched or strategy stock when `max_sector_share` is set, with the example files and the settings reference updated. Open points for task 5: a resting stop plus a trim can together cover more shares than are held, so the stop must be resized or cancelled first; protective sells skip the order-value check, decided by the operator on 2026-11-18 and changed in a follow-up.

### 3. Halts, the stop switch state and reset
- **What:** Migration **`0007`**: an append-only `halts` table (cause, evidence, time) and `halt_resets` (reason, time, device). An `internal/halt` package with `Throw(cause, evidence)` and `Reset(reason)`; throwing notifies through `notify.TradingHalted`. Wire every existing cause: a broker helper error or exit (supervisor `OnError`), `day.Halt()` from the after-close job, and an illegal order transition. The stop switch on Today becomes real: the current state, the active halts with their causes, a switch-on POST, and a reset POST with a required reason, both limited to approval devices. While halted, rules still run and record signals.
- **Verification:** Tests throw each cause and see the switch on and a notification on the fake; reset needs a reason and an approval device and is recorded; a second throw while on adds a halt record without losing the first.
- **Status:** [x] Shipped in [#117](https://github.com/alexherrero/sherwood/pull/117) (2026-11-18). Migration `0007` adds append-only `halts` (cause, evidence, time) and `halt_resets` (the newest halt cleared, a required reason, the approval device, time); the switch is on while any halt is newer than the last reset. `internal/halt` throws a halt, logs it and sends `notify.TradingHalted`, ignoring an identical repeat while it is active so a restarting helper cannot flood the log; a reset needs a reason and an approval device and is refused if a newer halt has arrived since the operator looked. Broker helper failures, price disagreements on held stocks and illegal order transitions (through `Switch.OrderError`) now throw it, and the causes for unknown orders, reconciliation and the daily loss are defined for later tasks. Today shows the switch, the active halts and their causes, a Stop all trading button and a reset form, both limited to approval devices. `halt.Active` is the read API for the risk check and the execution layer. Throwing never touches orders; declining and cancelling are task 7's.

### 4. Proposing orders from signals
- **What:** After each successful after-close attempt (hook into the job, not a separate clock), turn the date's new signals into orders: buys and adds sized with `Set.Sizing` at a day limit of the close times `1 + buy_limit_margin`, with the initial stop from `Set.InitialStop`; trims and ordinary exits as sells at the next open; protective sells (`stop_sell`) as stop orders that skip approval. Buys and every non-protective sell go to `awaiting_approval` with the risk verdict stored; a refused order goes to `blocked` with its reason. Approvals expire at the end of the trading day they were made for. A catch-up run for an earlier session never proposes a buy into the wrong session. Notify once with `notify.BuysWaiting(n)` when buys are waiting.
- **Verification:** Running the proposal twice for the same date creates one order per key; a catch-up date proposes no buy; a blocked order stores its reason; expiry moves an unapproved buy to `declined`; the notification carries only the count.
- **Status:** [ ]

### 5. Broker interface, paper broker and the execution layer
- **What:** `internal/broker` gains the design's adapter interface (place, cancel, look up, list open orders, list holdings, read cash and account value) with a scripted fake. The paper adapter wraps `fill.Apply`: orders sent in the 09:35 ET window rest as accepted, resting stops are kept for each holding and moved when a trailing rule raises them after the close, and fills are settled against that day's checked bar once the after-close attempt has it, writing `fills` and a new `holdings` snapshot with source `fills`. The execution layer is built only with a `risk.Checker`, re-checks every order immediately before sending, sends approved orders and protective stops at 09:35 ET, and on any adapter error or timeout moves the order to `unknown`, throws a halt and never retries. Nothing is sent while the switch is on, except that protective stops keep resting.
- **Verification:** Fill tests cover a gap down through a stop, a buy limit not reached, a touch that does not fill, and an open below the limit; a failed fake call moves the order to `unknown`, halts and makes exactly one call; the constructor refuses a nil checker; a sell and a buy of the same stock on one day is refused.
- **Status:** [ ]

### 6. Approvals and Orders views
- **What:** The Approvals view shows one card per waiting order with the rule's reason, the risk verdict re-run now, and approve and decline buttons (POST, anti-forgery token, approval devices only); an approval re-checks the order and refuses one that would now fail. The Orders view lists orders by date with their full history and fills. Today's banner links to waiting approvals.
- **Verification:** Handler tests approve, decline and see expiry; a POST from a non-approval device is refused; an approval after expiry is refused; values render from money types without floats.
- **Status:** [ ]

### 7. Stop switch actions, limits from the app and reconciliation
- **What:** Throwing the switch declines every buy awaiting approval and cancels every open buy at the broker; a second, separate confirmation also cancels resting protective stops. Settings gains the limits with a form to tighten any of them: a tightening applies at once and is recorded in `settings_changes` with time and source, a loosening from the app is refused, and the effective limit is the tighter of the file and the app's tightenings recorded since the strategy file's hash last changed. Settings also shows the mode and the paper record (return, holdings, orders). Reconciliation runs at 09:00 ET and after the after-close attempt that succeeds: it compares holdings, cash and open orders with the adapter's, settles any `unknown` order by looking it up, and any difference throws a halt with the evidence.
- **Verification:** A loosening attempt from the app is refused and a tightening is recorded and enforced; the switch test sees buys declined and cancelled while stops stay until the second confirmation; a reconciliation difference halts trading; an `unknown` order is settled by lookup without a resend.
- **Status:** [ ]

### 8. Nightly backup, job alerts and the runbook start
- **What:** An every-day job kind in the scheduler (it runs on non-trading days too) and the 02:00 online backup through SQLite's backup or `VACUUM INTO` to the backup folder named in the settings, checked by opening the copy and matching row counts, keeping a configured number of copies. Add alert constructors for a failed or missed job, a failed backup check and a broker login needed, and send them. Start `wiki/operational/runbook.md`: halts and their reset, reconciliation differences, restoring a backup, and rolling back across a migration.
- **Verification:** A test backs up, restores and matches row counts; a corrupted copy fails the check and alerts; the every-day job runs on a weekend under the fake clock; a failed job alerts once.
- **Status:** [ ]

## Order and parallelism

Tasks 1 and 2 can run together (only 1 adds a migration, `0006`). Then 3 (after 1; adds migration `0007`). Then 4 and 5 together (both after 1, 2 and 3; 4 owns the proposal hook in the after-close job and 5 owns the 09:35 send and the settlement hook, so agree file boundaries in the briefs). Then 6 (after 4), 7 (after 5 and 6), and 8, which only needs the scheduler and can run alongside 6 or 7.

## Risks / open questions

- Paper fills are approximate at end-of-day resolution; compare with live fills later.
- A buy proposed at 21:00 after a late source still has to wait for the next 09:35 window, so a late day loses no session; a day still missing at 21:00 proposes nothing.
- The sector limit needs sector data; this part takes it from the strategy file rather than a feed, and live-trading can revisit.
- The 09:35 window assumes the after-close proposal finished the evening before; a container that was down overnight must not send stale approvals (expiry handles it).

## Verification strategy

CI gates from foundations, plus the idempotency, no-retry and halt tests named above, and a no-float guard on `internal/risk` and `internal/orders`.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md` and the hand-over notes for paper-trading tasks. When the last task lands, close it out: status done, a summary in `progress.md`, and archive this file to `.harness/PLAN.archive.YYYYMMDD-paper-trading.md`.

## Locked design calls

See the final [Sherwood Design](../wiki/designs/sherwood.md). In short: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.

Refinement calls (2026-11-18):
- Orders reuse `signals.Key` as their idempotency key, under the orders table's own unique constraint.
- Proposal happens in the after-close attempt that succeeds, not on its own clock, so a late source never produces a proposal from a missing day.
- Paper fills are settled against the day's checked bar after the close; sends happen at 09:35 ET, so a paper order is `accepted` during the day and filled or expired that evening.
- Holdings change only through fills (source `fills`) and accepted reconciliations (source `reconcile`).
- The effective limit is the tighter of the strategy file and the app's recorded tightenings since the file last changed, so a restart never silently loosens a limit set from the phone.
- Sector data for the sector limit lives in the strategy file.
- Protective sells skip the order-value check (operator, 2026-11-18): shares held bounds them, and a holding that grew past the limit must keep a stop for every share. Task 5 resizes or cancels a holding's resting stop before sending a trim or exit, so the two never cover more shares than are held.
