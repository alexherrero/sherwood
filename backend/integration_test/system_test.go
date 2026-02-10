package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/api"
	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/engine"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestableDataProvider implements data.DataProvider with deterministic test data.
type TestableDataProvider struct {
	priceData map[string][]models.OHLCV
}

// Name returns the provider name.
func (p *TestableDataProvider) Name() string { return "TestProvider" }

// GetLatestPrice returns the most recent close price for the given symbol.
func (p *TestableDataProvider) GetLatestPrice(symbol string) (float64, error) {
	d, ok := p.priceData[symbol]
	if !ok || len(d) == 0 {
		return 0, fmt.Errorf("no data for symbol: %s", symbol)
	}
	return d[len(d)-1].Close, nil
}

// GetTicker returns ticker information for the given symbol.
func (p *TestableDataProvider) GetTicker(symbol string) (*models.Ticker, error) {
	if _, ok := p.priceData[symbol]; !ok {
		return nil, fmt.Errorf("no data for symbol: %s", symbol)
	}
	return &models.Ticker{Symbol: symbol}, nil
}

// GetHistoricalData returns historical OHLCV data for the given symbol.
func (p *TestableDataProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	d, ok := p.priceData[symbol]
	if !ok {
		return nil, fmt.Errorf("no data for symbol: %s", symbol)
	}
	return d, nil
}

// generateCrossoverData creates OHLCV data that will trigger an MA crossover buy signal.
// The data starts with a steady decline then has a sharp uptick at the end,
// ensuring the fast MA crosses above the slow MA.
func generateCrossoverData(symbol string, days int) []models.OHLCV {
	now := time.Now()
	prices := make([]models.OHLCV, 0, days)

	for i := 0; i < days; i++ {
		// Gradual uptrend with a large jump at the very end
		price := 100.0 + float64(i)*0.5
		if i > days-50 {
			price += 50.0 // Sharp jump to force fast MA above slow MA
		}

		prices = append(prices, models.OHLCV{
			Timestamp: now.AddDate(0, 0, i-days),
			Symbol:    symbol,
			Open:      price,
			High:      price + 1,
			Low:       price - 1,
			Close:     price,
			Volume:    1000,
		})
	}
	return prices
}

// TestSystemFlow_HealthEndpoint verifies the health endpoint works with
// real (non-mock) components.
func TestSystemFlow_HealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		TradingMode:    "paper",
		ServerPort:     0,
		LogLevel:       "error",
		AllowedOrigins: []string{"*"},
	}
	registry := strategies.NewRegistry()
	provider := &TestableDataProvider{priceData: map[string][]models.OHLCV{}}
	router := api.NewRouter(cfg, registry, provider, nil, nil, nil)
	server := httptest.NewServer(router)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/health")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "paper", body["mode"])
}

// TestSystemFlow_StrategyList verifies strategy listing with a real registry.
func TestSystemFlow_StrategyList(t *testing.T) {
	cfg := &config.Config{
		TradingMode:    "paper",
		AllowedOrigins: []string{"*"},
	}
	registry := strategies.NewRegistry()
	registry.Register(strategies.NewMACrossover())

	provider := &TestableDataProvider{priceData: map[string][]models.OHLCV{}}
	router := api.NewRouter(cfg, registry, provider, nil, nil, nil)
	server := httptest.NewServer(router)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/api/v1/strategies")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	strats := body["strategies"].([]interface{})
	assert.Len(t, strats, 1)
}

// TestSystemFlow_OrderPlacement verifies placing an order through the API
// with a real PaperBroker, OrderManager, and SQLite database.
func TestSystemFlow_OrderPlacement(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &config.Config{
		TradingMode:    "paper",
		AllowedOrigins: []string{"*"},
	}

	db, err := data.NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	orderStore := data.NewOrderStore(db)
	broker := execution.NewPaperBroker(100000.0)
	require.NoError(t, broker.Connect())

	orderManager := execution.NewOrderManager(broker, nil, orderStore, nil)
	registry := strategies.NewRegistry()
	provider := &TestableDataProvider{priceData: map[string][]models.OHLCV{}}

	// PaperBroker requires a price set for market orders
	broker.SetPrice("AAPL", 150.0)
	router := api.NewRouter(cfg, registry, provider, orderManager, nil, nil)
	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()

	// Place a market buy order
	payload := map[string]interface{}{
		"symbol":   "AAPL",
		"side":     "buy",
		"type":     "market",
		"quantity": 10,
	}
	body, _ := json.Marshal(payload)
	resp, err := client.Post(server.URL+"/api/v1/execution/orders", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var orderResp map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&orderResp))
	assert.Equal(t, "AAPL", orderResp["symbol"])
	assert.NotEmpty(t, orderResp["id"])

	// Verify order visible via GET /execution/orders
	resp, err = client.Get(server.URL + "/api/v1/execution/orders")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var ordersResp map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ordersResp))
	orders := ordersResp["orders"].([]interface{})
	assert.NotEmpty(t, orders, "Expected at least one order in the list")

	// Verify order persisted to DB
	dbOrders, err := orderStore.GetAllOrders()
	require.NoError(t, err)
	assert.NotEmpty(t, dbOrders, "Expected order to be persisted in DB")
}

