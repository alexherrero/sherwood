# Trading Strategies

Sherwood uses a modular strategy system that allows you to define, configure, and run trading strategies.

## Strategy Interface

All strategies implement the `Strategy` interface:

```go
type Strategy interface {
    Name() string
    Description() string
    Init(config map[string]interface{}) error
    OnData(data []models.OHLCV) models.Signal
    Validate() error
    GetParameters() map[string]Parameter
}
```

## Built-in Strategies

### Moving Average Crossover (`ma_crossover`)

Generates buy signals when a short-term moving average crosses above a long-term moving average, and sell signals when it crosses below.

**Parameters:**

| Parameter | Type | Default | Range | Description |
|-----------|------|---------|-------|-------------|
| `short_period` | int | 10 | 2-50 | Short MA period |
| `long_period` | int | 20 | 5-200 | Long MA period |

### RSI Momentum (`rsi_momentum`)

Capitalizes on mean reversion by identifying overbought and oversold conditions.

**Parameters:**

| Parameter | Type | Default | Range | Description |
|-----------|------|---------|-------|-------------|
| `period` | int | 14 | 2-100 | RSI lookback period |
| `overbought` | float | 70.0 | 50-95 | Sell threshold |
| `oversold` | float | 30.0 | 5-50 | Buy threshold |

### Bollinger Bands (`bb_mean_reversion`)

Uses volatility bands to identify potential price reversals.

**Parameters:**

| Parameter | Type | Default | Range | Description |
|-----------|------|---------|-------|-------------|
| `period` | int | 20 | 2-100 | SMA lookback period |
| `std_dev` | float | 2.0 | 0.5-5 | Standard deviation multiplier |

### MACD Trend Follower (`macd_trend_follower`)

Filters noise and identifies strong trends using moving average convergence divergence.

**Parameters:**

| Parameter | Type | Default | Range | Description |
|-----------|------|---------|-------|-------------|
| `fast_period` | int | 12 | 2-100 | Fast EMA period |
| `slow_period` | int | 26 | 2-200 | Slow EMA period |
| `signal_period` | int | 9 | 2-100 | Signal line period |

### NYC Market Close/Open (`nyc_close_open`)

Time-based strategy for Bitcoin/Crypto to capture overnight volatility.

**Parameters:**

| Parameter | Type | Default | Range | Description |
|-----------|------|---------|-------|-------------|
| `quantity` | float | 1.0 | >0 | Position size to trade |

**Example Configuration:**

```json
{
  "strategy": "ma_crossover",
  "config": {
    "short_period": 12,
    "long_period": 26
  }
}
```

## Creating Custom Strategies

### Step 1: Create Strategy File

Create a new file in `backend/strategies/`:

```go
package strategies

import "github.com/alexherrero/sherwood/backend/models"

type MyStrategy struct {
    *BaseStrategy
    // Add your parameters here
}

func NewMyStrategy() *MyStrategy {
    return &MyStrategy{
        BaseStrategy: NewBaseStrategy("my_strategy", "Description here"),
    }
}
```

### Step 2: Implement Required Methods

```go
func (s *MyStrategy) Init(config map[string]interface{}) error {
    // Load configuration
    return s.Validate()
}

func (s *MyStrategy) Validate() error {
    // Validate parameters
    return nil
}

func (s *MyStrategy) GetParameters() map[string]Parameter {
    return map[string]Parameter{
        // Define your parameters
    }
}

func (s *MyStrategy) OnData(data []models.OHLCV) models.Signal {
    // Generate trading signal
    return models.Signal{Type: models.SignalHold}
}
```

### Step 3: Register Strategy

Add to the registry in your initialization code:

```go
registry := strategies.NewRegistry()
registry.Register(strategies.NewMyStrategy())
```

## Signal Types

| Signal | Description |
|--------|-------------|
| `buy` | Open a long position |
| `sell` | Close position / go short |
| `hold` | No action |

## Signal Strength

| Strength | Confidence |
|----------|------------|
| `weak` | Low confidence |
| `moderate` | Medium confidence |
| `strong` | High confidence |
