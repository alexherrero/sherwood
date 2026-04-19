# Codebase Review & Production Readiness Report

## Executive Summary

The backend foundation for Sherwood is solid, with a well-structured Go application following the `DESIGN.md`. All core packages (`api`, `data`, `strategies`, `execution`, `backtesting`) are in place and contain functional code with unit tests.

The backend is now **production-ready for proof-of-concept deployment**. Critical gaps (trading engine, execution wiring, dynamic configuration, security hardening, API completeness) have been addressed. Remaining work is the **frontend** and **deployment configuration**.

## Critical Gaps (Must Fix for functionality)

### 1. Missing Trading Loop / Core Engine ✅ IMPLEMENTED

**Severity: Critical**

- **Issue:** The `main.go` file initializes the API server but does **not** start a trading loop. There is no mechanism to:
  - Poll data providers for real-time (or near real-time) prices.
  - Feed those prices into strategies.
  - Generate signals and execute orders via the `OrderManager`.
- **Impact:** The application cannot trade. It can only serve API requests.
- **Recommendation:** Implement a `Engine` struct (or `TradingLoop`) that runs in a separate goroutine, ticking at a configurable interval, fetching data, updating strategies, and executing orders.
- **Status:** Implemented in Phase 1. `TradingEngine` now runs in background, processes market data, and executes strategy signals.

### 2. Execution Wiring ✅ IMPLEMENTED

**Severity: Critical**

- **Issue:** The `OrderManager` and `Broker` components exist in the `execution` package but are **not wired up** in `main.go` or the `api` package.
- **Impact:** The API cannot query order status, place manual orders, or monitor positions. The strategies have no way to send orders to a broker.
- **Recommendation:** Initialize `OrderManager` (with a `PaperBroker` or real implementation) in `main.go` and inject it into both the `Strategy` runtime and the `API Handler`.
- **Status:** Implemented in Phase 1. `OrderManager` is wired in `main.go` and injected into both engine and API handlers.

### 3. Missing Frontend

**Severity: High**

- **Issue:** The `frontend` directory is completely missing. `DESIGN.md` specifies a React-based dashboard.
- **Impact:** No user interface for monitoring or configuration.
- **Recommendation:** Initialize the React project (Vite + TypeScript) as per design.
- **Status:** PENDING (See docs/PENDING.md)

### 4. Deployment Configuration

**Severity: High**

- **Issue:** `Dockerfile`, `docker-compose.yml`, and the `deployments/` directory are missing.
- **Impact:** Cannot be containerized or easily deployed.
- **Recommendation:** Create `backend/Dockerfile`, `frontend/Dockerfile`, and a root `docker-compose.yml` to orchestrate services (Backend, Frontend, Database).
- **Status:** PENDING (See docs/PENDING.md)

### 5. Hardcoded Data Provider ✅ IMPLEMENTED

**Severity: Medium**

- **Issue:** `main.go` explicitly initializes `providers.NewYahooProvider()`.
- **Impact:** Users cannot switch to Tiingo or Binance via configuration without code changes.
- **Recommendation:** Implement a factory pattern in `main.go` to initialize the correct provider based on `config.yaml` or `.env` `DATA_SOURCE` setting.
- **Status:** Implemented in Phase 2. `DATA_PROVIDER` environment variable with factory pattern supports yahoo, tiingo, and binance.

## Productionizing Improvements (Tweaks & Adjustments)

### 1. Enhanced Configuration & Secrets

- **Observation:** `.env` is used, which is good.
- **Suggestion:** rigorous validation of configuration on startup. Fail fast if required keys (like Tiingo API key or Broker credentials) are missing for the selected mode.

### 2. Structured Logging Context

- **Observation:** `zerolog` is used.
- **Suggestion:** Ensure all critical paths (order execution, strategy signals) include a `trace_id` or `correlation_id` to trace logic flow across components.

### 3. Graceful Shutdown for Trading

- **Observation:** `main.go` has graceful shutdown for the HTTP server.
- **Suggestion:** Extend this to the (to-be-implemented) Trading Loop. Ensure open positions are optionally closed or state is checkpointed to disk/DB before shutdown.

### 4. Database Persistence

- **Observation:** `data/database.go` seems to exist.
- **Suggestion:** Ensure `OrderManager` persists orders and positions to SQLite so state survives restarts. Currently, it might be in-memory only (needs verification of `OrderStore` implementation).

## Proposed Roadmap

1. **Phase 1: Wiring the Backend (The Loop)** ✅ COMPLETE
    - Implement the implementation of `execution.Engine` (Trading Loop).
    - Wire `OrderManager` and `Strategies` together.
    - Make `main.go` start the loop.
2. **Phase 2: Dynamic Configuration** ✅ COMPLETE
    - Support switching providers via config.
    - Support enabling/disabling strategies via config.
3. **Phase 3: Deployment & Docker** ⏳ PENDING
    - Create Dockerfiles and Compose setup.
4. **Phase 4: Frontend Implementation** ⏳ PENDING
    - Scaffolding the React app.
    - Integrating with the Backend API.

I recommend starting with **Phase 1** to get a working "backend bot" before moving to UI work.
