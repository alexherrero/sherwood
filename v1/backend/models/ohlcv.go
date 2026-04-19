// Package models provides shared domain models for the Sherwood trading engine.
// These models are used across all packages for consistent data representation.
package models

import (
	"time"
)

// OHLCV represents a single candlestick of price data.
// OHLCV stands for Open, High, Low, Close, Volume.
type OHLCV struct {
	// Timestamp is the start time of the candlestick period.
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	// Symbol is the ticker symbol (e.g., "AAPL", "BTC-USD").
	Symbol string `json:"symbol" db:"symbol"`
	// Open is the opening price for the period.
	Open float64 `json:"open" db:"open"`
	// High is the highest price during the period.
	High float64 `json:"high" db:"high"`
	// Low is the lowest price during the period.
	Low float64 `json:"low" db:"low"`
	// Close is the closing price for the period.
	Close float64 `json:"close" db:"close"`
	// Volume is the trading volume during the period.
	Volume float64 `json:"volume" db:"volume"`
}

// Ticker represents a tradable symbol.
type Ticker struct {
	// Symbol is the ticker symbol (e.g., "AAPL", "BTC-USD").
	Symbol string `json:"symbol" db:"symbol"`
	// Name is the full name of the asset.
	Name string `json:"name" db:"name"`
	// AssetType is the type of asset ("stock", "crypto", "forex").
	AssetType string `json:"asset_type" db:"asset_type"`
	// Exchange is the exchange where the asset is traded.
	Exchange string `json:"exchange" db:"exchange"`
}
