// Package strategies provides trading strategy interfaces and implementations.
package strategies

import (
	"fmt"

	"github.com/alexherrero/sherwood/backend/models"
)

// Strategy defines the interface for trading strategies.
// All strategy implementations must satisfy this interface.
type Strategy interface {
	// Name returns the strategy's unique identifier.
	Name() string

	// Description returns a human-readable description of the strategy.
	Description() string

	// Init initializes the strategy with configuration parameters.
	//
	// Args:
	//   - config: Strategy-specific configuration as key-value pairs
	//
	// Returns:
	//   - error: Any error encountered during initialization
	Init(config map[string]interface{}) error

	// OnData processes new market data and generates signals.
	//
	// Args:
	//   - data: Historical OHLCV data, most recent last
	//
	// Returns:
	//   - models.Signal: The trading signal (buy/sell/hold)
	OnData(data []models.OHLCV) models.Signal

	// Validate checks if the strategy configuration is valid.
	//
	// Returns:
	//   - error: Any validation error
	Validate() error

	// Timeframe returns the data interval required by the strategy (e.g., "1d", "1h", "15m").
	Timeframe() string

	// GetParameters returns the strategy's configurable parameters.
	//
	// Returns:
	//   - map[string]Parameter: Parameter definitions
	GetParameters() map[string]Parameter
}

// Parameter describes a configurable strategy parameter.
type Parameter struct {
	Type        string      `json:"type"`
	Default     interface{} `json:"default"`
	Min         interface{} `json:"min,omitempty"`
	Max         interface{} `json:"max,omitempty"`
	Description string      `json:"description"`
}

// BaseStrategy provides common functionality for strategies.
type BaseStrategy struct {
	name        string
	description string
	config      map[string]interface{}
}

// NewBaseStrategy creates a new BaseStrategy.
//
// Args:
//   - name: Strategy identifier
//   - description: Human-readable description
//
// Returns:
//   - *BaseStrategy: The base strategy instance
func NewBaseStrategy(name, description string) *BaseStrategy {
	return &BaseStrategy{
		name:        name,
		description: description,
		config:      make(map[string]interface{}),
	}
}

// Name returns the strategy name.
func (s *BaseStrategy) Name() string {
	return s.name
}

// Description returns the strategy description.
func (s *BaseStrategy) Description() string {
	return s.description
}

// Timeframe returns the data interval required by the strategy (e.g., "1d", "1h", "15m").
func (s *BaseStrategy) Timeframe() string {
	return "1d"
}

// Init initializes the base strategy.
func (s *BaseStrategy) Init(config map[string]interface{}) error {
	s.config = config
	return nil
}

// GetConfig returns a config value with a default.
func (s *BaseStrategy) GetConfig(key string, defaultValue interface{}) interface{} {
	if val, exists := s.config[key]; exists {
		return val
	}
	return defaultValue
}

// GetConfigInt returns an integer config value.
func (s *BaseStrategy) GetConfigInt(key string, defaultValue int) int {
	val := s.GetConfig(key, defaultValue)
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return defaultValue
	}
}

// GetConfigFloat returns a float config value.
func (s *BaseStrategy) GetConfigFloat(key string, defaultValue float64) float64 {
	val := s.GetConfig(key, defaultValue)
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return defaultValue
	}
}

// Registry manages available strategies.
type Registry struct {
	strategies map[string]Strategy
}

// NewRegistry creates a new strategy registry.
//
// Returns:
//   - *Registry: The registry instance
func NewRegistry() *Registry {
	return &Registry{
		strategies: make(map[string]Strategy),
	}
}

// Register adds a strategy to the registry.
//
// Args:
//   - strategy: Strategy to register
//
// Returns:
//   - error: Error if strategy name already registered
func (r *Registry) Register(strategy Strategy) error {
	name := strategy.Name()
	if _, exists := r.strategies[name]; exists {
		return fmt.Errorf("strategy already registered: %s", name)
	}
	r.strategies[name] = strategy
	return nil
}

// Get retrieves a strategy by name.
//
// Args:
//   - name: Strategy name
//
// Returns:
//   - Strategy: The strategy, or nil if not found
//   - bool: True if found
func (r *Registry) Get(name string) (Strategy, bool) {
	s, exists := r.strategies[name]
	return s, exists
}

// List returns all registered strategy names.
//
// Returns:
//   - []string: List of strategy names
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.strategies))
	for name := range r.strategies {
		names = append(names, name)
	}
	return names
}

// All returns all registered strategies.
//
// Returns:
//   - map[string]Strategy: All strategies
func (r *Registry) All() map[string]Strategy {
	return r.strategies
}
