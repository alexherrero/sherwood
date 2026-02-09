# Trade Execution Engine

The execution engine handles order management, broker integration, and risk controls.

## Components

### Broker Interface

Brokers execute trades against real or simulated markets:

| Broker | Type | Description |
|--------|------|-------------|
| `PaperBroker` | Simulated | Paper trading (no real money) |
| Robinhood | Live | (Planned) |
| Alpaca | Live | (Planned) |

### Order Manager

Handles order lifecycle:

- Order validation
- Risk checking
- Broker submission
- Order tracking

### Risk Manager

Enforces trading limits:

| Limit | Default | Description |
|-------|---------|-------------|
| Max Position Size | $10,000 | Maximum value per position |
| Max Daily Loss | $500 | Stop trading after this loss |
| Risk Per Trade | 2% | Maximum risk per trade |
| Max Open Orders | 10 | Maximum concurrent orders |

## Usage

### Paper Trading Setup

```go
import "github.com/alexherrero/sherwood/backend/execution"

// Create paper broker with $100,000 starting capital
broker := execution.NewPaperBroker(100000.0)
broker.Connect()

// Create risk manager
riskConfig := execution.DefaultRiskConfig()
riskManager := execution.NewRiskManager(riskConfig, broker)

// Create order manager
orderManager := execution.NewOrderManager(broker, riskManager)

// Submit a market buy order
order, err := orderManager.CreateMarketOrder("AAPL", models.OrderSideBuy, 10)
```

### Position Sizing

```go
balance, _ := broker.GetBalance()
positionSize := riskManager.CalculatePositionSize(
    150.0,  // Entry price
    145.0,  // Stop loss
    balance,
)
```

## Trading Safety Checklist

When working with execution code:

- [ ] Paper trading mode clearly marked
- [ ] Position size limits enforced
- [ ] Stop losses implemented
- [ ] Daily loss limits checked
- [ ] Risk per trade ≤ 2%
- [ ] Confirmation required for live mode
- [ ] All credentials in environment variables
- [ ] Extensive logging on order execution
- [ ] Alert system for anomalies
- [ ] Manual kill switch available

> ⚠️ **DISCLAIMER**: This is experimental software. Paper trade extensively before considering live trading.
