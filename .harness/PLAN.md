---
parent_design_doc: ../wiki/designs/sherwood.md
parent_part_slug: research-feed
---

# Plan: The research feed (from design sherwood)

**Status:** in progress
**Created:** 2026-11-15 (refined 2026-11-19 against the merged paper-trading code)
**Roadmap item:** part 5 of 6 of [Sherwood Design](../wiki/designs/sherwood.md); part file [research-feed](../wiki/designs/sherwood/parts/research-feed.md)
**Brief:** Serve the research export, accept uploaded feeds, run the nightly research job on the Mac under your Claude login, and show the feed, holding notes, signal explanations and the watchlist tap in the app.

## Goal

Each morning the app shows sourced candidate cases, notes on your holdings and plain-words explanations of the rules' signals. Nothing in the feed can create, approve or send an order, and a stale feed changes nothing about trading.

## Constraints

- The repo is public: placeholders only, nothing personal. Example feeds, exports and launchd files use obviously fake symbols, paths and tokens.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes; any check that needs the real account is run by the operator.
- No agent runs the real research job, calls the real NAS or uses the real feed token. The first real run is the operator's.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only (next free number: `0008`).
- The research session has no shell and no tool that can send to the NAS.
- All feed text renders escaped, and source links are shown with their domain.

## Out of scope

- Any AI part in creating, approving or sending orders.
- Live trading and its broker adapter (part 6).

## What already exists

- `config.Settings.FeedToken` (`secret.Secret`, `feed_token`, at least 32 characters, compared with `Matches`, kept out of logs). `web.Config` has no field for it yet.
- Every route sits behind the Serve login guard (`internal/web/guard.go`); every POST also needs the session's anti-forgery token (`checkChange`). No route accepts a machine caller today. The phone-app plan left this open: "A later part must decide how the Mac's feed upload passes the login check."
- `/feed` is a placeholder in the views table (`internal/web/shell.go`). Portfolio already has empty `Suggestion` and `Note` fields (`internal/web/portfolio.go`). Approval cards show the rule's reason (`internal/web/approvals.go`, `card()`).
- Signals carry `signals.Key`, which is also each order's idempotency key, so an explanation can be matched to an approval card by key.
- The watchlist lives only in the strategy file (`Strategy.Watchlist`), read directly by the proposal, execution and approvals snapshots and by the price fetch. Rules signal only on each strategy's own `stocks:` list.
- `internal/limits` overlays app changes on the strategy file and resets them when the file's hash changes; it is the pattern for a watchlist overlay.
- `internal/web` already imports `internal/execution` and `internal/broker`, so the feed code must live in its own package for the import-boundary test to mean anything.
- The scheduler has daily jobs only (trading-day or `EveryDay`), no interval jobs.
- The strategy-setup skill (`.claude/skills/strategy-setup/`) and its Go helper subcommands in `cmd/sherwood/setup.go` are the model for the research skill and the Mac-side script.

## Tasks

