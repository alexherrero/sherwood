package api

import (
	"encoding/json"
	"net/http"
)

// healthHandler returns the health status of the API.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// listStrategiesHandler returns a list of available strategies.
// TODO: Implement with strategy registry
func listStrategiesHandler(w http.ResponseWriter, r *http.Request) {
	strategies := []map[string]interface{}{
		{
			"name":        "ma_crossover",
			"description": "Moving Average Crossover Strategy",
			"parameters": map[string]interface{}{
				"short_period": 10,
				"long_period":  20,
			},
		},
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"strategies": strategies,
	})
}

// getStrategyHandler returns details for a specific strategy.
// TODO: Implement with strategy registry
func getStrategyHandler(w http.ResponseWriter, r *http.Request) {
	// Placeholder response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":        "ma_crossover",
		"description": "Moving Average Crossover Strategy",
		"parameters": map[string]interface{}{
			"short_period": map[string]interface{}{
				"type":    "int",
				"default": 10,
				"min":     2,
				"max":     50,
			},
			"long_period": map[string]interface{}{
				"type":    "int",
				"default": 20,
				"min":     5,
				"max":     200,
			},
		},
	})
}

// runBacktestHandler starts a new backtest.
// TODO: Implement with backtest engine
func runBacktestHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"id":      "backtest-001",
		"status":  "pending",
		"message": "Backtest queued for execution",
	})
}

// getBacktestResultHandler returns results for a completed backtest.
// TODO: Implement with backtest storage
func getBacktestResultHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":     "backtest-001",
		"status": "completed",
		"metrics": map[string]interface{}{
			"total_return":  0.0,
			"sharpe_ratio":  0.0,
			"max_drawdown":  0.0,
			"total_trades":  0,
			"winning_trades": 0,
		},
	})
}

// getConfigHandler returns the current configuration (non-sensitive).
func getConfigHandler(w http.ResponseWriter, r *http.Request) {
	// Only expose non-sensitive configuration
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version": "0.1.0",
		"api":     "v1",
	})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
