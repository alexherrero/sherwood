# Sherwood v1 — Tech Debt Retrospective

**Date:** 2026-04-27
**Source:** archived v1 codebase at [`v1/`](../../v1)
**Purpose:** Identify what v1 got wrong so v2 doesn't repeat it. This document is **input to v2 design** and a hard-constraints reference, not a v1 fix-list.

## Scoring framework

Each finding is scored using `(Impact + Risk) × (6 − Effort)`, where:

- **Impact (1–5):** how much it slows development or correctness.
- **Risk (1–5):** what breaks if we don't fix it (correctness, money, security).
- **Effort (1–5):** how hard to do it right in v2 from scratch (greenfield, not refactor).

---

## Tier 0 — Foundational, Must Get Right in v2 Design

Wrong choices here contaminate every line of code that follows.

| # | Issue | I | R | E | Score | v1 evidence |
|---|---|---|---|---|---|---|
| T0-1 | **`float64` for money/quantity everywhere** | 5 | 5 | 1 | **50** | `models/order.go:60-68`, `models/position.go:11-20`, `paper_broker.go:205` `pos.AverageCost = totalCost/totalQty`. Float drift on every fill. |
| T0-2 | **Order lifecycle anemic** — no `Accepted/PartialFill/Replaced/PendingCancel`, no idempotency key (`client_order_id`), no fees on `Trade` | 5 | 5 | 2 | **40** | `models/order.go:34-91`. Retry → double-fill. No P&L correctness. |
| T0-3 | **Risk manager wired as `nil` in production** + `risk.go` uses `* 100` placeholder for market-order valuation | 4 | 5 | 1 | **45** | `main.go:103`, `execution/risk.go:88`. False sense of safety. |
| T0-4 | **Engine "first strategy's timeframe wins" via random map iteration** + `processSymbol` re-fetches full lookback every tick | 4 | 5 | 2 | **36** | `engine/trading_engine.go:297-305`. Non-deterministic. |
| T0-5 | **No schema migration framework** — one giant `CREATE TABLE IF NOT EXISTS` block | 4 | 4 | 1 | **40** | `data/database.go:55-136`. First column add = hand-written ALTER. |

**v2 implication:** Decimal-money type, full-fledged `Order` aggregate with state machine + idempotency, mandatory wired risk subsystem, scheduler keyed on `(timeframe, strategy)` not symbol, migrations from commit #1.

---

## Tier 1 — High-Impact / Low-Effort When Building Fresh

Easy wins because they're greenfield decisions, not refactors.

| # | Issue | I | R | E | Score | v1 evidence |
|---|---|---|---|---|---|---|
| T1-1 | **No CI hygiene**: no `golangci-lint`, no `govulncheck`, no `-race`, no fuzz | 4 | 5 | 1 | **45** | `test-pipeline.yml` runs `go test -v ./...` only. |
| T1-2 | **Tests gated on `time.Sleep`** for synchronization — no clock injection | 4 | 4 | 1 | **40** | `trading_engine_test.go:138,175,212,274,328,438`. Flake bomb. |
| T1-3 | **Bash-curl integration tests** with PID files and `sleep 5` boot waits | 3 | 4 | 2 | **28** | `test-pipeline.yml:108-225`. Fragile, Linux-only, "ignored" failures suppressed. |
| T1-4 | **God `main()`** — 150 lines of imperative wiring, positional 8-arg constructors, magic constants | 4 | 3 | 1 | **35** | `main.go:27-177`. Every new subsystem balloons it. |
| T1-5 | **Stringly-typed strategy config** `map[string]interface{}` + hardcoded factory switch | 3 | 3 | 1 | **30** | `strategies/strategy.go:26`, `factory.go:17-32`. Silent fallback on type mismatch. |
| T1-6 | **Cancel doesn't update local cache or persist** — order state diverges across memory/broker/DB | 4 | 4 | 2 | **32** | `execution/order_manager.go:180-197`. |
| T1-7 | **`closeOnShutdown` ignores short positions** | 3 | 4 | 1 | **35** | `engine/trading_engine.go:217`. Silent risk exposure. |
| T1-8 | **Dead fields**: `Signal.StopLoss/TakeProfit` declared but ignored; duplicate `OrderStore` interface; alias endpoint `GetOrderHistory == GetOrders` | 3 | 3 | 1 | **30** | `signal.go:40-42`, `data/order_store.go:14` vs `execution/order_manager.go:19`, `handlers_orders.go:75`. |
| T1-9 | **Coverage gate is vanity** — 80% line coverage required, but live network call branches "ignored" in CI | 3 | 3 | 1 | **30** | `test-pipeline.yml:296-298`, `:374`. |
| T1-10 | **Coverage stage triple-runs tests** | 2 | 1 | 1 | **15** | `test-pipeline.yml:357-364`. Slow CI. |

**v2 implication:** CI quality gates from day 1; `Clock` interface injectable everywhere; `httptest`/testcontainers Go-native integration tests; `App` struct with options pattern; `Strategy[Cfg]` generic + `init()` self-registration; mutation-testing or critical-path coverage instead of line %.

