# Data Layer

The data layer provides market data access, storage, and caching for the Sherwood trading engine.

## Components

### Data Providers

Providers fetch market data from external sources. All providers implement the `DataProvider` interface:

```go
type DataProvider interface {
    Name() string
    GetHistoricalData(symbol string, start, end time.Time, interval string) ([]OHLCV, error)
    GetLatestPrice(symbol string) (float64, error)
    GetTicker(symbol string) (*Ticker, error)
}
```

#### Available Providers

| Provider | Asset Type | Status | Notes |
|----------|------------|--------|-------|
| Yahoo Finance | Stocks, ETFs, Crypto | ✅ Implemented | Uses `piquette/finance-go` (v1.1.0) |
| Tiingo | Stocks, ETFs | ✅ Implemented | Reliable backtest data. Requires API key |
| Binance | Crypto | ✅ Implemented | Global and US support via `adshao/go-binance` |

### Database (SQLite)

The database stores:

- **OHLCV data**: Historical price data
- **Tickers**: Symbol metadata
- **Orders**: Order history
- **Trades**: Executed trades
- **Positions**: Current holdings

### Caching

The caching layer reduces API calls and improves performance:

- **MemoryCache**: In-memory cache for development
- **CachedDataProvider**: Wraps any provider with caching

## Usage

### Fetching Historical Data

```go
import "github.com/alexherrero/sherwood/backend/data/providers"

// Yahoo Finance (no API key required)
yahoo := providers.NewYahooProvider()
data, err := yahoo.GetHistoricalData("AAPL", startDate, endDate, "1d")

// Tiingo (requires free API key from tiingo.com)
tiingo := providers.NewTiingoProvider(os.Getenv("TIINGO_API_KEY"))
data, err := tiingo.GetHistoricalData("AAPL", startDate, endDate, "1d")

// Binance (for crypto)
binance := providers.NewBinanceUSProvider("", "") // US users
data, err := binance.GetHistoricalData("BTC/USD", startDate, endDate, "1h")
```

### Using the Database

```go
import "github.com/alexherrero/sherwood/backend/data"

db, err := data.NewDB("./data/sherwood.db")
if err != nil {
    log.Fatal(err)
}

// Store data
err = db.SaveOHLCV(ohlcvData)

// Retrieve data
data, err := db.GetOHLCV("AAPL", startDate, endDate)
```

### Caching Data

```go
import "github.com/alexherrero/sherwood/backend/data"

cache := data.NewMemoryCache()
cachedProvider := data.NewCachedDataProvider(provider, cache, 5*time.Minute)

// Uses cache for repeated calls
price, err := cachedProvider.GetLatestPrice("AAPL")
```

## Data Flow

```
┌──────────────┐     ┌────────────────┐     ┌───────────────┐
│   Strategy   │ ──▶ │ CachedProvider │ ──▶ │ DataProvider  │
└──────────────┘     └────────────────┘     └───────────────┘
                            │                      │
                            ▼                      ▼
                     ┌────────────┐          ┌─────────────┐
                     │   Cache    │          │ External API│
                     └────────────┘          └─────────────┘
                                                   │
                                                   ▼
                                            ┌────────────┐
                                            │  Database  │
                                            └────────────┘
```
