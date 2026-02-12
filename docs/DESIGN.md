# Sherwood Design

## About Sherwood

Sherwood is a proof-of-concept automated trading engine and management dashboard. This project provides a foundation for executing algorithmic trades, performing regression testing against historical data, and managing bot configurations via a React-based web interface.

## Key Capabilities

- Backend engine that runs 24/7 for automated trading.
- Front-end web interface for managing trading plans, the trading engine, and visualizing performance.
- Download historical data for stocks and crypto from major exchanges.
- Validate models through rigorous backtesting.
- Deploy in "Dry Run" (Paper Trading) or Live environments.
- **Configuration** via environment variables for runtime flexibility and management via a web dashboard.
- Easily containerized using docker.

## Supported Strategies

The following strategies are planned for implementation:

### 1. Moving Average Crossover

- **Logic**: Buy when the short-term moving average crosses above the long-term moving average (Golden Cross); Sell when the short-term crosses below the long-term (Death Cross).
- **Goal**: Capture significant trends early.
- **Parameters**: Short Period (50), Long Period (200).

### 2. RSI (Relative Strength Index) Momentum

- **Logic**: Buy when the asset is oversold (RSI < 30) and sell when it is overbought (RSI > 70).
- **Goal**: Capitalize on mean reversion in ranging markets.
- **Parameters**: Period (default 14), Overbought Threshold (70), Oversold Threshold (30).

### 3. Bollinger Bands Mean Reversion

- **Logic**: Buy when price touches or breaks below the lower band; Sell when price touches or breaks above the upper band.
- **Goal**: Exploit price extremes assuming price will return to the mean.
- **Parameters**: Period (20), Standard Deviation Multiplier (2).

### 4. MACD (Moving Average Convergence Divergence) Trend Follower

- **Logic**: Buy when the MACD line crosses above the Signal line (bullish crossover); Sell when the MACD line crosses below the Signal line (bearish crossover).
- **Goal**: Identify and follow strong market trends.
- **Parameters**: Fast Period (12), Slow Period (26), Signal Period (9).

### 5. NYC Market Close/Open Strategy

- **Logic**: Buy Bitcoin at NYC market close (4 PM ET), sell one hour before market open (8:30 AM ET).
- **Goal**: Capitalize on overnight cryptocurrency volatility patterns.
- **Parameters**: Entry time, exit time, position size.

## Configuration

### Environment Variables

Sherwood uses environment variables for runtime configuration, making it Docker-friendly:

**Core Settings:**

- `SERVER_HOST` - Server bind address (default: "0.0.0.0")
- `SERVER_PORT` - Server port (default: 8099)
- `TRADING_MODE` - Trading mode: "dry_run", "paper", or "live"
- `LOG_LEVEL` - Logging level: "debug", "info", "warn", "error"
- `API_KEY` - API authentication key (required for security)
- `DATABASE_PATH` - SQLite database path

#### Providers and Strategies

- `DATA_PROVIDER` - Select data provider: "yahoo" (default), "tiingo", "binance"
- `ENABLED_STRATEGIES` - Comma-separated list of strategies to enable (default: "ma_crossover")
  - Available: `ma_crossover`, `rsi_momentum`, `bb_mean_reversion`, `macd_trend_follower`, `nyc_close_open`

**Provider API Keys:**

- `TIINGO_API_KEY` - Tiingo API key (required if using Tiingo provider)
- `BINANCE_API_KEY` - Binance API key (required if using Binance provider)
- `BINANCE_API_SECRET` - Binance API secret (required if using Binance provider)

**Example:**

```bash
# Use Yahoo provider with multiple strategies
DATA_PROVIDER=yahoo
ENABLED_STRATEGIES=ma_crossover,rsi_momentum,bb_mean_reversion
TRADING_MODE=dry_run
```

## API Endpoints

The backend exposes a RESTful API for frontend integration. All `/api/v1/*` endpoints require authentication via `X-Sherwood-API-Key` header.

### Health & Status

- `GET /health` - Health check (no auth required)
- `GET /api/v1/status` - Server status and mode

### Configuration Endpoints

- `GET /api/v1/config` - Current configuration (sanitized)
- `GET /api/v1/config/validation` - Configuration validation and details
  - Returns enabled strategies, provider status, warnings, and validation state
  - Useful for frontend settings pages

### Strategies

- `GET /api/v1/strategies` - List all available strategies
- `GET /api/v1/strategies/{name}` - Get strategy details

### Backtesting

