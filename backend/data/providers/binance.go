// Package providers contains data provider implementations.
package providers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	binance "github.com/adshao/go-binance/v2"

	"github.com/alexherrero/sherwood/backend/models"
)

// BinanceAPI defines the interface for Binance API calls.
type BinanceAPI interface {
	GetKlines(symbol, interval string, start, end int64, limit int) ([]*binance.Kline, error)
	GetPrices(symbol string) ([]*binance.SymbolPrice, error)
	GetExchangeInfo(symbol string) (*binance.ExchangeInfo, error)
}

// defaultBinanceAPI implements BinanceAPI using the official library.
type defaultBinanceAPI struct {
	client *binance.Client
}

func (api *defaultBinanceAPI) GetKlines(symbol, interval string, start, end int64, limit int) ([]*binance.Kline, error) {
	service := api.client.NewKlinesService().
		Symbol(symbol).
		Interval(interval).
		Limit(limit)

	if start > 0 {
		service = service.StartTime(start)
	}
	if end > 0 {
		service = service.EndTime(end)
	}

	return service.Do(context.Background())
}

func (api *defaultBinanceAPI) GetPrices(symbol string) ([]*binance.SymbolPrice, error) {
	return api.client.NewListPricesService().
		Symbol(symbol).
		Do(context.Background())
}

func (api *defaultBinanceAPI) GetExchangeInfo(symbol string) (*binance.ExchangeInfo, error) {
	return api.client.NewExchangeInfoService().
		Symbol(symbol).
		Do(context.Background())
}

// BinanceProvider fetches cryptocurrency data from Binance exchange.
// Uses the official Binance API via adshao/go-binance library.
// Supports both Binance.com (international) and Binance.US (for US users).
type BinanceProvider struct {
	api         BinanceAPI
	rateLimiter time.Time
	minInterval time.Duration
	useUS       bool
}

// NewBinanceProvider creates a new BinanceProvider instance for Binance.com.
//
// Args:
//   - apiKey: Binance API key (optional for public endpoints)
//   - apiSecret: Binance API secret (optional for public endpoints)
//
// Returns:
//   - *BinanceProvider: The provider instance
func NewBinanceProvider(apiKey, apiSecret string) *BinanceProvider {
	client := binance.NewClient(apiKey, apiSecret)
	return &BinanceProvider{
		api:         &defaultBinanceAPI{client: client},
		rateLimiter: time.Time{},
		minInterval: 100 * time.Millisecond, // ~10 requests/second max
		useUS:       false,
	}
}

// NewBinanceUSProvider creates a new BinanceProvider for Binance.US (for US users).
// Binance.US is required for users in the United States due to geo-restrictions.
//
// Args:
//   - apiKey: Binance.US API key (optional for public endpoints)
//   - apiSecret: Binance.US API secret (optional for public endpoints)
//
// Returns:
//   - *BinanceProvider: The provider instance configured for Binance.US
func NewBinanceUSProvider(apiKey, apiSecret string) *BinanceProvider {
	client := binance.NewClient(apiKey, apiSecret)
	client.BaseURL = "https://api.binance.us"
	return &BinanceProvider{
		api:         &defaultBinanceAPI{client: client},
		rateLimiter: time.Time{},
		minInterval: 100 * time.Millisecond,
		useUS:       true,
	}
}

// Name returns the provider name.
func (p *BinanceProvider) Name() string {
	return "binance"
}

// rateLimit ensures we don't exceed API rate limits.
func (p *BinanceProvider) rateLimit() {
	if !p.rateLimiter.IsZero() {
		elapsed := time.Since(p.rateLimiter)
		if elapsed < p.minInterval {
			time.Sleep(p.minInterval - elapsed)
		}
	}
	p.rateLimiter = time.Now()
}

// convertSymbol converts standard trading pair format to Binance format.
// e.g., "BTC/USD" -> "BTCUSDT", "ETH/BTC" -> "ETHBTC"
//
// Args:
//   - symbol: Standard trading pair (e.g., "BTC/USD", "ETH/USDT")
//
// Returns:
//   - string: Binance-compatible symbol
func convertSymbol(symbol string) string {
	// Uppercase first to handle lowercase input
	symbol = strings.ToUpper(symbol)
	// Remove slash
	symbol = strings.ReplaceAll(symbol, "/", "")
	// Convert USD to USDT for Binance (but avoid USDTT)
	if strings.HasSuffix(symbol, "USD") && !strings.HasSuffix(symbol, "USDT") {
		symbol = symbol + "T"
	}
	return symbol
}

