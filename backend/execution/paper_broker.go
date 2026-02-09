// Package execution provides paper trading broker implementation.
package execution

import (
	"fmt"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/rs/zerolog/log"
)

// PaperBroker simulates a broker for paper trading.
// No real money is at risk - all trades are simulated.
type PaperBroker struct {
	name         string
	connected    bool
	balance      models.Balance
	positions    map[string]models.Position
	orders       map[string]models.Order
	orderCounter int
	mu           sync.RWMutex
	latestPrices map[string]float64
}

// NewPaperBroker creates a new paper trading broker.
//
// Args:
//   - initialCash: Starting cash balance
//
// Returns:
//   - *PaperBroker: The paper broker instance
func NewPaperBroker(initialCash float64) *PaperBroker {
	return &PaperBroker{
		name:      "paper",
		connected: false,
		balance: models.Balance{
			Cash:           initialCash,
			Equity:         initialCash,
			BuyingPower:    initialCash,
			PortfolioValue: initialCash,
			UpdatedAt:      time.Now(),
		},
		positions:    make(map[string]models.Position),
		orders:       make(map[string]models.Order),
		orderCounter: 0,
		latestPrices: make(map[string]float64),
	}
}

// Name returns the broker name.
func (b *PaperBroker) Name() string {
	return b.name
}

// Connect establishes connection (instant for paper trading).
func (b *PaperBroker) Connect() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected = true
	log.Info().Msg("Paper broker connected")
	return nil
}

// Disconnect closes the connection.
func (b *PaperBroker) Disconnect() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected = false
	log.Info().Msg("Paper broker disconnected")
	return nil
}

// IsConnected returns true if connected.
func (b *PaperBroker) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connected
}

// SetPrice sets the latest price for a symbol (for simulation).
//
// Args:
//   - symbol: Ticker symbol
//   - price: Current price
func (b *PaperBroker) SetPrice(symbol string, price float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.latestPrices[symbol] = price
}

// PlaceOrder simulates order execution.
func (b *PaperBroker) PlaceOrder(order models.Order) (*models.Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.connected {
		return nil, fmt.Errorf("broker not connected")
	}

	// Generate order ID
	b.orderCounter++
	order.ID = fmt.Sprintf("paper-%06d", b.orderCounter)
	order.CreatedAt = time.Now()
	order.UpdatedAt = time.Now()
	order.Status = models.OrderStatusSubmitted

	// Get price for execution
	price := order.Price
	if order.Type == models.OrderTypeMarket {
		if latestPrice, ok := b.latestPrices[order.Symbol]; ok {
			price = latestPrice
		} else {
			return nil, fmt.Errorf("no price available for %s", order.Symbol)
		}
	}

	// Check if we have enough buying power for buys
	if order.Side == models.OrderSideBuy {
		cost := price * order.Quantity
		if cost > b.balance.BuyingPower {
			order.Status = models.OrderStatusRejected
			b.orders[order.ID] = order
			return &order, fmt.Errorf("insufficient buying power: need %.2f, have %.2f",
				cost, b.balance.BuyingPower)
		}
	}

	// Simulate instant fill for paper trading
	order.Status = models.OrderStatusFilled
	order.FilledQuantity = order.Quantity
	order.AveragePrice = price
	order.UpdatedAt = time.Now()

	// Update positions
	if order.Side == models.OrderSideBuy {
		b.executeBuy(order.Symbol, order.Quantity, price)
	} else {
		b.executeSell(order.Symbol, order.Quantity, price)
	}

	b.orders[order.ID] = order

	log.Info().
		Str("order_id", order.ID).
		Str("symbol", order.Symbol).
		Str("side", string(order.Side)).
		Float64("quantity", order.Quantity).
		Float64("price", price).
		Msg("Paper order executed")

	return &order, nil
}

// executeBuy updates positions and balance for a buy order.
func (b *PaperBroker) executeBuy(symbol string, quantity, price float64) {
	cost := quantity * price

	// Update balance
	b.balance.Cash -= cost
	b.balance.BuyingPower -= cost
	b.balance.UpdatedAt = time.Now()

	// Update or create position
	pos, exists := b.positions[symbol]
	if exists {
		totalQty := pos.Quantity + quantity
		totalCost := (pos.AverageCost * pos.Quantity) + cost
		pos.AverageCost = totalCost / totalQty
		pos.Quantity = totalQty
	} else {
		pos = models.Position{
			Symbol:      symbol,
			Quantity:    quantity,
			AverageCost: price,
		}
	}
	pos.CurrentPrice = price
	pos.MarketValue = pos.Quantity * price
	pos.UnrealizedPL = pos.MarketValue - (pos.Quantity * pos.AverageCost)
	pos.UpdatedAt = time.Now()
	b.positions[symbol] = pos
}

// executeSell updates positions and balance for a sell order.
func (b *PaperBroker) executeSell(symbol string, quantity, price float64) {
	proceeds := quantity * price

	// Update balance
	b.balance.Cash += proceeds
	b.balance.BuyingPower += proceeds
	b.balance.UpdatedAt = time.Now()

	// Update position
	pos, exists := b.positions[symbol]
	if exists {
		pos.Quantity -= quantity
		if pos.Quantity <= 0 {
			delete(b.positions, symbol)
		} else {
			pos.CurrentPrice = price
			pos.MarketValue = pos.Quantity * price
			pos.UnrealizedPL = pos.MarketValue - (pos.Quantity * pos.AverageCost)
			pos.UpdatedAt = time.Now()
			b.positions[symbol] = pos
		}
	}
}

// CancelOrder cancels a pending order.
func (b *PaperBroker) CancelOrder(orderID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	order, exists := b.orders[orderID]
	if !exists {
		return fmt.Errorf("order not found: %s", orderID)
	}

	if order.Status == models.OrderStatusFilled {
		return fmt.Errorf("cannot cancel filled order: %s", orderID)
	}

	order.Status = models.OrderStatusCancelled
	order.UpdatedAt = time.Now()
	b.orders[orderID] = order
	return nil
}

// GetOrder retrieves an order by ID.
func (b *PaperBroker) GetOrder(orderID string) (*models.Order, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	order, exists := b.orders[orderID]
	if !exists {
		return nil, fmt.Errorf("order not found: %s", orderID)
	}
	return &order, nil
}

// GetPositions retrieves all current positions.
func (b *PaperBroker) GetPositions() ([]models.Position, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	positions := make([]models.Position, 0, len(b.positions))
	for _, pos := range b.positions {
		positions = append(positions, pos)
	}
	return positions, nil
}

// GetPosition retrieves a specific position.
func (b *PaperBroker) GetPosition(symbol string) (*models.Position, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	pos, exists := b.positions[symbol]
	if !exists {
		return nil, fmt.Errorf("no position for %s", symbol)
	}
	return &pos, nil
}

// GetBalance retrieves account balance.
func (b *PaperBroker) GetBalance() (*models.Balance, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return &b.balance, nil
}