- `POST /api/v1/backtests` - Run a backtest
- `GET /api/v1/backtests/{id}` - Get backtest results

### Execution

- `GET /api/v1/execution/orders` - List all orders (supports pagination/filtering)
- `POST /api/v1/execution/orders` - Place a manual order
- `GET /api/v1/execution/orders/{id}` - Get single order details
- `DELETE /api/v1/execution/orders/{id}` - Cancel an order
- `GET /api/v1/execution/history` - List closed/filled orders
- `GET /api/v1/execution/positions` - Get current positions
- `GET /api/v1/execution/balance` - Get account balance

### Portfolio & Metrics

- `GET /api/v1/portfolio/summary` - Unified portfolio view
- `GET /api/v1/config/metrics` - Runtime performance metrics

### Market Data

- `GET /api/v1/data/history` - Get historical market data
  - Query params: `symbol`, `start`, `end`, `interval`

### Engine Control

- `POST /api/v1/engine/start` - Start the trading engine
- `POST /api/v1/engine/stop` - Stop the trading engine

### Configuration & Security

- `GET /api/v1/config` - Current configuration (sanitized)
- `GET /api/v1/config/validation` - Detail configuration validation
- `POST /api/v1/config/rotate-key` - Rotate the API authentication key

### Notifications

- `GET /api/v1/notifications` - Get system notifications
  - Query params: `limit`, `offset`
- `PUT /api/v1/notifications/{id}/read` - Mark a notification as read
- `PUT /api/v1/notifications/read-all` - Mark all notifications as read

### Real-time

- `GET /ws` - WebSocket endpoint for real-time updates (requires Auth)

## Technology Stack

### Backend/Trading Engine

**Language**: Golang

**Data & APIs**:

- `piquette/finance-go` - Yahoo Finance data
- `adshao/go-binance/v2` - Binance exchange API
- Tiingo REST API - Stock/ETF data (more reliable than Yahoo)
- `net/http` - HTTP client for REST APIs
- `gorilla/websocket` - Real-time data streams (planned)

**Data Storage**:

- PostgreSQL or SQLite - Relational data
- TimescaleDB or InfluxDB - Time-series data
- Redis - Caching and job queues

### Frontend Dashboard

**Framework**: React 18+ with TypeScript

**Core Libraries**:

- `react-router-dom` - Routing
- `axios` - API client
- `recharts` - Data visualization
- `material-ui` - UI components
- `react-query` - Data fetching and caching
- `redux` - State management
- `react-hook-form` - Form handling
- `date-fns` - Date manipulation

**Development Tools**:

- `vite` - Build tool
- `eslint` - Linting
- `prettier` - Code formatting
- `jest` & `react-testing-library` - Testing

### DevOps & Infrastructure

- **Docker** - Containerization
- **docker-compose** - Local development
- **GitHub Actions** - CI/CD, Coverage & Automated Releases
- **Nginx** - Reverse proxy
- **Let's Encrypt** - SSL certificates

## Project Structure

Maintain this modular organization:

```text
sherwood/
├── backend/
│   ├── main.go              # Main application entry point
│   ├── strategies/          # Trading strategy implementations
│   │   ├── strategy.go      # Strategy interface definition
│   │   ├── ma_crossover.go  # Example: Moving average crossover
│   │   └── ...
│   ├── data/                # Data ingestion and storage
│   │   ├── providers/       # Data source integrations
│   │   ├── models.go        # Database models (structs)
│   │   └── cache.go         # Caching layer
│   ├── execution/           # Trade execution engine
│   │   ├── broker.go        # Broker API integration
│   │   ├── order_manager.go # Order management
│   │   └── risk.go          # Risk management
│   ├── analysis/            # Analytical tools and performance metrics
│   │   ├── metrics.go       # Core metric calculations
│   │   └── ...
│   ├── backtesting/         # Backtesting framework
│   │   ├── engine.go        # Backtest engine
│   │   ├── metrics.go       # Backtest-specific metrics
│   │   └── reports.go       # Report generation
│   ├── api/                 # REST API
│   │   ├── handlers_*.go    # HTTP Handlers (orders, backtesting, etc.)
│   │   ├── middleware_*.go  # Auth, Audit, Trace, Rate Limiting, CORS
│   │   └── validation.go    # Input validation logic
│   ├── tracing/             # Structured logging with trace IDs
│   │   └── tracing.go       # Trace ID generation and context propagation
│   ├── config/              # Configuration management
│   ├── utils/               # Utility functions
│   └── models/              # Shared domain models
│
├── frontend/
│   ├── src/
│   │   ├── components/      # React components
│   │   ├── pages/           # Page components
│   │   ├── services/        # API service layer
│   │   ├── hooks/           # Custom React hooks
│   │   ├── utils/           # Utility functions
│   │   ├── types/           # TypeScript types
│   │   └── styles/          # CSS/styling
│   ├── public/              # Static assets
│   ├── package.json         # Node dependencies
│   └── tsconfig.json        # TypeScript config
│
├── deployments/             # Deployment configurations
│   ├── docker/              # Dockerfiles
│   └── k8s/                 # Kubernetes manifests (optional)
├── docs/                    # Documentation
├── wiki/                    # GitHub Wiki content (Source of Truth for Features)
├── scripts/                 # Utility scripts
├── .github/                 # GitHub Actions workflows
├── .env.example             # Environment variables template
├── docker-compose.yml       # Docker Compose config
├── go.mod                   # Go module definition
├── go.sum                   # Go module checksums
├── Makefile                 # Build and control targets
└── README.md                # Project documentation
```

