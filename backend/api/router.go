// Package api provides the REST API for the Sherwood trading engine.
// It includes routing, handlers, and middleware.
package api

import (
	"net/http"
	"time"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/engine"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/notifications"
	"github.com/alexherrero/sherwood/backend/realtime"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/alexherrero/sherwood/backend/tracing"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

// NewRouter creates and configures the main HTTP router.
//
// Args:
//   - cfg: Application configuration
//   - registry: Strategy registry
//   - provider: Data provider for backtesting
//   - orderManager: Order manager for execution data
//   - engine: Trading engine instance (optional)
//   - wsManager: WebSocket manager for real-time updates
//
// Returns:
//   - http.Handler: The configured router
func NewRouter(
	cfg *config.Config,
	registry *strategies.Registry,
	provider data.DataProvider,
	orderManager *execution.OrderManager,
	engine *engine.TradingEngine,
	wsManager *realtime.WebSocketManager,
	notificationManager *notifications.Manager,
) http.Handler {
	r := chi.NewRouter()

	// Middleware stack
	r.Use(middleware.RequestID)
	r.Use(TraceMiddleware)
	r.Use(middleware.RealIP)
	r.Use(zerologLogger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Rate limiting - prevent abuse
	// Global: 100 requests per minute per IP (protects against basic DoS)
	r.Use(httprate.LimitByIP(100, 1*time.Minute))
	// Burst protection: 20 requests per second per IP
	r.Use(httprate.LimitByIP(20, 1*time.Second))

	// Request body size limit - prevent memory exhaustion attacks
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Limit request body to 1MB
			r.Body = http.MaxBytesReader(w, r.Body, 1048576)
			next.ServeHTTP(w, r)
		})
	})

	// Security Headers
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Content-Security-Policy", "default-src 'self'")
			// HSTS only if TLS (which is not handled here but good practice to include if behind proxy)
			// w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			next.ServeHTTP(w, r)
		})
	})

	// CORS middleware for frontend
	r.Use(newCORSMiddleware(cfg))

	// Initialize handler with dependencies
	h := NewHandler(registry, provider, cfg, orderManager, engine, wsManager, notificationManager)

	// Public routes
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"service": "sherwood-api",
			"version": "1.0.0",
			"status":  "running",
		})
	})

	// WebSocket endpoint (only if wsManager is available)
	if wsManager != nil {
		r.Get("/ws", h.wsManager.HandleWebSocket)
	}

	// Health check endpoint
	r.Get("/health", h.HealthHandler)

	// API v1 routes (protected)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(AuthMiddleware(cfg))
		r.Use(AuditMiddleware)

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
			r.Get("/orders/{id}", h.GetOrderHandler)
			r.Patch("/orders/{id}", h.ModifyOrderHandler) // New route
			r.Delete("/orders/{id}", h.CancelOrderHandler)
			r.Get("/history", h.GetOrderHistoryHandler) // Alias/wrapper for GetOrders
			r.Get("/trades", h.GetTradesHandler)        // New route
			r.Get("/positions", h.GetPositionsHandler)
			r.Get("/balance", h.GetBalanceHandler)
		})

		// Portfolio routes
		r.Route("/portfolio", func(r chi.Router) {
			r.Get("/summary", h.GetPortfolioSummaryHandler)
			r.Get("/performance", h.GetPortfolioPerformanceHandler)
		})

		// Market Data routes
		r.Route("/data", func(r chi.Router) {
			r.Get("/history", h.GetHistoricalDataHandler)
		})

		// Engine routes
		r.Route("/engine", func(r chi.Router) {
			r.Post("/start", h.StartEngineHandler)
			r.Post("/stop", h.StopEngineHandler)
		})

		// Notification routes
		r.Route("/notifications", func(r chi.Router) {
			r.Get("/", h.GetNotificationsHandler)
			r.Put("/read-all", h.MarkAllReadHandler)
			r.Put("/{id}/read", h.MarkNotificationReadHandler)
		})

		// Config routes
		r.Route("/config", func(r chi.Router) {
			r.Get("/", h.GetConfigHandler)
			r.Get("/metrics", h.MetricsHandler)
			r.Get("/validation", h.GetConfigValidationHandler)
			r.Patch("/system", h.UpdateSystemConfigHandler)
			r.Post("/rotate-key", h.RotateAPIKeyHandler)
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
// Includes the trace_id from context for request correlation.
func zerologLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		logger := tracing.Logger(r.Context())
		logger.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", ww.Status()).
			Dur("duration", time.Since(start)).
			Msg("request completed")
	})
}

// newCORSMiddleware creates CORS middleware with origin whitelisting.
func newCORSMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check if origin is in allowed list
			allowed := false
			for _, allowedOrigin := range cfg.AllowedOrigins {
				if origin == allowedOrigin {
					allowed = true
					break
				}
			}

			// Set CORS headers if origin is allowed
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Sherwood-API-Key")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Max-Age", "3600")
			}

			// Handle preflight request
			if r.Method == "OPTIONS" {
				if allowed {
					w.WriteHeader(http.StatusOK)
				} else {
					w.WriteHeader(http.StatusForbidden)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
