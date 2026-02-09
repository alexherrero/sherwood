package data

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrderStore_SaveOrder verifies order persistence.
func TestOrderStore_SaveOrder(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	order := models.Order{
		ID:             "order-123",
		Symbol:         "BTC-USD",
		Side:           models.OrderSideBuy,
		Type:           models.OrderTypeMarket,
		Quantity:       0.5,
		Price:          50000.0,
		Status:         models.OrderStatusFilled,
		FilledQuantity: 0.5,
		AveragePrice:   50100.0,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err = store.SaveOrder(order)
	require.NoError(t, err)

	// Verify order was saved
	retrieved, err := store.GetOrder("order-123")
	require.NoError(t, err)
	assert.Equal(t, order.ID, retrieved.ID)
	assert.Equal(t, order.Symbol, retrieved.Symbol)
	assert.Equal(t, order.Side, retrieved.Side)
	assert.Equal(t, order.Quantity, retrieved.Quantity)
}

// TestOrderStore_SaveOrder_Update verifies upsert behavior.
func TestOrderStore_SaveOrder_Update(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	// Insert original order
	order := models.Order{
		ID:        "order-123",
		Symbol:    "BTC-USD",
		Side:      models.OrderSideBuy,
		Type:      models.OrderTypeMarket,
		Quantity:  0.5,
		Status:    models.OrderStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.SaveOrder(order)

	// Update with same ID
	order.Status = models.OrderStatusFilled
	order.FilledQuantity = 0.5
	order.AveragePrice = 50000.0
	order.UpdatedAt = time.Now()

	err = store.SaveOrder(order)
	require.NoError(t, err)

	// Should have updated, not created duplicate
	retrieved, err := store.GetOrder("order-123")
	require.NoError(t, err)
	assert.Equal(t, models.OrderStatusFilled, retrieved.Status)
	assert.Equal(t, 0.5, retrieved.FilledQuantity)
}

// TestOrderStore_GetOrder_NotFound verifies error handling.
func TestOrderStore_GetOrder_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	_, err = store.GetOrder("nonexistent")
	assert.Error(t, err)
}

// TestOrderStore_GetAllOrders verifies retrieving multiple orders.
func TestOrderStore_GetAllOrders(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	// Save multiple orders
	orders := []models.Order{
		{
			ID:        "order-1",
			Symbol:    "BTC-USD",
			Side:      models.OrderSideBuy,
			Type:      models.OrderTypeMarket,
			Quantity:  0.5,
			Status:    models.OrderStatusFilled,
			CreatedAt: time.Now().Add(-2 * time.Hour),
			UpdatedAt: time.Now().Add(-2 * time.Hour),
		},
		{
			ID:        "order-2",
			Symbol:    "ETH-USD",
			Side:      models.OrderSideSell,
			Type:      models.OrderTypeLimit,
			Quantity:  1.0,
			Price:     3000.0,
			Status:    models.OrderStatusPending,
			CreatedAt: time.Now().Add(-1 * time.Hour),
			UpdatedAt: time.Now().Add(-1 * time.Hour),
		},
		{
			ID:        "order-3",
			Symbol:    "BTC-USD",
			Side:      models.OrderSideSell,
			Type:      models.OrderTypeMarket,
			Quantity:  0.25,
			Status:    models.OrderStatusFilled,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}

	for _, order := range orders {
		_ = store.SaveOrder(order)
	}

	// Retrieve all
	retrieved, err := store.GetAllOrders()
	require.NoError(t, err)
	assert.Len(t, retrieved, 3)

	// Should be ordered by created_at DESC (newest first)
	assert.Equal(t, "order-3", retrieved[0].ID)
	assert.Equal(t, "order-2", retrieved[1].ID)
	assert.Equal(t, "order-1", retrieved[2].ID)
}

// TestOrderStore_DeleteOrder verifies order deletion.
func TestOrderStore_DeleteOrder(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	order := models.Order{
		ID:        "order-123",
		Symbol:    "BTC-USD",
		Side:      models.OrderSideBuy,
		Type:      models.OrderTypeMarket,
		Quantity:  0.5,
		Status:    models.OrderStatusFilled,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.SaveOrder(order)

	// Delete
	err = store.DeleteOrder("order-123")
	require.NoError(t, err)

	// Verify deleted
	_, err = store.GetOrder("order-123")
	assert.Error(t, err)
}

// TestOrderStore_SavePosition verifies position persistence.
func TestOrderStore_SavePosition(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	position := models.Position{
		Symbol:      "BTC-USD",
		Quantity:    0.5,
		AverageCost: 50000.0,
		UpdatedAt:   time.Now(),
	}

	err = store.SavePosition(position)
	require.NoError(t, err)

	// Verify position was saved
	retrieved, err := store.GetPosition("BTC-USD")
	require.NoError(t, err)
	assert.Equal(t, position.Symbol, retrieved.Symbol)
	assert.Equal(t, position.Quantity, retrieved.Quantity)
	assert.Equal(t, position.AverageCost, retrieved.AverageCost)
}

// TestOrderStore_SavePosition_Update verifies position updates.
func TestOrderStore_SavePosition_Update(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	// Initial position
	position := models.Position{
		Symbol:      "BTC-USD",
		Quantity:    0.5,
		AverageCost: 50000.0,
		UpdatedAt:   time.Now(),
	}
	_ = store.SavePosition(position)

	// Update position
	position.Quantity = 1.0
	position.AverageCost = 51000.0
	position.UpdatedAt = time.Now()

	err = store.SavePosition(position)
	require.NoError(t, err)

	// Should have updated
	retrieved, err := store.GetPosition("BTC-USD")
	require.NoError(t, err)
	assert.Equal(t, 1.0, retrieved.Quantity)
	assert.Equal(t, 51000.0, retrieved.AverageCost)
}

// TestOrderStore_GetAllPositions verifies retrieving multiple positions.
func TestOrderStore_GetAllPositions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	positions := []models.Position{
		{Symbol: "BTC-USD", Quantity: 0.5, AverageCost: 50000.0, UpdatedAt: time.Now()},
		{Symbol: "ETH-USD", Quantity: 2.0, AverageCost: 3000.0, UpdatedAt: time.Now()},
		{Symbol: "AAPL", Quantity: 10.0, AverageCost: 150.0, UpdatedAt: time.Now()},
	}

	for _, pos := range positions {
		_ = store.SavePosition(pos)
	}

	retrieved, err := store.GetAllPositions()
	require.NoError(t, err)
	assert.Len(t, retrieved, 3)

	// Should be ordered by symbol ASC
	assert.Equal(t, "AAPL", retrieved[0].Symbol)
	assert.Equal(t, "BTC-USD", retrieved[1].Symbol)
	assert.Equal(t, "ETH-USD", retrieved[2].Symbol)
}

// TestOrderStore_SaveTrade verifies trade recording.
func TestOrderStore_SaveTrade(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	trade := models.Trade{
		ID:         "trade-123",
		OrderID:    "order-123",
		Symbol:     "BTC-USD",
		Side:       models.OrderSideBuy,
		Quantity:   0.5,
		Price:      50000.0,
		ExecutedAt: time.Now(),
	}

	err = store.SaveTrade(trade)
	require.NoError(t, err)

	// Verify trade was saved by querying database directly
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM trades WHERE id = ?", "trade-123")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestOrderStore_EmptyDatabase verifies empty query results.
func TestOrderStore_EmptyDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	store := NewOrderStore(db)

	// Get all orders from empty database
	orders, err := store.GetAllOrders()
	require.NoError(t, err)
	assert.Empty(t, orders)

	// Get all positions from empty database
	positions, err := store.GetAllPositions()
	require.NoError(t, err)
	assert.Empty(t, positions)
}