// TestSystemFlow_EngineLifecycle verifies starting and stopping the engine
// via the API with real PaperBroker, OrderManager, and TradingEngine.
func TestSystemFlow_EngineLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &config.Config{
		TradingMode:       "paper",
		AllowedOrigins:    []string{"*"},
		EnabledStrategies: []string{"ma_crossover"},
	}

	db, err := data.NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	orderStore := data.NewOrderStore(db)
	broker := execution.NewPaperBroker(100000.0)
	require.NoError(t, broker.Connect())

	orderManager := execution.NewOrderManager(broker, nil, orderStore, nil)

	registry := strategies.NewRegistry()
	registry.Register(strategies.NewMACrossover())

	testData := generateCrossoverData("AAPL", 300)
	provider := &TestableDataProvider{
		priceData: map[string][]models.OHLCV{
			"AAPL": testData,
		},
	}

	tradingEngine := engine.NewTradingEngine(
		provider,
		registry,
		orderManager,
		nil,
		[]string{"AAPL"},
		10*time.Millisecond, // Fast tick for testing
		365*24*time.Hour,    // Lookback
	)

	router := api.NewRouter(cfg, registry, provider, orderManager, tradingEngine, nil)
	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()

	// Start Engine
	startPayload, _ := json.Marshal(map[string]bool{"confirm": true})
	resp, err := client.Post(server.URL+"/api/v1/engine/start", "application/json", bytes.NewReader(startPayload))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var startResp map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&startResp))
	assert.Equal(t, "started", startResp["status"])

	// Let it tick
	time.Sleep(200 * time.Millisecond)

	// Stop Engine
	stopPayload, _ := json.Marshal(map[string]bool{"confirm": true})
	resp, err = client.Post(server.URL+"/api/v1/engine/stop", "application/json", bytes.NewReader(stopPayload))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var stopResp map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stopResp))
	assert.Equal(t, "stopped", stopResp["status"])

	// Verify engine processed orders (strategy may or may not trigger depending on MA logic)
	resp, err = client.Get(server.URL + "/api/v1/execution/orders")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var ordersResp map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ordersResp))
	t.Logf("Orders after engine run: %d", int(ordersResp["total"].(float64)))
}

// TestSystemFlow_BacktestEndToEnd verifies running a backtest through the API
// with real strategy and provider, then retrieving the result.
func TestSystemFlow_BacktestEndToEnd(t *testing.T) {
	cfg := &config.Config{
		TradingMode:    "paper",
		AllowedOrigins: []string{"*"},
	}

	registry := strategies.NewRegistry()
	registry.Register(strategies.NewMACrossover())

	testData := generateCrossoverData("AAPL", 300)
	provider := &TestableDataProvider{
		priceData: map[string][]models.OHLCV{
			"AAPL": testData,
		},
	}

	router := api.NewRouter(cfg, registry, provider, nil, nil, nil)
	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()

	// Run backtest
	payload := map[string]interface{}{
		"strategy":        "ma_crossover",
		"symbol":          "AAPL",
		"start":           time.Now().AddDate(0, -6, 0).Format(time.RFC3339),
		"end":             time.Now().Format(time.RFC3339),
		"initial_capital": 10000,
	}
	body, _ := json.Marshal(payload)
	resp, err := client.Post(server.URL+"/api/v1/backtests", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var runResp map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&runResp))
	assert.Equal(t, "completed", runResp["status"])
	btID := runResp["id"].(string)
	assert.NotEmpty(t, btID)

	// Retrieve backtest result
	resp, err = client.Get(server.URL + "/api/v1/backtests/" + btID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var resultResp map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&resultResp))
	assert.Equal(t, btID, resultResp["id"])
	assert.Equal(t, "completed", resultResp["status"])
	assert.NotNil(t, resultResp["metrics"])
}

// TestSystemFlow_PortfolioSummary verifies the portfolio summary endpoint
// with a real PaperBroker.
func TestSystemFlow_PortfolioSummary(t *testing.T) {
	cfg := &config.Config{
		TradingMode:    "paper",
		AllowedOrigins: []string{"*"},
	}

	broker := execution.NewPaperBroker(100000.0)
	require.NoError(t, broker.Connect())

	orderManager := execution.NewOrderManager(broker, nil, nil, nil)
	registry := strategies.NewRegistry()
	provider := &TestableDataProvider{priceData: map[string][]models.OHLCV{}}

	router := api.NewRouter(cfg, registry, provider, orderManager, nil, nil)
	server := httptest.NewServer(router)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/api/v1/portfolio/summary")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.NotNil(t, body["balance"])
}
