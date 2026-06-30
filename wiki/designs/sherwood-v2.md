---
title: Sherwood v2 — System Design
status: draft
kind: design
scope: arc
area: sherwood
seeded: 2026-05-07
---

# Sherwood v2 — System Design

|  |  |  |  |  |  |
| --- | --- | --- | --- | --- | --- |
| **Status** | Draft v0.1 | **Created** | 2026-05-07 | **Last updated** | 2026-05-07 |

|  |  |
| --- | --- |
| **Authors** | Alex Herrero (alexmherrero@gmail.com) |
| **Contributors** | — |
| **Tracking project** | _TBD: github.com/alexherrero/sherwood/projects/N_ |

---

## 1. Objective

Build a self-hosted automated trading platform — Sherwood v2 — that:

1. Watches the S&P 500 (and crypto) on multiple timeframes and runs configurable strategies against live market data.
2. Backtests strategies against historical bars to evaluate "how would this plan have performed."
3. Lets a single operator (the author) chat with an AI agent (Claude / Gemini via existing CLI subscription) to design strategies, classify market regime, interpret backtest reports, and propose trade plans. The AI is **research-and-planning by default** — humans approve any new position, modification, or close. The AI is **also authorized to act protectively on its own** when scheduled monitors detect capital-protection signals (drawdown limits, regime breaks, volatility spikes): it can halt strategies and cancel pending orders without per-action approval. The AI never opens, closes, or modifies positions autonomously — only humans do that.
4. Runs as a Docker container on a home FreeNAS server with $0/month operating cost.
5. Exposes a phone-friendly web UI on the home network, protected by a single shared password and a 30-day session cookie.
6. **Eventually delivers a beautiful, Robinhood-inspired browser UI** — not in the v2.0 hard scope, but the API, event model, auth flow, and data shapes are designed from day one to support a polished, mobile-first web experience as a follow-on milestone (v2.x).
7. Begins life as paper-trading with a deferred but designed-in path to live trading.

The design also incorporates the lessons learned from v1; see the [v1 tech-debt retrospective](../../docs/retros/v1-tech-debt.md) for the hard constraints carried into v2.

---

## 2. Background

Sherwood v1 (archived at [`v1/`](../../v1)) was a Go proof-of-concept (~17,000 LOC, 49 tests, SQLite, chi router, 5 built-in strategies, paper-trading only). It validated that the basic shape of the problem was tractable in Go and produced a working trading engine plus REST API plus weekly cross-platform release pipeline.

The v1 audit (full report in [`docs/retros/v1-tech-debt.md`](../../docs/retros/v1-tech-debt.md)) surfaced foundational debt that cannot be retrofitted without effectively rewriting the core: float64 money, an optional/unwired risk subsystem, stringly-typed strategy configuration, time-based test flakes, and a 150-line god-main with positional 8-argument constructors. Rather than refactor incrementally, v2 starts from the lessons learned and rebuilds the engine, data layer, and execution path with these constraints baked in.

Two opportunities motivate v2:

- **Scale up to S&P 500 watch lists**, which forces a streaming-first data layer and a bounded scheduler — both novel for v2.
- **Bring AI into the platform** as an on-demand research assistant, leveraging the operator's existing Claude / Gemini subscription via the headless CLI rather than paid API tokens. AI participates in strategy design, market analysis, and backtest interpretation, but never in trade execution.

---

## 3. Design

### 3.1 Overview

Sherwood v2 is a single Go binary that runs as a Docker container alongside Postgres on a home FreeNAS server. The binary hosts:

- A **REST + WebSocket API** for the web UI and machine-to-machine clients.
- A **trading engine** that subscribes to live market data, runs registered strategies on a `(timeframe, strategy)` schedule, and emits signals.
- An **execution layer** that converts signals into orders, applies risk checks (mandatory), routes to a broker (paper, Robinhood, or Alpaca), and tracks order lifecycle through a persisted state machine.
- A **backtest service** that replays historical bars deterministically and produces an equity curve and metrics.
- An **AI subsystem** that subprocesses out to `claude` or `gemini` CLIs to run skills (sherwood-authored or imported from `anthropics/financial-services`) and returns schema-validated JSON for the application to act on.
- An **auth subsystem** with a single shared password and 30-day-max session cookies.

Architecture diagram (logical):

```
                         ┌──────────────────────────────────────┐
                         │        FreeNAS docker stack          │
                         │                                      │
   Phone / Browser ──┐   │   ┌──────────────────────────┐       │
                     │   │   │   sherwood (Go binary)   │       │
                     │   │   │                          │       │
   Internal CLI/  ───┴──HTTP─▶│ • REST + WS API          │       │
   webhook                │   │ • Auth                   │       │
                         │   │ • Engine + Scheduler     │       │
                         │   │ • Execution + Risk       │       │
                         │   │ • Backtester             │       │
                         │   │ • AI adapter             │       │
                         │   │ • Cron (scheduled jobs)  │       │
                         │   └────┬─────────┬───────────┘       │
                         │        │         │                   │
                         │        │         │ subprocess         │
                         │        │         ▼                   │
                         │        │  ┌─────────────────┐        │
                         │        │  │ claude / gemini │        │
                         │        │  │ CLI (mounted    │        │
                         │        │  │ ~/.claude RO)   │        │
                         │        │  └─────────────────┘        │
                         │        │                              │
                         │        ▼                              │
                         │  ┌──────────┐                         │
                         │  │ Postgres │                         │
                         │  └──────────┘                         │
                         └──────────────────────────────────────┘
                                  │
                                  │ outbound https/wss only
                                  ▼
                ┌──────────────────────────────────────┐
                │ Data + Broker providers              │
                │ Yahoo, Alpaca (REST + stream),       │
                │ Robinhood Crypto, Robinhood Stocks   │
                └──────────────────────────────────────┘
```

Key principles:

- **Deterministic engine, AI on the side.** The trading hot path has no LLM dependency at runtime. AI is invoked synchronously by user action or by a cron-scheduled job that produces structured proposals reviewed by the human before any portfolio change.
- **Clock-injected everywhere.** Every time read goes through a `Clock` interface so tests are wall-clock-independent and backtests are deterministic.
- **Streaming-first data layer.** Providers expose `Subscribe(symbols, timeframe)` for live data and `History(symbol, timeframe, range)` for backfill. The engine is push-driven on bar close events, never polling.
- **Bounded concurrency.** Per-provider semaphores throttle outbound requests and prevent goroutine stampedes at S&P 500 scale.
- **Decimal everywhere money or quantity is involved.** `shopspring/decimal` is the canonical type. JSON marshals as strings; Postgres stores `numeric(20,8)`.
- **Mandatory risk.** The engine constructor refuses to run without a configured `Risk` implementation. Limits are validated by integration tests.

