# Pending Features

This document tracks future feature ideas and enhancements that are not currently prioritized for implementation. Each entry includes enough detail to pick up and implement later.

Features are ordered by complexity from least to most complex.

---

## 1. Hot-Swapping Strategies

**Complexity:** Medium-High

**Description:**
Enable/disable trading strategies at runtime without restarting the application. Users should be able to activate or deactivate strategies through API endpoints while the engine is running.

**Current Limitation:**
Strategies are loaded once at startup from `ENABLED_STRATEGIES` environment variable. Changes require full application restart.

**Implementation Requirements:**

1. **API Endpoints:**
   - `POST /api/v1/strategies/{name}/enable` - Enable a strategy
   - `POST /api/v1/strategies/{name}/disable` - Disable a strategy
   - Both should require confirmation and return strategy status

2. **Thread-Safe Registry:**
   - Add `Enable(name string) error` method to `strategies.Registry`
   - Add `Disable(name string) error` method to `strategies.Registry`
   - Use mutex protection for concurrent access
   - Maintain internal map of enabled/disabled state

3. **Engine Coordination:**
   - Modify `TradingEngine.loop()` to check if strategy is enabled before processing
   - Handle case where strategy is disabled mid-execution
   - Ensure no race conditions on market data processing
   - Add event logging for strategy state changes

4. **Position Management:**
   - Decision needed: What happens to open positions when strategy disabled?
     - Option A: Prevent disabling if strategy has open positions
     - Option B: Close all positions before disabling (requires confirmation)
     - Option C: Transfer positions to manual management
   - Implement position ownership tracking

5. **State Persistence:**
   - Store enabled/disabled state in database
   - Restore state on restart (override or merge with env var?)
   - Add configuration for default behavior

6. **Frontend Integration:**
   - Add toggle switches to strategy list UI
   - Show real-time status updates
   - Display warnings for strategies with open positions
   - Confirm before disabling strategy with active trades

**Edge Cases to Handle:**

- Disabling a strategy that's currently executing a trade
- Enabling a strategy that requires initialization
- Multiple concurrent enable/disable requests
- Network failures during state change
- Restart behavior with partially enabled strategies

**Testing Requirements:**

- Unit tests for registry enable/disable
- Integration tests for API endpoints
- Race condition testing with concurrent requests
- Position management scenarios
- State persistence verification

---

## [Future Feature Template]

**Complexity:** [Low/Medium/High/Very High]

**Description:**
[Brief description of the feature]

**Implementation Requirements:**

1. [Requirement 1]
2. [Requirement 2]
...

**Edge Cases to Handle:**

- [Edge case 1]
- [Edge case 2]

**Testing Requirements:**

- [Test requirement 1]
- [Test requirement 2]
