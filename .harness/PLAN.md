---
parent_design_doc: ../wiki/designs/sherwood.md
parent_part_slug: phone-app
---

# Plan: Phone app and read-only holdings (from design sherwood)

**Status:** in progress
**Created:** 2026-11-15
**Roadmap item:** part 2 of 6 of [Sherwood Design](../wiki/designs/sherwood.md); part file [phone-app](../wiki/designs/sherwood/parts/phone-app.md)
**Brief:** Put sherwood on your phone as an installable web app over Tailscale Serve, with identity, anti-forgery and approval-device checks, the Today and Portfolio views and web push. Add the broker helper's login command and read-only holdings and cash calls, and import holdings read-only.

## Goal

You can install sherwood on your home screen, see Today and your Portfolio, and receive a test notification. Your holdings are imported without any order being possible.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only.
- Only GET reads and only POST changes; every POST carries an anti-forgery token.
- This plan runs alongside prices-and-rules. Only one session at a time, across both plans, may add a migration; the coordinator names its number in the brief, because the runner refuses gaps and repeats.
- `cmd/sherwood/main.go`, `internal/logging/allowlist.go` and `go.mod` are shared with prices-and-rules: keep edits there small and rebase (never back-merge) when `main` moves.

## Out of scope

- Orders, approvals and the stop switch (paper-trading). Prices and suggestions (prices-and-rules).

## Tasks

