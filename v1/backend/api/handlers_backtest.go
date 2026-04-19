package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/alexherrero/sherwood/backend/backtesting"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// RunBacktestRequest defines the payload for starting a backtest.
type RunBacktestRequest struct {
	Strategy       string                 `json:"strategy" validate:"required,min=1,max=50"`
	Symbol         string                 `json:"symbol" validate:"required,min=1,max=20"`
	Start          time.Time              `json:"start" validate:"required"`
	End            time.Time              `json:"end" validate:"required,gtfield=Start"`
	InitialCapital float64                `json:"initial_capital" validate:"required,gt=0,lte=10000000"`
	StrategyConfig map[string]interface{} `json:"strategy_config"`
}

// RunBacktestHandler starts a new backtest.
func (h *Handler) RunBacktestHandler(w http.ResponseWriter, r *http.Request) {
	var req RunBacktestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate request
	if valErr := validateStruct(req); valErr != nil {
		writeValidationError(w, valErr)
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
