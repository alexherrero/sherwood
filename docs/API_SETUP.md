# API Setup Guide

Configure and run the Sherwood REST API.

## Quick Start

```powershell
cd c:\Users\alex\AntiGravity\sherwood
go run ./backend/main.go
```

The API will be available at `http://localhost:8080`.

## Configuration

All configuration is done via environment variables or `.env` file:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | 8080 | API server port |
| `HOST` | 0.0.0.0 | API server host |
| `LOG_LEVEL` | info | Logging level (debug, info, warn, error) |
| `TRADING_MODE` | dry_run | Trading mode (dry_run or live) |

## Verify Installation

```powershell
# Health check
curl http://localhost:8080/health

# Get status
curl http://localhost:8080/api/v1/status

# List strategies
curl http://localhost:8080/api/v1/strategies
```

## CORS

CORS is enabled for all origins in development. For production, configure specific allowed origins.
