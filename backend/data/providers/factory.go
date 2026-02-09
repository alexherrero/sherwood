// Package providers contains data provider implementations.
package providers

import (
	"fmt"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/data"
)

// ProviderType represents the type of data provider.
type ProviderType string

const (
	// ProviderYahoo represents Yahoo Finance provider.
	ProviderYahoo ProviderType = "yahoo"
	// ProviderTiingo represents Tiingo provider (more reliable than Yahoo).
	ProviderTiingo ProviderType = "tiingo"
	// ProviderBinance represents Binance exchange provider.
	ProviderBinance ProviderType = "binance"
)

// NewProvider creates a data provider based on the specified type.
//
// Args:
//   - providerType: Type of provider to create
//   - cfg: Application configuration (for API keys, etc.)
//
// Returns:
//   - data.DataProvider: The created provider
//   - error: Any error encountered
func NewProvider(providerType ProviderType, cfg *config.Config) (data.DataProvider, error) {
	switch providerType {
	case ProviderYahoo:
		return NewYahooProvider(), nil

	case ProviderTiingo:
		apiKey := ""
		if cfg != nil {
			apiKey = cfg.TiingoAPIKey
		}
		return NewTiingoProvider(apiKey), nil

	case ProviderBinance:
		apiKey := ""
		apiSecret := ""
		useBinanceUS := true // Default to US for safety
		if cfg != nil {
			apiKey = cfg.BinanceAPIKey
			apiSecret = cfg.BinanceAPISecret
			useBinanceUS = cfg.UseBinanceUS
		}
		if useBinanceUS {
			return NewBinanceUSProvider(apiKey, apiSecret), nil
		}
		return NewBinanceProvider(apiKey, apiSecret), nil

	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}

// NewProviderFromString creates a provider from a string type name.
//
// Args:
//   - providerType: String name of the provider type
//   - cfg: Application configuration
//
// Returns:
//   - data.DataProvider: The created provider
//   - error: Any error encountered
func NewProviderFromString(providerType string, cfg *config.Config) (data.DataProvider, error) {
	switch providerType {
	case "yahoo":
		return NewProvider(ProviderYahoo, cfg)
	case "tiingo":
		return NewProvider(ProviderTiingo, cfg)
	case "binance":
		return NewProvider(ProviderBinance, cfg)
	default:
		return nil, fmt.Errorf("unknown provider type: %s", providerType)
	}
}

// AvailableProviders returns a list of all available provider types.
func AvailableProviders() []ProviderType {
	return []ProviderType{ProviderYahoo, ProviderTiingo, ProviderBinance}
}
