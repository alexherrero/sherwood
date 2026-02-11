// Package execution provides order management functionality.
package execution

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/realtime"
	"github.com/rs/zerolog/log"
)

// OrderStore defines persistence operations for orders and positions.
type OrderStore interface {
	SaveOrder(order models.Order) error
	GetOrder(orderID string) (*models.Order, error)
	GetAllOrders() ([]models.Order, error)
	SavePosition(position models.Position) error
	GetAllPositions() ([]models.Position, error)
	GetSystemConfig(key string) (string, error)
	SetSystemConfig(key, value string) error
}

// OrderManager handles order lifecycle and execution.
type OrderManager struct {
	broker      Broker
	riskManager *RiskManager
	orders      map[string]models.Order // In-memory cache
	store       OrderStore              // Database persistence
	wsManager   *realtime.WebSocketManager
	mu          sync.RWMutex
}

// NewOrderManager creates a new order manager.
//
// Args:
//   - broker: The broker for order execution
//   - riskManager: Risk manager for position limits
//   - store: Optional persistent storage for orders (can be nil)
//   - wsManager: WebSocket manager for real-time updates (can be nil)
//
// Returns:
//   - *OrderManager: The order manager instance
func NewOrderManager(
	broker Broker,
	riskManager *RiskManager,
	store OrderStore,
	wsManager *realtime.WebSocketManager,
) *OrderManager {
	return &OrderManager{
		broker:      broker,
		riskManager: riskManager,
		orders:      make(map[string]models.Order),
		store:       store,
		wsManager:   wsManager,
	}
}

// SubmitOrder validates and submits an order for execution.
// The context carries audit information (user IP, API key ID) for logging.
//
// Args:
//   - ctx: Context with audit information
//   - order: The order to submit
//
// Returns:
//   - *models.Order: The submitted order
//   - error: Any error encountered
func (om *OrderManager) SubmitOrder(ctx context.Context, order models.Order) (*models.Order, error) {
	// Validate order
	if err := om.validateOrder(order); err != nil {
		return nil, fmt.Errorf("order validation failed: %w", err)
	}

	// Check risk limits
	if om.riskManager != nil {
		if err := om.riskManager.CheckOrder(order); err != nil {
			return nil, fmt.Errorf("risk check failed: %w", err)
		}
	}

	// Submit to broker
	result, err := om.broker.PlaceOrder(order)
	if err != nil {
		return nil, fmt.Errorf("broker rejected order: %w", err)
	}

	// Store order in memory
	om.mu.Lock()
	om.orders[result.ID] = *result
	om.mu.Unlock()

	// Persist to database
	if om.store != nil {
		if err := om.store.SaveOrder(*result); err != nil {
			log.Error().Err(err).Str("order_id", result.ID).Msg("Failed to persist order")
		}
	}

	// Audit log with requestor context
	log.Info().
		Str("order_id", result.ID).
		Str("symbol", result.Symbol).
		Str("side", string(result.Side)).
		Str("type", string(result.Type)).
		Float64("quantity", result.Quantity).
		Float64("price", result.Price).
		Str("status", string(result.Status)).
		Str("user_ip", auditIPFromCtx(ctx)).
		Str("api_key_id", auditKeyIDFromCtx(ctx)).
		Msg("Order submitted")

	// Broadcast update
	if om.wsManager != nil {
		om.wsManager.Broadcast("order_update", result)
	}

	return result, nil
}

// validateOrder checks basic order validity.
func (om *OrderManager) validateOrder(order models.Order) error {
	if order.Symbol == "" {
		return fmt.Errorf("symbol is required")
	}
	if order.Quantity <= 0 {
		return fmt.Errorf("quantity must be positive")
	}
	if order.Type == models.OrderTypeLimit && order.Price <= 0 {
		return fmt.Errorf("limit orders require a positive price")
	}
	return nil
}

