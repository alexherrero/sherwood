package api

import (
	"fmt"
	"net/http"

	"github.com/alexherrero/sherwood/backend/analysis"
)

// GetPortfolioPerformanceHandler returns aggregate performance metrics.
//
// @Summary      Get Performance Metrics
// @Description  Calculates and returns performance metrics based on trade history.
// @Tags         portfolio
// @Accept       json
// @Produce      json
// @Success      200  {object}  analysis.PerformanceMetrics
// @Failure      500  {object}  ErrorResponse
// @Router       /portfolio/performance [get]
func (h *Handler) GetPortfolioPerformanceHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Get all orders (including filled ones)
	// We use GetAllOrders because we want to analyze the entire history
	// In a real system, we might want date range filtering, but for now global metrics.
	orders, err := h.orderManager.GetAllOrders()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to retrieve order history: %v", err))
		return
	}

	// 2. Determine initial capital base for equity curve
	initialCapital := 100000.0 // Default fallback for paper trading

	storedCapital, err := h.orderManager.GetInitialCapital()
	if err == nil && storedCapital > 0 {
		initialCapital = storedCapital
	}

	// 3. Calculate metrics
	metrics := analysis.CalculateMetrics(orders, initialCapital)

	// 4. Return JSON
	writeJSON(w, http.StatusOK, metrics)
}
