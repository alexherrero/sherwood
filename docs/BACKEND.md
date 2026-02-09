# Backend Architecture

Sherwood's backend is built with Go, providing a high-performance trading engine with REST API endpoints.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                     REST API (Chi Router)                    │
├─────────────────────────────────────────────────────────────┤
│  Handlers: Strategies | Backtests | Config | Health         │
├──────────┬──────────────┬─────────────┬────────────────────┤
│ Strategies│  Backtesting │  Execution  │    Data Layer     │
│ Framework │    Engine    │   Engine    │                    │
├──────────┴──────────────┴─────────────┼────────────────────┤
│              Shared Domain Models      │    Providers       │
├────────────────────────────────────────┴────────────────────┤
│                   Configuration (config)                     │
├─────────────────────────────────────────────────────────────┤
│                      SQLite Database                         │
└─────────────────────────────────────────────────────────────┘
```

## Package Structure

| Package | Description |
|---------|-------------|
| `main.go` | Application entry point |
| `config/` | Configuration loading and validation |
| `models/` | Shared domain models (OHLCV, Order, Position, Signal) |
| `api/` | REST API router, handlers, and middleware |
| `strategies/` | Trading strategy interface and implementations |
| `data/` | Data providers and caching layer |
| `execution/` | Trade execution and order management |
| `backtesting/` | Backtest engine and metrics |

## Core Models

- **OHLCV**: Candlestick price data (Open, High, Low, Close, Volume)
- **Order**: Trading order with lifecycle tracking
- **Position**: Current portfolio holdings
- **Signal**: Trading signals from strategies

## API Endpoints

See [API.md](./API.md) for complete endpoint documentation.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check |
| GET | `/api/v1/strategies` | List strategies |
| GET | `/api/v1/strategies/{name}` | Get strategy details |
| POST | `/api/v1/backtests` | Run backtest |
| GET | `/api/v1/backtests/{id}` | Get backtest results |
| GET | `/api/v1/config` | Get configuration |
| GET | `/api/v1/status` | Get engine status |

## Trading Modes

- **dry_run**: Paper trading (default) - simulated trades with no real money
- **live**: Live trading - real money at risk

> ⚠️ **WARNING**: Live trading involves substantial risk. Always test extensively in dry_run mode first.