### 1. Web server and the Serve identity check
- **What:** Add `internal/web`: an HTTP server on `:8080` (the port `compose.example.yaml` reserves) started and stopped by `cmd/sherwood` with the rest of the app. Middleware requires the allowed `Tailscale-User-Login` header, refuses anything else, and checks an anti-forgery token on every POST. Approving a buy and resetting the stop switch can be limited to approval devices, identified by the tailnet address Serve forwards (`X-Forwarded-For`, trusted only because the port is bound to loopback). Change `approval_devices` in `internal/config` to tailnet addresses (100.64.0.0/10 or `fd7a:115c:a1e0::/48`) and update the example file and the settings reference page to match.
- **Verification:** Tests refuse a missing or wrong login, a POST without a token or with a stale one, a GET that would change state, and an approval-device POST from another address; start-up tests show the server stopping cleanly on SIGTERM.
- **Status:** [x] Shipped in [#88](https://github.com/alexherrero/sherwood/pull/88) (2026-11-16). `internal/web` serves on `:8080`, started and stopped by `cmd/sherwood` with the rest of the app and shut down cleanly on SIGTERM. Every route, `/health` included, sits behind one guard: security headers on every response (a CSP with no inline script or style, frame denial, `no-store` by default), exactly one `Tailscale-User-Login` header equal to the allowed login, and on every POST an anti-forgery token checked in constant time plus a `Sec-Fetch-Site` check. Views mount with `HandleRead` (GET only) and actions with `HandleChange` (POST only); `web.ApprovalDevicesOnly` limits an action to approval devices, identified by the address Serve forwards. `approval_devices` in the settings file are now tailnet addresses, and a wrong entry is refused without echoing its value. Logs carry the matched route pattern, never the raw path, address or login. The design's wording was amended to match. A later part must decide how the Mac's feed upload passes the login check.

### 2. Installable app shell
- **What:** Server-rendered layout built for phones (Go `html/template`, contextual escaping), the app manifest and icon, a little JavaScript, and a service worker that caches only the shell (layout, styles, icon). Navigation between the six views, with the ones not built yet saying so.
- **Verification:** A test confirms the service worker's cache list has no data routes; a test confirms every data response sends `Cache-Control: no-store`; a manual install on the phone opens full-screen (operator check).
- **Status:** [x] Shipped in [#93](https://github.com/alexherrero/sherwood/pull/93) (2026-11-18). `internal/web` now renders a phone-first layout with `html/template` from embedded templates and static files, with Today at `/` and the five other views at `/approvals`, `/portfolio`, `/feed`, `/orders` and `/settings`, each saying "Not built yet" until its task lands. The manifest, an original pine-tree icon (PNG sizes drawn from one tested description) and light and dark themes make it installable. The service worker is served at `/sw.js` for root scope, caches only the seven shell files under a version that changes with them, always sends navigations to the network and shows a built-in offline page rather than any saved data. Tests prove the worker's list holds no data route and that every data response, including errors and refusals, is `no-store`. A later view plugs in by adding a template and a `load` function to the `views` table; the operator still has to install it on the phone and check it opens full-screen.

### 3. Holdings and cash tables
- **What:** A migration for holdings (symbol, quantity, average cost, source, as-of time) and cash snapshots, using `money` and `Quantity` as TEXT in STRICT tables, with store accessors. These are shared with prices-and-rules, whose 16:30 job values the portfolio from them.
- **Verification:** Round-trip tests through the store; no REAL column (the store's existing test enforces it); a test refuses a 21st holding being written without the over-20 flag.
- **Status:** [x] Shipped in [#92](https://github.com/alexherrero/sherwood/pull/92) (2026-11-18). Migration `0003` keeps holdings and cash as add-only snapshots, the newest being current, with `current_holdings` and `current_cash` views. Triggers refuse updates and deletes, out-of-order snapshots and rows beyond a snapshot's stated count, and the schema ties the over-20 flag to the count, so a 21st holding cannot be written without it. The new `internal/holdings` package records a set and its cash in one transaction (or on the caller's), reads the current or any earlier set, values holdings exactly with the money types, and refuses bad symbols, repeats, zero shares, negative costs and out-of-order times without writing anything. Snapshot sources are import, fills and reconcile, so later parts can record fills and accept broker figures without a new migration.

### 4. Today and Portfolio views
- **What:** Today shows the paper or live banner (words, not colour alone), a placeholder stop switch, any halt, and the last run of each job from `job_runs`. Portfolio lists holdings with value and gain or loss from stored data, at most 20, with room for the suggestion and AI note columns later parts fill.
- **Verification:** Handler tests render both views from fixture data with placeholder symbols; the banner test checks the text; values render from `money` types without floats.
- **Status:** [x] Shipped in [#99](https://github.com/alexherrero/sherwood/pull/99) (2026-11-18). Today shows the paper or live banner in words, a placeholder stop switch, any halt and the last run of each job from `job_runs`; Portfolio lists the current holdings and cash from `internal/holdings`, with a "not imported yet" state, an over-limit warning in words, values and gain or loss from exact money types ("Not available" until a checked close exists), and empty suggestion and note columns for later parts. `web.Config` now requires the store and the mode, passed from `cmd/sherwood`. Both views are built on the shared components from the layout redesign (#97); the operator is still choosing the final design, so a later layout change may restyle them.

### 5. Web push channel
- **What:** A `Notifier` interface with a fake, a web push implementation using the VAPID keys already in the settings file (pick a maintained Go web push library and record it), a migration for push subscriptions, and a subscribe flow in the app.
- **Verification:** Tests check the payload carries only the short alert text (no amounts, symbols or reasons) and that no test reaches a push service; a test push reaches the phone (operator check).
- **Status:** [ ]

### 6. Broker helper login and read-only calls
- **What:** Add the unofficial Robinhood client library to the helper, pinned to one reviewed version with hashes. Add the one-time login command (`python3 -m brokerhelper login`) that prompts on the terminal and writes the owner-only `broker.yaml` (mode 0600, refuses to write anywhere else), and two read-only ops, `holdings` and `cash`, returning decimals as strings. The library is reached only through a thin wrapper that tests replace with a fake.
- **Verification:** Helper tests run against a fake library and never import the real one's network paths; a test confirms the helper offers no order ops yet; a test confirms no secret value reaches stdout, stderr or an error reply. The real login is run by the operator.
- **Status:** [x] Shipped in [#100](https://github.com/alexherrero/sherwood/pull/100) (2026-11-18). The helper pins `robin-stocks` 3.4.0 with hashes in both lock files, reached only through a thin wrapper that tests replace with a fake. `python3 -m brokerhelper login` prompts on the terminal and writes the owner-only `broker.yaml` (mode 0600, refusing any other place), and two read-only ops, `holdings` and `cash`, return decimals as strings. Tests prove the helper offers no order op, never imports the library's network paths, and never lets a secret reach stdout, stderr or an error reply. No agent ran the real login: the first login, the file check and noting how long the session lasts are the operator's.

### 7. Read-only holdings import
- **What:** A one-shot subcommand, `sherwood import-holdings`, that starts the broker helper just for the call (the long-running service still starts it only in live mode), reads holdings and cash through the Go client, writes them to the tables from task 3, and flags more than 20 holdings. It creates no order rows and has no path to any order op.
- **Verification:** Tests against the fake helper import holdings and cash, create no order rows, and halt on any helper error with no retry; the first real import is run by the operator.
- **Status:** [x] Shipped in [#102](https://github.com/alexherrero/sherwood/pull/102) (2026-11-18). `sherwood import-holdings` starts the broker helper once on a private socket just for the run (the service still starts it only in live mode), calls the read-only `holdings` op with a 120-second limit and `cash` with 30 seconds, decodes replies strictly so a changed shape stops the import, upper-cases symbols, and writes holdings and cash as one import snapshot in a single `holdings.Record` call, after the helper has exited. More than 20 holdings is recorded with the flag and a warning line, never refused. Any helper error, bad reply or refused value halts with "nothing was written" and no retry or restart. It prints a table to compare with the broker's app and has no path to any order op. The first real import, and the check that the cash figure is the right one, are the operator's. Design B from #97 was kept, so the layout question is closed.

### 8. Install and broker-login how-tos
- **What:** Wiki how-to pages for installing sherwood on the NAS (Compose, app-data ownership, Tailscale Serve to `127.0.0.1:8080`, installing to the home screen, enabling notifications) and for logging in to the broker on the NAS and running the first import.
- **Verification:** Pages use placeholders only and match `compose.example.yaml` and the real commands; a privacy scan finds nothing personal.
- **Status:** [ ]

## Order and parallelism

Task 1 first. Then 2, 3 and 6 can run together (3 is the only one adding a migration). Then 4 (after 2 and 3), 5 (after 2; adds a migration, so not alongside another migration task), and 7 (after 3 and 6). Task 8 last.

## Risks / open questions

- Web push on iPhone needs the app installed to the home screen first; the how-to says so.
- The unofficial library's login flow may change; the helper isolates it.
- Serve's identity headers are trustworthy only because the port is bound to loopback; a test or start-up check should refuse to listen on anything but loopback inside the container's published mapping. If that cannot be checked from inside the container, the install how-to must say so plainly.

## Verification strategy

CI gates from foundations (task 10), plus handler tests for every route's identity and token checks.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md`. Prices-and-rules runs alongside this plan and is tracked in place at `.harness/designs/sherwood/queued-plans/prices-and-rules.PLAN.md`. When the last task lands, close it out: status done, a summary in `progress.md`, and archive this file to `.harness/PLAN.archive.YYYYMMDD-phone-app.md`.

## Locked design calls

See the final [Sherwood Design](../wiki/designs/sherwood.md). In short: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.

Refinement calls (2026-11-16):
- The holdings import is a one-shot subcommand that starts the helper only for itself, so the long-running service keeps the design's rule of starting the helper only in live mode.
- Approval devices are identified by tailnet address, because Serve forwards the caller's address but no device name.
- The holdings and cash tables belong to this part and are shared with prices-and-rules.
