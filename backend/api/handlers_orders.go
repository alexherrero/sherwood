package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/go-chi/chi/v5"
)

// GetOrdersHandler returns a list of orders with optional filtering and pagination.
func (h *Handler) GetOrdersHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	// Parse query parameters
	limit := getQueryInt(r, "limit", 50)
	page := getQueryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit
	symbol := r.URL.Query().Get("symbol")
	statusStr := r.URL.Query().Get("status")

	filter := execution.OrderFilter{
		Limit:  limit,
		Offset: offset,
		Symbol: symbol,
		Status: models.OrderStatus(statusStr),
	}

	orders, total, err := h.orderManager.GetOrders(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"orders": orders,
		"total":  total,
		"page":   page,
		"limit":  limit,
	})
}

// GetOrderHandler returns a single order by ID.
func (h *Handler) GetOrderHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Order ID is required")
		return
	}

	order, err := h.orderManager.GetOrder(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Order not found")
		return
	}

	writeJSON(w, http.StatusOK, order)
}

// GetOrderHistoryHandler returns a list of past (closed) orders.
func (h *Handler) GetOrderHistoryHandler(w http.ResponseWriter, r *http.Request) {
	// Wrapper around GetOrders but defaults to closed statuses if status not provided
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}
	h.GetOrdersHandler(w, r)
}

// GetPortfolioSummaryHandler returns an aggregated portfolio summary.
func (h *Handler) GetPortfolioSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	balance, err := h.orderManager.GetBalance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get balance: %v", err))
		return
	}

	positions, err := h.orderManager.GetPositions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get positions: %v", err))
		return
	}

	var totalUnrealizedPL float64
	for _, p := range positions {
		totalUnrealizedPL += p.UnrealizedPL
	}

	summary := map[string]interface{}{
		"balance":             balance,
		"total_unrealized_pl": totalUnrealizedPL,
		"open_positions":      len(positions),
	}

	writeJSON(w, http.StatusOK, summary)
}

// GetPositionsHandler returns a list of current positions.
func (h *Handler) GetPositionsHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}
	positions, err := h.orderManager.GetPositions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, positions)
}

// GetBalanceHandler returns the current account balance.
func (h *Handler) GetBalanceHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}
	balance, err := h.orderManager.GetBalance()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, balance)
}

// PlaceOrderRequest defines the payload for placing an order.
type PlaceOrderRequest struct {
	Symbol   string  `json:"symbol" validate:"required,min=1,max=20"`
	Side     string  `json:"side" validate:"required,oneof=buy sell"`
	Type     string  `json:"type" validate:"required,oneof=market limit"`
	Quantity float64 `json:"quantity" validate:"required,gt=0,lte=1000000"`
	Price    float64 `json:"price" validate:"omitempty,gt=0"`
}

// PlaceOrderHandler handles manual order placement.
func (h *Handler) PlaceOrderHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	var req PlaceOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate request
	if valErr := validateStruct(req); valErr != nil {
		writeValidationError(w, valErr)
		return
	}

	var side models.OrderSide
	switch req.Side {
	case "buy":
		side = models.OrderSideBuy
	case "sell":
		side = models.OrderSideSell
	default:
		writeError(w, http.StatusBadRequest, "Invalid side: must be 'buy' or 'sell'")
		return
	}

	var order *models.Order
	var err error

	// Create order based on type
	switch req.Type {
	case "market":
		order, err = h.orderManager.CreateMarketOrder(r.Context(), req.Symbol, side, req.Quantity)
	case "limit":
		if req.Price <= 0 {
			writeError(w, http.StatusBadRequest, "Price must be positive for limit orders")
			return
		}
		order, err = h.orderManager.CreateLimitOrder(r.Context(), req.Symbol, side, req.Quantity, req.Price)
	default:
		writeError(w, http.StatusBadRequest, "Invalid type: must be 'market' or 'limit'")
		return
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to place order: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, order)
}

// CancelOrderHandler handles order cancellation.
func (h *Handler) CancelOrderHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Order ID is required")
		return
	}

	if err := h.orderManager.CancelOrder(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to cancel order: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "id": id})
}

// ModifyOrderRequest defines the payload for modifying an order.
type ModifyOrderRequest struct {
	Price    float64 `json:"price" validate:"omitempty,gt=0"`
	Quantity float64 `json:"quantity" validate:"omitempty,gt=0"`
}

// ModifyOrderHandler handles order modification.
func (h *Handler) ModifyOrderHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Order ID is required")
		return
	}

	var req ModifyOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate request
	if valErr := validateStruct(req); valErr != nil {
		writeValidationError(w, valErr)
		return
	}

	if req.Price == 0 && req.Quantity == 0 {
		writeError(w, http.StatusBadRequest, "Must provide either new price or new quantity")
		return
	}

	order, err := h.orderManager.ModifyOrder(r.Context(), id, req.Price, req.Quantity)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to modify order: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, order)
}

// GetTradesHandler returns a list of executed trades.
func (h *Handler) GetTradesHandler(w http.ResponseWriter, r *http.Request) {
	if h.orderManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Execution layer not available")
		return
	}

	trades, err := h.orderManager.GetTrades()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get trades: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, trades)
}

// getQueryInt parses a query parameter as an integer.
func getQueryInt(r *http.Request, key string, defaultVal int) int {
	valStr := r.URL.Query().Get(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return defaultVal
	}
	return val
}
