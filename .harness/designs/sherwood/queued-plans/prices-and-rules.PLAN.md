---
parent_design_doc: ../../../../wiki/designs/sherwood.md
parent_part_slug: prices-and-rules
---

# Plan: Prices, rules and the setup skill (from design sherwood)

**Status:** in progress
**Created:** 2026-11-15
**Roadmap item:** part 3 of 6 of [Sherwood Design](../../../../wiki/designs/sherwood.md); part file [prices-and-rules](../../../../wiki/designs/sherwood/parts/prices-and-rules.md)
**Brief:** Fetch free end-of-day prices with a cross-check, build the rule library and the rule runner that turns prices into signals, schedule the 16:30 ET job, build the back-tester, and write the setup skill that generates a strategy file on the Mac.

## Goal

After each trading day's close, sherwood records prices and signals with plain reasons and the strategy file's hash, and you can back-test any strategy. The setup skill writes a valid strategy file for your install.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only.
- Rules are pure functions of prices, holdings, the strategy file and the trading date.
- No test reaches a price source over the network; recorded-shape fixtures use made-up values.
- This plan runs alongside phone-app. Only one session at a time, across both plans, may add a migration; the coordinator names its number in the brief, because the runner refuses gaps and repeats.
- `cmd/sherwood/main.go`, `internal/logging/allowlist.go` and `go.mod` are shared with phone-app: keep edits there small and rebase (never back-merge) when `main` moves.

## Out of scope

- Turning signals into orders (paper-trading). AI explanations (research-feed).

## Tasks

