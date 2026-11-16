---
parent_design_doc: ../../../../wiki/designs/sherwood.md
parent_part_slug: prices-and-rules
---

# Plan: Prices, rules and the setup skill (from design sherwood)

**Status:** draft
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

## Out of scope

- Turning signals into orders (paper-trading). AI explanations (research-feed).

## Tasks

### 1. Price sources and cross-check
- **What:** A price-source interface, a primary and a cross-check free end-of-day source, a bars table, and fail-closed handling of gaps and disagreements.
- **Verification:** Tests with fake sources show no signals on a missing day and none for a stock whose sources disagree.
- **Status:** [ ]

### 2. Rule library
- **What:** Moving-average cross, breakout, momentum ranking, pullback, fixed stop, trailing stop and sizing rules, each with a mechanical reason.
- **Verification:** Table tests per rule cover signal and no-change days.
- **Status:** [ ]

### 3. Rule runner and signals
- **What:** Run each strategy's rules for the day, record signals with reasons, the strategy hash and their idempotency keys, and the hold, add or trim suggestion per holding.
- **Verification:** Two runs on the same inputs give identical signals and keys.
- **Status:** [ ]

### 4. The 16:30 ET job
- **What:** Register the after-close job: fetch, cross-check, value the portfolio, run rules.
- **Verification:** Fake-clock tests run it once per trading day and catch up after downtime.
- **Status:** [ ]

### 5. Back-tester
- **What:** Replay past bars through the same rule functions with the paper fill model, and report return, drawdown, win rate and turnover.
- **Verification:** The same inputs give the same report on every run.
- **Status:** [ ]

### 6. Setup skill
- **What:** A skill in the repo that interviews you on the Mac, writes a strategy file from the rule library, validates it and back-tests it.
- **Verification:** Its output passes validation; its examples use placeholders only.
- **Status:** [ ]

### 7. Rule library reference and setup how-to
- **What:** Wiki reference for every rule type and a how-to for the setup skill.
- **Verification:** Every rule type in code appears on the reference page.
- **Status:** [ ]

## Risks / open questions

- Choosing the two free price sources: their terms and limits decide the pick; record it in the design's Document History.
- Splits: a share-count change at the broker halts trading until confirmed; the rule side uses split-adjusted bars.

## Verification strategy

CI gates from foundations, plus determinism tests for the runner and the back-tester.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md`. This plan is queued: before starting it, archive the finished active plan to `.harness/archive/` and move this file to `.harness/PLAN.md`. Refine the task list with `/plan` before `/work`.

## Locked design calls

See the final [Sherwood Design](../../../../wiki/designs/sherwood.md). In short: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.