// LoadOrders restores orders from persistent storage on startup.
//
// This method should be called after creating the OrderManager to restore
// state from previous sessions. It loads all orders from the database
// into the in-memory cache.
//
// Returns:
//   - error: Any error encountered during load
func (om *OrderManager) LoadOrders() error {
	if om.store == nil {
		return nil // No persistence configured
	}

	orders, err := om.store.GetAllOrders()
	if err != nil {
		return fmt.Errorf("failed to load orders: %w", err)
	}

	om.mu.Lock()
	defer om.mu.Unlock()

	for _, order := range orders {
		om.orders[order.ID] = order
	}

	log.Info().Int("count", len(orders)).Msg("Loaded orders from database")
	return nil
}

// CancelOrder cancels an order.
// The context carries audit information (user IP, API key ID) for logging.
//
// Args:
//   - ctx: Context with audit information
//   - orderID: ID of the order to cancel
//
// Returns:
//   - error: Any error encountered
func (om *OrderManager) CancelOrder(ctx context.Context, orderID string) error {
	log.Info().
		Str("order_id", orderID).
		Str("user_ip", auditIPFromCtx(ctx)).
		Str("api_key_id", auditKeyIDFromCtx(ctx)).
		Msg("Order cancellation requested")

	err := om.broker.CancelOrder(orderID)
	if err != nil {
		log.Warn().
			Str("order_id", orderID).
			Err(err).
			Msg("Order cancellation failed")
	}
	return err
}

// GetOrder retrieves an order by ID.
//
// Args:
//   - orderID: ID of the order
//
// Returns:
//   - *models.Order: The order
//   - error: Any error encountered
func (om *OrderManager) GetOrder(orderID string) (*models.Order, error) {
	// Check local cache first
	om.mu.RLock()
	order, exists := om.orders[orderID]
	om.mu.RUnlock()

	if exists {
		return &order, nil
	}

	// Fetch from broker
	return om.broker.GetOrder(orderID)
}

// OrderFilter defines criteria for filtering orders.
type OrderFilter struct {
	Symbol string
	Status models.OrderStatus
	Limit  int
	Offset int
}

// GetOrders retrieves orders matching the filter criteria.
//
// Args:
//   - filter: Filter criteria
//
// Returns:
//   - []models.Order: Matching orders
//   - int: Total count of matching orders (before pagination)
//   - error: Any error encountered
func (om *OrderManager) GetOrders(filter OrderFilter) ([]models.Order, int, error) {
	om.mu.RLock()
	defer om.mu.RUnlock()

	// 1. Filter
	var filtered []models.Order
	for _, order := range om.orders {
		if filter.Symbol != "" && order.Symbol != filter.Symbol {
			continue
		}
		if filter.Status != "" && order.Status != filter.Status {
			continue
		}
		filtered = append(filtered, order)
	}

	totalCount := len(filtered)

	// 2. Sort (by CreatedAt descending)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	// 3. Paginate
	if filter.Offset >= totalCount {
		return []models.Order{}, totalCount, nil
	}

	end := filter.Offset + filter.Limit
	if filter.Limit == 0 {
		end = totalCount // No limit
	}
	if end > totalCount {
		end = totalCount
	}

	return filtered[filter.Offset:end], totalCount, nil
}

// GetAllOrders returns all tracked orders.
//
// Returns:
//   - []models.Order: All orders
//   - error: Any error encountered
func (om *OrderManager) GetAllOrders() ([]models.Order, error) {
	orders, _, err := om.GetOrders(OrderFilter{})
	return orders, err
}

