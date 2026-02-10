package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ListStrategiesHandler returns all available trading strategies.
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
