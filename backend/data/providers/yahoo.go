// Package providers contains data provider implementations.
package providers

import (
	"fmt"
	"time"

	"github.com/piquette/finance-go/chart"
	"github.com/piquette/finance-go/datetime"
	"github.com/piquette/finance-go/quote"

	"github.com/alexherrero/sherwood/backend/models"
)

// YahooProvider fetches market data from Yahoo Finance.
// Uses the unofficial Yahoo Finance API via piquette/finance-go library.
type YahooProvider struct {
	// rateLimiter controls request rate to avoid API throttling.
	lastRequest time.Time
	minInterval time.Duration
}

// NewYahooProvider creates a new YahooProvider instance.
//
// Returns:
//   - *YahooProvider: The provider instance
func NewYahooProvider() *YahooProvider {
	return &YahooProvider{
		lastRequest: time.Time{},
		minInterval: 200 * time.Millisecond, // ~5 requests/second max
	}
}

// Name returns the provider name.
func (p *YahooProvider) Name() string {
	return "yahoo"
}

// rateLimit ensures we don't exceed API rate limits.
func (p *YahooProvider) rateLimit() {
	if !p.lastRequest.IsZero() {
		elapsed := time.Since(p.lastRequest)
		if elapsed < p.minInterval {
			time.Sleep(p.minInterval - elapsed)
		}
	}
	p.lastRequest = time.Now()
}

// mapInterval converts standard interval strings to finance-go datetime.Interval.
//
// Args:
//   - interval: Standard interval string (e.g., "1d", "1h", "5m")
//
// Returns:
//   - datetime.Interval: The mapped interval
//   - error: If the interval is not supported
func mapInterval(interval string) (datetime.Interval, error) {
	switch interval {
	case "1m":
		return datetime.OneMin, nil
	case "2m":
		return datetime.TwoMins, nil
	case "5m":
		return datetime.FiveMins, nil
	case "15m":
		return datetime.FifteenMins, nil
	case "30m":
		return datetime.ThirtyMins, nil
	case "1h":
		return datetime.OneHour, nil
	case "1d":
		return datetime.OneDay, nil
	case "5d":
		return datetime.FiveDay, nil
	case "1wk":
		return datetime.OneMonth, nil // Approximation
	case "1mo":
		return datetime.OneMonth, nil
	case "3mo":
		return datetime.ThreeMonth, nil
	default:
		return datetime.OneDay, fmt.Errorf("unsupported interval: %s", interval)
	}
}

// GetHistoricalData fetches OHLCV data from Yahoo Finance.
//
// Args:
//   - symbol: Ticker symbol (e.g., "AAPL", "BTC-USD")
//   - start: Start date
//   - end: End date
//   - interval: Time interval (e.g., "1d", "1h", "5m")
//
// Returns:
//   - []models.OHLCV: Historical data
//   - error: Any error encountered
func (p *YahooProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	p.rateLimit()

	mappedInterval, err := mapInterval(interval)
	if err != nil {
		return nil, fmt.Errorf("failed to map interval: %w", err)
	}

	params := &chart.Params{
		Symbol:   symbol,
		Interval: mappedInterval,
		Start:    datetime.New(&start),
		End:      datetime.New(&end),
	}

	iter := chart.Get(params)
	if iter.Err() != nil {
		return nil, fmt.Errorf("failed to fetch chart data for %s: %w", symbol, iter.Err())
	}

	var ohlcvData []models.OHLCV
	for iter.Next() {
		bar := iter.Bar()
		if bar == nil {
			continue
		}

		ohlcv := models.OHLCV{
			Timestamp: time.Unix(int64(bar.Timestamp), 0),
			Symbol:    symbol,
			Open:      bar.Open.InexactFloat64(),
			High:      bar.High.InexactFloat64(),
			Low:       bar.Low.InexactFloat64(),
			Close:     bar.Close.InexactFloat64(),
			Volume:    float64(bar.Volume),
		}
		ohlcvData = append(ohlcvData, ohlcv)
	}

	if iter.Err() != nil {
		return nil, fmt.Errorf("error iterating chart data for %s: %w", symbol, iter.Err())
	}

	if len(ohlcvData) == 0 {
		return nil, fmt.Errorf("no data returned for symbol %s", symbol)
	}

	return ohlcvData, nil
}

// GetLatestPrice fetches the current price from Yahoo Finance.
//
// Args:
//   - symbol: Ticker symbol
//
// Returns:
//   - float64: Current price
//   - error: Any error encountered
func (p *YahooProvider) GetLatestPrice(symbol string) (float64, error) {
	p.rateLimit()

	q, err := quote.Get(symbol)
	if err != nil {
		return 0.0, fmt.Errorf("failed to fetch quote for %s: %w", symbol, err)
	}

	if q == nil {
		return 0.0, fmt.Errorf("no quote data returned for %s", symbol)
	}

	return q.RegularMarketPrice, nil
}

// GetTicker fetches ticker information from Yahoo Finance.
//
// Args:
//   - symbol: Ticker symbol
//
// Returns:
//   - *models.Ticker: Ticker information
//   - error: Any error encountered
func (p *YahooProvider) GetTicker(symbol string) (*models.Ticker, error) {
	p.rateLimit()

	q, err := quote.Get(symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch quote for %s: %w", symbol, err)
	}

	if q == nil {
		return nil, fmt.Errorf("no quote data returned for %s", symbol)
	}

	// Determine asset type based on quote type
	assetType := "stock"
	if q.QuoteType == "CRYPTOCURRENCY" {
		assetType = "crypto"
	} else if q.QuoteType == "CURRENCY" {
		assetType = "forex"
	} else if q.QuoteType == "ETF" {
		assetType = "etf"
	}

	return &models.Ticker{
		Symbol:    symbol,
		Name:      q.ShortName,
		AssetType: assetType,
		Exchange:  q.FullExchangeName,
	}, nil
}
