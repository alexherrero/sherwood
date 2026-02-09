// Package data provides data storage and provider interfaces for market data.
package data

import (
	"time"

	"github.com/alexherrero/sherwood/backend/models"
)

// DataProvider defines the interface for market data sources.
// Implementations fetch historical and real-time price data.
type DataProvider interface {
	// Name returns the provider name (e.g., "yahoo", "ccxt").
	Name() string

	// GetHistoricalData fetches OHLCV data for a symbol within a date range.
	//
	// Args:
	//   - symbol: Ticker symbol (e.g., "AAPL", "BTC-USD")
	//   - start: Start of the date range
	//   - end: End of the date range
	//   - interval: Time interval (e.g., "1d", "1h", "5m")
	//
	// Returns:
	//   - []models.OHLCV: Historical price data
	//   - error: Any error encountered
	GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error)

	// GetLatestPrice fetches the current price for a symbol.
	//
	// Args:
	//   - symbol: Ticker symbol
	//
	// Returns:
	//   - float64: Current price
	//   - error: Any error encountered
	GetLatestPrice(symbol string) (float64, error)

	// GetTicker fetches ticker information for a symbol.
	//
	// Args:
	//   - symbol: Ticker symbol
	//
	// Returns:
	//   - *models.Ticker: Ticker information
	//   - error: Any error encountered
	GetTicker(symbol string) (*models.Ticker, error)
}

// DataCallback is a function type for real-time data updates.
type DataCallback func(data models.OHLCV)

// StreamingProvider extends DataProvider with real-time streaming.
type StreamingProvider interface {
	DataProvider

	// Subscribe starts receiving real-time updates for a symbol.
	//
	// Args:
	//   - symbol: Ticker symbol to subscribe to
	//   - callback: Function called when new data arrives
	//
	// Returns:
	//   - error: Any error encountered during subscription
	Subscribe(symbol string, callback DataCallback) error

	// Unsubscribe stops receiving updates for a symbol.
	//
	// Args:
	//   - symbol: Ticker symbol to unsubscribe from
	//
	// Returns:
	//   - error: Any error encountered
	Unsubscribe(symbol string) error
}
