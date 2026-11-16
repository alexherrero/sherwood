---
parent_design_doc: ../../../../wiki/designs/sherwood.md
parent_part_slug: research-feed
---

# Plan: The research feed (from design sherwood)

**Status:** draft
**Created:** 2026-11-15
**Roadmap item:** part 5 of 6 of [Sherwood Design](../../../../wiki/designs/sherwood.md); part file [research-feed](../../../../wiki/designs/sherwood/parts/research-feed.md)
**Brief:** Publish the research export, accept uploaded feeds, run the nightly research job on the Mac under your Claude login, and show the feed, holding notes, signal explanations and the watchlist tap in the app.

## Goal

Each morning the app shows sourced candidate cases, notes on your holdings and plain-words explanations of the rules' signals. Nothing in the feed can create or send an order.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only.
- The research session has no shell and no tool that can send to the NAS.
- All feed text renders escaped.

## Out of scope

- Any AI part in creating, approving or sending orders.

## Tasks

### 1. Research export and feed token
- **What:** A read-only export of symbols, prices and signals behind the feed token, with no balances or account details.
- **Verification:** A test checks the export's fields against an allowlist.
- **Status:** [ ]

### 2. Feed schema and upload endpoint
- **What:** The feed schema (candidate case, holding note, signal explanation, each with sources) and an endpoint that can only add feed rows.
- **Verification:** Tests reject off-schema items and items without a source; an import-boundary test finds no path from the feed code to execution.
- **Status:** [ ]

### 3. Research skill and Mac job
- **What:** The research skill, the plain fetch-and-upload script, and the launchd job with its restricted tool list.
- **Verification:** A test checks the tool list; a dry run with a fake export writes a valid feed file.
- **Status:** [ ]

### 4. Feed view, notes and watchlist tap
- **What:** The Feed view with source links and domains, notes on Portfolio, explanations on approval cards, and "Add to watchlist".
- **Verification:** A feed item containing markup renders as plain text; the watchlist tap needs the anti-forgery token.
- **Status:** [ ]

### 5. Research job how-to
- **What:** Wiki how-to for setting up the job on the Mac.
- **Verification:** Placeholders only.
- **Status:** [ ]

## Risks / open questions

- The Mac asleep means a stale feed; trading carries on.

## Verification strategy

CI gates from foundations, plus the schema, escaping and import-boundary tests.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md`. This plan is queued: before starting it, archive the finished active plan to `.harness/archive/` and move this file to `.harness/PLAN.md`. Refine the task list with `/plan` before `/work`.

## Locked design calls

See the final [Sherwood Design](../../../../wiki/designs/sherwood.md). In short: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.
