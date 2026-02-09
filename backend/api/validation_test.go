package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateStruct verifies struct validation logic.
func TestValidateStruct(t *testing.T) {
	t.Run("ValidRequest", func(t *testing.T) {
		req := PlaceOrderRequest{
			Symbol:   "BTC-USD",
			Side:     "buy",
			Type:     "market",
			Quantity: 0.01,
		}

		err := validateStruct(req)
		assert.Nil(t, err)
	})

	t.Run("MissingRequiredField", func(t *testing.T) {
		req := PlaceOrderRequest{
			Symbol:   "", // Missing required field
			Side:     "buy",
			Type:     "market",
			Quantity: 0.01,
		}

		err := validateStruct(req)
		require.NotNil(t, err)
		assert.Equal(t, "Validation failed", err.Error)
		assert.Equal(t, "VALIDATION_ERROR", err.Code)
		assert.Contains(t, err.Details, "Symbol")
		assert.Equal(t, "This field is required", err.Details["Symbol"])
	})

	t.Run("InvalidQuantity", func(t *testing.T) {
		req := PlaceOrderRequest{
			Symbol:   "BTC-USD",
			Side:     "buy",
			Type:     "market",
			Quantity: -1.0, // Invalid negative quantity
		}

		err := validateStruct(req)
		require.NotNil(t, err)
		assert.Equal(t, "VALIDATION_ERROR", err.Code)
		assert.Contains(t, err.Details, "Quantity")
	})

	t.Run("InvalidSide", func(t *testing.T) {
		req := PlaceOrderRequest{
			Symbol:   "BTC-USD",
			Side:     "invalid", // Invalid side value
			Type:     "market",
			Quantity: 0.01,
		}

		err := validateStruct(req)
		require.NotNil(t, err)
		assert.Equal(t, "VALIDATION_ERROR", err.Code)
		assert.Contains(t, err.Details, "Side")
		assert.Contains(t, err.Details["Side"], "one of")
	})

	t.Run("MultipleErrors", func(t *testing.T) {
		req := PlaceOrderRequest{
			Symbol:   "",        // Missing
			Side:     "invalid", // Invalid
			Type:     "market",
			Quantity: -1.0, // Invalid
		}

		err := validateStruct(req)
		require.NotNil(t, err)
		assert.Len(t, err.Details, 3) // Should have 3 error details
		assert.Contains(t, err.Details, "Symbol")
		assert.Contains(t, err.Details, "Side")
		assert.Contains(t, err.Details, "Quantity")
	})

	t.Run("BacktestValidation", func(t *testing.T) {
		// Test with RunBacktestRequest
		start, _ := time.Parse("2006-01-02", "2023-01-01")
		end, _ := time.Parse("2006-01-02", "2023-12-31")

		req := RunBacktestRequest{
			Strategy:       "ma_crossover",
			Symbol:         "BTC-USD",
			Start:          start,
			End:            end,
			InitialCapital: 10000,
		}

		err := validateStruct(req)
		assert.Nil(t, err)
	})

	t.Run("BacktestMissingFields", func(t *testing.T) {
		req := RunBacktestRequest{
			Strategy:       "", // Missing
			Symbol:         "BTC-USD",
			Start:          time.Time{}, // Zero value
			End:            time.Time{}, // Zero value
			InitialCapital: 0,           // Invalid
		}

		err := validateStruct(req)
		require.NotNil(t, err)
		assert.Contains(t, err.Details, "Strategy")
		// Start/End might not be in details if zero values pass the required check differently
		// but InitialCapital should fail gt=0
		assert.Contains(t, err.Details, "InitialCapital")
	})
}

// TestWriteValidationError verifies error response formatting.
func TestWriteValidationError(t *testing.T) {
	t.Run("StandardFormat", func(t *testing.T) {
		validationErr := &ValidationError{
			Error: "Validation failed",
			Code:  "VALIDATION_ERROR",
			Details: map[string]string{
				"Symbol":   "This field is required",
				"Quantity": "Value must be greater than 0",
			},
		}

		rec := httptest.NewRecorder()
		writeValidationError(rec, validationErr)

		// Check status code
		assert.Equal(t, 400, rec.Code)

		// Check content type
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		// Parse response
		var response APIError
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Verify response structure
		assert.Equal(t, "Validation failed", response.Error)
		assert.Equal(t, "VALIDATION_ERROR", response.Code)

		details, ok := response.Details.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "This field is required", details["Symbol"])
		assert.Equal(t, "Value must be greater than 0", details["Quantity"])
	})

	t.Run("NoDetails", func(t *testing.T) {
		validationErr := &ValidationError{
			Error:   "Validation failed",
			Code:    "VALIDATION_ERROR",
			Details: map[string]string{},
		}

		rec := httptest.NewRecorder()
		writeValidationError(rec, validationErr)

		assert.Equal(t, 400, rec.Code)

		var response APIError
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		details, ok := response.Details.(map[string]interface{})
		require.True(t, ok)
		assert.Empty(t, details)
	})
}