## Development Guidelines

**Implementation Checklist**:

- [ ] Inherit from BaseStrategy
- [ ] Implement all abstract methods
- [ ] Add parameter validation
- [ ] Include docstrings with parameter descriptions
- [ ] Handle edge cases (missing data, NaN values)
- [ ] Add logging for signal generation
- [ ] Write unit tests with sample data

## Common Tasks & Solutions

## Agent Behavior & Constraints

### What You MUST Do

✅ **Execute autonomously**: Complete tasks without asking unnecessary questions
✅ **Write complete code**: Provide full, working implementations
✅ **Include error handling**: Always check errors explicitly (no `_` ignores) and handle failures gracefully
✅ **Add logging**: Include appropriate logging statements
✅ **Create tests**: Write unit tests for new functionality
✅ **Update documentation**: Keep docs in sync with code changes
✅ **Follow patterns**: Use existing code style and architecture
✅ **Validate inputs**: Check parameters before use
✅ **Handle edge cases**: Consider boundary conditions
✅ **Be explicit about safety**: Always emphasize experimental nature for trading code

### What You MUST NOT Do

❌ **Generate investment advice**: This is a software development tool, not financial advice
❌ **Guarantee returns**: Never claim or imply trading strategies will be profitable
❌ **Encourage live trading**: Always recommend paper trading first
❌ **Hardcode secrets**: Never put credentials in code
❌ **Skip testing**: Don't deploy untested code
❌ **Ignore warnings**: Trading bugs can be very costly
❌ **Make assumptions**: Clarify ambiguous requirements
❌ **Break modularity**: Keep code organized and maintainable
❌ **Leave TODOs**: Complete implementations or note explicitly what's incomplete

### When to Ask for Clarification

Ask questions only when:

- Requirements are genuinely ambiguous
- Multiple valid approaches exist with different tradeoffs
- User needs to make a strategic decision
- Missing critical information (API keys, specific data sources)

**Don't ask when**:

- You can make reasonable assumptions
- Best practices are clear
- The task has a standard solution

### Response Style

**Be Direct**: Start with action, not preamble

```text
Good: "Creating RSI strategy with 14-period default..."
Bad: "I'd be happy to help you create an RSI strategy! First, let me explain what RSI is..."
```

**Be Comprehensive**: Include everything needed

```text
Good: [Full code + tests + docs + usage example]
Bad: [Code snippet only, "you can add tests later"]
```

**Be Practical**: Focus on working solutions

```text
Good: "Using yfinance for data - it's free and reliable"
Bad: "You could use Bloomberg API, Reuters, or..."
```

### Trading-Specific Safety Protocol

Every time you work on trading execution code, include this checklist:

```markdown
## Trading Safety Checklist
- [ ] Paper trading mode clearly marked
- [ ] Position size limits enforced
- [ ] Stop losses implemented
- [ ] Daily loss limits checked
- [ ] Risk per trade ≤ 2%
- [ ] Confirmation required for live mode
- [ ] All credentials in environment variables
- [ ] Extensive logging on order execution
- [ ] Alert system for anomalies
- [ ] Manual kill switch available
```

## Financial Disclaimer Template

When discussing trading strategies or implementation, include:

```text
⚠️ DISCLAIMER: This is experimental software for educational purposes only.
- Not financial advice
- Not guaranteed to work or be profitable  
- Past performance ≠ future results
- Trading involves substantial risk of loss
- Paper trade extensively before considering live trading
- Consult qualified financial professionals for investment decisions
```
