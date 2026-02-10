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

## 7. Benchmark Tests for Performance Monitoring

**Complexity:** Medium

**Description:**
Add benchmark tests to detect performance regressions in critical paths (backtesting, order placement, data fetching).

**Current State:**
No benchmark tests exist. Manual performance testing only.

**Implementation Requirements:**

1. **Backtest Engine Benchmarks:** Test with various data set sizes (1 month, 1 year, 5 years)
2. **Order Placement Throughput:** Measure orders/second capacity
3. **Data Provider Latency:** Benchmark historical data fetching
4. **API Response Times:** Benchmark common endpoints
5. **CI Integration:** Track benchmark results over time

**Files to Create:**

- `backend/backtesting/engine_bench_test.go`
- `backend/execution/order_manager_bench_test.go`
- `backend/data/providers/yahoo_bench_test.go`
- `backend/api/handlers_bench_test.go`

**Example:**

```go
func BenchmarkBacktest_LargeDataset(b *testing.B) {
    // Generate 1 year of daily data
    data := generateMockData(365)
    strategy := strategies.NewMACrossover()
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        engine.Run(strategy, data, config)
    }
}
```

---

## 8. Concurrent Operation Tests

**Complexity:** Medium-High

**Description:**
Add tests to verify thread safety and race condition protection in concurrent scenarios (multiple simultaneous orders, parallel backtests).

**Current State:**
No explicit concurrent operation tests. Thread safety assumed but not verified.

**Implementation Requirements:**

1. **Concurrent Order Placement:** 10+ goroutines placing orders simultaneously
2. **Parallel Backtests:** Multiple backtests running concurrently
3. **Strategy Hot-Swap During Trading:** Enable/disable strategies while engine running
4. **Data Provider Concurrent Requests:** Multiple threads fetching data
5. **Race Detector:** Run tests with `-race` flag in CI

**Test Strategy:**

```go
func TestConcurrentOrderPlacement(t *testing.T) {
    var wg sync.WaitGroup
    orderManager := setupOrderManager()
    
    // Launch 10 goroutines placing orders
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            order, err := orderManager.CreateMarketOrder(
                "AAPL", models.OrderSideBuy, 10)
            assert.NoError(t, err)
        }(i)
    }
    
    wg.Wait()
    // Verify all orders processed correctly
}
```

---

## 9. Edge Case Test Coverage

**Complexity:** Low-Medium

**Description:**
Expand test coverage for boundary conditions and edge cases in validation, data handling, and error scenarios.

**Missing Edge Cases:**

1. **Input Validation:**
   - Order with quantity = 0
   - Negative prices
   - Symbol with special characters (Unicode, emojis)
   - Extremely long symbol names (>100 chars)
   - Future dates in historical data requests

2. **Resource Limits:**
   - Backtest with 10+ year date range
   - Order list with 10,000+ orders
   - Concurrent backtest limit testing

3. **Error Recovery:**
   - Database connection loss during operation
   - Broker connection timeout
   - Data provider rate limit hit
   - Disk full during database write

4. **State Management:**
   - Server restart with pending orders
   - Partial fill handling
   - Balance reconciliation after failed trades

**Implementation:**
Add table-driven tests for validation edge cases, integration tests for error recovery scenarios.

---

## 10. Property-Based Testing

**Complexity:** High

**Description:**
Implement property-based testing for complex logic using testing frameworks like `gopter` to generate random test cases and verify invariants.

**Use Cases:**

1. **Backtest Engine:** Verify account balance never goes negative, equity curve properties
2. **Order Manager:** Verify order state transitions valid, balance always matches positions
3. **Risk Manager:** Verify limits always enforced regardless of input
4. **Technical Indicators:** Verify mathematical properties (SMA always within data range, etc.)

**Example:**

```go
import "github.com/leanovate/gopter"

func TestBacktest_BalanceNeverNegative(t *testing.T) {
    properties := gopter.NewProperties(nil)
    
    properties.Property("balance never negative", prop.ForAll(
        func(trades []Trade) bool {
            result := runBacktest(trades)
            return result.FinalBalance >= 0
        },
        genTrades(),
    ))
    
    properties.TestingRun(t)
}
```

---

## 11. Frontend Implementation

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

```plaintext
frontend/
├── src/
│   ├── components/
│   ├── pages/
│   ├── services/    # API client
│   ├── hooks/
│   └── store/       # Redux
```

---

## 12. Advanced Backend Endpoints (Phase 2 Completion)

**Complexity:** Medium

**Description:**
Implement the remaining API endpoints identified during the backend review to support full frontend functionality.

**Missing Endpoints:**

1. `GET /api/v1/execution/trades` - List individual trade executions
2. `PATCH /api/v1/execution/orders/{id}` - Modify open orders (price/quantity)
3. `GET /api/v1/portfolio/performance` - Portfolio performance metrics (P&L, Sharpe, etc.)
4. `GET /api/v1/notifications` - System alerts and notifications
5. `GET /api/v1/strategies/{name}/backtest` - Retrieve pre-calculated backtest results

**Implementation Requirements:**

- Create new handlers in `backend/api/handlers.go`
- Update `backend/execution/order_manager.go` to support order modification
- Implement performance calculation logic in `backend/models` or `backend/analysis` package

---

## 13. Configuration Hot-Reload

**Complexity:** Medium

**Description:**
Allow updating the application configuration without a full server restart.

**Implementation Requirements:**

1. Add `POST /api/v1/config/reload` endpoint (Admin-only)
2. Implement `config.Reload()` method to re-read `.env` or config sources
3. Notify components (Engine, Data Provider) of configuration changes
4. Handle safe transitions (e.g., don't swap data provider while fetching data)

**Files to Modify:**

- `backend/config/config.go`
- `backend/api/handlers.go`
- `backend/engine/trading_engine.go`
