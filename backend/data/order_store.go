// Package data provides database connection and persistence.
package data

import (
	"fmt"

	"github.com/alexherrero/sherwood/backend/models"
)

// OrderStore provides persistence operations for orders and positions.
//
// This interface defines methods for saving and retrieving trading orders
// and positions from the database, enabling state to survive application restarts.
type OrderStore interface {
	// SaveOrder persists an order to the database.
	//
	// Args:
	//   - order: The order to save
	//
	// Returns:
	//   - error: Any error encountered during save
	SaveOrder(order models.Order) error

	// GetOrder retrieves an order by ID.
	//
	// Args:
	//   - orderID: Unique identifier of the order
	//
	// Returns:
	//   - *models.Order: The order if found
	//   - error: Any error encountered, or ErrNotFound if order doesn't exist
	GetOrder(orderID string) (*models.Order, error)

	// GetAllOrders retrieves all orders from the database.
	//
	// Returns:
	//   - []models.Order: All persisted orders
	//   - error: Any error encountered
	GetAllOrders() ([]models.Order, error)

	// DeleteOrder removes an order from the database.
	//
	// Args:
	//   - orderID: Unique identifier of the order to delete
	//
	// Returns:
	//   - error: Any error encountered
	DeleteOrder(orderID string) error

	// SavePosition persists a position to the database.
	//
	// Args:
	//   - position: The position to save
	//
	// Returns:
	//   - error: Any error encountered during save
	SavePosition(position models.Position) error

	// GetPosition retrieves a position by symbol.
	//
	// Args:
	//   - symbol: Ticker symbol
	//
	// Returns:
	//   - *models.Position: The position if found
	//   - error: Any error encountered, or ErrNotFound if position doesn't exist
	GetPosition(symbol string) (*models.Position, error)

	// GetAllPositions retrieves all positions from the database.
	//
	// Returns:
	//   - []models.Position: All persisted positions
	//   - error: Any error encountered
	GetAllPositions() ([]models.Position, error)

	// SaveTrade records a trade execution.
	//
	// Args:
	//   - trade: The trade to record
	//
	// Returns:
	//   - error: Any error encountered during save
	SaveTrade(trade models.Trade) error
}

// SQLOrderStore implements OrderStore using SQLite.
type SQLOrderStore struct {
	db *DB
}

// NewOrderStore creates a new SQL-based order store.
//
// Args:
//   - db: Database connection
//
// Returns:
//   - *SQLOrderStore: The order store instance
func NewOrderStore(db *DB) *SQLOrderStore {
	return &SQLOrderStore{db: db}
}

// SaveOrder persists an order to the database.
func (s *SQLOrderStore) SaveOrder(order models.Order) error {
	query := `
		INSERT OR REPLACE INTO orders (id, symbol, side, type, quantity, price, status, filled_quantity, average_price, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		order.ID,
		order.Symbol,
		order.Side,
		order.Type,
		order.Quantity,
		order.Price,
		order.Status,
		order.FilledQuantity,
		order.AveragePrice,
		order.CreatedAt,
		order.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save order: %w", err)
	}
	return nil
}

// GetOrder retrieves an order by ID.
func (s *SQLOrderStore) GetOrder(orderID string) (*models.Order, error) {
	var order models.Order
	query := `
		SELECT id, symbol, side, type, quantity, price, status, filled_quantity, average_price, created_at, updated_at
		FROM orders
		WHERE id = ?
	`
	err := s.db.Get(&order, query, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get order: %w", err)
	}
	return &order, nil
}

// GetAllOrders retrieves all orders from the database.
func (s *SQLOrderStore) GetAllOrders() ([]models.Order, error) {
	var orders []models.Order
	query := `
		SELECT id, symbol, side, type, quantity, price, status, filled_quantity, average_price, created_at, updated_at
		FROM orders
		ORDER BY created_at DESC
	`
	err := s.db.Select(&orders, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get all orders: %w", err)
	}
	return orders, nil
}

// DeleteOrder removes an order from the database.
func (s *SQLOrderStore) DeleteOrder(orderID string) error {
	query := `DELETE FROM orders WHERE id = ?`
	_, err := s.db.Exec(query, orderID)
	if err != nil {
		return fmt.Errorf("failed to delete order: %w", err)
	}
	return nil
}

// SavePosition persists a position to the database.
func (s *SQLOrderStore) SavePosition(position models.Position) error {
	query := `
		INSERT OR REPLACE INTO positions (symbol, quantity, average_cost, updated_at)
		VALUES (?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		position.Symbol,
		position.Quantity,
		position.AverageCost,
		position.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save position: %w", err)
	}
	return nil
}

// GetPosition retrieves a position by symbol.
func (s *SQLOrderStore) GetPosition(symbol string) (*models.Position, error) {
	var position models.Position
	query := `
		SELECT symbol, quantity, average_cost, updated_at
		FROM positions
		WHERE symbol = ?
	`
	err := s.db.Get(&position, query, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get position: %w", err)
	}
	return &position, nil
}

// GetAllPositions retrieves all positions from the database.
func (s *SQLOrderStore) GetAllPositions() ([]models.Position, error) {
	var positions []models.Position
	query := `
		SELECT symbol, quantity, average_cost, updated_at
		FROM positions
		ORDER BY symbol ASC
	`
	err := s.db.Select(&positions, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get all positions: %w", err)
	}
	return positions, nil
}

// SaveTrade records a trade execution.
func (s *SQLOrderStore) SaveTrade(trade models.Trade) error {
	query := `
		INSERT OR REPLACE INTO trades (id, order_id, symbol, side, quantity, price, executed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		trade.ID,
		trade.OrderID,
		trade.Symbol,
		trade.Side,
		trade.Quantity,
		trade.Price,
		trade.ExecutedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save trade: %w", err)
	}
	return nil
}
