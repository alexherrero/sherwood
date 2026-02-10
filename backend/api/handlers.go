package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/backend/backtesting"
	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/engine"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/realtime"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/rs/zerolog/log"
)

// Handler holds the HTTP handlers for the API.
type Handler struct {
	registry     *strategies.Registry
	provider     data.DataProvider
	config       *config.Config
	orderManager *execution.OrderManager
	engine       *engine.TradingEngine
	wsManager    *realtime.WebSocketManager
	startTime    time.Time

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
//   - wsManager: WebSocket manager for real-time updates
//
// Returns:
//   - *Handler: The handler instance
func NewHandler(
	registry *strategies.Registry,
	provider data.DataProvider,
	cfg *config.Config,
	orderManager *execution.OrderManager,
	engine *engine.TradingEngine,
	wsManager *realtime.WebSocketManager,
) *Handler {
	return &Handler{
		registry:     registry,
		provider:     provider,
		config:       cfg,
		orderManager: orderManager,
		engine:       engine,
		wsManager:    wsManager,
		startTime:    time.Now(),
		results:      make(map[string]*backtesting.BacktestResult),
	}
}

// writeError writes a JSON error response.
// The optional code argument allows specifying a machine-readable error code.
// If code is not provided, it defaults to a generic error code based on status.
func writeError(w http.ResponseWriter, status int, message string, code ...string) {
	errCode := "UNKNOWN_ERROR"
	if len(code) > 0 {
		errCode = code[0]
	} else {
		// Infer code from status
		switch status {
		case http.StatusBadRequest:
			errCode = "BAD_REQUEST"
		case http.StatusUnauthorized:
			errCode = "UNAUTHORIZED"
		case http.StatusForbidden:
			errCode = "FORBIDDEN"
		case http.StatusNotFound:
			errCode = "NOT_FOUND"
		case http.StatusServiceUnavailable:
			errCode = "SERVICE_UNAVAILABLE"
		case http.StatusInternalServerError:
			errCode = "INTERNAL_ERROR"
		}
	}

	resp := APIError{
		Error: message,
		Code:  errCode,
	}
	writeJSON(w, status, resp)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Error().Err(err).Msg("Failed to write JSON response")
	}
}
