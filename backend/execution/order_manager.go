// Package execution provides order management functionality.
package execution

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/rs/zerolog/log"
)

// OrderStore defines persistence operations for orders and positions.
type OrderStore interface {
	SaveOrder(order models.Order) error
	GetOrder(orderID string) (*models.Order, error)
	GetAllOrders() ([]models.Order, error)
	SavePosition(position models.Position) error
	GetAllPositions() ([]models.Position, error)
}

// OrderManager handles order lifecycle and execution.
type OrderManager struct {
	broker      Broker
	riskManager *RiskManager
	orders      map[string]models.Order // In-memory cache
	store       OrderStore              // Database persistence
	mu          sync.RWMutex
}

// NewOrderManager creates a new order manager.
//
// Args:
//   - broker: The broker for order execution
//   - riskManager: Risk manager for position limits
//   - store: Optional persistent storage for orders (can be nil)
//
// Returns:
//   - *OrderManager: The order manager instance
func NewOrderManager(broker Broker, riskManager *RiskManager, store OrderStore) *OrderManager {
	return &OrderManager{
		broker:      broker,
		riskManager: riskManager,
		orders:      make(map[string]models.Order),
		store:       store,
	}
}

// SubmitOrder validates and submits an order for execution.
//
// Args:
//   - order: The order to submit
//
// Returns:
//   - *models.Order: The submitted order
//   - error: Any error encountered
func (om *OrderManager) SubmitOrder(order models.Order) (*models.Order, error) {
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

	log.Info().
		Str("order_id", result.ID).
		Str("symbol", result.Symbol).
		Str("side", string(result.Side)).
		Str("status", string(result.Status)).
		Msg("Order submitted")

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
//
// Args:
//   - orderID: ID of the order to cancel
//
// Returns:
//   - error: Any error encountered
func (om *OrderManager) CancelOrder(orderID string) error {
	return om.broker.CancelOrder(orderID)
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
func (om *OrderManager) GetAllOrders() []models.Order {
	orders, _, _ := om.GetOrders(OrderFilter{})
	return orders
}

// CreateMarketOrder creates a market order.
//
// Args:
//   - symbol: Ticker symbol
//   - side: Buy or sell
//   - quantity: Amount to trade
//
// Returns:
//   - *models.Order: The submitted order
//   - error: Any error encountered
func (om *OrderManager) CreateMarketOrder(symbol string, side models.OrderSide, quantity float64) (*models.Order, error) {
	order := models.Order{
		Symbol:    symbol,
		Side:      side,
		Type:      models.OrderTypeMarket,
		Quantity:  quantity,
		Status:    models.OrderStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return om.SubmitOrder(order)
}

// CreateLimitOrder creates a limit order.
//
// Args:
//   - symbol: Ticker symbol
//   - side: Buy or sell
//   - quantity: Amount to trade
//   - price: Limit price
//
// Returns:
//   - *models.Order: The submitted order
//   - error: Any error encountered
func (om *OrderManager) CreateLimitOrder(symbol string, side models.OrderSide, quantity, price float64) (*models.Order, error) {
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
	return om.SubmitOrder(order)
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
