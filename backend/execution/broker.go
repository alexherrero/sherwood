// Package execution provides trade execution and order management.
package execution

import (
	"github.com/alexherrero/sherwood/backend/models"
)

// Broker defines the interface for executing trades.
// Implementations connect to real brokers (Robinhood, Alpaca) or paper trading.
type Broker interface {
	// Name returns the broker name.
	Name() string

	// Connect establishes connection to the broker.
	//
	// Returns:
	//   - error: Any connection error
	Connect() error

	// Disconnect closes the broker connection.
	//
	// Returns:
	//   - error: Any error during disconnect
	Disconnect() error

	// IsConnected returns true if connected to the broker.
	IsConnected() bool

	// PlaceOrder submits an order to the broker.
	//
	// Args:
	//   - order: The order to place
	//
	// Returns:
	//   - *models.Order: The submitted order with ID
	//   - error: Any error encountered
	PlaceOrder(order models.Order) (*models.Order, error)

	// CancelOrder cancels a pending order.
	//
	// Args:
	//   - orderID: ID of the order to cancel
	//
	// Returns:
	//   - error: Any error encountered
	CancelOrder(orderID string) error

	// GetOrder retrieves an order by ID.
	//
	// Args:
	//   - orderID: ID of the order
	//
	// Returns:
	//   - *models.Order: The order
	//   - error: Any error encountered
	GetOrder(orderID string) (*models.Order, error)

	// GetPositions retrieves all current positions.
	//
	// Returns:
	//   - []models.Position: Current positions
	//   - error: Any error encountered
	GetPositions() ([]models.Position, error)

	// GetPosition retrieves a specific position.
	//
	// Args:
	//   - symbol: The ticker symbol
	//
	// Returns:
	//   - *models.Position: The position
	//   - error: Any error encountered
	GetPosition(symbol string) (*models.Position, error)

	// GetBalance retrieves account balance.
	//
	// Returns:
	//   - *models.Balance: Account balance
	//   - error: Any error encountered
	GetBalance() (*models.Balance, error)

	// GetTrades retrieves executed trades.
	//
	// Returns:
	//   - []models.Trade: Executed trades
	//   - error: Any error encountered
	GetTrades() ([]models.Trade, error)

	// ModifyOrder updates an existing open order.
	//
	// Args:
	//   - orderID: ID of the order to modify
	//   - newPrice: New limit price (0 to keep current)
	//   - newQuantity: New quantity (0 to keep current)
	//
	// Returns:
	//   - *models.Order: The modified order
	//   - error: Any error encountered
	ModifyOrder(orderID string, newPrice, newQuantity float64) (*models.Order, error)
}
