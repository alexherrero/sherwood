package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/alexherrero/sherwood/backend/internal/engine"
)

type Handler struct {
	engine *engine.Engine
}

func NewHandler(e *engine.Engine) *Handler {
	return &Handler{
		engine: e,
	}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	
	// Public routes
	r.Get("/status", h.GetStatus)
	
	// API group
	r.Route("/api", func(r chi.Router) {
		r.Get("/health", h.GetStatus)
		r.Post("/engine/start", h.StartEngine)
		r.Post("/engine/stop", h.StopEngine)
	})
	
	return r
}

func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "online", 
		"version": "0.1.0",
		"mode": "development", // TODO: Read from config
	})
}
