package api

import (
	"net/http"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/rs/zerolog/log"
)

// GetConfigHandler returns the current configuration (sanitized).
func (h *Handler) GetConfigHandler(w http.ResponseWriter, r *http.Request) {
	// Don't return secrets!
	safeConfig := map[string]interface{}{
		"server_port":  h.config.ServerPort,
		"server_host":  h.config.ServerHost,
		"trading_mode": h.config.TradingMode,
		"log_level":    h.config.LogLevel,
	}
	writeJSON(w, http.StatusOK, safeConfig)
}

// GetConfigValidationHandler returns configuration validation status and details.
func (h *Handler) GetConfigValidationHandler(w http.ResponseWriter, r *http.Request) {
	// Collect enabled strategies with details
	enabledStrategies := make([]map[string]interface{}, 0, len(h.config.EnabledStrategies))
	invalidStrategies := make([]string, 0)

	for _, strategyName := range h.config.EnabledStrategies {
		if strategy, ok := h.registry.Get(strategyName); ok {
			enabledStrategies = append(enabledStrategies, map[string]interface{}{
				"name":        strategy.Name(),
				"description": strategy.Description(),
				"status":      "active",
			})
		} else {
			invalidStrategies = append(invalidStrategies, strategyName)
		}
	}

	// Get all available strategies for reference
	availableStrategies := h.registry.List()

	// Determine provider status
	providerStatus := map[string]interface{}{
		"name":        h.config.DataProvider,
		"type":        h.provider.Name(), // Actual provider name
		"status":      "connected",
		"description": getProviderDescription(h.config.DataProvider),
	}

	// Overall validation status
	isValid := len(invalidStrategies) == 0 && len(enabledStrategies) > 0

	response := map[string]interface{}{
		"valid": isValid,
		"configuration": map[string]interface{}{
			"trading_mode":       h.config.TradingMode,
			"server_port":        h.config.ServerPort,
			"log_level":          h.config.LogLevel,
			"data_provider":      h.config.DataProvider,
			"enabled_strategies": h.config.EnabledStrategies,
		},
		"provider": providerStatus,
		"strategies": map[string]interface{}{
			"enabled":   enabledStrategies,
			"available": availableStrategies,
			"invalid":   invalidStrategies,
			"count": map[string]int{
				"enabled":   len(enabledStrategies),
				"available": len(availableStrategies),
				"invalid":   len(invalidStrategies),
			},
		},
		"warnings": generateConfigWarnings(h.config, len(enabledStrategies)),
	}

	writeJSON(w, http.StatusOK, response)
}

// RotateAPIKeyHandler generates a new API key and returns it.
func (h *Handler) RotateAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	newKey, err := h.config.RotateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to rotate API key")
		log.Error().Err(err).Msg("Failed to rotate API key")
		return
	}

	log.Info().Msg("API Key rotated successfully")

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"api_key": newKey,
		"message": "API key rotated successfully. Please update your client configuration.",
	})
}

// getProviderDescription returns a human-readable description for a provider.
func getProviderDescription(providerName string) string {
	descriptions := map[string]string{
		"yahoo":   "Yahoo Finance - Free, no API key required",
		"tiingo":  "Tiingo - Professional grade data, API key required",
		"binance": "Binance - Cryptocurrency exchange data",
	}
	if desc, ok := descriptions[providerName]; ok {
		return desc
	}
	return "Unknown provider"
}

// generateConfigWarnings generates warnings about configuration issues.
func generateConfigWarnings(cfg *config.Config, enabledCount int) []string {
	warnings := make([]string, 0)

	if enabledCount == 0 {
		warnings = append(warnings, "No strategies enabled - engine will not execute any trades")
	}

	if cfg.IsLive() && cfg.APIKey == "" {
		warnings = append(warnings, "Running in LIVE mode without API_KEY - this is insecure!")
	}

	if cfg.DataProvider == "tiingo" && cfg.TiingoAPIKey == "" {
		warnings = append(warnings, "Tiingo provider selected but TIINGO_API_KEY not set")
	}

	if cfg.DataProvider == "binance" && (cfg.BinanceAPIKey == "" || cfg.BinanceAPISecret == "") {
		warnings = append(warnings, "Binance provider selected but API credentials not set")
	}

	return warnings
}
