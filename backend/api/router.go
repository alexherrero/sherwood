// Package api provides the REST API for the Sherwood trading engine.
// It includes routing, handlers, and middleware.
package api

import (
	"net/http"
	"time"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"
)

// NewRouter creates and configures the main HTTP router.
//
// Args:
//   - cfg: Application configuration
//
// Returns:
//   - http.Handler: The configured router
func NewRouter(cfg *config.Config) http.Handler {
	r := chi.NewRouter()

	// Middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(zerologLogger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS middleware for frontend
	r.Use(corsMiddleware)

	// Health check endpoint
	r.Get("/health", healthHandler)

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		// Strategies routes
		r.Route("/strategies", func(r chi.Router) {
			r.Get("/", listStrategiesHandler)
			r.Get("/{name}", getStrategyHandler)
		})

		// Backtest routes
		r.Route("/backtests", func(r chi.Router) {
			r.Post("/", runBacktestHandler)
			r.Get("/{id}", getBacktestResultHandler)
		})

		// Config routes
		r.Route("/config", func(r chi.Router) {
			r.Get("/", getConfigHandler)
		})

		// Status endpoint
		r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
			if cfg.IsDryRun() {
				writeJSON(w, http.StatusOK, map[string]string{
					"mode":   "dry_run",
					"status": "running",
				})
			} else {
				writeJSON(w, http.StatusOK, map[string]string{
					"mode":   "live",
					"status": "running",
				})
			}
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
