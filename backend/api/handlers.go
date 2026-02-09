package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/backend/backtesting"
	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// Handler holds dependencies for API handlers.
type Handler struct {
	registry *strategies.Registry
	provider data.DataProvider
	config   *config.Config // App config

	// In-memory store for backtest results
	// In production, this should be a persistent database
	results map[string]*backtesting.BacktestResult
	mu      sync.RWMutex
}

// NewHandler creates a new API handler with dependencies.
func NewHandler(registry *strategies.Registry, provider data.DataProvider, cfg *config.Config) *Handler {
	return &Handler{
		registry: registry,
		provider: provider,
		config:   cfg,
		results:  make(map[string]*backtesting.BacktestResult),
	}
}

// HealthHandler returns the health status of the API.
func (h *Handler) HealthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"mode":   string(h.config.TradingMode),
	})
}

// ListStrategiesHandler returns a list of available strategies.
func (h *Handler) ListStrategiesHandler(w http.ResponseWriter, r *http.Request) {
	strategiesList := h.registry.List()
	details := make([]map[string]interface{}, 0, len(strategiesList))

	for _, name := range strategiesList {
		if strategy, ok := h.registry.Get(name); ok {
			details = append(details, map[string]interface{}{
				"name":        strategy.Name(),
				"description": strategy.Description(),
				"parameters":  strategy.GetParameters(),
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"strategies": details,
	})
}

// GetStrategyHandler returns details for a specific strategy.
func (h *Handler) GetStrategyHandler(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	strategy, ok := h.registry.Get(name)
	if !ok {
		http.Error(w, "Strategy not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":        strategy.Name(),
		"description": strategy.Description(),
		"parameters":  strategy.GetParameters(),
	})
}

// RunBacktestRequest defines the payload for starting a backtest.
type RunBacktestRequest struct {
	Strategy       string                 `json:"strategy"`
	Symbol         string                 `json:"symbol"`
	Start          time.Time              `json:"start"`
	End            time.Time              `json:"end"`
	InitialCapital float64                `json:"initial_capital"`
	StrategyConfig map[string]interface{} `json:"strategy_config"`
}

// RunBacktestHandler starts a new backtest.
func (h *Handler) RunBacktestHandler(w http.ResponseWriter, r *http.Request) {
	var req RunBacktestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Strategy == "" || req.Symbol == "" {
		http.Error(w, "Strategy and symbol are required", http.StatusBadRequest)
		return
	}

	// Get strategy
	strategy, ok := h.registry.Get(req.Strategy)
	if !ok {
		http.Error(w, fmt.Sprintf("Strategy '%s' not found", req.Strategy), http.StatusBadRequest)
		return
	}

	// Initialize strategy with config
	if err := strategy.Init(req.StrategyConfig); err != nil {
		http.Error(w, fmt.Sprintf("Failed to initialize strategy: %v", err), http.StatusBadRequest)
		return
	}

	// Fetch data
	// Using "1d" interval for default backtesting
	data, err := h.provider.GetHistoricalData(req.Symbol, req.Start, req.End, "1d")
	if err != nil {
		log.Error().Err(err).Str("symbol", req.Symbol).Msg("Failed to fetch historical data")
		http.Error(w, "Failed to fetch historical data", http.StatusInternalServerError)
		return
	}

	// Configure backtest
	btConfig := backtesting.BacktestConfig{
		Symbol:         req.Symbol,
		StartDate:      req.Start,
		EndDate:        req.End,
		InitialCapital: req.InitialCapital,
		Commission:     0.001, // Default 0.1% commission
	}

	// Run backtest (synchronous for now, could be async)
	engine := backtesting.NewEngine()
	result, err := engine.Run(strategy, data, btConfig)
	if err != nil {
		log.Error().Err(err).Msg("Backtest execution failed")
		http.Error(w, fmt.Sprintf("Backtest failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Store result
	h.mu.Lock()
	h.results[result.ID] = result
	h.mu.Unlock()

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"id":      result.ID,
		"status":  "completed", // For sync execution
		"message": "Backtest completed successfully",
		"metrics": result.Metrics,
	})
}

// GetBacktestResultHandler returns results for a completed backtest.
func (h *Handler) GetBacktestResultHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	h.mu.RLock()
	result, ok := h.results[id]
	h.mu.RUnlock()

	if !ok {
		http.Error(w, "Backtest not found", http.StatusNotFound)
		return
	}

	// Generate report for summary
	report := backtesting.NewReport(result)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":         result.ID,
		"status":     "completed",
		"strategy":   result.Strategy,
		"config":     result.Config,
		"metrics":    result.Metrics,
		"summary":    report.Summary(),
		"chart_data": result.EquityCurve, // For frontend plotting
	})
}

// GetConfigHandler returns the current configuration (non-sensitive).
func (h *Handler) GetConfigHandler(w http.ResponseWriter, r *http.Request) {
	// Only expose non-sensitive configuration
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":      "0.1.0",
		"api_version":  "v1",
		"trading_mode": h.config.TradingMode,
	})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Error().Err(err).Msg("Failed to write JSON response")
	}
}
