# Backend Setup Guide

This guide covers setting up, running, and verifying the Sherwood trading engine backend and API.

## Prerequisites

- **Go 1.21+**: [Download Go](https://go.dev/dl/)

## Installation

```powershell
# Clone the repository
git clone https://github.com/alexherrero/sherwood.git
cd sherwood

# Install Go dependencies
go mod download
```

## Configuration

Create a `.env` file in the project root:

```env
# Server settings
PORT=8099
HOST=0.0.0.0
API_KEY=your-secret-key # Protects API access

# Trading mode: 'dry_run', 'paper', or 'live'
TRADING_MODE=dry_run

# Database
DATABASE_PATH=./data/sherwood.db

# Logging: debug, info, warn, error
LOG_LEVEL=info

# Data Provider API Keys (optional, tests will use mocks if missing)
BINANCE_API_KEY=your_binance_key
BINANCE_API_SECRET=your_binance_secret
TIINGO_API_KEY=your_tiingo_key
```

> ⚠️ **Never commit `.env` to version control!**

## Running the Backend

```powershell
# From project root
go run ./backend/main.go
```

Expected output:

```text
Starting Sherwood Trading Engine...
📝 Paper trading mode (dry run)
🚀 API server listening on 0.0.0.0:8099
```

## Verifying the Installation

You can verify the backend is running by checking the API endpoints.

```powershell
# Health check (Public)
curl http://localhost:8099/health
# Returns: {"status":"ok"}

# Get engine status (Requires API Key)
curl -H "X-Sherwood-API-Key: your-secret-key" http://localhost:8099/api/v1/status
# Returns: {"mode":"dry_run","status":"running"}

# List available strategies (Requires API Key)
curl -H "X-Sherwood-API-Key: your-secret-key" http://localhost:8099/api/v1/strategies
```

## Running Tests

```powershell
# Run all backend tests
go test ./backend/... -v

# Run with coverage
go test ./backend/... -cover
```

## Building for Production

```powershell
# Build the binary
go build -o sherwood.exe ./backend/main.go

# Run the binary
./sherwood.exe
```

## Troubleshooting

### Port Already in Use

Change the port in `.env` to an other unused port (example below):

```env
PORT=9000
```
