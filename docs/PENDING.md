# Pending Features

This document tracks future feature ideas and enhancements that are not currently prioritized for implementation. Each entry includes enough detail to pick up and implement later.

Features are ordered by complexity from least to most complex.

---

## 1. Structured Logging with Trace IDs

**Complexity:** Low

**Description:**
Add correlation/trace IDs to all critical log paths (order execution, strategy signals) to enable tracing logic flow across components.

**Current State:**
`zerolog` is used for logging, but lacks trace context across distributed operations.

**Implementation Requirements:**

1. Add `trace_id` to request context in API middleware
2. Generate unique IDs for engine operations (strategy evaluation, order placement)
3. Propagate trace IDs through function calls via context
4. Update critical log points: order execution, strategy signals, data provider calls, engine ticks
5. Use `log.With().Str("trace_id", traceID)` pattern

**Files to Modify:**

- `backend/api/middleware.go` - Add trace ID middleware
- `backend/engine/engine.go` - Generate trace IDs for ticks
- `backend/execution/order_manager.go` - Include trace in orders

---

## 2. Database Persistence for Orders

**Complexity:** Low-Medium

**Description:**
Verify and ensure that `OrderManager` persists orders and positions to SQLite so state survives application restarts.

**Implementation Requirements:**

1. Review `execution/order_manager.go` to check if orders are persisted or in-memory only
2. If missing, implement `SaveOrder(order *Order) error` in order store
3. Load pending orders on startup
4. Persist position updates with transaction support
5. Handle database write failures gracefully

**Schema Needed:**

- Orders table (id, symbol, side, type, quantity, price, status, timestamps)
- Positions table (symbol, quantity, avg_price, unrealized_pnl)
- Trade history table

---

## 3. Enhanced Configuration Validation

**Complexity:** Medium

**Description:**
Add rigorous validation of configuration on startup with fail-fast behavior. Ensure required API keys and credentials are present for the selected trading mode and data provider.

**Current State:**
Basic config validation exists. Config validation endpoint shows warnings, but doesn't enforce requirements.

**Implementation Requirements:**

1. Add `config.Validate() error` method called during startup
2. Mode-specific validation (live requires broker credentials, paper requires API keys)
3. Provider-specific validation (Tiingo requires API key, Binance requires key+secret)
4. Helpful error messages with fix suggestions
5. Return detailed errors for missing required fields

---

## 4. Graceful Shutdown for Trading Engine

**Complexity:** Medium

**Description:**
Extend graceful shutdown beyond HTTP server to include the trading engine. Ensure open positions can optionally be closed or state checkpointed before shutdown.

**Current State:**
`main.go` has graceful shutdown for HTTP server only.

**Implementation Requirements:**

1. Extend signal handler to coordinate with engine
2. Add `engine.Shutdown(ctx context.Context) error` method
3. Stop accepting new signals, wait for in-flight orders
4. Configurable option to close all positions (CLOSE_ON_SHUTDOWN)
5. Checkpoint state to database before exit
6. Shutdown engine first, then API server

---

## 5. Hot-Swapping Strategies

**Complexity:** Medium-High

**Description:**
Enable/disable trading strategies at runtime without restarting the application.

**Current Limitation:**
Strategies are loaded once at startup from `ENABLED_STRATEGIES` environment variable.

**Implementation Requirements:**

1. **API Endpoints:** `POST /api/v1/strategies/{name}/enable` and `/disable`
2. **Thread-Safe Registry:** Add Enable/Disable methods with mutex protection
3. **Engine Coordination:** Check if strategy enabled before processing
4. **Position Management:** Decide what happens to positions when strategy disabled
5. **State Persistence:** Store enabled/disabled state in database
6. **Frontend Integration:** Toggle switches in strategy list UI

**Edge Cases:**

- Disabling strategy during trade execution
- Multiple concurrent enable/disable requests
- Restart behavior with partially enabled strategies

---

## 6. Deployment Configuration (Docker)

**Complexity:** High

**Description:**
Create Dockerfiles and docker-compose configuration to containerize the application.

**Implementation Requirements:**

1. **Backend Dockerfile:** Multi-stage build (golang builder + alpine runtime)
2. **Frontend Dockerfile:** Build React app with Vite, serve with nginx
3. **docker-compose.yml:** Services for backend, frontend, database with health checks
4. **Deployment Directory:** `deployments/docker/` and `deployments/k8s/` (optional)
5. **Documentation:** Update README with Docker usage instructions

**Files to Create:**

- `backend/Dockerfile`
- `frontend/Dockerfile`
- `docker-compose.yml`
- `docker-compose.dev.yml`
- `.dockerignore`

---

## 7. Frontend Implementation

**Complexity:** High

**Description:**
Build the React-based web dashboard for Sherwood as specified in DESIGN.md.

**Implementation Requirements:**

1. **Scaffolding:** Initialize Vite + React + TypeScript project
2. **Core Pages:** Dashboard, Strategies, Backtesting, Orders, Settings
3. **API Integration:** axios client with react-query for data fetching
4. **State Management:** Redux for global state, React Query for server state
5. **Visualization:** Recharts for equity curves and performance metrics
6. **Responsive Design:** Material-UI components, mobile-friendly, dark mode

**Directory Structure:**

```
frontend/
├── src/
│   ├── components/
│   ├── pages/
│   ├── services/    # API client
│   ├── hooks/
│   └── store/       # Redux
```
