package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware(t *testing.T) {
	// Setup a simple handler to wrap
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("No API Key Configured (Allow all)", func(t *testing.T) {
		cfg := &config.Config{APIKey: ""}
		middleware := AuthMiddleware(cfg)
		handler := middleware(nextHandler)

		req := httptest.NewRequest("GET", "/api/v1/protected", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("API Key Configured - Missing Header", func(t *testing.T) {
		cfg := &config.Config{APIKey: "secret123"}
		middleware := AuthMiddleware(cfg)
		handler := middleware(nextHandler)

		req := httptest.NewRequest("GET", "/api/v1/protected", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("API Key Configured - Wrong Header", func(t *testing.T) {
		cfg := &config.Config{APIKey: "secret123"}
		middleware := AuthMiddleware(cfg)
		handler := middleware(nextHandler)

		req := httptest.NewRequest("GET", "/api/v1/protected", nil)
		req.Header.Set("X-Sherwood-API-Key", "wrong-key")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("API Key Configured - Correct Header", func(t *testing.T) {
		cfg := &config.Config{APIKey: "secret123"}
		middleware := AuthMiddleware(cfg)
		handler := middleware(nextHandler)

		req := httptest.NewRequest("GET", "/api/v1/protected", nil)
		req.Header.Set("X-Sherwood-API-Key", "secret123")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})
}
