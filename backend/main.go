// Package main is the entry point for the Sherwood trading engine.
// It initializes all components and starts the API server.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexherrero/sherwood/backend/api"
	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/data/providers"
	"github.com/alexherrero/sherwood/backend/engine"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/realtime"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Configure zerolog for structured logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	log.Info().Msg("Starting Sherwood Trading Engine...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	// Set log level
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// Log trading mode warning
	if cfg.IsLive() {
		log.Warn().Msg("⚠️  LIVE TRADING MODE - Real money at risk!")
	} else {
		log.Info().Msg("📝 Paper trading mode (dry run)")
	}

	// Initialize WebSocket Manager
	wsManager := realtime.NewWebSocketManager()
	go wsManager.Run()

	// Initialize Strategy Registry
	registry := strategies.NewRegistry()

	// Register strategies based on configuration
	log.Info().Msgf("Enabled strategies: %v", cfg.EnabledStrategies)
	if len(cfg.EnabledStrategies) == 0 {
		log.Warn().Msg("No strategies enabled - engine will run but not execute trades")
	}

	for _, strategyName := range cfg.EnabledStrategies {
		strategy, err := strategies.NewStrategyByName(strategyName)
		if err != nil {
			log.Fatal().Err(err).Msgf("Failed to create strategy: %s", strategyName)
		}
		if err := registry.Register(strategy); err != nil {
			log.Fatal().Err(err).Msgf("Failed to register strategy: %s", strategyName)
		}
		log.Info().Msgf("✓ Registered strategy: %s", strategyName)
	}

	// Initialize Data Provider based on configuration
	log.Info().Msgf("Using data provider: %s", cfg.DataProvider)
	provider, err := providers.NewProviderFromString(cfg.DataProvider, cfg)
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed to create data provider: %s", cfg.DataProvider)
	}

	// Initialize Database
	db, err := data.NewDB(cfg.DatabasePath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize database")
	}
	defer db.Close()

	// Initialize Order Store
	orderStore := data.NewOrderStore(db)

	// Initialize Execution Layer (Paper Trading for now)
	initialCash := 100000.0
	broker := execution.NewPaperBroker(initialCash)
	if err := broker.Connect(); err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to paper broker")
	}

	// Initialize Order Manager with persistence and WebSocket
	orderManager := execution.NewOrderManager(broker, nil, orderStore, wsManager)

	// Restore orders from database
	if err := orderManager.LoadOrders(); err != nil {
		log.Warn().Err(err).Msg("Failed to load orders from database")
	}

	// Initialize Trading Engine
	// Hardcoded symbols for now
	symbols := []string{"SPY", "BTC-USD", "ETH-USD", "AAPL", "MSFT"}
	tradingEngine := engine.NewTradingEngine(
		provider,
		registry,
		orderManager,
		wsManager,
		symbols,
		1*time.Minute,    // Tick every minute
		100*24*time.Hour, // Lookback 100 days
	)

	// Start Trading Engine
	ctx, cancelEngine := context.WithCancel(context.Background())
	if err := tradingEngine.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to start trading engine")
	}

	// Create API router with WebSocket Manager
	router := api.NewRouter(cfg, registry, provider, orderManager, tradingEngine, wsManager)

	// Create HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.ServerHost, cfg.ServerPort),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Info().Msgf("🚀 API server listening on %s:%d", cfg.ServerHost, cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}()

	// Wait for interrupt signal for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down server...")

	// Stop Trading Engine
	cancelEngine()
	tradingEngine.Stop()

	// Given outstanding requests 30 seconds to complete
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()

	if err := server.Shutdown(ctxShutdown); err != nil {
		log.Fatal().Err(err).Msg("Server forced to shutdown")
	}

	log.Info().Msg("Server exited gracefully")
}
