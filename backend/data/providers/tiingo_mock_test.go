package providers

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockRoundTripper allows mocking HTTP responses
type MockRoundTripper struct {
	RoundTripFunc func(req *http.Request) *http.Response
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.RoundTripFunc(req), nil
}

func TestTiingoProvider_GetTicker_Mock(t *testing.T) {
	// Setup Provider
	p := NewTiingoProvider("test-key")

	// Mock HTTP Client
	mockTransport := &MockRoundTripper{
		RoundTripFunc: func(req *http.Request) *http.Response {
			assert.Equal(t, "https://api.tiingo.com/tiingo/daily/AAPL", req.URL.String())
			assert.Equal(t, "Token test-key", req.Header.Get("Authorization"))

			jsonResp := `{
				"ticker": "AAPL",
				"name": "Apple Inc",
				"description": "Tech company",
				"exchangeCode": "NASDAQ"
			}`
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(jsonResp)),
				Header:     make(http.Header),
			}
		},
	}
	p.httpClient.Transport = mockTransport

	// Test
	ticker, err := p.GetTicker("AAPL")
	require.NoError(t, err)
	assert.Equal(t, "AAPL", ticker.Symbol)
	assert.Equal(t, "Apple Inc", ticker.Name)
	assert.Equal(t, "NASDAQ", ticker.Exchange)
}

func TestTiingoProvider_GetLatestPrice_Mock(t *testing.T) {
	p := NewTiingoProvider("test-key")

	mockTransport := &MockRoundTripper{
		RoundTripFunc: func(req *http.Request) *http.Response {
			assert.Equal(t, "https://api.tiingo.com/tiingo/daily/AAPL/prices", req.URL.String())

			jsonResp := `[
				{"date":"2023-01-01T00:00:00.000Z", "adjClose": 150.0},
				{"date":"2023-01-02T00:00:00.000Z", "adjClose": 155.0}
			]`
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(jsonResp)),
				Header:     make(http.Header),
			}
		},
	}
	p.httpClient.Transport = mockTransport

	price, err := p.GetLatestPrice("AAPL")
	require.NoError(t, err)
	assert.Equal(t, 155.0, price)
}

func TestTiingoProvider_GetHistoricalData_Mock(t *testing.T) {
	p := NewTiingoProvider("test-key")

	mockTransport := &MockRoundTripper{
		RoundTripFunc: func(req *http.Request) *http.Response {
			// Check params
			q := req.URL.Query()
			assert.Equal(t, "2023-01-01", q.Get("startDate"))

			jsonResp := `[
				{
					"date":"2023-01-01T00:00:00.000Z", 
					"adjOpen": 100.0,
					"adjHigh": 110.0,
					"adjLow": 90.0,
					"adjClose": 105.0,
					"volume": 1000000
				}
			]`
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(jsonResp)),
				Header:     make(http.Header),
			}
		},
	}
	p.httpClient.Transport = mockTransport

	start, _ := time.Parse("2006-01-02", "2023-01-01")
	end, _ := time.Parse("2006-01-02", "2023-01-02")

	data, err := p.GetHistoricalData("AAPL", start, end, "1d")
	require.NoError(t, err)
	require.Len(t, data, 1)
	assert.Equal(t, 105.0, data[0].Close)
}

func TestTiingoProvider_ErrorHandling_Mock(t *testing.T) {
	p := NewTiingoProvider("test-key")

	mockTransport := &MockRoundTripper{
		RoundTripFunc: func(req *http.Request) *http.Response {
			return &http.Response{
				StatusCode: 401,
				Body:       io.NopCloser(bytes.NewBufferString("Unauthorized")),
				Header:     make(http.Header),
			}
		},
	}
	p.httpClient.Transport = mockTransport

	_, err := p.GetTicker("AAPL")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 401")
}
