---
parent_design_doc: ../../../../wiki/designs/sherwood.md
parent_part_slug: phone-app
---

# Plan: Phone app and read-only holdings (from design sherwood)

**Status:** draft
**Created:** 2026-11-15
**Roadmap item:** part 2 of 6 of [Sherwood Design](../../../../wiki/designs/sherwood.md); part file [phone-app](../../../../wiki/designs/sherwood/parts/phone-app.md)
**Brief:** Put sherwood on your phone as an installable web app over Tailscale Serve, with identity, anti-forgery and approval-device checks, the Today and Portfolio views and web push. Add the broker helper's login command and read-only holdings and cash calls, and import holdings read-only.

## Goal

You can install sherwood on your home screen, see Today and your Portfolio, and receive a test notification. Your holdings are imported without any order being possible.

## Constraints

- The repo is public: placeholders only, nothing personal.
- No agent signs in to, calls or tests against Robinhood or any broker, and no agent places, simulates against a live account, or scripts a trade. Broker tests use fakes; any check that needs the real account is run by the operator.
- Decimals for money, an injected clock, no sleeping in tests, migrations numbered and forward-only.
- Only GET reads and only POST changes; every POST carries an anti-forgery token.

## Out of scope

- Orders, approvals and the stop switch (paper-trading). Prices and suggestions (prices-and-rules).

## Tasks

### 1. Serve identity check and anti-forgery tokens
- **What:** Middleware that requires the allowed Serve login header and checks anti-forgery tokens on POST, plus the approval-device allowlist by tailnet address.
- **Verification:** Tests refuse a missing or wrong login, a POST without a token, and an approval-device POST from another address.
- **Status:** [ ]

### 2. Installable app shell
- **What:** Server-rendered layout built for phones, the app manifest and icon, and a service worker that caches only the shell.
- **Verification:** A test confirms the service worker's cache list has no data routes; a manual install on the phone opens full-screen (operator check).
- **Status:** [ ]

### 3. Today and Portfolio views
- **What:** Today shows the mode banner, a placeholder stop switch and job runs; Portfolio lists holdings with value and gain or loss from stored data.
- **Verification:** Handler tests render both views from fixture data with placeholder symbols; the mode banner does not rely on colour alone.
- **Status:** [ ]

### 4. Web push channel
- **What:** A notifier interface, a web push implementation with keys in the app settings file, and a subscribe flow in the app.
- **Verification:** Tests check the payload carries only the short alert text; a test push reaches the phone (operator check).
- **Status:** [ ]

### 5. Broker helper login and read-only calls
- **What:** The helper's one-time login command writing the owner-only broker settings file, and holdings and cash calls through the pinned library.
- **Verification:** Helper tests run against a fake library; a test confirms the helper offers no order calls yet; the real login is run by the operator.
- **Status:** [ ]

### 6. Read-only holdings import
- **What:** Import holdings and cash through the Go client into the database, flagging more than 20 holdings.
- **Verification:** Tests against the fake helper import holdings and create no order rows; the first real import is run by the operator.
- **Status:** [ ]

### 7. Install how-to
- **What:** Wiki how-to pages for installing on the NAS and logging in to the broker.
- **Verification:** Pages use placeholders only and match the Compose file and commands.
- **Status:** [ ]

## Risks / open questions

- Web push on iPhone needs the app installed to the home screen first; the how-to says so.
- The unofficial library's login flow may change; the helper isolates it.

## Verification strategy

CI gates from foundations, plus handler tests for every route's identity and token checks.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`, then read the newest entries in `.harness/progress.md`. This plan is queued: before starting it, archive the finished active plan to `.harness/archive/` and move this file to `.harness/PLAN.md`. Refine the task list with `/plan` before `/work`.

## Locked design calls

See the final [Sherwood Design](../../../../wiki/designs/sherwood.md). In short: Go application with Python only in the broker helper; SQLite; decimals; a mandatory risk check on every order; buys wait for approval and only protective sells go through on their own; any broker error halts trading with no retry; the AI never creates or sends an order; broker secrets only in the owner-only settings file on the NAS.
