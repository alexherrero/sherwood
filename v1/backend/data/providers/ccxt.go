// Package providers contains data provider implementations.
package providers

import (
	"fmt"

	"github.com/alexherrero/sherwood/backend/data"
)

// SupportedExchanges lists all supported cryptocurrency exchanges.
var SupportedExchanges = []string{"binance"}

// ExchangeProvider wraps exchange-specific providers under a unified interface.
// This provides compatibility with the original CCXT naming convention.
type ExchangeProvider struct {
	provider data.DataProvider
	exchange string
}

// NewExchangeProvider creates a provider for the specified exchange.
// Currently supports: binance
//
// Args:
//   - exchange: Exchange identifier (e.g., "binance")
//   - apiKey: API key for the exchange
//   - apiSecret: API secret for the exchange
//
// Returns:
//   - *ExchangeProvider: The provider instance
//   - error: If the exchange is not supported
func NewExchangeProvider(exchange, apiKey, apiSecret string) (*ExchangeProvider, error) {
	var provider data.DataProvider

	switch exchange {
	case "binance":
		provider = NewBinanceProvider(apiKey, apiSecret)
	default:
		return nil, fmt.Errorf("unsupported exchange: %s (supported: %v)", exchange, SupportedExchanges)
	}

	return &ExchangeProvider{
		provider: provider,
		exchange: exchange,
	}, nil
}

// Name returns the provider name.
func (e *ExchangeProvider) Name() string {
	return fmt.Sprintf("exchange-%s", e.exchange)
}

// GetHistoricalData delegates to the underlying exchange provider.
func (e *ExchangeProvider) GetHistoricalData(symbol string, start, end interface{}, interval string) (interface{}, error) {
	// This is a legacy wrapper - prefer using NewBinanceProvider directly
	return nil, fmt.Errorf("use NewBinanceProvider directly for type-safe access")
}

// Exchange returns the underlying exchange name.
func (e *ExchangeProvider) Exchange() string {
	return e.exchange
}

// Provider returns the underlying typed provider.
func (e *ExchangeProvider) Provider() data.DataProvider {
	return e.provider
}