// mapBinanceInterval converts standard interval strings to Binance interval format.
//
// Args:
//   - interval: Standard interval string (e.g., "1d", "1h", "5m")
//
// Returns:
//   - string: Binance interval string
//   - error: If the interval is not supported
func mapBinanceInterval(interval string) (string, error) {
	switch interval {
	case "1m":
		return "1m", nil
	case "3m":
		return "3m", nil
	case "5m":
		return "5m", nil
	case "15m":
		return "15m", nil
	case "30m":
		return "30m", nil
	case "1h":
		return "1h", nil
	case "2h":
		return "2h", nil
	case "4h":
		return "4h", nil
	case "6h":
		return "6h", nil
	case "8h":
		return "8h", nil
	case "12h":
		return "12h", nil
	case "1d":
		return "1d", nil
	case "3d":
		return "3d", nil
	case "1w", "1wk":
		return "1w", nil
	case "1M", "1mo":
		return "1M", nil
	default:
		return "", fmt.Errorf("unsupported interval: %s", interval)
	}
}

// GetHistoricalData fetches OHLCV data from Binance.
// Supports pagination for large date ranges (max 1000 candles per request).
//
// Args:
//   - symbol: Trading pair (e.g., "BTC/USD", "ETH/USDT")
//   - start: Start date
//   - end: End date
//   - interval: Time interval (e.g., "1h", "4h", "1d")
//
// Returns:
//   - []models.OHLCV: Historical data
//   - error: Any error encountered
func (p *BinanceProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	binanceSymbol := convertSymbol(symbol)
	binanceInterval, err := mapBinanceInterval(interval)
	if err != nil {
		return nil, fmt.Errorf("failed to map interval: %w", err)
	}

	var allKlines []models.OHLCV
	currentStart := start

	// Paginate through the data (max 1000 candles per request)
	for currentStart.Before(end) {
		p.rateLimit()

		klines, err := p.api.GetKlines(binanceSymbol, binanceInterval, currentStart.UnixMilli(), end.UnixMilli(), 1000)

		if err != nil {
			return nil, fmt.Errorf("failed to fetch klines for %s: %w", binanceSymbol, err)
		}

		if len(klines) == 0 {
			break
		}

		for _, k := range klines {
			open, _ := strconv.ParseFloat(k.Open, 64)
			high, _ := strconv.ParseFloat(k.High, 64)
			low, _ := strconv.ParseFloat(k.Low, 64)
			closePrice, _ := strconv.ParseFloat(k.Close, 64)
			volume, _ := strconv.ParseFloat(k.Volume, 64)

			ohlcv := models.OHLCV{
				Timestamp: time.UnixMilli(k.OpenTime),
				Symbol:    symbol,
				Open:      open,
				High:      high,
				Low:       low,
				Close:     closePrice,
				Volume:    volume,
			}
			allKlines = append(allKlines, ohlcv)
		}

		// Move to next batch - use the close time of the last candle + 1ms
		lastKline := klines[len(klines)-1]
		currentStart = time.UnixMilli(lastKline.CloseTime + 1)

		// If we got less than 1000, we've reached the end
		if len(klines) < 1000 {
			break
		}
	}

	if len(allKlines) == 0 {
		return nil, fmt.Errorf("no data returned for symbol %s", symbol)
	}

	return allKlines, nil
}

// GetLatestPrice fetches the current price from Binance.
//
// Args:
//   - symbol: Trading pair
//
// Returns:
//   - float64: Current price
//   - error: Any error encountered
func (p *BinanceProvider) GetLatestPrice(symbol string) (float64, error) {
	p.rateLimit()

	binanceSymbol := convertSymbol(symbol)

	prices, err := p.api.GetPrices(binanceSymbol)

	if err != nil {
		return 0.0, fmt.Errorf("failed to fetch price for %s: %w", binanceSymbol, err)
	}

	if len(prices) == 0 {
		return 0.0, fmt.Errorf("no price data returned for %s", symbol)
	}

	price, err := strconv.ParseFloat(prices[0].Price, 64)
	if err != nil {
		return 0.0, fmt.Errorf("failed to parse price for %s: %w", symbol, err)
	}

	return price, nil
}

// GetTicker fetches ticker information from Binance.
//
// Args:
//   - symbol: Trading pair
//
// Returns:
//   - *models.Ticker: Ticker information
//   - error: Any error encountered
func (p *BinanceProvider) GetTicker(symbol string) (*models.Ticker, error) {
	p.rateLimit()

	binanceSymbol := convertSymbol(symbol)

	info, err := p.api.GetExchangeInfo(binanceSymbol)

	if err != nil {
		return nil, fmt.Errorf("failed to fetch exchange info for %s: %w", binanceSymbol, err)
	}

	if len(info.Symbols) == 0 {
		return nil, fmt.Errorf("no symbol info returned for %s", symbol)
	}

	symbolInfo := info.Symbols[0]

	return &models.Ticker{
		Symbol:    symbol,
		Name:      fmt.Sprintf("%s/%s", symbolInfo.BaseAsset, symbolInfo.QuoteAsset),
		AssetType: "crypto",
		Exchange:  "binance",
	}, nil
}
