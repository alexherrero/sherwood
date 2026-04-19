package api

import (
	"context"
	"encoding/json"
	"net/http"
)

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
