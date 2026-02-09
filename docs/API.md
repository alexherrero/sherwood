# REST API Documentation

Sherwood's REST API provides endpoints for managing strategies, running backtests, and monitoring the trading engine.

## Base URL

```
http://localhost:8099
```

## Authentication

Currently no authentication is required for development. Production deployments should implement API key or JWT authentication.

---

## Endpoints

### Health Check

Check if the API is running.

```
GET /health
```

**Response:**

```json
{
  "status": "ok"
}
```

---

### Engine Status

Get the current trading engine status.

```
GET /api/v1/status
```

**Response:**

```json
{
  "mode": "dry_run",
  "status": "running"
}
```

---

### Strategies

#### List Strategies

```
GET /api/v1/strategies
```

**Response:**

```json
{
  "strategies": [
    {
      "name": "ma_crossover",
      "description": "Moving Average Crossover Strategy",
      "parameters": {
        "short_period": 10,
        "long_period": 20
      }
    }
  ]
}
```

#### Get Strategy Details

```
GET /api/v1/strategies/{name}
```

**Response:**

```json
{
  "name": "ma_crossover",
  "description": "Moving Average Crossover Strategy",
  "parameters": {
    "short_period": {
      "type": "int",
      "default": 10,
      "min": 2,
      "max": 50
    },
    "long_period": {
      "type": "int",
      "default": 20,
      "min": 5,
      "max": 200
    }
  }
}
```

---

### Backtests

#### Run Backtest

```
POST /api/v1/backtests
```

**Request Body:**

```json
{
  "strategy": "ma_crossover",
  "symbol": "AAPL",
  "start_date": "2023-01-01",
  "end_date": "2023-12-31",
  "initial_capital": 100000,
  "config": {
    "short_period": 12,
    "long_period": 26
  }
}
```

**Response:**

```json
{
  "id": "backtest-001",
  "status": "pending",
  "message": "Backtest queued for execution"
}
```

#### Get Backtest Results

```
GET /api/v1/backtests/{id}
```

**Response:**

```json
{
  "id": "backtest-001",
  "status": "completed",
  "metrics": {
    "total_return": 12.5,
    "sharpe_ratio": 1.23,
    "max_drawdown": 8.5,
    "total_trades": 15,
    "winning_trades": 10,
    "win_rate": 66.67
  }
}
```

---

### Configuration

```
GET /api/v1/config
```

**Response:**

```json
{
  "version": "0.1.0",
  "api": "v1"
}
```

---

## Error Responses

All errors return a JSON object with an error message:

```json
{
  "error": "Description of the error"
}
```

**HTTP Status Codes:**

- `200` - Success
- `201` - Created
- `202` - Accepted (async operation started)
- `400` - Bad Request
- `404` - Not Found
- `500` - Internal Server Error
