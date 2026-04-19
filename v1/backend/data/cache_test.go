package data

import (
	"context"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewMemoryCache verifies cache creation.
func TestNewMemoryCache(t *testing.T) {
	cache := NewMemoryCache()
	assert.NotNil(t, cache)
	assert.NotNil(t, cache.data)
}

// TestMemoryCache_SetAndGet verifies basic set/get operations.
func TestMemoryCache_SetAndGet(t *testing.T) {
	cache := NewMemoryCache()
	ctx := context.Background()

	err := cache.Set(ctx, "test-key", []byte("test-value"), time.Minute)
	require.NoError(t, err)

	value, err := cache.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.Equal(t, []byte("test-value"), value)
}

// TestMemoryCache_Get_NotFound verifies error on missing key.
func TestMemoryCache_Get_NotFound(t *testing.T) {
	cache := NewMemoryCache()
	ctx := context.Background()

	_, err := cache.Get(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

// TestMemoryCache_Get_Expired verifies expired entries are removed.
func TestMemoryCache_Get_Expired(t *testing.T) {
	cache := NewMemoryCache()
	ctx := context.Background()

	// Set with very short TTL
	err := cache.Set(ctx, "short-lived", []byte("data"), time.Millisecond)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	_, err = cache.Get(ctx, "short-lived")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

// TestMemoryCache_Delete verifies deletion.
func TestMemoryCache_Delete(t *testing.T) {
	cache := NewMemoryCache()
	ctx := context.Background()

	_ = cache.Set(ctx, "to-delete", []byte("data"), time.Minute)
	err := cache.Delete(ctx, "to-delete")
	require.NoError(t, err)

	_, err = cache.Get(ctx, "to-delete")
	assert.Error(t, err)
}

// TestMemoryCache_Delete_NonExistent verifies deleting non-existent key is safe.
func TestMemoryCache_Delete_NonExistent(t *testing.T) {
	cache := NewMemoryCache()
	ctx := context.Background()

	err := cache.Delete(ctx, "nonexistent")
	assert.NoError(t, err) // Should not error
}

// TestMemoryCache_Overwrite verifies overwriting existing key.
func TestMemoryCache_Overwrite(t *testing.T) {
	cache := NewMemoryCache()
	ctx := context.Background()

	_ = cache.Set(ctx, "key", []byte("value1"), time.Minute)
	_ = cache.Set(ctx, "key", []byte("value2"), time.Minute)

	value, err := cache.Get(ctx, "key")
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), value)
}

// mockDataProvider is a simple mock for testing CachedDataProvider.
type mockDataProvider struct {
	priceCallCount  int
	tickerCallCount int
}

func (m *mockDataProvider) Name() string {
	return "mock"
}

func (m *mockDataProvider) GetLatestPrice(symbol string) (float64, error) {
	m.priceCallCount++
	return 150.0, nil
}

func (m *mockDataProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	return []models.OHLCV{{Symbol: symbol, Close: 150.0}}, nil
}

func (m *mockDataProvider) GetTicker(symbol string) (*models.Ticker, error) {
	m.tickerCallCount++
	return &models.Ticker{Symbol: symbol, Name: "Mock Stock"}, nil
}

// TestNewCachedDataProvider verifies cached provider creation.
func TestNewCachedDataProvider(t *testing.T) {
	mock := &mockDataProvider{}
	cache := NewMemoryCache()

	cached := NewCachedDataProvider(mock, cache, time.Minute)

	assert.NotNil(t, cached)
	assert.Equal(t, "cached-mock", cached.Name())
}

// TestCachedDataProvider_GetLatestPrice_CacheHit verifies cache hit.
func TestCachedDataProvider_GetLatestPrice_CacheHit(t *testing.T) {
	mock := &mockDataProvider{}
	cache := NewMemoryCache()
	cached := NewCachedDataProvider(mock, cache, time.Minute)

	// First call - cache miss
	price1, err := cached.GetLatestPrice("AAPL")
	require.NoError(t, err)
	assert.Equal(t, 150.0, price1)
	assert.Equal(t, 1, mock.priceCallCount)

	// Second call - cache hit
	price2, err := cached.GetLatestPrice("AAPL")
	require.NoError(t, err)
	assert.Equal(t, 150.0, price2)
	assert.Equal(t, 1, mock.priceCallCount) // Should not have increased
}

// TestCachedDataProvider_GetTicker_CacheHit verifies cache hit for ticker.
func TestCachedDataProvider_GetTicker_CacheHit(t *testing.T) {
	mock := &mockDataProvider{}
	cache := NewMemoryCache()
	cached := NewCachedDataProvider(mock, cache, time.Minute)

	// First call
	ticker1, err := cached.GetTicker("AAPL")
	require.NoError(t, err)
	assert.Equal(t, "Mock Stock", ticker1.Name)
	assert.Equal(t, 1, mock.tickerCallCount)

	// Second call - cache hit
	ticker2, err := cached.GetTicker("AAPL")
	require.NoError(t, err)
	assert.Equal(t, "Mock Stock", ticker2.Name)
	assert.Equal(t, 1, mock.tickerCallCount) // Should not increase
}

// TestCachedDataProvider_GetHistoricalData verifies pass-through (no cache).
func TestCachedDataProvider_GetHistoricalData(t *testing.T) {
	mock := &mockDataProvider{}
	cache := NewMemoryCache()
	cached := NewCachedDataProvider(mock, cache, time.Minute)

	start := time.Now().AddDate(0, -1, 0)
	end := time.Now()

	data, err := cached.GetHistoricalData("AAPL", start, end, "1d")
	require.NoError(t, err)
	assert.Len(t, data, 1)
}
