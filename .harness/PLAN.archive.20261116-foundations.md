---
parent_design_doc: ../wiki/designs/sherwood.md
parent_part_slug: foundations
---

# Plan: Foundations: container, storage and guard rails (from design sherwood)

**Status:** done
**Created:** 2026-11-15
**Roadmap item:** part 1 of 6 of [Sherwood Design](../wiki/designs/sherwood.md); part file [foundations](../wiki/designs/sherwood/parts/foundations.md)
**Brief:** Build the base every later part stands on: the Go application and the Python broker helper in one container, SQLite with numbered migrations, exact decimal money, an injected clock and the exchange calendar, the scheduler's job records, validated settings and strategy files, masked secrets, and CI. Nothing in this part trades or talks to a broker.

## Goal

When this plan is done, the container builds and starts on a machine with example settings, refuses to start when a limit is missing, and runs an empty scheduler on a fake or real clock. Money cannot be stored or sent as a float, a secret cannot be printed in clear, and CI checks all of it on every pull request.

## Constraints

- The repo is public. Example files use placeholder values only; no holdings, strategies, limits, balances, account details or names.
- No agent signs in to, calls or tests against Robinhood or any broker. The broker helper in this part offers only `ping`.
- Money, prices and share counts are decimals end to end and stored in SQLite as text.
- No test reads the wall clock or sleeps.
- Migrations are numbered and forward-only from the first commit.
- New code lives in a Go module at the repo root; `v1/` keeps its own module and stays untouched.

## Out of scope

- The phone app, identity checks and holdings import (see `phone-app.PLAN.md`).
- Prices, rules, the back-tester and the setup skill (see `prices-and-rules.PLAN.md`).
- Orders, the risk check and the stop switch (see `paper-trading.PLAN.md`).
- The research feed (see `research-feed.PLAN.md`) and live trading (see `live-trading.PLAN.md`).

## Tasks

