package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewProvider verifies the provider factory.
func TestNewProvider(t *testing.T) {
	tests := []struct {
		name         string
		providerType ProviderType
		wantName     string
		wantErr      bool
	}{
		{"yahoo provider", ProviderYahoo, "yahoo", false},
		{"tiingo provider", ProviderTiingo, "tiingo", false},
		{"binance provider", ProviderBinance, "binance", false},
		{"unsupported provider", ProviderType("invalid"), "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewProvider(tt.providerType, nil)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, provider.Name())
		})
	}
}

// TestNewProviderFromString verifies string-based provider creation.
func TestNewProviderFromString(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		wantName     string
		wantErr      bool
	}{
		{"yahoo string", "yahoo", "yahoo", false},
		{"tiingo string", "tiingo", "tiingo", false},
		{"binance string", "binance", "binance", false},
		{"unknown string", "unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewProviderFromString(tt.providerType, nil)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, provider.Name())
		})
	}
}

// TestAvailableProviders verifies the list of available providers.
func TestAvailableProviders(t *testing.T) {
	providers := AvailableProviders()
	assert.Contains(t, providers, ProviderYahoo)
	assert.Contains(t, providers, ProviderTiingo)
	assert.Contains(t, providers, ProviderBinance)
	assert.Len(t, providers, 3)
}
