# Sherwood 📈

A simple, self-hosted trading platform that's easy to use and doesn't require coding experience. Sherwood covers everything from historical data analysis and strategy backtesting to paper trading and live execution — all managed through a clean API with a web dashboard on the way.

## Build and Test Status

[![Backend](https://github.com/alexherrero/sherwood/actions/workflows/backend.yml/badge.svg)](https://github.com/alexherrero/sherwood/actions/workflows/backend.yml)

## ⚠️ In Development

Sherwood is a proof of concept under active development. Features may be incomplete, unstable, or change without notice. It is currently intended for paper trading and simulations only — not for production use with real funds. See the [Disclaimer](#%EF%B8%8F-disclaimer) for more details.

---

## ✨ Notable Features

- **Multiple Trading Strategies** — 5 built-in strategies (MA Crossover, RSI Momentum, Bollinger Band Mean Reversion, MACD Trend Follower, NYC Close-Open) with runtime configuration
- **Backtesting Engine** — Test strategies against historical data before risking capital
- **Multiple Data Providers** — Yahoo Finance, Tiingo, Binance, and Binance.US with automatic fallback
- **Paper & Live Trading** — Dry-run, paper, or live modes with a single config change
- **Full REST API** — Manage strategies, orders, backtests, portfolio performance, and notifications via API
- **Persistent Order Storage** — SQLite-backed order state that survives restarts
- **Notification System** — In-app alerts with WebSocket broadcasting for real-time updates
- **Configuration Hot-Reload** — Update log levels, credentials, and settings without downtime
- **Graceful Shutdown** — Safe engine shutdown with order cancellation, position closure, and state checkpointing
- **Structured Logging & Tracing** — Correlation trace IDs across every request, engine tick, and trade execution
- **Input Validation & Security** — Request body limits, API key authentication, and audit logging
- **Automated Releases** — Weekly cross-platform builds via GitHub Actions

### 🗺️ What's Next

See the **[Roadmap](https://github.com/alexherrero/sherwood/wiki/Roadmap)** for what's planned — including concurrent testing, strategy hot-swapping, Docker deployment, a React dashboard, and AI-powered strategy creation.

---

## 🛠 Tech Stack

- **Core Engine:** Go (Golang)
- **API Framework:** go-chi
- **Database:** SQLite (sqlx)
- **Dashboard:** React & TypeScript (planned)
- **Deployment:** Docker & Docker Compose (planned)

### Data Providers

| Provider | Asset Type | API Key Required |
| -------- | ---------- | ---------------- |
| Yahoo Finance | Stocks, ETFs, Crypto | No |
| Tiingo | Stocks, ETFs | Yes (free at tiingo.com) |
| Binance | Crypto | Optional |
| Binance.US | Crypto (US users) | Optional |

---

## 📦 Getting Started

### 1. Prerequisites

- [Go 1.21+](https://go.dev/dl/) installed
- [Docker](https://www.docker.com/get-started) (optional, for containerized deployment)
- API credentials for your exchange provider

### 2. Configuration

Copy the example environment file and add your credentials:

```bash
cp .env.example .env
```

Edit `.env` with your settings:

```env
# General Configuration
PORT=8099
TRADING_MODE=dry_run  # Options: 'dry_run', 'paper', or 'live'
DATABASE_PATH=./data/sherwood.db
API_KEY=your-secret-key-here # Protects API access

# Robinhood credentials (Optional - Planned for future live trading)
# RH_USERNAME=your_email@example.com
# RH_PASSWORD=your_password
# RH_MFA_CODE=your_mfa_secret

# Data Provider API Keys (optional, tests will use mocks if missing)
BINANCE_API_KEY=your_binance_api_key
BINANCE_API_SECRET=your_binance_api_secret
TIINGO_API_KEY=your_tiingo_api_key
```

### 3. Run the Application

```bash
# Build and run
go build -o sherwood ./backend/main.go
./sherwood

# Or run directly
go run ./backend/main.go
```

### 4. Run Tests

```bash
# Run all tests
go test ./... -v
```

---

## 📁 Project Structure

```plaintext
sherwood/
├── backend/
│   ├── main.go              # Application entry point
│   ├── api/                 # REST API handlers and middleware
│   ├── config/              # Configuration management
│   ├── data/                # Data layer and providers
│   ├── models/              # Shared domain models
│   ├── strategies/          # Trading strategies
│   ├── execution/           # Trade execution and order management
│   ├── engine/              # Core trading engine
│   ├── notifications/       # Notification system
│   ├── tracing/             # Request tracing and correlation
│   └── backtesting/         # Backtesting framework
├── wiki/                    # Project wiki (auto-published)
├── docs/                    # Documentation
├── .env.example             # Environment template
└── go.mod                   # Go module definition
```

---

## 📄 License

GNU General Public License v3.0 - see LICENSE file for details.

## ⚠️ Disclaimer

Sherwood is provided **as-is** for educational and simulation purposes only.

- **Not a financial company.** Sherwood is an open-source project. We do not provide financial advice, financial services, or investment recommendations of any kind.
- **Paper trading and simulations only.** This platform is not intended for use beyond paper trading and backtesting simulations. Any use in live trading environments is entirely at the user's own discretion and risk.
- **No guarantees.** We make no guarantees regarding the functionality, reliability, accuracy, or performance of this software. It may contain bugs, produce incorrect results, or fail without warning.
- **Use at your own risk.** By using Sherwood, you accept full responsibility for any outcomes. The authors and contributors are not liable for any losses, damages, or consequences arising from the use of this software.
- **Trading involves risk.** Trading financial instruments carries substantial risk of loss. Past performance — real or simulated — is not indicative of future results.
