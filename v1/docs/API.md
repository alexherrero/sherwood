# REST API Documentation

Sherwood's REST API provide a comprehensive interface for managing trading strategies, running backtests, monitoring the trading engine, and managing orders.

## Base URL

```bash
http://localhost:8099
```

## Authentication

All endpoints under `/api/v1/` require an API key passed in the `X-Sherwood-API-Key` header.

If the `API_KEY` environment variable is not set, authentication is disabled (development mode only).

---

## Public Endpoints

### Health Check

Check if the service and its subsystems are running.
`GET /health`

**Response:**

```json
{
  "status": "ok",
  "mode": "dry_run",
  "timestamp": "2026-02-09T18:00:00Z",
  "checks": {
    "broker": "connected",
    "database": "connected"
  }
}
```

---

## Protected Endpoints (`/api/v1`)

### Engine Control

#### Engine Status

`GET /api/v1/status` - Current mode and running status.

#### Start Engine

`POST /api/v1/engine/start` - Resume automated trading.

#### Stop Engine

`POST /api/v1/engine/stop` - Pause automated trading.

### Strategies

#### List Strategies

`GET /api/v1/strategies` - Returns all registered strategies and their parameters.

#### Get Strategy

`GET /api/v1/strategies/{name}` - Detail of a specific strategy.

### Backtesting

#### Run Backtest

`POST /api/v1/backtests` - Submit a backtest request.
**Body:**

```json
{
  "strategy": "ma_crossover",
  "symbol": "AAPL",
  "start_date": "2023-01-01",
  "end_date": "2023-12-31",
  "initial_capital": 100000,
  "config": { "short_period": 12, "long_period": 26 }
}
```

#### Get Results

`GET /api/v1/backtests/{id}` - Retrieve metrics and trade history.

### Execution (Live/Paper Trading)

#### List Orders

`GET /api/v1/execution/orders` - List active orders. Supports query params: `symbol`, `side`, `status`, `limit`, `offset`.

#### Get Order

`GET /api/v1/execution/orders/{id}` - Details of a specific order.

#### Place Order

`POST /api/v1/execution/orders` - Place a manual Market or Limit order.
**Body:**

```json
{
  "symbol": "BTC/USD",
  "side": "buy",
  "type": "limit",
  "quantity": 0.5,
  "price": 42000.0
}
```

#### Cancel Order

`DELETE /api/v1/execution/orders/{id}` - Cancel a pending order.

#### Order History

`GET /api/v1/execution/history` - List filled or canceled orders.

#### Positions

`GET /api/v1/execution/positions` - Current portfolio holdings.

#### Balance

`GET /api/v1/execution/balance` - Account cash and equity.

### Portfolio & Management

#### Portfolio Summary

`GET /api/v1/portfolio/summary` - Aggregated view of balance, positions, and recent performance.

#### Runtime Metrics

`GET /api/v1/config/metrics` - Performance statistics (request counts, latencies).

#### Rotate API Key

`POST /api/v1/config/rotate-key` - Update the `API_KEY` for the session.

---

## Error Responses

Errors use a standardized format:

```json
{
  "error": "Short description of what went wrong",
  "code": "ERROR_CODE_IDENTIFIER" (optional)
}
```

### Common Status Codes

- `200` OK
- `201` Created
- `202` Accepted (Async task started)
- `400` Bad Request (Validation failed)
- `401` Unauthorized (Missing/Wrong API Key)
- `429` Too Many Requests (Rate limit hit)
- `500` Internal Server Error
