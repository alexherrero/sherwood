// Package api provides the REST API for the Sherwood trading engine.
// It includes routing, handlers, and middleware.
package api

import (
	"net/http"
	"time"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"
)

// NewRouter creates and configures the main HTTP router.
//
// Args:
//   - cfg: Application configuration
//   - registry: Strategy registry
//   - provider: Data provider for backtesting
//   - orderManager: Order manager for execution data
//
// Returns:
//   - http.Handler: The configured router
func NewRouter(cfg *config.Config, registry *strategies.Registry, provider data.DataProvider, orderManager *execution.OrderManager) http.Handler {
	r := chi.NewRouter()

	// Middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(zerologLogger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS middleware for frontend
	r.Use(corsMiddleware)

	// Initialize handler with dependencies
	h := NewHandler(registry, provider, cfg, orderManager)

	// Health check endpoint
	r.Get("/health", h.HealthHandler)

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		// Strategies routes
		r.Route("/strategies", func(r chi.Router) {
			r.Get("/", h.ListStrategiesHandler)
			r.Get("/{name}", h.GetStrategyHandler)
		})

		// Backtest routes
		r.Route("/backtests", func(r chi.Router) {
			r.Post("/", h.RunBacktestHandler)
			r.Get("/{id}", h.GetBacktestResultHandler)
		})

		// Execution routes
		r.Route("/execution", func(r chi.Router) {
			r.Get("/orders", h.GetOrdersHandler)
			r.Post("/orders", h.PlaceOrderHandler)
			r.Get("/positions", h.GetPositionsHandler)
			r.Get("/balance", h.GetBalanceHandler)
		})

		// Config routes
		r.Route("/config", func(r chi.Router) {
			r.Get("/", h.GetConfigHandler)
		})

		// Status endpoint
		r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
			status := "running"
			if cfg.IsDryRun() {
				status = "dry_run"
			}
			writeJSON(w, http.StatusOK, map[string]string{
				"mode":   status,
				"status": "active",
			})
		})
	})

	return r
}

// zerologLogger is middleware that logs requests using zerolog.
func zerologLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", ww.Status()).
			Dur("duration", time.Since(start)).
			Msg("request completed")
	})
}

// corsMiddleware handles CORS headers for frontend requests.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