---

## Tier 2 — Important but Negotiable (Decisions for ADRs)

These are real choices we'll formalize as ADRs in Phase 2.

| # | Issue | I | R | E | Score | v1 evidence |
|---|---|---|---|---|---|---|
| T2-1 | **`piquette/finance-go` unofficial Yahoo scraper** is the default data path in CI | 3 | 4 | 3 | **21** | `go.mod`, `test-pipeline.yml:125`. Yahoo TOS-blocks scrapers. |
| T2-2 | **`zerolog` + `godotenv` + `chi`** could be replaced by stdlib `log/slog` + Go 1.22 mux | 2 | 2 | 2 | **16** | `go.mod`. Surface-trim opportunity. |
| T2-3 | **SQLite `REAL` for money** plus no `SetMaxOpenConns(1)` for SQLite writer contention | 4 | 4 | 3 | **24** | `database.go:62-65`. Postgres question on table. |
| T2-4 | **`gorilla/websocket`** archived 2022 / revived; modern alternative is `coder/websocket` | 2 | 2 | 2 | **16** | `go.mod`. |
| T2-5 | **Single-instance rate limiter** (`httprate`) won't survive multi-replica | 2 | 3 | 3 | **15** | `router.go:55,57`. |

**v2 implication:** ADRs for: data provider strategy, database choice (Postgres vs SQLite), logging/router minimalism, rate-limit deployment topology.

---

## Tier 3 — Documentation & Release Hygiene

| # | Issue | I | R | E | Score | v1 evidence |
|---|---|---|---|---|---|---|
| T3-1 | **`DESIGN.md` lies about the system** — references TimescaleDB, Redis, React frontend, Docker, Nginx — none implemented | 3 | 2 | 2 | **20** | `docs/DESIGN.md:181-216`. |
| T3-2 | **Agent-prompt material in DESIGN.md** ("✅ MUST Do / ❌ MUST NOT") bloats canonical architecture doc | 2 | 1 | 1 | **15** | `docs/DESIGN.md:296-391`. |
| T3-3 | **No runbook, no deployment guide, no config reference table** | 3 | 3 | 3 | **18** | `POST /config/rotate-key` exists with no operational doc. |
| T3-4 | **Releases**: no semver, no darwin/arm64, no checksums, no SBOM, no signing, no provenance | 2 | 3 | 3 | **15** | `auto_release.yml:99-104`. |
| T3-5 | **Wiki deploys decoupled from versioned releases** | 2 | 2 | 2 | **16** | `deploy_wiki.yml`. Doc drift. |

**v2 implication:** Doc split — `docs/ARCHITECTURE.md` (as-built only), `docs/RUNBOOK.md`, `docs/CONFIG.md` (generated from struct tags), `docs/DEPLOYMENT.md`, `docs/adr/`. Wiki regenerated from `docs/` in CI. Goreleaser + cosign for releases.

---

## The "5 Things v2 Must Not Repeat"

If we forget everything else from this retro, these are the five hard constraints:

1. **Floats for money.** Decimal type from line 1.
2. **Optional/unwired risk subsystem.** Risk is non-negotiable, validated, exercised in tests.
3. **Stringly-typed config + hardcoded factory switches.** Generics + `init()` registration.
4. **`time.Sleep` in tests.** Clock injection mandatory; sleeping is a code-review blocker.
5. **God `main()` with positional constructors.** `App` builder, options pattern, per-phase shutdown deadlines.

---

## Phased v2 Build Order

This is not a v1 fix-list — it is the **v2 build order**, prioritized by what unblocks what.

**Phase A — Foundation** (before any business logic)
- Decimal money type
- Migration framework (golang-migrate or goose)
- `Clock` abstraction
- `App` / lifecycle scaffold with options + per-phase shutdown
- CI gates: `golangci-lint`, `govulncheck`, `-race`, fuzz scaffolding
- ADR-0001..ADR-0005 written

**Phase B — Core Domain**
- `Order` aggregate with state machine + idempotency + fees
- `Risk` interface required at construction
- `Strategy[Cfg]` generic + `init()` registration + `OnFill`/`Warmup` lifecycle
- Scheduler keyed on `(timeframe, strategy)` with bounded concurrency

**Phase C — Integration & Quality**
- Go-native `httptest`/testcontainers integration tests (replaces bash-curl)
- Property-based tests for indicators/P&L/Sharpe
- Provider contract tests with fault injection
- Doc structure (ARCHITECTURE/RUNBOOK/CONFIG/DEPLOYMENT)

**Phase D — Production Hardening**
- Goreleaser, multi-arch, cosign signing, SBOM
- Docker images
- Operational runbooks

---

## Caveats

- The two source audits did not read every test file or every middleware file. The patterns above are representative based on samples from the engine, integration test, API handlers, strategies, data, and execution packages.
- `dependabot.yml` was reportedly absent at audit time, but git history shows dependabot PRs — config may have been pruned during archival.
- Some "bugs" (e.g., `dailyPnL` never updated) could be wired in code that wasn't read end-to-end. Verify before assuming.
