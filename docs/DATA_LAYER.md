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

| Provider | Asset Type | Status |
|----------|------------|--------|
| Yahoo Finance | Stocks | Stub (planned) |
| CCXT | Crypto | Stub (planned) |

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

provider := providers.NewYahooProvider("")
data, err := provider.GetHistoricalData("AAPL", startDate, endDate, "1d")
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
