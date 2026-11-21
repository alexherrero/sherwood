---
parent_design_doc: ../wiki/designs/sherwood.md
parent_part_slug: research-feed
---

# Plan: The research feed (from design sherwood)

**Status:** done
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
- **Status:** [x] Shipped in [#129](https://github.com/alexherrero/sherwood/pull/129) (2026-11-21). `internal/feed` holds the export types (`Export`, `ExportSymbol`, `Close`, `ExportSignal`, strict `DecodeExport`), the feed file types and `Check(data, known)`, and storage. An off-schema file is refused whole (over 1 MiB, unknown fields, wrong types, trailing data, wrong version, more than 200 items); individual items are dropped with a reason for no source, an unknown kind, a non-http(s) link, text over 2000 characters, a candidate on a held stock, a note on a stock not held, an explanation whose key is not in the export's signals, and repeats. `Known` comes from the export, so the NAS and the Mac drop the same items. Migration `0008` adds append-only `feed_uploads` and `feed_items` with CHECKs that mirror `Check`; `Save` writes the upload and its items in one transaction, and the readers give the latest upload, its candidates, the latest note per held symbol and the explanation for a signal key. An upload with every item dropped still counts as fresh. A parser-walk import-boundary test refuses execution, broker, orders, proposal, halt and risk with a self-test, and a no-float guard covers the package. Next free migration: `0009`.

### 2. Export and upload endpoints behind the feed token
- **What:** A machine route kind in `internal/web` (`HandleMachine`) for the Mac's script: the Serve login guard still applies, then `Authorization: Bearer <feed token>` checked with `FeedToken.Matches`; no session cookie or anti-forgery token, JSON only, body capped, never logged. `GET /research/export` builds the export on request: the symbols held and watched, their recent checked closes, and the latest trading date's signals with their rule reasons, with no balances, share counts, cash, order details or account details. `POST /feed/upload` validates with `internal/feed`, stores the accepted items and the upload row in one transaction, and answers with what was accepted and dropped; it can only add rows. The upload is taken in when it arrives rather than by a 15-minute job; amend the design's schedule row and add an amendment-log entry in the same change.
- **Verification:** A test checks the export's fields against an allowlist; a request without the token, with a wrong token, or without the Serve login is refused; an off-schema upload is refused and stores nothing; an upload with a sourceless item stores the rest and reports the drop; the token never appears in a log line.
- **Status:** [x] Shipped in [#131](https://github.com/alexherrero/sherwood/pull/131) (2026-11-21). `Server.HandleMachine` registers routes for the Mac's script: the Serve login guard still applies, then a request carrying `Sec-Fetch-Site` (a browser) is refused, `Authorization: Bearer <feed token>` is required and checked with `FeedToken.Matches`, uploads must be JSON and are capped at `feed.MaxFileBytes`, and every refusal logs only a fixed reason. `web.Config.FeedToken` is required. `feed.BuildExport` builds the export from the held symbols (symbol column only), the watchlist, the newest 30 usable closes and the latest rules-run date's signals; `GET /research/export` serves it and an allowlist test pins its fields. `POST /feed/upload` builds the export at arrival, runs `feed.Check`, refuses an off-schema file with 400 and stores nothing, and otherwise saves the upload and answers with its id, the accepted count and every drop. The design's schedule now takes a feed in when it is uploaded, with a Document History row, and the settings reference says what the token opens. No migration.

### 3. Research skill, Mac script and launchd job
- **What:** `sherwood research-export` and `sherwood research-upload` subcommands (beside `fetch-bars` in `cmd/sherwood/setup.go`) that download the export into the job's work folder and validate and upload a feed file, using the same `internal/feed` validator as the NAS. The research skill (`.claude/skills/research/SKILL.md`, with an example feed using fake symbols) tells Claude to read the export, research the web, and write one feed JSON file in its output folder. An example launchd plist and a small wrapper that runs export, then headless Claude with the skill and a restricted tool list (read, web fetch, web search, and write limited to the output folder; no shell, no network tool that can reach the NAS), then upload. The token and NAS address come from a file outside the repo, with placeholders in the examples.
- **Verification:** A test checks the tool list in the wrapper has no shell and no tool that can send to the NAS; a dry run with a fake export and a fake NAS (an `httptest` server) writes and uploads a valid feed file; the example files hold placeholders only.
- **Status:** [x] Shipped in [#132](https://github.com/alexherrero/sherwood/pull/132) (2026-11-21). `sherwood research-export` downloads the export with the bearer token, checks it with `feed.DecodeExport` and writes it owner-only; `sherwood research-upload` runs the same `feed.Check` the NAS runs, prints what would be dropped, and posts the file byte for byte (`-check-only` sends nothing). Both read `nas_url` and `feed_token` from an owner-only `research.yaml` outside the repo, refuse the placeholder token, plain http to anything but loopback and redirects, and never print the token. `.claude/skills/research/` holds the skill (the AI never suggests a size or an action; web text is data; public pages only; a source on every item; exactly one `out/feed.json`) with an example export and feed that check clean. `deploy/mac/` holds the wrapper script, an example launchd plist (21:30 nightly) and an example config; the wrapper runs the export, then `claude -p --restricted --strict-mcp-config` in `dontAsk` mode with only Read, Write, WebFetch and WebSearch, writes limited to the output folder, Bash and `*.ts.net` fetches denied, the skill copied in so Claude never sees the repo or the token, then the upload. A test parses the wrapper and fails on a shell, an extra tool or a missing denial, and a dry run against a fake NAS uploads a valid feed with the bearer token. The `claude` flags were taken from its help and docs, not run; the first real run is the operator's. No migration.

### 4. Feed view, holding notes, explanations and the stale note
- **What:** The Feed view lists the latest upload's candidate cases with each source's link and domain. Portfolio fills each holding's `Note` from the latest holding note (and `Suggestion` from `holding_suggestions` while there). Approval cards show the signal explanation matched by the order's key, beside the rule's reason. When the latest upload is older than a fixed threshold, or there is none, the Feed view shows a quiet note and the cards fall back to the rules' own reasons; trading is unaffected and no alert is sent.
- **Verification:** A feed item containing markup renders as plain text; a link shows its domain; a stale feed shows the quiet note and changes no order, check or job.
- **Status:** [x] Shipped in [#136](https://github.com/alexherrero/sherwood/pull/136) (2026-11-21). The Feed view lists the latest upload's candidate cases with their sources, each title linked with `noopener noreferrer` and its domain shown beside it; only http(s) links with a host get a link, and a non-ASCII host gets a look-alike warning. Portfolio shows each holding's latest feed note and every strategy's latest suggestion with its stop, and approval cards show the explanation matched by the order's key under the rule's reason. `feed.StaleAfter` is 36 hours: a note or explanation shows only while its own upload is fresh, the Feed view shows a quiet note when the latest upload is stale or missing (still listing the candidates), and nothing is alerted. All feed text renders through `html/template` escaping. A web test renders all three pages with no, stale and fresh feeds and finds orders, history, the risk checker's inputs, job runs and alerts unchanged, and a second boundary test refuses any trading-path package importing `internal/feed`. No migration.

### 5. The watchlist tap
- **What:** An "Add to watchlist" button on each candidate case (POST, anti-forgery token). The addition is recorded in the app (migration `0009`, or a `settings_changes`-style overlay as `internal/limits` does) and read through one effective-watchlist reader that replaces the direct `strategy.Watchlist` reads in the proposal, execution, approvals and price-fetch code. Strategies gain an opt-in in the strategy file (for example `include_watchlist: true`); a strategy that opts in runs its rules over the effective watchlist as well as its own `stocks:`, so an added stock can produce a buy signal, and that buy still passes the risk check and waits for approval. The strategy-setup skill, the example strategy, the settings reference and the design (with an amendment-log entry) are updated to match.
- **Verification:** The tap needs the anti-forgery token; an addition is recorded and appears in the effective watchlist; the effective watchlist resets when the strategy file changes, as limits do; a strategy that opts in signals on an added stock and one that doesn't never does; no order is created by the tap itself.
- **Status:** [x] Shipped in [#138](https://github.com/alexherrero/sherwood/pull/138) (2026-11-21). Each candidate on the Feed view has an "Add to watchlist" button (`POST /watchlist/add`, approval devices only, anti-forgery token); a stock already watched shows that instead, and while a sector limit is in force a stock with no sector in the strategy file shows how to add one and no button. `limits.Watch` records the addition as a `settings_changes` row (`watchlist.add`, source `phone`), and `limits.Watchlist` gives the effective watchlist: the file's, then the app's additions made under the loaded file's hash, so they reset when the file changes, exactly like tightenings. It replaces every direct watchlist read: the proposal, execution and approvals snapshots, the after-close price fetch and rule run, and the research export. Strategies gain an optional `include_watchlist`; one that opts in runs its rules over the effective watchlist as well as its own stocks, and any buy still goes through the proposal, the risk check and approval; the tap creates no order. The settings reference, the strategy-setup skill and its example, the example strategy, the design (Document History row for the 2026-11-20 decision), the how-to and the runbook (now naming the 36-hour threshold) were updated. No migration.

### 6. Research job how-to
- **What:** `wiki/how-to/set-up-the-research-job.md`: building the helper, the work folder, the token file, the launchd job, a first dry run, and what a stale feed means. Add a stale-feed line to the runbook.
- **Verification:** Placeholders only; the page links from Home.
- **Status:** [x] Shipped in [#134](https://github.com/alexherrero/sherwood/pull/134) (2026-11-21). `wiki/how-to/set-up-the-research-job.md` walks through what the job does and the one rule, what the Mac needs (on the tailnet as the owner, Go, Claude Code 2.1.248 or later, a signed-in `claude`, the feed token), building the helper into an owner-only folder, writing `research.yaml` without the token touching the shell history, a check-only run with the example feed, a first run by hand, installing and reading the launchd job, what a stale feed means, a troubleshooting table keyed on the real messages, and the WebFetch-and-tailnet residual risk. The runbook gains a stale-feed and failed-upload section, and Home links the page. Docs only.

## Order and parallelism

Task 1 first (adds migration `0008`). Then 2 and 3 side by side (2 owns `internal/web`'s machine routes; 3 owns `cmd/sherwood/setup.go`, the skill and the Mac files). Then 4 (after 2; owns the Feed, Portfolio and Approvals views). Then 5 (after 4; adds `0009` if it needs a table). Task 6 alongside 4 or 5.

## Risks / open questions

- **Decided by the operator (2026-11-20):** a stock added to the watchlist is run through the rules of every strategy that opts in to the watchlist, as the design describes; see the locked calls.
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
- **A watchlist addition is run through the rules** of each strategy that opts in to the watchlist (operator, 2026-11-20). Any resulting buy is proposed, risk-checked and waits for approval like any other; the tap itself never creates an order.
- Feed rows are append-only and kept; the app shows the latest upload's candidates and the latest note per holding.
- The Mac-side script is a `sherwood` subcommand, so the NAS and the Mac validate with the same code.
- See the final [Sherwood Design](../wiki/designs/sherwood.md) for the rest: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; broker secrets only in the owner-only settings file on the NAS.
