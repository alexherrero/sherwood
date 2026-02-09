// Package providers contains data provider implementations.
package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
)

const (
	tiingoBaseURL = "https://api.tiingo.com"
)

// TiingoProvider fetches market data from Tiingo API.
// Tiingo offers reliable stock data with a generous free tier (500 req/hour).
// Get a free API key at: https://www.tiingo.com/
type TiingoProvider struct {
	apiKey      string
	httpClient  *http.Client
	rateLimiter time.Time
	minInterval time.Duration
}

// NewTiingoProvider creates a new TiingoProvider instance.
//
// Args:
//   - apiKey: Tiingo API key (required, get free at tiingo.com)
//
// Returns:
//   - *TiingoProvider: The provider instance
func NewTiingoProvider(apiKey string) *TiingoProvider {
	return &TiingoProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		rateLimiter: time.Time{},
		minInterval: 100 * time.Millisecond, // ~10 requests/second
	}
}

// Name returns the provider name.
func (p *TiingoProvider) Name() string {
	return "tiingo"
}

// rateLimit ensures we don't exceed API rate limits.
func (p *TiingoProvider) rateLimit() {
	if !p.rateLimiter.IsZero() {
		elapsed := time.Since(p.rateLimiter)
		if elapsed < p.minInterval {
			time.Sleep(p.minInterval - elapsed)
		}
	}
	p.rateLimiter = time.Now()
}

// doRequest performs an authenticated HTTP request to Tiingo API.
func (p *TiingoProvider) doRequest(endpoint string, params url.Values) ([]byte, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("tiingo API key is required (get free at tiingo.com)")
	}

	p.rateLimit()

	reqURL := fmt.Sprintf("%s%s", tiingoBaseURL, endpoint)
	if params != nil {
		reqURL = fmt.Sprintf("%s?%s", reqURL, params.Encode())
	}

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Token %s", p.apiKey))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// tiingoPriceData represents Tiingo's daily price response structure.
type tiingoPriceData struct {
	Date     string  `json:"date"`
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	Volume   float64 `json:"volume"`
	AdjOpen  float64 `json:"adjOpen"`
	AdjHigh  float64 `json:"adjHigh"`
	AdjLow   float64 `json:"adjLow"`
	AdjClose float64 `json:"adjClose"`
}

// tiingoMetaData represents Tiingo's ticker metadata response.
type tiingoMetaData struct {
	Ticker      string `json:"ticker"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Exchange    string `json:"exchangeCode"`
}

// GetHistoricalData fetches OHLCV data from Tiingo.
//
// Args:
//   - symbol: Ticker symbol (e.g., "AAPL")
//   - start: Start date
//   - end: End date
//   - interval: Time interval (only "1d" supported for EOD data)
//
// Returns:
//   - []models.OHLCV: Historical data
//   - error: Any error encountered
func (p *TiingoProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	// Tiingo EOD API only supports daily data
	if interval != "1d" && interval != "daily" {
		return nil, fmt.Errorf("tiingo EOD API only supports daily interval (1d), got: %s", interval)
	}

	params := url.Values{}
	params.Set("startDate", start.Format("2006-01-02"))
	params.Set("endDate", end.Format("2006-01-02"))

	endpoint := fmt.Sprintf("/tiingo/daily/%s/prices", symbol)
	body, err := p.doRequest(endpoint, params)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical data for %s: %w", symbol, err)
	}

	var priceData []tiingoPriceData
	if err := json.Unmarshal(body, &priceData); err != nil {
		return nil, fmt.Errorf("failed to parse response for %s: %w", symbol, err)
	}

	if len(priceData) == 0 {
		return nil, fmt.Errorf("no data returned for symbol %s", symbol)
	}

	ohlcvData := make([]models.OHLCV, len(priceData))
	for i, pd := range priceData {
		timestamp, _ := time.Parse(time.RFC3339, pd.Date)
		ohlcvData[i] = models.OHLCV{
			Timestamp: timestamp,
			Symbol:    symbol,
			Open:      pd.AdjOpen, // Use adjusted prices
			High:      pd.AdjHigh,
			Low:       pd.AdjLow,
			Close:     pd.AdjClose,
			Volume:    pd.Volume,
		}
	}

	return ohlcvData, nil
}

// GetLatestPrice fetches the current/latest price from Tiingo.
//
// Args:
//   - symbol: Ticker symbol
//
// Returns:
//   - float64: Latest closing price
//   - error: Any error encountered
func (p *TiingoProvider) GetLatestPrice(symbol string) (float64, error) {
	endpoint := fmt.Sprintf("/tiingo/daily/%s/prices", symbol)
	body, err := p.doRequest(endpoint, nil)
	if err != nil {
		return 0.0, fmt.Errorf("failed to fetch price for %s: %w", symbol, err)
	}

	var priceData []tiingoPriceData
	if err := json.Unmarshal(body, &priceData); err != nil {
		return 0.0, fmt.Errorf("failed to parse response for %s: %w", symbol, err)
	}

	if len(priceData) == 0 {
		return 0.0, fmt.Errorf("no price data returned for %s", symbol)
	}

	// Return the most recent adjusted close price
	return priceData[len(priceData)-1].AdjClose, nil
}

// GetTicker fetches ticker information from Tiingo.
//
// Args:
//   - symbol: Ticker symbol
//
// Returns:
//   - *models.Ticker: Ticker information
//   - error: Any error encountered
func (p *TiingoProvider) GetTicker(symbol string) (*models.Ticker, error) {
	endpoint := fmt.Sprintf("/tiingo/daily/%s", symbol)
	body, err := p.doRequest(endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ticker info for %s: %w", symbol, err)
	}

	var meta tiingoMetaData
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse ticker info for %s: %w", symbol, err)
	}

	return &models.Ticker{
		Symbol:    meta.Ticker,
		Name:      meta.Name,
		AssetType: "stock", // Tiingo EOD is primarily stocks/ETFs
		Exchange:  meta.Exchange,
	}, nil
}
