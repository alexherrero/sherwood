# Backend Setup Guide

This guide covers setting up and running the Sherwood trading engine backend.

## Prerequisites

- **Go 1.21+**: [Download Go](https://go.dev/dl/)
- **GCC** (for SQLite): Required for CGO-enabled sqlite3 driver

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
PORT=8080
HOST=0.0.0.0

# Trading mode: 'dry_run' (paper) or 'live'
TRADING_MODE=dry_run

# Database
DATABASE_PATH=./data/sherwood.db

# Logging: debug, info, warn, error
LOG_LEVEL=info

# Robinhood credentials (optional)
RH_USERNAME=your_email@example.com
RH_PASSWORD=your_password
RH_MFA_CODE=your_mfa_secret
```

> ⚠️ **Never commit `.env` to version control!**

## Running the Backend

```powershell
# From project root
go run ./backend/main.go
```

Expected output:

```
Starting Sherwood Trading Engine...
📝 Paper trading mode (dry run)
🚀 API server listening on 0.0.0.0:8080
```

## Verifying the Installation

```powershell
# Health check
curl http://localhost:8080/health
# Returns: {"status":"ok"}

# Get engine status
curl http://localhost:8080/api/v1/status
# Returns: {"mode":"dry_run","status":"running"}
```

## Building for Production

```powershell
# Build the binary
go build -o sherwood.exe ./backend/main.go

# Run the binary
./sherwood.exe
```

## Running Tests

```powershell
# Run all backend tests
go test ./backend/... -v

# Run with coverage
go test ./backend/... -cover
```

## Troubleshooting

### CGO/SQLite Issues

If you see errors about CGO or sqlite3:

1. Install GCC via [MSYS2](https://www.msys2.org/) or [TDM-GCC](https://jmeubank.github.io/tdm-gcc/)
2. Ensure GCC is in your PATH
3. Set `CGO_ENABLED=1` if needed

### Port Already in Use

Change the port in `.env`:

```env
PORT=9000
```
