package strategies

import (
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
)

// NYCCloseOpen implements a strategy that buys at NYC market close and sells before open.
// Buys BTC at 16:00 ET and sells at 08:30 ET.
//
// NOTE: This strategy is intended for assets that trade 24/7 (like Crypto) or have
// extended trading hours (Pre-market/After-hours) that allow execution at these times.
// Standard equity markets are closed at 16:00 ET and open at 09:30 ET, so executing
// exactly at these times requires an exchange that supports it.
type NYCCloseOpen struct {
	*BaseStrategy
	location *time.Location
}

// NewNYCCloseOpen creates a new NYC Close/Open strategy.
func NewNYCCloseOpen() *NYCCloseOpen {
	return &NYCCloseOpen{
		BaseStrategy: NewBaseStrategy(
			"nyc_close_open",
			"NYC Close/Open Strategy - Buy at 16:00 ET, Sell at 08:30 ET (Requires 24/7 or extended hours execution)",
		),
	}
}

// Init initializes the strategy.
func (s *NYCCloseOpen) Init(config map[string]interface{}) error {
	if err := s.BaseStrategy.Init(config); err != nil {
		return err
	}

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// Fallback to FixedZone if LoadLocation fails (common in some container environments)
		// EST is UTC-5, EDT is UTC-4. For simplicity in this fallback, we might not handle DST perfectly without a proper DB.
		// However, standard environments should have tzdata.
		return fmt.Errorf("failed to load NYC timezone: %w", err)
	}
	s.location = loc

	return nil
}

// Validate checks if the strategy configuration is valid.
func (s *NYCCloseOpen) Validate() error {
	return nil
}

// GetParameters returns the strategy's parameter definitions.
func (s *NYCCloseOpen) GetParameters() map[string]Parameter {
	return map[string]Parameter{
		"buy_hour": {
			Type:        "int",
			Default:     16,
			Description: "Hour to buy (ET)",
			Min:         0,
			Max:         23,
		},
		"buy_minute": {
			Type:        "int",
			Default:     0,
			Description: "Minute to buy (ET)",
			Min:         0,
			Max:         59,
		},
		"sell_hour": {
			Type:        "int",
			Default:     8,
			Description: "Hour to sell (ET)",
			Min:         0,
			Max:         23,
		},
		"sell_minute": {
			Type:        "int",
			Default:     30,
			Description: "Minute to sell (ET)",
			Min:         0,
			Max:         59,
		},
	}
}

// OnData processes OHLCV data and generates trading signals.
func (s *NYCCloseOpen) OnData(data []models.OHLCV) models.Signal {
	signal := models.Signal{
		Type:         models.SignalHold,
		Strength:     models.SignalStrengthWeak,
		StrategyName: s.Name(),
		Reason:       "Time condition not met",
	}

	if len(data) == 0 {
		signal.Reason = "No data available"
		return signal
	}

	candle := data[len(data)-1]

	// Ensure we have a valid location loaded
	if s.location == nil {
		// Try loading again or default to UTC if strictly necessary, but better to fail safely/log error.
		// For now, let's assume Init worked or we shouldn't be running.
		// If Init failed, the engine likely wouldn't start this strategy.
		// Defensive coding:
		loc, err := time.LoadLocation("America/New_York")
		if err != nil {
			signal.Reason = "Timezone data missing"
			return signal
		}
		s.location = loc
	}

	candleTimeNYC := candle.Timestamp.In(s.location)
	hour := candleTimeNYC.Hour()
	minute := candleTimeNYC.Minute()

	// Strategy Logic:
	// Buy at 16:00 ET (Market Close)
	// Sell at 08:30 ET (Pre-Market / Before Open)

	// Check for weekends (time.Saturday = 6, time.Sunday = 0)
	// We might want to hold over the weekend?
	// Request said: "purchase bitcoin at the close of the NYC market, and sell it an hour before the market opens."
	// Stock market is closed weekends, but crypto is 24/7.
	// The prompt implies "NYC market" schedule.
	// Usually this means Mon-Fri schedule.
	// If we buy Friday Close, do we sell Saturday morning? Or Monday morning?
	// "Sell it an hour before the market opens" implies the NEXT market open.
	// So Friday Close -> Monday Open.

	// If it's Saturday or Sunday, we generally don't generate NEW signals,
	// but if we are holding, we wait.

	isWeekend := candleTimeNYC.Weekday() == time.Saturday || candleTimeNYC.Weekday() == time.Sunday

	buyHour := s.GetConfigInt("buy_hour", 16)
	buyMinute := s.GetConfigInt("buy_minute", 0)
	sellHour := s.GetConfigInt("sell_hour", 8)
	sellMinute := s.GetConfigInt("sell_minute", 30)

	if !isWeekend {
		if hour == buyHour && minute == buyMinute {
			signal.Type = models.SignalBuy
			signal.Strength = models.SignalStrengthStrong
			signal.Reason = fmt.Sprintf("Market Close (%02d:%02d ET) on %s", buyHour, buyMinute, candleTimeNYC.Weekday())
			signal.Symbol = candle.Symbol
			signal.Price = candle.Close
		} else if hour == sellHour && minute == sellMinute {
			signal.Type = models.SignalSell
			signal.Strength = models.SignalStrengthStrong
			signal.Reason = fmt.Sprintf("Pre-Market (%02d:%02d ET) on %s", sellHour, sellMinute, candleTimeNYC.Weekday())
			signal.Symbol = candle.Symbol
			signal.Price = candle.Close
		}
	}

	return signal
}
