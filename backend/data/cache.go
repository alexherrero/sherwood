// Package data provides caching functionality.
package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
)

// Cache provides an interface for caching market data.
type Cache interface {
	// Get retrieves a value from the cache.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value in the cache with an expiration.
	Set(ctx context.Context, key string, value []byte, expiration time.Duration) error

	// Delete removes a value from the cache.
	Delete(ctx context.Context, key string) error
}

// MemoryCache is a simple in-memory cache implementation.
// For production, use Redis via the RedisCache implementation.
type MemoryCache struct {
	data map[string]cacheEntry
}

type cacheEntry struct {
	value     []byte
	expiresAt time.Time
}

// NewMemoryCache creates a new in-memory cache.
//
// Returns:
//   - *MemoryCache: The cache instance
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		data: make(map[string]cacheEntry),
	}
}

// Get retrieves a value from the cache.
//
// Args:
//   - ctx: Context for cancellation
//   - key: Cache key
//
// Returns:
//   - []byte: Cached value
//   - error: Error if key not found or expired
func (c *MemoryCache) Get(ctx context.Context, key string) ([]byte, error) {
	entry, exists := c.data[key]
	if !exists {
		return nil, fmt.Errorf("key not found: %s", key)
	}

	if time.Now().After(entry.expiresAt) {
		delete(c.data, key)
		return nil, fmt.Errorf("key expired: %s", key)
	}

	return entry.value, nil
}

// Set stores a value in the cache.
//
// Args:
//   - ctx: Context for cancellation
//   - key: Cache key
//   - value: Value to store
//   - expiration: TTL for the cache entry
//
// Returns:
//   - error: Any error encountered
func (c *MemoryCache) Set(ctx context.Context, key string, value []byte, expiration time.Duration) error {
	c.data[key] = cacheEntry{
		value:     value,
		expiresAt: time.Now().Add(expiration),
	}
	return nil
}

// Delete removes a value from the cache.
//
// Args:
//   - ctx: Context for cancellation
//   - key: Cache key
//
// Returns:
//   - error: Any error encountered
func (c *MemoryCache) Delete(ctx context.Context, key string) error {
	delete(c.data, key)
	return nil
}

// CachedDataProvider wraps a DataProvider with caching.
type CachedDataProvider struct {
	provider DataProvider
	cache    Cache
	ttl      time.Duration
}

// NewCachedDataProvider creates a new cached data provider.
//
// Args:
//   - provider: The underlying data provider
//   - cache: Cache implementation
//   - ttl: Cache TTL for data
//
// Returns:
//   - *CachedDataProvider: The cached provider
func NewCachedDataProvider(provider DataProvider, cache Cache, ttl time.Duration) *CachedDataProvider {
	return &CachedDataProvider{
		provider: provider,
		cache:    cache,
		ttl:      ttl,
	}
}

// Name returns the provider name with cache prefix.
func (c *CachedDataProvider) Name() string {
	return fmt.Sprintf("cached-%s", c.provider.Name())
}

// GetLatestPrice fetches price with caching.
//
// Args:
//   - symbol: Ticker symbol
//
// Returns:
//   - float64: Current price
//   - error: Any error encountered
func (c *CachedDataProvider) GetLatestPrice(symbol string) (float64, error) {
	ctx := context.Background()
	key := fmt.Sprintf("price:%s", symbol)

	// Try cache first
	if data, err := c.cache.Get(ctx, key); err == nil {
		var price float64
		if err := json.Unmarshal(data, &price); err == nil {
			return price, nil
		}
	}

	// Fetch from provider
	price, err := c.provider.GetLatestPrice(symbol)
	if err != nil {
		return 0, err
	}

	// Store in cache
	if data, err := json.Marshal(price); err == nil {
		c.cache.Set(ctx, key, data, c.ttl)
	}

	return price, nil
}

// GetHistoricalData fetches historical data (not cached as it's typically large).
func (c *CachedDataProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	return c.provider.GetHistoricalData(symbol, start, end, interval)
}

// GetTicker fetches ticker info with caching.
func (c *CachedDataProvider) GetTicker(symbol string) (*models.Ticker, error) {
	ctx := context.Background()
	key := fmt.Sprintf("ticker:%s", symbol)

	// Try cache first
	if data, err := c.cache.Get(ctx, key); err == nil {
		var ticker models.Ticker
		if err := json.Unmarshal(data, &ticker); err == nil {
			return &ticker, nil
		}
	}

	// Fetch from provider
	ticker, err := c.provider.GetTicker(symbol)
	if err != nil {
		return nil, err
	}

	// Store in cache
	if data, err := json.Marshal(ticker); err == nil {
		c.cache.Set(ctx, key, data, c.ttl)
	}

	return ticker, nil
}
