# Sherwood 📈

A modular, proof-of-concept automated trading engine and management dashboard. This project provides a foundation for executing algorithmic trades, performing regression testing against historical data, and managing bot configurations via a React-based web interface.

## Build and Test Status

[![Backend](https://github.com/alexherrero/sherwood/actions/workflows/backend.yml/badge.svg?branch=main)](https://github.com/alexherrero/sherwood/actions/workflows/backend.yml)

## 🚀 Overview

`Sherwood` is designed for those who want a simple, basic trading bot without the complexities of scripts or code. It covers all the basics, historical data and modeling for stocks and crypto, basic trading plans / rules, backtesting and **paper trading (dry run)** or **Live** functionality.

## ⚠️ In Development

Sherwood is experimental. It is not expected to work nor should you consider it reliable for any purpose. Code here is intended to demonstrate the potential of AI-assisted software development and should not be used for any real-world trading. Use at your own risk.

---

## 🛠 Tech Stack

* **Core Engine:** Go (Golang)
* **API Framework:** go-chi
* **Database:** SQLite (sqlx)
* **Dashboard:** React & TypeScript (planned)
* **Deployment:** Docker & Docker Compose

### Data Providers

| Provider | Asset Type | API Key Required |
|----------|------------|------------------|
| Yahoo Finance | Stocks, ETFs, Crypto | No |
| Tiingo | Stocks, ETFs | Yes (free at tiingo.com) |
| Binance | Crypto | Optional |
| Binance.US | Crypto (US users) | Optional |

---

## 📦 Getting Started

### 1. Prerequisites

* [Go 1.21+](https://go.dev/dl/) installed
* [Docker](https://www.docker.com/get-started) (optional, for containerized deployment)
* API credentials for your exchange provider

### 2. Configuration

Copy the example environment file and add your credentials:

```bash
cp .env.example .env
```

Edit `.env` with your settings:

```env
# General Configuration
PORT=8080
TRADING_MODE=dry_run  # Options: 'dry_run' or 'live'
DATABASE_PATH=./data/sherwood.db

# Robinhood Credentials (required for live trading)
RH_USERNAME=your_email@example.com
RH_PASSWORD=your_password
RH_MFA_CODE=your_device_mfa_secret

# Binance Configuration (for crypto)
BINANCE_USE_US=true  # Set to true for US users
BINANCE_API_KEY=your_binance_api_key
BINANCE_API_SECRET=your_binance_api_secret

# Tiingo API Key (get free at tiingo.com)
TIINGO_API_KEY=your_tiingo_api_key
```

### 3. Run the Application

```bash
# Build and run
go build -o sherwood ./backend/...
./sherwood

# Or run directly
go run ./backend/...
```

### 4. Run Tests

```bash
# Unit tests only
go test ./...

# Include integration tests (requires API keys)
go test ./... -tags=integration
```

---

## 📁 Project Structure

```
sherwood/
├── backend/
│   ├── main.go              # Application entry point
│   ├── api/                 # REST API handlers
│   ├── config/              # Configuration management
│   ├── data/                # Data layer
│   │   └── providers/       # Market data providers
│   ├── models/              # Domain models
│   ├── strategies/          # Trading strategies
│   └── execution/           # Trade execution (planned)
├── frontend/                # React dashboard (planned)
├── docs/                    # Documentation
├── .env.example             # Environment template
└── go.mod                   # Go module definition
```

---

## 📄 License

GNU General Public License v3.0 - see LICENSE file for details.

## ⚠️ Disclaimer

This is experimental software for educational purposes only.

* Not financial advice
* Not guaranteed to work or be profitable
* Trading involves substantial risk of loss
* Paper trade extensively before considering live trading
