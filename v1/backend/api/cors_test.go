package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/stretchr/testify/assert"
)

// TestCORSMiddleware verifies CORS middleware behavior.
func TestCORSMiddleware(t *testing.T) {
	cfg := &config.Config{
		AllowedOrigins: []string{"http://localhost:3000", "http://localhost:8099"},
	}

	middleware := newCORSMiddleware(cfg)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	t.Run("AllowedOrigin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Verify CORS headers are set
		assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "GET, POST, PUT, DELETE, PATCH, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
		assert.Equal(t, "Content-Type, Authorization, X-Sherwood-API-Key", rec.Header().Get("Access-Control-Allow-Headers"))
		assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
		assert.Equal(t, "3600", rec.Header().Get("Access-Control-Max-Age"))
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("RejectedOrigin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", "http://evil.com")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Verify no CORS headers are set for rejected origin
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
		// Request should still proceed (just without CORS headers)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("PreflightAllowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Verify CORS headers are set
		assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
		// OPTIONS request should return 200 for allowed origins
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("PreflightRejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		req.Header.Set("Origin", "http://evil.com")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Verify no CORS headers for rejected origin
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		// OPTIONS request should return 403 for rejected origins
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("NoOriginHeader", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		// No Origin header set
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// No CORS headers should be set
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		// Request should still proceed normally
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("MultipleAllowedOrigins", func(t *testing.T) {
		// Test second allowed origin
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", "http://localhost:8099")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Verify CORS headers are set for second allowed origin
		assert.Equal(t, "http://localhost:8099", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}
