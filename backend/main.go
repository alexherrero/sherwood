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
	"github.com/alexherrero/sherwood/backend/data/providers"
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

	// Initialize Strategy Registry
	registry := strategies.NewRegistry()
	if err := registry.Register(strategies.NewMACrossover()); err != nil {
		log.Error().Err(err).Msg("Failed to register MA Crossover strategy")
	}

	// Initialize Data Provider
	// Default to Yahoo for now as it requires no configuration
	// In the future, this could be configured via config.yaml
	provider := providers.NewYahooProvider()

	// Create API router
	router := api.NewRouter(cfg, registry, provider)

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

	// Give outstanding requests 30 seconds to complete
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal().Err(err).Msg("Server forced to shutdown")
	}

	log.Info().Msg("Server exited gracefully")
}