// CreateMarketOrder creates a market order.
// The context carries audit information (user IP, API key ID) for logging.
//
// Args:
//   - ctx: Context with audit information
//   - symbol: Ticker symbol
//   - side: Buy or sell
//   - quantity: Amount to trade
//
// Returns:
//   - *models.Order: The submitted order
//   - error: Any error encountered
func (om *OrderManager) CreateMarketOrder(ctx context.Context, symbol string, side models.OrderSide, quantity float64) (*models.Order, error) {
	order := models.Order{
		Symbol:    symbol,
		Side:      side,
		Type:      models.OrderTypeMarket,
		Quantity:  quantity,
		Status:    models.OrderStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return om.SubmitOrder(ctx, order)
}

// CreateLimitOrder creates a limit order.
// The context carries audit information (user IP, API key ID) for logging.
//
// Args:
//   - ctx: Context with audit information
//   - symbol: Ticker symbol
//   - side: Buy or sell
//   - quantity: Amount to trade
//   - price: Limit price
//
// Returns:
//   - *models.Order: The submitted order
//   - error: Any error encountered
func (om *OrderManager) CreateLimitOrder(ctx context.Context, symbol string, side models.OrderSide, quantity, price float64) (*models.Order, error) {
	order := models.Order{
		Symbol:    symbol,
		Side:      side,
		Type:      models.OrderTypeLimit,
		Quantity:  quantity,
		Price:     price,
		Status:    models.OrderStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return om.SubmitOrder(ctx, order)
}

// GetPositions retrieves all current positions from the broker.
//
// Returns:
//   - []models.Position: Current positions
//   - error: Any error encountered
func (om *OrderManager) GetPositions() ([]models.Position, error) {
	return om.broker.GetPositions()
}

// GetBalance retrieves the current account balance from the broker.
//
// Returns:
//   - *models.Balance: Account balance
//   - error: Any error encountered
func (om *OrderManager) GetBalance() (*models.Balance, error) {
	return om.broker.GetBalance()
}

// GetTrades retrieves executed trades from the broker.
//
// Returns:
//   - []models.Trade: Executed trades
//   - error: Any error encountered
func (om *OrderManager) GetTrades() ([]models.Trade, error) {
	return om.broker.GetTrades()
}

// ModifyOrder modifies an existing open order.
// The context carries audit information (user IP, API key ID) for logging.
//
// Args:
//   - ctx: Context with audit information
//   - orderID: ID of the order to modify
//   - newPrice: New limit price (0 to keep current)
//   - newQuantity: New quantity (0 to keep current)
//
// Returns:
//   - *models.Order: The modified order
//   - error: Any error encountered
func (om *OrderManager) ModifyOrder(ctx context.Context, orderID string, newPrice, newQuantity float64) (*models.Order, error) {
	log.Info().
		Str("order_id", orderID).
		Float64("new_price", newPrice).
		Float64("new_quantity", newQuantity).
		Str("user_ip", auditIPFromCtx(ctx)).
		Str("api_key_id", auditKeyIDFromCtx(ctx)).
		Msg("Order modification requested")

	order, err := om.broker.ModifyOrder(orderID, newPrice, newQuantity)
	if err != nil {
		log.Warn().
			Str("order_id", orderID).
			Err(err).
			Msg("Order modification failed")
		return nil, err
	}

	// Update local cache
	om.mu.Lock()
	om.orders[order.ID] = *order
	om.mu.Unlock()

	// Update persistence
	if om.store != nil {
		if err := om.store.SaveOrder(*order); err != nil {
			log.Error().Err(err).Str("order_id", order.ID).Msg("Failed to persist modified order")
		}
	}

	return order, nil
}

// GetInitialCapital retrieves the initial capital from configuration.
func (om *OrderManager) GetInitialCapital() (float64, error) {
	if om.store == nil {
		return 0, nil
	}

	valStr, err := om.store.GetSystemConfig("initial_capital")
	if err != nil {
		// Treat missing key as not found/default
		return 0, err
	}

	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid initial capital value '%s': %w", valStr, err)
	}

	return val, nil
}

// SetInitialCapital stores the initial capital in configuration.
func (om *OrderManager) SetInitialCapital(amount float64) error {
	if om.store == nil {
		return fmt.Errorf("no persistence configured")
	}

	valStr := strconv.FormatFloat(amount, 'f', 2, 64)
	return om.store.SetSystemConfig("initial_capital", valStr)
}
