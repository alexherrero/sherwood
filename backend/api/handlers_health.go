package api

import (
	"net/http"
	"runtime"
	"time"
)

// HealthHandler returns the health status of the API.
func (h *Handler) HealthHandler(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]string)
	status := "ok"

	// Check Broker
	if h.orderManager != nil {
		checks["execution"] = "active"
	} else {
		checks["execution"] = "disabled"
	}

	// Check Data Provider
	if h.provider != nil {
		checks["data_provider"] = h.provider.Name()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    status,
		"mode":      string(h.config.TradingMode),
		"timestamp": time.Now(),
		"checks":    checks,
	})
}

// MetricsHandler returns basic runtime statistics.
func (h *Handler) MetricsHandler(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	metrics := map[string]interface{}{
		"goroutines": runtime.NumGoroutine(),
		"memory": map[string]uint64{
			"alloc":       m.Alloc,
			"total_alloc": m.TotalAlloc,
			"sys":         m.Sys,
			"num_gc":      uint64(m.NumGC),
		},
		"uptime_seconds": time.Since(h.startTime).Seconds(),
		"timestamp":      time.Now(),
	}

	writeJSON(w, http.StatusOK, metrics)
}
