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
| GET | `/health` | Health and subsystem status |
| GET | `/api/v1/status` | Engine mode and status |
| GET | `/api/v1/strategies` | List all trading strategies |
| POST | `/api/v1/backtests` | Execute strategy backtest |
| GET | `/api/v1/execution/orders` | List and filter active orders |
| POST | `/api/v1/execution/orders` | Place manual Market/Limit order |
| GET | `/api/v1/execution/balance` | Real-time account balance |
| GET | `/api/v1/portfolio/summary` | Portfolio performance overview |

## Trading Modes

- **dry_run**: Simulated trading with basic data (default)
- **paper**: Realistic paper trading against exchange data
- **live**: Real money execution on supported exchanges

> ⚠️ **WARNING**: Live trading involves substantial risk. Always test extensively in dry_run mode first.
