package api

import (
	"encoding/json"
	"net/http"
)

func (h *Handler) StartEngine(w http.ResponseWriter, r *http.Request) {
    // In a real app, we'd handle context cancellation properly 
    // and likely run this in a goroutine if not already running
    // For now, since it's already running in main, this might just be a no-op 
    // or we change the design to let API control start/stop.
    // Let's assume for this step we just acknowledge.
    
    // TODO: Connect to actual engine start/stop logic if needed distinct from main startup
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"message": "Engine start request received"})
}

func (h *Handler) StopEngine(w http.ResponseWriter, r *http.Request) {
    h.engine.Stop()
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"message": "Engine stop request received"})
}
