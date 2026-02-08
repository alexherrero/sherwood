package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
    "github.com/alexherrero/sherwood/backend/internal/api"
	"github.com/alexherrero/sherwood/backend/internal/data"
	"github.com/alexherrero/sherwood/backend/internal/engine"
	"github.com/alexherrero/sherwood/backend/internal/strategies"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Heartbeat("/ping"))

	// Initialize Core Components
	provider := data.NewMockProvider()
	eng := engine.NewEngine(provider, "MOCK-BTC")
	
	// Add default strategy
	strat := strategies.NewCrossoverStrategy(10, 20)
	eng.AddStrategy(strat)

	// Start Engine in background
	go func() {
		if err := eng.Start(context.Background()); err != nil {
			log.Printf("Engine stopped with error: %v", err)
		}
	}()

	apiHandler := api.NewHandler(eng)

	// Mount API routes
	r.Mount("/", apiHandler.Routes())

	log.Printf("Starting server on port %s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