### 1. Price sources and cross-check
- **What:** Add `internal/prices`: a price-source interface returning split-adjusted daily bars (open, high, low, close as `money.Price`, volume), a primary and a cross-check free end-of-day source (keyless or free-key, chosen on their terms and limits, recorded in the design's Document History), a fake for tests, a migration for a bars table, and fail-closed handling: a missing day gives no bars for anyone that day, and a held or watched stock whose sources disagree beyond `price_cross_check_tolerance` is marked so it gets no signals, with a held stock flagged for a halt.
- **Verification:** Tests with fake sources show nothing stored as usable on a missing day, a disagreeing stock excluded and flagged, and parsing of each source's recorded response shape (fixtures hand-written with made-up values).
- **Status:** [x] Shipped in [#89](https://github.com/alexherrero/sherwood/pull/89) (2026-11-16). `internal/prices` has the source interface, a fake, Tiingo as the primary (a documented API on a free personal-use plan with split factors) and Stooq as the cross-check (a free key since early 2026), both keys as `secret.Secret` settings under `prices:`, and the pick recorded in the design's Document History. Migration 0002 adds the bars, day and check tables. Only the trading date's own bar is compared across sources; any gap makes the whole day unusable, and a stock whose sources disagree beyond the tolerance is excluded, with a held one flagged for a halt that paper-trading will act on. History is re-fetched from the primary on each run, so it follows a split. Tests use hand-written fixtures and `httptest` only. Open points for the 16:30 job: a source may not have published the day's bar by 16:30, so a later retry may be needed, and Tiingo's 50 requests an hour bounds re-runs.

### 2. Rule library
- **What:** Add `internal/rules`: moving-average cross, breakout, momentum ranking, pullback, fixed stop, trailing stop, and sizing by fixed amount and by risk, each a pure function with its own settings schema parsed from the strategy file's `config.Scalar` text, each outcome (buy, add, trim or sell, no change) carrying a short mechanical reason. Maths on `Decimal()` with an explicit precision, back through `money.RoundPrice` and the `QuantityPolicy`. The rule registry here and `config`'s known rule types must agree, checked by a test.
- **Verification:** Table tests per rule cover signal and no-change days and bad settings; the registry test fails when a name is added to one side only.
- **Status:** [x] Shipped in [#90](https://github.com/alexherrero/sherwood/pull/90) (2026-11-16). `internal/rules` implements all eight rule types as pure functions with their own settings schemas, parsed from the strategy file's text with errors naming the setting. Signal rules fire on the day their condition starts to hold, so a declined buy is not proposed again the next day; stops are levels and sell every day a holding sits at or below them. Comparisons cross-multiply instead of dividing, so every one is exact, and a no-float guard covers the package. `config.RuleTypes()` stays the one list of names and a test fails if the rule definitions drift from it. The example strategy file now passes `rules.Validate`, but nothing calls `Validate` at start-up yet; task 3 does that. Rules can need up to 302 bars of history.

### 3. Rule runner and signals
- **What:** Run each strategy's rules for one trading date, recording signals with their reasons, the strategy file's hash and their idempotency keys (strategy, stock, side, signal kind, trading date) under a unique constraint (a migration), and the hold, add or trim suggestion per holding.
- **Verification:** Two runs on the same inputs give identical signals and keys and a single row each; an excluded stock (task 1) gets no signal.
- **Status:** [ ]

### 4. The 16:30 ET job
- **What:** Register the after-close job with the scheduler in `cmd/sherwood`: fetch and cross-check the day's bars, value the portfolio from the holdings and cash tables (phone-app task 3), run the rules, and record everything under the run's input hash. Publishing the research export is left to research-feed.
- **Verification:** Fake-clock tests run it once per trading day, skip holidays, catch up after downtime with each run using its own date's bars, and produce no signals on a missing day.
- **Status:** [ ]

### 5. Paper fill model and back-tester
- **What:** Add the paper fill model as a pure function (a buy fills at the next open if the open is at or below its limit, otherwise at the limit if the day traded through it; a stop fills when the low reaches it, at the stop or at the open if it opened below), so paper-trading's paper broker reuses it. Then the back-tester: replay past bars through the same rule functions and fill model, and report return, drawdown, win rate and turnover per strategy, as a `sherwood backtest` subcommand.
- **Verification:** Table tests for every fill case; the same inputs give the same report on every run.
- **Status:** [x] Shipped in [#95](https://github.com/alexherrero/sherwood/pull/95) (2026-11-18). `internal/fill` is the paper fill model as a pure function: a buy limit fills at the open when the open is at or below the limit, otherwise at the limit only if the low went strictly below it (a touch is not a fill); a protective stop fills at the open on a gap, otherwise at the stop when the low reaches it; a rule exit or trim sells at the open. `internal/backtest` replays bars day by day with no look-ahead (a test proves a cut-short run matches), combines each stock's rules in a fixed order (stop sell, other sell, trim, buy or add) with the first rule's reason, sizes buys by the strategy's sizing rule at a limit above the close, keeps each strategy to its own cash and `max_holdings`, and reports return, maximum drawdown, win rate and turnover in exact decimals, byte-identical on every run. `sherwood backtest` reads split-adjusted CSV bars from a folder and needs no database or network. The order risk limits are not applied in a back-test, which task 7's docs must say. Task 3's runner must combine rules the same way.

### 6. Setup skill
- **What:** A skill in the repo that interviews you on the Mac (goals, amount, loss tolerance, sectors), writes a strategy file from the rule library, validates it with the real loader, and back-tests it. It never sees the broker login and never talks to the NAS, and says plainly that its output is not financial advice.
- **Verification:** Its output passes validation; its examples use placeholders only; a privacy scan of the skill finds nothing personal.
- **Status:** [ ]

### 7. Rule library reference and setup how-to
- **What:** Wiki reference for every rule type and its settings, and a how-to for the setup skill.
- **Verification:** Every rule type in code appears on the reference page, checked by a script.
- **Status:** [ ]

## Order and parallelism

Tasks 1 and 2 can run together (only 1 adds a migration). Then 3 (after 1 and 2; adds a migration) and 5 (after 2). Task 4 waits for 3 and for phone-app task 3 (holdings tables). Then 6 (after 5), and 7 last.

## Risks / open questions

- Free sources change terms and have gaps; the cross-check and fail-closed handling contain the damage, but a shared upstream error would pass.
- Splits: a share-count change at the broker halts trading until confirmed (live-trading); the rule side uses split-adjusted bars.
- The sector limit needs a sector source (SEC industry codes); it is optional, so it can wait for paper-trading's risk check.

## Verification strategy

CI gates from foundations (task 10), plus determinism tests for the runner and the back-tester.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md`. When the last task lands, close it out: status done, a summary in `progress.md`, and archive this file to `.harness/PLAN.archive.YYYYMMDD-prices-and-rules.md`.

## Locked design calls

See the final [Sherwood Design](../../../../wiki/designs/sherwood.md). In short: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.

Refinement calls (2026-11-16):
- The paper fill model is built here as a pure function, because the back-tester needs it before the paper broker exists; paper-trading wraps it.
- Signals get their idempotency keys here; paper-trading's orders reuse the same key under their own unique constraint.