### 1. Go module and repo layout
- **What:** Create the root Go module with `cmd/sherwood` and `internal/` packages, a `Makefile` with `build`, `test`, `lint` and `vet` targets, and `.gitignore` entries for the real settings files and local databases.
- **Verification:** `go build ./...` and `go vet ./...` pass at the root and do not include `v1/`; `git check-ignore` matches the real settings file names.
- **Status:** [x] Shipped in [#64](https://github.com/alexherrero/sherwood/pull/64) (2026-11-16). The root module `github.com/alexherrero/sherwood` (go 1.26, no dependencies yet) has a minimal `cmd/sherwood` whose `main` calls `run() error`, and eight package stubs under `internal/` (money, clock, store, config, secret, logging, scheduler, broker), each a `doc.go` for now. The `Makefile` has `build`, `test` (with `-race`), `vet`, `lint` and `all`, acting on the root module only; `.golangci.yml` is golangci-lint v2 with the standard set plus gosec, errorlint, revive (exported rule on) and a few others. The real files are named `settings.yaml`, `broker.yaml` and `strategy.yaml` and are ignored anywhere in the tree, as are `*.db`, `*-wal` and `*-shm`. `v1/go.mod` declares the same module path, which is fine because the nested module is never reached by `./...`; it does mean no `go.work` may include both, and Dependabot must list `/` and `/v1` separately.

### 2. Decimal money types
- **What:** Add `Money`, `Price` and `Quantity` types over a decimal library, with SQL scan and value as text, JSON as strings, and the written rounding policy (prices to the cent, whole shares unless fractional is on).
- **Verification:** Table tests round-trip values through SQLite and JSON exactly; decoding a JSON number fails; a lint rule or test fails if `float32` or `float64` appears in the money packages.
- **Status:** [x] Shipped in [#76](https://github.com/alexherrero/sherwood/pull/76) (2026-11-16). `internal/money` builds `Money`, `Price` and `Quantity` on `github.com/shopspring/decimal` v1.4.0, chosen over apd for its immutable values. `Price` is never negative and always whole cents: parsing refuses sub-cent text, and computed prices round to the cent, halves away from zero. `Quantity` follows a `QuantityPolicy` the caller must choose (whole or fractional shares, to a millionth); its zero value is invalid, and sizing always rounds down so it never buys more than it worked out. Each type stores as SQLite text and travels as a JSON string, and scanning or decoding a number, null or bool fails. The types cannot be compared with `==`, because 1.5 and 1.50 differ in form. `TestNoFloatInMoney` fails the build if a float type or float conversion appears in the package. Money columns must never be ordered or compared in SQL, because text order is not numeric order.

### 3. Clock and exchange calendar
- **What:** Add a `Clock` interface with real and fake implementations, and an exchange calendar from a holiday table in the repo covering this year and next, with half days, in US Eastern time.
- **Verification:** Tests check the published holidays and half days for both years, weekends and daylight-saving changes; a check fails if any non-test package outside `clock` calls `time.Now`.
- **Status:** [x] Shipped in [#71](https://github.com/alexherrero/sherwood/pull/71) (2026-11-16). `internal/clock` has the `Clock` interface (`Now`, `After`), the wall clock `Real{}`, and a `Fake` that tests set and advance and that never sleeps. `clock.NYSE()` is the exchange calendar for 2026 and 2027, built from a holiday table checked against NYSE's published schedule, in `America/New_York` with `time/tzdata` embedded; it answers trading day, half day, session open and close, and next or previous trading day, and any date in an uncovered year is an `ErrNotCovered` error rather than a guess. A test derives each covered year from the exchange's standing rules and requires the table to match. The December guard is `CheckCoverage(now)`, which refuses from 1 December when next year is missing; start-up must call it (task 9). `TestNoWallClockOutsideClock` parses the module and fails on any `time.Now`, `Since` or `Until` outside `internal/clock`, with one counted exception: the broker client's socket deadline.

### 4. SQLite store and migrations
- **What:** Open SQLite in write-ahead mode with a pure-Go driver and one writer, and add a forward-only migration runner that applies numbered SQL files inside a transaction. The first migration creates `schema_migrations`, `job_runs` and `settings_changes`.
- **Verification:** Tests show a fresh database migrating to the latest version, a broken migration rolling back and returning an error that stops start-up, and a second run applying nothing.
- **Status:** [x] Shipped in [#66](https://github.com/alexherrero/sherwood/pull/66) (2026-11-16). `internal/store` opens the file with `modernc.org/sqlite` v1.62.0, the pure-Go driver v1 already used, so the container needs no cgo. One writer connection is kept for the life of the process with `BEGIN IMMEDIATE` transactions, and a separate read-only pool serves reads; both run in WAL mode with foreign keys and a busy timeout. The forward-only runner applies embedded `migrations/NNNN_name.sql` files in order, each in its own transaction with its `schema_migrations` row, and every failure (bad SQL, a gap, a renamed file, or a database made by a newer build) wraps `store.ErrMigration`, the start-up stop signal. `0001_init.sql` creates `schema_migrations`, `job_runs` (unique on job name and trading date, with input hash, times and outcome) and `settings_changes` (append-only by trigger, source `phone` or `nas`). Every table is STRICT with only TEXT and INTEGER columns, and a test fails any later migration that adds a REAL column. The store reads no clock; callers pass `now`.

### 5. Settings and strategy files
- **What:** Load the app settings file and the strategy file, validate the strategy file against a schema (every limit present, known rule types, strategy shares no more than the whole), and compute its hash. Ship `settings.example.yaml` and `strategy.example.yaml` with placeholders.
- **Verification:** Table tests cover each missing limit, an unknown rule type and over-allocated shares, each refusing with a message naming the problem; the example files validate.
- **Status:** [x] Shipped in [#74](https://github.com/alexherrero/sherwood/pull/74) (2026-11-16). `internal/config` reads both files with `go.yaml.in/yaml/v3` in strict mode (unknown fields, repeated keys, empty files and extra documents are refused) and hashes the strategy file's bytes with SHA-256. Validation lists every problem at once, each naming its field: each missing limit, an unknown rule type, strategies' shares adding up to more than the whole, plus a few consistency checks (a buy margin wider than the allowed limit distance, amounts past the cent, malformed symbols). Shares are written as fractions and amounts as dollars; whole-number limits refuse `3.5` rather than truncating it. Because the money types had not merged yet, limits use a text-backed `config.Decimal` that never passes through a float; swapping to the money types is a small follow-up. YAML errors hide the offending value, so a secret pasted into the wrong field never reaches the log. `settings.example.yaml` and `strategy.example.yaml` ship with placeholders and validate.

### 6. Masked secrets and the logger
- **What:** Add a `Secret` type whose text, format and JSON forms are a mask, and a structured JSON logger that writes only named fields from an allowlist.
- **Verification:** Tests print, format, log and JSON-encode a secret and find only the mask; logging an unlisted field fails the test.
- **Status:** [x] Shipped in [#69](https://github.com/alexherrero/sherwood/pull/69) (2026-11-16). `internal/secret` has `Secret`, whose string, format (every verb), JSON, text, YAML and slog forms are all the mask `********`; `Reveal()` is the only way to read it, `Matches` compares in constant time, and unmarshal errors never echo the input. `internal/logging` wraps slog's JSON handler so only keys on one commented allowlist are written; an unlisted key is dropped and named in a `refused_fields` field, and values must be plain (no headers, requests, URLs, byte slices or structs). A guard test keeps credential-like words out of the allowlist. `loggingtest.New(t)` gives every package's tests a logger that fails the test on any refused field. Secrets in settings are declared as `secret.Secret`, so writing settings back out writes the mask unless a writer reveals them deliberately.

### 7. Scheduler job records
- **What:** Add the scheduler: jobs keyed by name and trading date, each run recorded with an input hash, run in US Eastern time on trading days, and caught up in order after downtime. Register no trading jobs yet.
- **Verification:** Fake-clock tests show a job running once per trading day, skipping holidays, catching up missed days in order after a restart, and not running twice for the same date.
- **Status:** [x] Shipped in [#73](https://github.com/alexherrero/sherwood/pull/73) (2026-11-16). `internal/scheduler` runs registered daily jobs at a time of day in US Eastern on trading days, driven by the injected clock and the NYSE calendar. Each run is claimed with one upsert on `job_runs`, so a run that has succeeded or been skipped never runs again, even across processes; a failed or interrupted run is retried on the next start by updating its row. Each run's input hash covers its trading date and the bytes it reads. After downtime it catches up the last five trading dates in the order the runs fell due, and each catch-up run carries its own date and session, so the buy job in a later part can skip a session it was not approved for. An uncovered calendar year, a database error or a job panic is returned as a halt rather than skipped. No jobs are registered yet. Interval jobs (every 15 minutes) and the 02:00 backup are not daily trading-day jobs and will need their own kind in a later part.

### 8. Broker helper scaffold
- **What:** Add the Python broker helper with a Unix-socket server that speaks one JSON request and reply per call and offers only `ping`, its dependencies locked with hashes, and a Go supervisor and client that start it, restart it and treat silence as an error.
- **Verification:** `pytest` passes; a Go contract test starts the helper, pings it, kills it and sees the error reported; the lock file installs with `--require-hashes`.
- **Status:** [x] Shipped in [#67](https://github.com/alexherrero/sherwood/pull/67) (2026-11-16). `brokerhelper/` holds the Python helper: no runtime dependencies yet, pytest as a hash-locked dev dependency (`lock.sh` regenerates both lock files with `uv`), and 31 tests. It serves one JSON line in and one JSON line out per connection over a 0600 Unix socket, offers only `ping`, refuses JSON floats so decimals must travel as strings, never retries, and reports an unexpected exception by its type name only, so broker details cannot leak. `internal/broker` has a Go client whose errors (`ErrUnavailable`, `ErrNoReply`, `ErrBadReply`, `HelperError`) are all broker errors that must halt and never retry, and a supervisor that starts the helper, waits for `ready`, restarts it on exit and reports every start failure and exit to its caller. A contract test starts the real helper, pings it, kills it and sees the error.

### 9. Container and Compose file
- **What:** Add a multi-stage Dockerfile (Go build, slim Python runtime, non-root user, base images pinned by digest) and an example Compose file that publishes the web port on `127.0.0.1` only and mounts the application-data folder.
- **Verification:** `docker build` succeeds; the container starts with the example files and stops with a clear error when a limit is removed.
- **Status:** [x] Shipped in [#77](https://github.com/alexherrero/sherwood/pull/77) (2026-11-16). `cmd/sherwood` now starts in a fixed order and stops at the first failure: logger, calendar coverage check, settings, strategy (its hash logged), store and migrations, the broker helper only in live mode, then the scheduler with no jobs until SIGTERM. Every failure exits 1 with `refusing to start:` and a cause that names the field and holds no secret. The multi-stage `Dockerfile` builds a static Go binary and runs it on `python:3.13-slim` as uid 10001, both base images pinned by digest; `.dockerignore` keeps real settings files and databases out of the image. `compose.example.yaml` publishes port 8080 on `127.0.0.1` only and mounts the app-data folder at `/data`. Docker was not available on the Mac, so the first real `docker build` falls to CI (task 10); the start-up half is proven by tests that run the built binary against the example files and against a copy missing a limit. A broker helper error is only logged for now; the halt comes in a later part.

### 10. CI
- **What:** Replace the placeholder workflow. Keep the v1 build for Dependabot, and add Go vet, lint, `govulncheck`, race-enabled tests, Python tests, a credential scan and the container build, every action pinned by commit. Extend Dependabot to the root Go module, the helper's Python dependencies and the Dockerfile.
- **Verification:** The pull request's checks all pass; adding a fake credential-shaped string on a scratch branch makes the scan fail.
- **Status:** [x] Shipped in [#82](https://github.com/alexherrero/sherwood/pull/82) (2026-11-16). `ci.yml` keeps `Build (v1)` unchanged and adds seven checks on every pull request and push to main: `Go vet`, `Go lint` (golangci-lint v2.15.0), `Go vulnerability check` (govulncheck v1.9.0), `Go tests (race)` (with Python on the runner, so the binary, helper-contract and live-mode start-up tests run rather than skip), `Python helper tests`, `Credential scan` (gitleaks 8.30.1 as a checksum-verified binary, over the tree and each PR's own commits, with a path-only allowlist in `.gitleaks.toml`) and `Container build`. The container check is the first real `docker build`: it passed unchanged, then a smoke test runs the image read-only with no network, sees a clean start and stop with the example files, and sees the field-named refusal when a limit is removed. Every action is pinned by commit. A planted fake key on a throwaway PR (#83, closed, branch deleted) made the credential scan fail. Dependabot now covers both Go modules, the helper's pip locks, the Dockerfile and the actions; gitleaks and govulncheck versions are bumped by hand. Only `Build (v1)` is a required check until the operator adds the new names to the ruleset.

### 11. Settings and strategy file reference
- **What:** Write the wiki reference page for both files: every field, its type, and which ones are required, using placeholder values.
- **Verification:** Every field in the schema appears on the page; a privacy scan of the page finds nothing personal.
- **Status:** [x] Shipped in [#79](https://github.com/alexherrero/sherwood/pull/79) (2026-11-16). `wiki/reference/settings-and-strategy-files.md`, linked from a new Reference section in `wiki/Home.md`, covers every field of both files with its type, whether it is required, its accepted range and the exact error text, captured by running the real parser. It also explains where the files live, that limits have no defaults, that the strategy file's hash marks every signal so even a comment makes a new version, and that a tightening from the phone is recorded while a loosening is a file edit and a restart. A script found all 40 schema names on the page, and a privacy scan found nothing personal. Per-rule settings are left to the rule library reference in prices-and-rules. A follow-up, [#81](https://github.com/alexherrero/sherwood/pull/81), moved the two dollar limits (`max_order_value`, `daily_loss_limit`) onto `money.Money` without changing the file format or any error text; the ratio limits stay on `config.Decimal`.

## Risks / open questions

- Choosing the decimal and SQLite libraries: a pure-Go SQLite driver avoids cgo in the container. Pick in task 4 and record it in the design's Document History if it changes anything there.
- The holiday table needs a yearly update. Task 3 adds a test that fails in December when next year is missing, so the update cannot be forgotten.
- The credential scan can flag placeholders. Keep placeholders obviously fake and allowlist them by path, not by pattern.

## Verification strategy

`make vet lint test` for Go with `-race`, `pytest` for the broker helper, `govulncheck`, the credential scan and `docker build`, all in CI on every pull request. Tasks close only when CI is green on the pull request.

## How to resume

Read this file's task list and find the first task with `Status: [ ]`. Then read the newest entries in `.harness/progress.md`. The next parts' plans are queued in `.harness/designs/sherwood/queued-plans/` in this order: phone-app, prices-and-rules, paper-trading, research-feed, live-trading.

## Locked design calls

These come from the final [Sherwood Design](../wiki/designs/sherwood.md); change them only by amending the design.

- Go for the application; Python only in the broker helper, which alone reads the broker secrets.
- SQLite with numbered, forward-only migrations from the first commit.
- Decimal money everywhere, stored as text.
- An injected clock everywhere; the scheduler is deterministic per trading date.
- Every order will pass a mandatory risk check, and the AI will never create or send an order; nothing in this part may make either harder.
- Broker secrets live only in the owner-only settings file on the NAS, never in the repo, the vault, logs or test fixtures.
