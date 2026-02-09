package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/backend/backtesting"
	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/engine"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// Handler holds the HTTP handlers for the API.
type Handler struct {
	registry     *strategies.Registry
	provider     data.DataProvider
	config       *config.Config
	orderManager *execution.OrderManager
	engine       *engine.TradingEngine

	// In-memory store for backtest results
	// In production, this should be a persistent database
	results map[string]*backtesting.BacktestResult
	mu      sync.RWMutex
}

// NewHandler creates a new handler instance.
//
// Args:
//   - registry: Strategy registry
//   - provider: Data provider
//   - config: Application configuration
//   - orderManager: Order manager for execution data
//   - engine: Trading engine instance (optional)
//
// Returns:
//   - *Handler: The handler instance
func NewHandler(
	registry *strategies.Registry,
	provider data.DataProvider,
	config *config.Config,
	orderManager *execution.OrderManager,
	engine *engine.TradingEngine,
) *Handler {
	return &Handler{
		registry:     registry,
		provider:     provider,
		config:       config,
		orderManager: orderManager,
		engine:       engine,
		results:      make(map[string]*backtesting.BacktestResult),
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

// GetHistoricalDataHandler returns historical market data.
func (h *Handler) GetHistoricalDataHandler(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, "Symbol is required")
		return
	}

	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	interval := r.URL.Query().Get("interval")

	if interval == "" {
		interval = "1d"
	}

	// Default to last 30 days if not specified
	end := time.Now()
	if endStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endStr); err == nil {
			end = parsed
		}
	}

	start := end.AddDate(0, 0, -30)
	if startStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startStr); err == nil {
			start = parsed
		}
	}

	data, err := h.provider.GetHistoricalData(symbol, start, end, interval)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to fetch data: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, data)
}

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

// GetOrdersHandler returns a list of all orders.
func (h *Handler) GetOrdersHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}
	orders := h.orderManager.GetAllOrders()
	writeJSON(w, http.StatusOK, orders)
}

// GetPositionsHandler returns a list of current positions.
func (h *Handler) GetPositionsHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}
	positions, err := h.orderManager.GetPositions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, positions)
}

// GetBalanceHandler returns the current account balance.
func (h *Handler) GetBalanceHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}
	balance, err := h.orderManager.GetBalance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, balance)
}

// PlaceOrderRequest defines the payload for placing an order.
type PlaceOrderRequest struct {
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"` // "buy" or "sell"
	Type     string  `json:"type"` // "market" or "limit"
	Quantity float64 `json:"quantity"`
	Price    float64 `json:"price"` // Optional, required for limit orders
}

// PlaceOrderHandler handles manual order placement.
func (h *Handler) PlaceOrderHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	var req PlaceOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate inputs
	if req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "Symbol is required")
		return
	}
	if req.Quantity <= 0 {
		writeError(w, http.StatusBadRequest, "Quantity must be positive")
		return
	}

	var side models.OrderSide
	switch req.Side {
	case "buy":
		side = models.OrderSideBuy
	case "sell":
		side = models.OrderSideSell
	default:
		writeError(w, http.StatusBadRequest, "Invalid side: must be 'buy' or 'sell'")
		return
	}

	var order *models.Order
	var err error

	// Create order based on type
	switch req.Type {
	case "market":
		order, err = h.orderManager.CreateMarketOrder(req.Symbol, side, req.Quantity)
	case "limit":
		if req.Price <= 0 {
			writeError(w, http.StatusBadRequest, "Price must be positive for limit orders")
			return
		}
		order, err = h.orderManager.CreateLimitOrder(req.Symbol, side, req.Quantity, req.Price)
	default:
		writeError(w, http.StatusBadRequest, "Invalid type: must be 'market' or 'limit'")
		return
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to place order: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, order)
}

// CancelOrderHandler handles order cancellation.
func (h *Handler) CancelOrderHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Order ID is required")
		return
	}

	if err := h.orderManager.CancelOrder(id); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to cancel order: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "id": id})
}

// EngineControlRequest defines the payload for engine control.
type EngineControlRequest struct {
	Confirm bool `json:"confirm"`
}

// StartEngineHandler starts the trading engine.
func (h *Handler) StartEngineHandler(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "Trading engine not available")
		return
	}

	var req EngineControlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirm {
		writeError(w, http.StatusBadRequest, "Confirmation required: {\"confirm\": true}")
		return
	}

	// Iterate over strategies and ensure they are initialized if needed?
	// For now, just start the engine loop.
	// NOTE: Engine.Start is blocking if not run in goroutine, but our implementation
	// launches a goroutine internally. Let's check TradingEngine.go content.
	// Checked: engine.Start calls go e.loop(ctx), so it is non-blocking. Perfect.

	// We need a context. Use background for now since request context cancels on finish.
	// Warning: If we use r.Context(), engine stops when request ends.
	// We should probably have a persistent context in main, but for now context.Background is safer than r.Context.
	// Actually, the engine might have been initialized with a context in main?
	// The Start method takes a context.
	if err := h.engine.Start(context.Background()); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// StopEngineHandler stops the trading engine.
func (h *Handler) StopEngineHandler(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "Trading engine not available")
		return
	}

	var req EngineControlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirm {
		writeError(w, http.StatusBadRequest, "Confirmation required: {\"confirm\": true}")
		return
	}

	h.engine.Stop()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Error().Err(err).Msg("Failed to write JSON response")
	}
}