### 3.2 Infrastructure

**Runtime topology.** A single FreeNAS host running Docker (or TrueNAS SCALE's native container support). The deployment artifact is a `docker-compose.yml` checked into the repo plus a multi-arch container image (`linux/amd64`, `linux/arm64`) published to GHCR on each release.

```yaml
# compose.yml (illustrative)
services:
  sherwood:
    image: ghcr.io/alexherrero/sherwood:v2.0.0
    restart: unless-stopped
    environment:
      DATABASE_URL: postgres://sherwood:...@postgres:5432/sherwood?sslmode=disable
      SHERWOOD_PASSWORD_HASH: ${SHERWOOD_PASSWORD_HASH}
      AI_PROVIDER: claude
      LOG_LEVEL: info
    ports: ["8099:8099"]
    volumes:
      - /mnt/tank/sherwood/data:/app/data:rw
      - ${HOME}/.claude:/home/sherwood/.claude:ro    # subscription auth
      - /mnt/tank/sherwood/skills:/app/.claude/skills:ro
    depends_on: [postgres]
  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: sherwood
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: sherwood
    volumes:
      - /mnt/tank/sherwood/pg:/var/lib/postgresql/data
```

**Network model.** Containers share an internal Docker network. Sherwood listens on `:8099` and is exposed to the LAN by FreeNAS port mapping. Optional reverse proxy (Caddy / nginx-proxy-manager) terminates TLS and serves a `https://sherwood.local` URL — out of scope for the binary; it serves plain HTTP.

**Persistence.** Postgres is the system of record for everything except short-term in-memory caches. The `/app/data` mount holds:

- `data/bars/` — Parquet exports of historical bars for offline backtesting (optional)
- `data/backtests/` — backtest reports
- `data/secrets/` — runtime-generated keys (e.g., session signing key)

**Backups.** FreeNAS snapshot of `/mnt/tank/sherwood/pg` daily; `pg_dump` weekly to the same dataset; both retained 90 days. (FreeNAS handles snapshot rotation natively.)

**Configuration.** Environment variables via `compose.yml`. A `config.example.yaml` checked in for non-secret defaults. Secrets via `${VAR}` interpolation from a `.env` file the operator owns. **No `.env` file is loaded by the Go binary directly** — Docker handles it. This avoids v1's `godotenv`-in-prod smell.

**Build pipeline.** GitHub Actions builds:
1. Lint (`golangci-lint`), vuln scan (`govulncheck`), test (`go test -race -count=1 ./...`), coverage (≥ critical-path %).
2. On tag, build static binary for `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64` plus container image, push to GHCR with cosign signing and SBOM attestation.
3. Wiki regenerated from `wiki/` directory on push to `main`.

### 3.3 Detailed Design

#### 3.3.1 Domain Model

Money and quantity types are `decimal.Decimal` (from `github.com/shopspring/decimal`) wrapped in named types for clarity:

```go
type (
    Money    decimal.Decimal // currency-amount, e.g., USD
    Quantity decimal.Decimal // share or unit count
    Price    decimal.Decimal // per-unit price
)
```

Marshal as JSON strings (`"123.45"`) to avoid float drift on the client. Stored as `numeric(20, 8)` in Postgres.

**Order aggregate** has an explicit state machine and an idempotency key:

```go
type Order struct {
    ID            uuid.UUID       // server-assigned
    ClientOrderID uuid.UUID       // client-generated, idempotency key
    AccountID     string
    Symbol        string
    Side          Side            // Buy | Sell
    Type          OrderType       // Market | Limit | Stop | StopLimit
    TimeInForce   TimeInForce     // GTC | IOC | FOK | DAY
    Quantity      Quantity
    LimitPrice    *Price          // nullable
    StopPrice     *Price          // nullable
    Status        OrderStatus
    FilledQty     Quantity
    AvgFillPrice  *Price
    Fees          Money
    CreatedAt     time.Time
    UpdatedAt     time.Time
    StrategyID    *uuid.UUID      // null if manual
    BrokerOrderID *string         // assigned by broker
}

// State machine:
//   Created → Validated → Submitted → Accepted →
//     (PartiallyFilled → Filled | Rejected | Canceled | Replaced | Expired)
```

Every transition is appended to `order_events` so we can replay the history. The current `Status` is denormalized on the `orders` row for fast reads.

**Position** has a `Side` (Long | Short | Flat) and is *derived* from filled trades; it is cached for fast reads but always reconcilable from `trades`. It does **not** persist `MarketValue` or `UnrealizedPL` — those are computed on read.

**Trade** captures the result of a fill:

```go
type Trade struct {
    ID         uuid.UUID
    OrderID    uuid.UUID
    Symbol     string
    Side       Side
    Quantity   Quantity
    Price      Price
    Fees       Money
    OccurredAt time.Time
}
```

**Strategy[Cfg]** is a generic interface with full lifecycle:

```go
type Strategy[Cfg any] interface {
    Name() string
    Init(cfg Cfg) error
    Warmup(bars []Bar) error
    OnBar(ctx Context, bar Bar) ([]Signal, error)
    OnFill(ctx Context, fill Fill) error
    State() any
}
```

Self-registration via `init()`:

```go
func init() {
    strategies.Register("ma_crossover", func() Strategy[MACrossoverCfg] { ... })
}
```

Adding a new strategy is a single `init()` call — no edits to a central factory or `AvailableStrategies` slice.

**Signal** carries an explicit position-sizing intent:

```go
type Signal struct {
    Symbol      string
    Side        Side
    Sizing      Sizing      // FixedQty | RiskPct | NotionalUSD — typed, not optional
    OrderType   OrderType
    LimitPrice  *Price
    StopLoss    *Price      // wired to engine; not dead
    TakeProfit  *Price      // wired to engine; not dead
}
```

#### 3.3.2 Application Lifecycle

The `App` builder owns construction and shutdown:

```go
func New(ctx context.Context, opts ...Option) (*App, error)

func (a *App) Run(ctx context.Context) error
func (a *App) Shutdown(ctx context.Context) error
```

`Option` is a functional-options pattern (`WithDB`, `WithBroker`, `WithClock`, …). No positional 8-argument constructors anywhere. Shutdown takes a context with a deadline and runs subsystems in dependency order, each with its own per-phase deadline taken from configuration:

1. Stop accepting new HTTP requests (server.Shutdown, deadline 10s)
2. Stop ingest streams (close subscriptions, deadline 5s)
3. Stop scheduler (drain in-flight bars, deadline 30s)
4. Cancel all pending orders if `CLOSE_ON_SHUTDOWN=true` (deadline 60s)
5. Flush AI thread persistence and metrics (deadline 5s)
6. Close DB pool (deadline 5s)

A failure at any phase logs the failure but does not abort subsequent phases — graceful degradation.

#### 3.3.3 Data Layer

**Provider interface** — one shape for both push and pull:

```go
type DataProvider interface {
    Name() string
    Subscribe(ctx context.Context, symbols []string, tf Timeframe) (<-chan Bar, error)
    History(ctx context.Context, symbol string, tf Timeframe, from, to time.Time) ([]Bar, error)
    Capabilities() ProviderCapabilities  // streaming?, max-symbols?, rate-limits?
}
```

Implementations: `yahoo`, `alpaca`, `robinhood_crypto`, `robinhood_stocks` (with explicit fragility warning). Provider selection is per-symbol-class:

```yaml
data_routing:
  stocks:    alpaca       # primary, streaming
  stocks_bf: yahoo        # backfill / fallback
  crypto:    robinhood_crypto
```

**Bar storage** is a partitioned Postgres table:

```sql
CREATE TABLE bars (
    symbol     text not null,
    timeframe  text not null,
    ts         timestamptz not null,
    open       numeric(20,8) not null,
    high       numeric(20,8) not null,
    low        numeric(20,8) not null,
    close      numeric(20,8) not null,
    volume     numeric(20,8) not null,
    primary key (symbol, timeframe, ts)
) PARTITION BY LIST (timeframe);

-- partitions per timeframe:
CREATE TABLE bars_1m  PARTITION OF bars FOR VALUES IN ('1m');
CREATE TABLE bars_5m  PARTITION OF bars FOR VALUES IN ('5m');
CREATE TABLE bars_1h  PARTITION OF bars FOR VALUES IN ('1h');
CREATE TABLE bars_1d  PARTITION OF bars FOR VALUES IN ('1d');
```

Retention policy via cron: trim `bars_1m` to last 30 days, `bars_5m` to 6 months, `bars_1h` to 5 years, `bars_1d` forever. Deletion uses partition DROP where possible (e.g., daily sub-partitions on 1m), batched DELETE otherwise.

**SQLite fallback** uses the same schema with timeframe filter instead of partitions, single `numeric(20,8)`-equivalent text encoding via `decimal`'s string form, and `SetMaxOpenConns(1)` to avoid writer contention. Selection is automatic from `DATABASE_URL` scheme (`postgres://` vs `sqlite://`).

**Cache layer.** In-memory ring buffer per (symbol, timeframe) holding the last N bars (configurable, default 500). Used by strategies for indicator computation without round-tripping to Postgres on every tick. Invalidated on `OnBar` event arrival.

#### 3.3.4 Engine and Scheduler

The **scheduler** is keyed on `(timeframe, strategy)` tuples, not symbols. Each tuple gets a goroutine that:

1. Waits on a bar-close event for any of its subscribed symbols at its timeframe (delivered via channel from the provider layer).
2. Updates the in-memory cache for that (symbol, timeframe).
3. Calls `strategy.OnBar(ctx, bar)` with the fresh cache view.
4. Hands resulting signals to the execution layer.

Per-strategy state is **per-symbol** by construction — strategies are instantiated once per `(strategy, symbol)` pair, eliminating v1's bug where a single MACrossover state mixed across symbols.

A **bounded worker pool** per provider throttles outbound provider calls. At S&P 500 scale, no provider receives more than its rate limit (e.g., Alpaca free tier ≤ 200 req/min) regardless of how many symbols are subscribed.

The clock is `Clock` interface; tests inject a `FakeClock` that advances on demand. Production wires `RealClock`. No `time.Sleep` ever appears in test code.

#### 3.3.5 Execution and Risk

**Risk subsystem is non-optional.** The engine's constructor signature is:

```go
func NewEngine(deps EngineDeps) *Engine

type EngineDeps struct {
    Broker  Broker
    Risk    Risk     // required; nil panics with a helpful message
    Store   OrderStore
    Clock   Clock
    Logger  *slog.Logger
}
```

`Risk` enforces, on every order submission:

- Per-symbol position-size cap (notional and quantity).
- Per-strategy notional cap.
- Daily loss limit with real-time P&L recomputation from `trades`.
- Maximum open orders.
- Symbol-class allowlist (e.g., crypto-only strategy refuses to submit equity orders).

Each rule is independently testable with table-driven tests covering both the pass and reject paths.

**Broker interface** unifies paper, Robinhood (crypto + stocks), Alpaca:

```go
type Broker interface {
    PlaceOrder(ctx context.Context, o Order) (BrokerOrder, error)
    CancelOrder(ctx context.Context, id string) error
    ReplaceOrder(ctx context.Context, id string, modifications Modifications) (BrokerOrder, error)
    GetOrder(ctx context.Context, id string) (BrokerOrder, error)
    ListOrders(ctx context.Context, filter OrderFilter) ([]BrokerOrder, error)
    Positions(ctx context.Context) ([]Position, error)
    Balances(ctx context.Context) (Balances, error)
    OnEvent() <-chan BrokerEvent  // fills, rejections, cancels (push)
}
```

The execution layer maintains **state-machine integrity** on every event: a partial fill from the broker stream advances the order to `PartiallyFilled` and appends an `order_event`. A divergent broker state triggers a `BrokerSyncReconcile` job (every 60s).

**Idempotency.** Every order carries a `client_order_id`. The broker is asked to honor it (Alpaca and Robinhood both support this); the local store guarantees that re-submission with the same `client_order_id` is a no-op. This eliminates v1's risk of double-fill on retry.

#### 3.3.6 Backtesting

Bar-replay backtest only in v2.0 (per requirements). The backtester:

1. Accepts a `Strategy[Cfg]` instance, a symbol set, a timeframe, a date range, and starting capital.
2. Replays bars from `bars` table in chronological order, calling `OnBar` for each.
3. Routes signals through the same execution path as live trading, but with a **deterministic paper broker** that fills at the bar's `close` (configurable: `open`, `vwap`, `mid`, or simulated slippage model).
4. Captures equity curve, drawdown, Sharpe, Sortino, win rate, max consecutive losses, exposure-time distribution.
5. Persists report to `backtests` table and renders an interactive HTML chart.

Determinism is enforced: no wall-clock reads, no goroutines, deterministic random seed if any random component exists. A backtest is a `pure` function of (strategy, config, bars).

#### 3.3.7 AI Integration

**Provider abstraction:**

```go
type AIProvider interface {
    Name() string                                                       // "claude" | "gemini"
    Invoke(ctx context.Context, req AIRequest) (AIResponse, error)
}

type AIRequest struct {
    Skill       string                  // e.g., "sherwood:strategy-design"
    Inputs      map[string]any
    OutputSchema *jsonschema.Schema     // optional; if set, response is validated
    ThreadID    *string                 // for persistent chat
    Provider    string                  // override default
}

type AIResponse struct {
    Text         string
    Structured   json.RawMessage         // if OutputSchema was set
    UsedTokens   TokenUsage
    LatencyMs    int64
    SkillVersion string
}
```

**Implementations:**
- `claude_cli.go` — subprocesses `claude -p "<prompt>" --output-format json --skill <skill>` with stdin carrying inputs; mounts host `~/.claude` read-only for subscription auth.
- `gemini_cli.go` — analogous for Gemini CLI.
- Both record full request and response in `ai_invocations` for audit and cost visibility.

**Skills** live in `.claude/skills/` mounted into the container. v2.0 ships:

| Skill | Purpose | Output |
| --- | --- | --- |
| `sherwood:strategy-design` | Propose a `Strategy[Cfg]` config given user constraints + market regime | JSON config validated against `Cfg` schema |
| `sherwood:market-regime` | Classify current market (trending / mean-reverting / choppy / risk-off) | `{regime, confidence, evidence}` |
| `sherwood:backtest-interpreter` | Read a backtest report and write plain-English analysis | `{summary, strengths, weaknesses, suggestions}` |
| `sherwood:trade-rationale` | Generate "why this trade" text for a signal | `{rationale, risks}` |
| `sherwood:risk-review` | Flag concentration / correlation in current portfolio | `{flags[], suggestions[]}` |
| `sherwood:halt-monitor` | Inspect drawdown, volatility regime, and recent fills; recommend halt-or-continue | `{decision, severity, evidence, recommended_actions[]}` |

Plus a curated subset from `anthropics/financial-services`: `competitive-analysis`, `sector-overview`, `earnings-analysis`, `thesis-tracker`. Skills are versioned with the binary; a release ships a known-good skill set.

**Authority model — four tiers.** AI authority is bounded and explicit. Every action falls into one of:

| Tier | Description | Approval | Examples |
| --- | --- | --- | --- |
| **A — Auto-display** | Read-only output | None | Analysis, summaries, regime classification, backtest interpretation |
| **B — Protective autonomy** (configurable, default off) | Reversible safety actions that *reduce* exposure | Pre-authorized via `ai_halt_policy`; per-action audit log + push notification | Halt all strategies, halt one strategy, cancel pending orders |
| **C — Always-approve** | Any portfolio-changing or strategy-changing action | Explicit user click on a queued proposal | Strategy creation/tuning, order placement, position close, broker config change |
| **D — Never-AI** | Forbidden to AI under any policy in v2.x | n/a | Direct order placement when live trading lands; opening new positions; modifying live position size |

The asymmetry is intentional: AI may pull the emergency brake (Tier B) but never the gas (Tier C/D). Halting is reversible and bounds the worst-case outcome to "missed trades"; opening or closing a position is not, and bounds the worst-case to "realized loss."

**Halt policy.** Tier B is opt-in via an `ai_halt_policy` config defining the monitors, conditions, and allowed actions:

```yaml
ai_halt_policy:
  enabled: true
  monitors:
    - name: daily_drawdown_breach
      schedule: "*/15 9-16 * * 1-5"     # every 15 min during US market hours
      skill: sherwood:halt-monitor
      condition: "decision == 'halt' && severity >= 'high'"
      actions: [halt_all_strategies, cancel_pending_orders]
    - name: regime_break
      schedule: "0 */1 * * *"            # hourly
      skill: sherwood:market-regime
      condition: "regime == 'risk-off' && confidence > 0.8"
      actions: [halt_all_strategies]
  notifications:
    - webhook: "${HALT_WEBHOOK_URL}"     # e.g., Discord, Pushover, ntfy
```

Policies are versioned in DB; changes require explicit user save. Disabling a monitor is one click.

**Persistent threads** for the Chat surface only. One-shot skill invocations are stateless. Threads stored in `ai_threads` and `ai_messages`.

**Scheduled AI jobs** are managed by an internal cron (Go `robfig/cron`-style or stdlib timers). Job definitions live in `ai_jobs` table:

```
ai_jobs:
  id, name, schedule (cron expr), skill, inputs (jsonb), enabled, last_run, next_run, tier
```

The `tier` column declares which authority tier this job operates at; a job marked Tier B that triggers a halt action records both the AI invocation and the resulting `halt_event` row. Tier C jobs produce draft proposals that surface in the UI's review queue — nothing is auto-applied.

#### 3.3.8 API and WebSocket Contracts

**Base path:** `/api/v1/`. **Error envelope:**

```json
{ "error": { "code": "validation_failed", "message": "...", "details": {...} } }
```

**Endpoint groups:**

- `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, `GET /auth/sessions`, `DELETE /auth/sessions/{id}`
- `GET/POST/PATCH/DELETE /strategies`, `POST /strategies/{id}/start`, `POST /strategies/{id}/stop`
- `GET/POST /orders`, `GET /orders/{id}`, `POST /orders/{id}/cancel`
- `GET /positions`, `GET /portfolio/summary`, `GET /portfolio/equity-curve?range=1d|7d|30d|all`
- `POST /backtests`, `GET /backtests`, `GET /backtests/{id}`
- `POST /ai/invoke` (one-shot), `POST /ai/threads`, `GET /ai/threads/{id}`, `POST /ai/threads/{id}/message`, `GET/POST/PATCH /ai/jobs`
- `GET /admin/config`, `POST /admin/config/reload`, `GET /admin/health`, `GET /admin/metrics` (Prometheus)

**WebSocket** at `/ws`. Client sends `{type: "subscribe", topics: ["orders", "positions", "ai.thread.<id>"]}`. Server pushes:

```json
{ "type": "order.updated", "data": { ... }, "ts": "2026-05-07T12:34:56Z" }
{ "type": "position.changed", "data": { ... }, "ts": "..." }
{ "type": "ai.message.appended", "data": { "thread_id": "...", "message": {...} }, "ts": "..." }
```

CORS configured permissively for the LAN origin, restrictive otherwise.

#### 3.3.9 Auth (Password + Session)

**Password setup.** On first boot, if no `SHERWOOD_PASSWORD_HASH` is set, log a generated bcrypt hash for a random 32-character password to stdout and exit. Operator copies hash into env. Password is rotated by setting a new hash and restarting; existing sessions remain valid until natural expiration (or are revoked via `/auth/sessions`).

**Login.** `POST /auth/login {password}` → bcrypt-compare against hash. On success, generate a 32-byte random session token, hash with SHA-256 for storage, set `Set-Cookie: sherwood_session=<token>; HttpOnly; SameSite=Lax; Path=/; Max-Age=2592000` (30 days). Also returns `{token}` in JSON body for non-browser clients to use as `Authorization: Bearer <token>`.

**Sessions table:**

```sql
CREATE TABLE sessions (
    id           uuid primary key default gen_random_uuid(),
    token_hash   text not null unique,
    created_at   timestamptz not null default now(),
    last_seen_at timestamptz not null default now(),
    expires_at   timestamptz not null,        -- hard cap = created_at + 30d
    user_agent   text,
    ip           inet
);
```

**Sliding expiration.** Each authenticated request bumps `last_seen_at`, but `expires_at` is hard-capped at `created_at + 30 days` — no "trust this device" extension. Past 30 days, the session is invalid and the user must re-login.

**Logout.** `POST /auth/logout` deletes the session row and returns `Set-Cookie: sherwood_session=; Max-Age=0`.

**Rate limit** on `/auth/login`: 5 attempts per IP per 15 minutes; longer back-off on repeated failures.

### 3.4 Alternatives Considered

| Decision | Chosen | Alternatives | Why |
| --- | --- | --- | --- |
| Decimal type | `shopspring/decimal` | `int64` minor units; `cockroachdb/apd` | Most ubiquitous, JSON string marshaling, good ecosystem. `int64` unit-of-account is fragile across asset classes (stocks vs crypto with 8 decimal places). |
| Database default | Postgres in compose | SQLite-only; cloud Postgres (Neon/Supabase) | Postgres self-hosted on FreeNAS gives partitioning and 0$ cost. SQLite supported via `DATABASE_URL` for dev/lightweight deployments. |
| Migration tool | `golang-migrate` | `goose`, `atlas` | Most mature, CLI is widely known, simple `up.sql / down.sql` files. |
| Logger | `log/slog` (stdlib) | `zerolog`, `zap` | Stdlib in Go 1.21+, removes a dependency, structured by default. |
| HTTP router | `net/http` (Go 1.22+) | `chi`, `gin`, `echo` | Go 1.22 stdlib supports method+path routing. Eliminates a dep. Reassess if middleware ecosystem need exceeds stdlib. |
| Strategy config | Generic `Strategy[Cfg]` | `map[string]interface{}`; codegen | Type safety at compile time; YAML unmarshals into typed `Cfg`; defaults come from struct tags. |
| Strategy registration | `init()` self-registration | Central factory switch | Adding strategy = drop-in file. No central drift. |
| AI invocation | Subprocess CLI | Direct API with key; gRPC sidecar | Uses subscription = $0/mo. No per-token cost. CLI is mature and headless-supported. Sidecar adds container surface. |
| AI primary | Claude with Gemini swappable | Claude only; Gemini only; OpenAI | Claude has best skill ecosystem (Anthropic financial-services repo). Gemini wired for fallback because rate limits exist on subscriptions. |
| AI auth model | Mount host `~/.claude` RO | API key env var; OAuth | Subscription is $0; API key is paid. RO mount prevents container from modifying credentials. |
| Auth model | Password + session cookie (single shared) | API key only; OAuth/OIDC; mTLS | Phone-friendly; UX-safe; no full user table needed for solo operation. Forward-compatible to multi-user. |
| Frontend timing | Backend assumes UI is coming, UI shipped at v2.3 | Strict M2M; build polished UI in v2.0 milestone | API designed CORS-safe and event-rich from v2.0; the "Robinhood-inspired" UI is its own polish milestone with a dedicated design-system pass. Avoids slowing v2.0 on UI quality bar. |
| UI architecture | Same-repo `web/` workspace, bundled via `embed.FS` into the Go binary | Separate repo + separate deploy; SSR Go templates only | Single artifact deploy matches FreeNAS-container model; `embed.FS` keeps the binary self-contained; design-system polish is achievable with React/Svelte tooling that Go templates cannot match. |
| Order lifecycle | Explicit state machine + events | Status field only | Audit, replayable history, partial fill correctness. |
| Risk subsystem | Required at construction | Optional with defaults | Eliminates v1's `nil`-in-prod foot-gun. |
| Engine scheduling | `(timeframe, strategy)` keyed | `symbol`-keyed (v1) | v1's "first strategy's timeframe wins" was non-deterministic. New model is correct by construction. |
| Backtest scope | Bar-replay only | Walk-forward; tick-replay; monte-carlo | Matches stated requirement. Other modes deferred to a later milestone. |
| Container image | Multi-arch (amd64 + arm64) | amd64 only | FreeNAS hardware varies; arm64 covers Pi/Apple-Silicon scenarios at near-zero cost via `buildx`. |
| Hosting | Docker compose on FreeNAS | VPS rental; Kubernetes; Fly.io | $0/mo; matches user's existing infra. Compose works on most container orchestrators if migrated later. |

### 3.5 Dependencies

| Package | Purpose | Risk |
| --- | --- | --- |
| `github.com/shopspring/decimal` | Decimal money/quantity type | Mature, low risk. |
| `github.com/jackc/pgx/v5` | Postgres driver + pool | Mature, idiomatic Go. |
| `modernc.org/sqlite` | Pure-Go SQLite for fallback path | CGO-free, big transpiled C surface; acceptable for dev. |
| `github.com/golang-migrate/migrate/v4` | Schema migrations | Mature; CLI-and-library use both supported. |
| `github.com/google/uuid` | UUIDs | Stdlib-quality. |
| `github.com/coder/websocket` | WebSocket server | Modern replacement for `gorilla/websocket`. |
| `github.com/go-playground/validator/v10` | Struct-tag validation | Pragmatic for now; revisit with OpenAPI later. |
| `github.com/robfig/cron/v3` | Scheduled-job parsing | Mature. |
| `github.com/santhosh-tekuri/jsonschema/v5` | JSON Schema validation for AI outputs | Mature. |
| `golang.org/x/crypto/bcrypt` | Password hashing | Stdlib-adjacent, well-vetted. |
| `github.com/prometheus/client_golang` | Metrics | Industry standard. |
| `github.com/stretchr/testify` | Test assertions | Standard. |
| `github.com/leanovate/gopter` | Property-based testing for indicators / P&L | Adds strong invariants for math-heavy code. |
| `github.com/alpacahq/alpaca-trade-api-go/v3` | Alpaca SDK | Official, maintained. |
| (Robinhood) | Custom client; no official SDK | High risk — see Tech Debt. |

The dependency surface targets ~15 direct deps total — meaningfully smaller than v1's. `chi`, `zerolog`, `gorilla/websocket`, `joho/godotenv`, and `httprate` are explicitly dropped.

### 3.6 Migrations

Schema versioning via `golang-migrate`. Files in `migrations/` numbered sequentially:

```
migrations/
  000001_init.up.sql
  000001_init.down.sql
  000002_partitions.up.sql
  ...
```

Migrations run on container startup behind a leader-election lock (the `schema_migrations` table itself). Failed migrations halt boot — operator must address.

For SQLite path, a parallel migration set in `migrations_sqlite/` is required because partition syntax doesn't apply. `golang-migrate` is invoked with the appropriate path based on `DATABASE_URL` scheme.

Rollback procedure: see [§6.4 Rollback Strategy](#64-rollback-strategy).

Data migration from v1's SQLite database is **out of scope** for v2.0. v2 launches with empty state. (A one-time `cmd/import-v1-data` could be added later if the operator wants order/trade history preserved.)

### 3.7 Technical Debt

**Carried-forward from v1 — explicitly NOT addressed in v2.0:**

- No live trading execution. Risk subsystem and order state machine are designed to support live, but real-money brokers are gated off.
- No multi-user auth or RBAC. Single-operator only.
- No high-availability deployment. Single-node.
- No data migration from v1.

**New debt v2.0 knowingly takes on:**

- **Robinhood unofficial API.** Robinhood does not publish an official trading API for stocks; the implementation will use the community library `robinhood-api` style. This is acknowledged fragility — breakage on Robinhood's whim is expected. Robinhood Crypto has an official API and is the safer surface.
- **Yahoo Finance dependency.** `piquette/finance-go` (or successor) remains the free fallback for backfill but is recognized as TOS-risky and rate-limited. Tiingo / Polygon are designed-in as no-code provider swaps for when Yahoo gets blocked.
- **Skill versioning.** AI skills are mounted from a host directory and versioned alongside the binary; coordinated updates require operator awareness. A future improvement is to embed skills in the container image.
- **No mobile-app.** Phone access is via mobile browser. PWA installability targeted but not a v2.0 hard requirement.
- **Backtest determinism for streaming-ingest paths.** Backtests use historical bars and are deterministic; the live path's reordering is not reproducible. This is fundamental and accepted — backtests are the canonical reproducibility surface.

Each item is tracked in [`docs/retros/v2-debt-register.md`](../../docs/retros/v2-debt-register.md) (created at first commit) and revisited every release.

---

## 4. Quality Attributes

### 4.1 Security

- **Auth:** single shared password, bcrypt hashed (cost 12), session cookies with `HttpOnly` and `SameSite=Lax`. CSRF mitigated by `SameSite=Lax` + double-submit token for state-changing requests.
- **Transport:** binary serves plain HTTP on the LAN; TLS terminated at reverse proxy (operator-configured). External exposure requires deliberate router configuration plus reverse-proxy + Let's Encrypt; not a default.
- **Secrets:** never committed. Postgres password and bcrypt hash via `.env` not loaded by Go directly. AI subscription via mounted host config.
- **Input validation:** all user-controlled inputs pass through validator; SQL queries via parameterized `pgx` queries; no string concatenation.
- **Dependencies:** `govulncheck` runs in CI on every push. Dependabot configured for security updates.
- **Container:** runs as non-root user; read-only root filesystem where feasible; no privileged mode.
- **Audit:** every order, AI invocation, login, and config change is logged with operator identity (effectively "the operator" for v2.0).

### 4.2 Reliability

- Single-node best-effort; designed to recover from restart in < 30s with Postgres warm.
- Order-state recovery from `order_events` event log on boot; no in-memory state that's not derivable from the DB.
- Broker reconciliation job runs every 60s to detect divergence between local store and broker state.
- Streaming-data subscriptions auto-reconnect with exponential backoff; gap-fill via `History` REST on reconnection.
- CLOSE_ON_SHUTDOWN flag (default off in paper mode, opt-in for live) cancels pending orders and optionally flattens positions on graceful shutdown.

### 4.3 Data Integrity

- Decimal money everywhere; no float arithmetic on price or quantity.
- Order events are immutable, append-only. Current-state denormalization is reconcilable from events.
- Postgres ACID transactions wrap multi-row writes (e.g., order placement + event append + position update).
- Daily DB backup via `pg_dump` plus FreeNAS dataset snapshots; tested restore quarterly.
- Schema migrations are versioned, reversible, and tested in CI against a fresh DB.

### 4.4 Privacy

- All data is private to the single operator.
- AI invocations include only data the operator already owns (their own portfolio, their own backtest reports). No third-party PII.
- Logs scrub sensitive headers (`Authorization`, `Cookie`).
- AI provider transport: subprocess pipes to `claude` / `gemini`, which call Anthropic / Google APIs respectively. Operator's existing privacy posture with those vendors applies.

### 4.5 Scalability

v2.0 target envelope (per [B1 in requirements](#)):

- ~500 symbols subscribed
- ~5–10 strategies running concurrently (typically per-symbol-class)
- Mixed timeframes, peak ~500 bars/min ingested
- Single Postgres instance, ~3.5 GB at full retention

Beyond v2.0:
- Provider rate-limit pressure is the first ceiling; mitigation = paid Tiingo/Polygon swap.
- Postgres single-instance writes are the second ceiling at ~100x current scale; mitigation = horizontal partitioning (per-symbol-class shards) or move to TimescaleDB hypertables.
- Engine concurrency is bounded by GOMAXPROCS × per-provider semaphores; vertical scaling on FreeNAS hardware suffices for at least 5x growth.

### 4.6 Latency

- **Tick-to-order budget:** < 1 second p99 (per [B2 in requirements](#)).
- Engine path: bar-close event → cache update → strategy `OnBar` → signal → risk → broker submit. Target < 200 ms in-process; dominated by broker network call.
- AI invocations are out-of-band (user-initiated or scheduled); not in the hot path. Typical CLI subprocess latency: 3–15 s per skill.
- WebSocket event push: < 50 ms from internal event to client.

### 4.7 Abuse

- Login rate-limited (5/15min/IP).
- AI invocation rate-limited (configurable; default 10/min per session).
- API rate-limited (default 100/min per session).
- Outbound broker calls are bounded by per-broker semaphores so a runaway strategy can't DDOS the broker.
- A "kill switch" endpoint (`POST /admin/halt`) immediately stops all strategies and cancels pending orders. The operator can trigger it manually with a confirmation token; the AI halt policy (see §3.3.7, Tier B) can trigger the same path autonomously when configured. Both routes write to `halt_events` with full context (caller, reason, evidence) and emit a high-priority notification.
- Strategy CPU budget enforced by goroutine-per-strategy + watchdog; a strategy exceeding budget for N consecutive bars is auto-paused.

### 4.8 Accessibility

- Web UI follows WCAG 2.1 AA targets where applicable: keyboard navigation for all interactive controls, semantic HTML, color-contrast ratios ≥ 4.5:1, no information-by-color-alone, focus indicators visible.
- Phone-first responsive layout; no horizontal scrolling at 360 px width.
- Server emits accessible HTML (no client-rendered-only content for critical surfaces).
- Charts include accessible alternatives (data tables) on demand.

### 4.9 Testability

- `Clock` interface injected everywhere. `FakeClock` advances on test demand.
- Provider, broker, AI, and risk are interfaces; in-memory fakes are first-class deliverables alongside real implementations.
- Property-based tests (`gopter`) for indicators (RSI, SMA, MACD, BB) and P&L invariants.
- Go-native HTTP integration tests via `httptest`; **no bash-curl tests**.
- `testcontainers-go` for Postgres-backed integration tests.
- Coverage policy: critical-path coverage tracked separately from line %; mutation testing (`go-mutesting`) on the engine and execution packages quarterly.
- CI runs `go test -race -count=1 ./...` plus `golangci-lint`, `govulncheck`, and a fuzz pass on JSON-API parsing on every PR.

### 4.10 Internationalization and Localization

Out of scope for v2.0. The UI is English-only; numbers are USD with `decimal` strings; timestamps are UTC in storage and rendered in the operator's local time on the client.

Forward-compatibility: text strings live in a single `i18n/en.json` so a future locale add is a translation file.

### 4.11 Compliance

This is a self-hosted personal-use system. No regulatory regime applies to paper trading.

For the eventual live-trading milestone (post-v2.0), the operator (not the system) is responsible for tax reporting and any FINRA / SEC obligations. The system supports compliance by:

- Persisting an immutable order-and-trade audit log.
- Exporting trade history in `csv` / `json` for tax tools.
- Logging every AI-generated proposal with the user-approval timestamp.

---

## 5. Project Management

### 5.1 Work Estimates

Phases align with the v2 build order in the [tech-debt retro](../../docs/retros/v1-tech-debt.md):

| Phase | Scope | Est. effort |
| --- | --- | --- |
| **A — Foundation** | Decimal type, `App` scaffold, migration framework, `Clock` abstraction, CI gates (lint/vuln/race), repo skeleton, first foundational decisions (see [Amendment log](#amendment-log)) | 1–2 weeks |
| **B — Core Domain** | Order aggregate + state machine, Risk subsystem, `Strategy[Cfg]` + self-registration, scheduler, paper broker | 2–3 weeks |
| **C — Data + Providers** | Postgres schema + partitions, Yahoo provider (REST), Alpaca provider (REST + stream), Robinhood Crypto provider, in-memory cache | 2 weeks |
| **D — API + Auth + UI shell** | REST + WebSocket, password auth, sessions, basic web UI shell with login + portfolio + strategies pages | 2 weeks |
| **E — Backtesting** | Bar-replay engine, equity-curve report, HTML chart | 1 week |
| **F — AI Integration** | Provider abstraction, claude_cli subprocess, skill loading, schema validation, chat thread persistence, scheduled jobs | 2 weeks |
| **G — Robinhood Stocks (best-effort)** | Unofficial-API broker; isolated and gated behind feature flag | 1 week |
| **H — Production hardening** | Goreleaser, multi-arch images, cosign signing, SBOM, runbook, deployment guide | 1 week |

Total: **12–14 weeks of focused effort.** No fixed deadline; scope reduces before it slips.

### 5.2 Documentation Plan

| Doc | Path | Owner | Cadence |
| --- | --- | --- | --- |
| System Design (this doc) | `wiki/designs/sherwood-v2.md` | Author | Update on architectural change |
| Decision records | this design's [Amendment log](#amendment-log) | Author | One entry per significant decision |
| Runbook | `wiki/Runbook.md` | Author | Update on incident; reviewed monthly |
| Deployment Guide | `wiki/Deployment.md` | Author | Update on infra change |
| API Reference | `wiki/API.md` (auto-generated from OpenAPI) | CI | On every API change |
| Configuration Reference | `wiki/Configuration.md` (auto-generated from struct tags) | CI | On every config field change |
| Strategy Authoring Guide | `wiki/Strategy-Authoring.md` | Author | On strategy interface change |
| AI Skill Authoring | `wiki/AI-Skills.md` | Author | On skill addition |
| UI Design System | `wiki/UI-Design-System.md` (added v2.3) | Author | On design-system change |
| Tech-Debt Register | `docs/retros/v2-debt-register.md` | Author | Reviewed each release |
| Release Notes | GitHub Releases | CI + Author | Per release |

The repo's `wiki/` directory is the canonical operator-facing surface and is auto-deployed to GitHub Wiki on push to `main`.

### 5.3 Launch Plans

**v2.0 — paper-trading single-operator MVP**

- Phases A–F + H complete
- Phase G (Robinhood stocks) **deferred or feature-flagged off**
- Single FreeNAS deployment
- Acceptance criteria:
  - Boot from cold to "ready" in < 60 s
  - Subscribe to S&P 500 successfully via Alpaca stream
  - Run one MA-crossover strategy + one RSI strategy end-to-end on paper
  - Backtest produces identical results across 3 consecutive runs (deterministic)
  - AI strategy-design skill returns valid JSON validated against `Cfg` schema
  - All CI gates green; `golangci-lint`, `govulncheck`, race detector pass
  - 30-day session login from phone-on-LAN works in Safari and Chrome

**v2.1 — Robinhood stocks broker + scheduled AI jobs**

**v2.2 — additional strategies (Bollinger, MACD), portfolio analytics in UI**

**v2.3 — Robinhood-inspired web UI (the polish milestone).** Mobile-first, brand-quality design system; live equity curve, position cards, order ticket, strategy detail pages, AI chat surface as a first-class screen; PWA installability; shipped as a separate `web/` workspace built and bundled into the Go binary's embedded `fs`. Acceptance criteria include Lighthouse mobile scores ≥ 90 across performance / accessibility / best-practices.

**v3.0 — live trading (real-money), additional brokers (Interactive Brokers), HA**

---

## 6. Operations

### 6.1 SLAs

Self-hosted, single-operator, **best-effort** — no formal SLA. Targets the operator holds the system to:

- Boot-to-ready < 60 s after `docker compose up`.
- Order-acknowledged-by-broker p99 < 1 s (network conditions permitting).
- Backtest reproducibility 100% (deterministic given identical inputs).
- AI invocation success rate > 95% (excludes provider rate-limits and outages, which surface as "ratelimited" / "provider_unavailable" errors, not faults).
- Streaming subscription uptime > 99% (excluding upstream Alpaca/Robinhood outages).

### 6.2 Monitoring & Alerting

Prometheus metrics endpoint at `/admin/metrics`. Recommended alert rules:

| Metric | Alert when | Severity |
| --- | --- | --- |
| `sherwood_engine_tick_lag_seconds{quantile="0.99"}` | > 2 s for 5 min | warning |
| `sherwood_engine_tick_lag_seconds{quantile="0.99"}` | > 5 s for 5 min | critical |
| `sherwood_broker_reconcile_diverged_total` | rate > 1 / min for 10 min | warning |
| `sherwood_provider_subscription_status{status="disconnected"}` | == 1 for 2 min | warning |
| `sherwood_orders_rejected_total{reason!="risk_block"}` | rate > 5 / min for 5 min | warning |
| `sherwood_ai_invocation_failures_total` | rate > 0.5 / min for 10 min | warning |
| `sherwood_db_pool_in_use` | > 80% of max_open for 10 min | warning |
| `sherwood_app_panic_total` | > 0 | critical |
| `sherwood_login_failures_total` | rate > 10 / min for 5 min | warning (possible brute force) |

Alerting is operator-configured (Grafana / Alertmanager / a webhook to a private Discord). The binary does not ship its own alerting plane.

### 6.3 Logging Plan

- Structured JSON via `log/slog` to stdout; Docker collects.
- Levels: `debug | info | warn | error`. Default `info` in prod.
- Request log line on every API call: `request_id`, `method`, `path`, `status`, `latency_ms`, `session_id`.
- Engine log line on every signal: `strategy_id`, `symbol`, `side`, `quantity`, `signal_id`.
- Order events: `order_id`, `client_order_id`, `from_state`, `to_state`, `broker_order_id`.
- Sensitive scrubbing: `Authorization`, `Cookie`, password fields stripped before log emission.
- Log retention: container stdout retained by Docker driver (operator-configured); Postgres-side audit log retained 90 days, archived to FreeNAS dataset.

### 6.4 Rollback Strategy

**Container image:** GHCR retains all tagged versions. Rollback = update `image:` tag in `compose.yml` to previous version, `docker compose up -d`. Boot validates schema version is compatible (or runs `down` migration if downgrade is allowed for that step).

**Schema migrations:** every `up.sql` has a corresponding `down.sql`. A migration is **rollback-safe** only if data preservation is documented. Destructive migrations (e.g., column drops) require:

1. An amendment-log entry in the governing design documenting the migration.
2. Pre-migration DB snapshot (`pg_dump` or FreeNAS snapshot).
3. Operator-acknowledged in `migrations/NNN_DESTRUCTIVE.md` companion file.

Rollback procedure for a destructive migration is: stop container → restore snapshot → start container with previous image. Loss of any data committed between snapshot and rollback is accepted.

**Configuration rollback:** environment values are version-controlled by the operator (`.env` file in their own home-server repo); rollback is `git revert` plus `docker compose up -d`.

**Strategy rollback:** strategies are stored in DB with versioned configs. Reverting a strategy = restoring its config row from `strategy_config_history` (append-only).

**AI skills rollback:** skills mounted from a versioned directory; rollback = swap the mount target or `git checkout` the skills directory and restart container.

---

## Amendment log

**2026-06-30 — Adopted the six-section wiki model; retired the planned ADR class.** This design moved into `wiki/designs/` as a living design and gains this amendment log as the home for build decisions; the Phase-A "ADRs 1–5", the documentation-plan `docs/adr/NNNN-*.md` row, and the destructive-migration "ADR" step now route to entries here instead of standalone records under `docs/adr/`. *Why not keep `docs/adr/`:* the ADR model was retired across the operator's repos — a standalone record re-creates the chain-read drift (a superseding decision left pointing back through a superseded file) that a living body collapses, so sherwood adopts the same model agentm + crickets did rather than carry a parallel artifact class. *Re-audit trigger:* a durable decision class emerges with no living-design home, or a future host reintroduces ADRs as the operator default.

---

_End of design document._
