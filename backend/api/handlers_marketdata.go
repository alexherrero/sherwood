package api

import (
	"fmt"
	"net/http"
	"time"
)

// GetHistoricalDataHandler returns historical market data.
func (h *Handler) GetHistoricalDataHandler(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, "Symbol is required")
		return
	}

	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	interval := r.URL.Query().Get("interval")

	if interval == "" {
		interval = "1d"
	}

	// Default to last 30 days if not specified
	end := time.Now()
	if endStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endStr); err == nil {
			end = parsed
		}
	}

	start := end.AddDate(0, 0, -30)
	if startStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startStr); err == nil {
			start = parsed
		}
	}

	data, err := h.provider.GetHistoricalData(symbol, start, end, interval)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to fetch data: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, data)
}