### 1. Feed package, schema and storage (migration 0008)
- **What:** A new `internal/feed` package holding the export and feed types, the feed schema and its validator, and storage. The feed has three item kinds, each with plain text and one or more sources (URL plus title): a candidate case (a stock not held), a holding note (a held stock) and a signal explanation (carrying the signal's key). Validation rejects an off-schema file outright and drops any item without a source, an unknown kind, a non-http(s) link or text over a fixed length, saying why for each. Migration `0008` adds an append-only `feed_uploads` table (time, item counts, dropped counts) and an append-only `feed_items` table (upload, kind, symbol, signal key, text, sources as JSON), with indexes for the latest note per symbol and the explanation per key. Readers: latest upload, candidates from the latest upload, latest note per held symbol, explanation by key. An import-boundary test runs over `internal/feed`'s transitive imports and refuses `internal/execution`, `internal/broker`, `internal/orders`, `internal/proposal`, `internal/halt` and `internal/risk`, with a self-test that it catches a planted import.
- **Verification:** Tests reject an off-schema file and drop items without a source or with a `javascript:` link; storage round-trips; the import-boundary test passes and its self-test fails on a planted import; a no-float guard covers the package.
- **Status:** [ ]

### 2. Export and upload endpoints behind the feed token
- **What:** A machine route kind in `internal/web` (`HandleMachine`) for the Mac's script: the Serve login guard still applies, then `Authorization: Bearer <feed token>` checked with `FeedToken.Matches`; no session cookie or anti-forgery token, JSON only, body capped, never logged. `GET /research/export` builds the export on request: the symbols held and watched, their recent checked closes, and the latest trading date's signals with their rule reasons, with no balances, share counts, cash, order details or account details. `POST /feed/upload` validates with `internal/feed`, stores the accepted items and the upload row in one transaction, and answers with what was accepted and dropped; it can only add rows. The upload is taken in when it arrives rather than by a 15-minute job; amend the design's schedule row and add an amendment-log entry in the same change.
- **Verification:** A test checks the export's fields against an allowlist; a request without the token, with a wrong token, or without the Serve login is refused; an off-schema upload is refused and stores nothing; an upload with a sourceless item stores the rest and reports the drop; the token never appears in a log line.
- **Status:** [ ]

### 3. Research skill, Mac script and launchd job
- **What:** `sherwood research-export` and `sherwood research-upload` subcommands (beside `fetch-bars` in `cmd/sherwood/setup.go`) that download the export into the job's work folder and validate and upload a feed file, using the same `internal/feed` validator as the NAS. The research skill (`.claude/skills/research/SKILL.md`, with an example feed using fake symbols) tells Claude to read the export, research the web, and write one feed JSON file in its output folder. An example launchd plist and a small wrapper that runs export, then headless Claude with the skill and a restricted tool list (read, web fetch, web search, and write limited to the output folder; no shell, no network tool that can reach the NAS), then upload. The token and NAS address come from a file outside the repo, with placeholders in the examples.
- **Verification:** A test checks the tool list in the wrapper has no shell and no tool that can send to the NAS; a dry run with a fake export and a fake NAS (an `httptest` server) writes and uploads a valid feed file; the example files hold placeholders only.
- **Status:** [ ]

### 4. Feed view, holding notes, explanations and the stale note
- **What:** The Feed view lists the latest upload's candidate cases with each source's link and domain. Portfolio fills each holding's `Note` from the latest holding note (and `Suggestion` from `holding_suggestions` while there). Approval cards show the signal explanation matched by the order's key, beside the rule's reason. When the latest upload is older than a fixed threshold, or there is none, the Feed view shows a quiet note and the cards fall back to the rules' own reasons; trading is unaffected and no alert is sent.
- **Verification:** A feed item containing markup renders as plain text; a link shows its domain; a stale feed shows the quiet note and changes no order, check or job.
- **Status:** [ ]

### 5. The watchlist tap
- **What:** An "Add to watchlist" button on each candidate case (POST, anti-forgery token). The addition is recorded in the app (migration `0009`, or a `settings_changes`-style overlay as `internal/limits` does) and read through one effective-watchlist reader that replaces the direct `strategy.Watchlist` reads in the proposal, execution, approvals and price-fetch code. What an addition makes the rules do is the open question below; build what the operator decides.
- **Verification:** The tap needs the anti-forgery token; an addition is recorded and appears in the effective watchlist; the effective watchlist resets when the strategy file changes, as limits do; no order is created by the tap.
- **Status:** [ ]

### 6. Research job how-to
- **What:** `wiki/how-to/set-up-the-research-job.md`: building the helper, the work folder, the token file, the launchd job, a first dry run, and what a stale feed means. Add a stale-feed line to the runbook.
- **Verification:** Placeholders only; the page links from Home.
- **Status:** [ ]

## Order and parallelism

Task 1 first (adds migration `0008`). Then 2 and 3 side by side (2 owns `internal/web`'s machine routes; 3 owns `cmd/sherwood/setup.go`, the skill and the Mac files). Then 4 (after 2; owns the Feed, Portfolio and Approvals views). Then 5 (after 4 and the operator's answer; adds `0009` if it needs a table). Task 6 alongside 4 or 5.

## Risks / open questions

- **Open, for the operator:** what does adding a candidate to the watchlist do? The design says "the rules decide whether to buy it", but rules only run on each strategy's `stocks:` list today, so a watchlist addition alone would only start its price fetch and let the risk check pass it. Either strategies gain an opt-in to run their rules over the watchlist too, or the tap only watches the stock and making it tradable stays a strategy-file change through `/strategy-setup`.
- The Mac asleep means a stale feed; trading carries on.
- Prompt injection through news can only produce a bad feed item; the escaping, the schema and the import boundary are the defences.

## Verification strategy

CI gates from foundations, plus the schema, escaping, token and import-boundary tests above.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md` and the hand-over notes for research-feed tasks. When the last task lands, close it out: status done, a summary in `progress.md`, and archive this file to `.harness/PLAN.archive.YYYYMMDD-research-feed.md`.

## Locked design calls

- The design's one rule stands: the AI never creates, approves or sends an order, and the feed code has no import path to execution.
- **The Mac's script passes the Serve login and then the feed token.** The Mac is signed in to the tailnet as the owner, so it passes the login guard; the feed routes then require the bearer token instead of the session's anti-forgery token. A tagged device or another login is still refused.
- **The export is built on request**, not published by the 16:30 job; it carries symbols, recent closes and signal reasons only.
- **An upload is validated and stored when it arrives**, in one transaction, rather than staged for a 15-minute intake job; task 2 amends the design.
- Feed rows are append-only and kept; the app shows the latest upload's candidates and the latest note per holding.
- The Mac-side script is a `sherwood` subcommand, so the NAS and the Mac validate with the same code.
- See the final [Sherwood Design](../wiki/designs/sherwood.md) for the rest: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; broker secrets only in the owner-only settings file on the NAS.
