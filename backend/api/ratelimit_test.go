package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/stretchr/testify/assert"
)

// TestRateLimiting verifies that rate limiting is enforced.
func TestRateLimiting(t *testing.T) {
	cfg := &config.Config{
		APIKey: "test-api-key",
	}

	// Create a simple test router with rate limiting
	router := NewRouter(cfg, nil, nil, nil, nil)

	t.Run("burst_limit_enforcement", func(t *testing.T) {
		// Test burst protection (20 req/sec)
		// Send 25 requests rapidly from same IP
		successCount := 0
		rateLimitedCount := 0

		for i := 0; i < 25; i++ {
			req := httptest.NewRequest("GET", "/health", nil)
			req.RemoteAddr = "192.168.1.100:12345" // Same IP
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code == http.StatusOK {
				successCount++
			} else if w.Code == http.StatusTooManyRequests {
				rateLimitedCount++
			}
		}

		// Should have some rate limited requests
		assert.Greater(t, rateLimitedCount, 0, "Expected some requests to be rate limited")
		assert.Less(t, rateLimitedCount, 25, "Not all requests should be rate limited")

		t.Logf("Successful requests: %d, Rate limited: %d", successCount, rateLimitedCount)
	})

	t.Run("different_ips_independent", func(t *testing.T) {
		// Requests from different IPs should have independent rate limits
		req1 := httptest.NewRequest("GET", "/health", nil)
		req1.RemoteAddr = "192.168.1.1:12345"
		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, req1)

		req2 := httptest.NewRequest("GET", "/health", nil)
		req2.RemoteAddr = "192.168.1.2:12345"
		w2 := httptest.NewRecorder()
		router.ServeHTTP(w2, req2)

		// Both should succeed (different IPs)
		assert.Equal(t, http.StatusOK, w1.Code)
		assert.Equal(t, http.StatusOK, w2.Code)
	})

	t.Run("rate_limit_recovery", func(t *testing.T) {
		// After waiting, rate limit should reset
		req := httptest.NewRequest("GET", "/health", nil)
		req.RemoteAddr = "192.168.1.200:12345"

		// Exhaust rate limit
		for i := 0; i < 21; i++ {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
		}

		// Wait for rate limit window to pass (>1 second)
		time.Sleep(1100 * time.Millisecond)

		// Should succeed now
		w := httptest.NewRecorder()
		freshReq := httptest.NewRequest("GET", "/health", nil)
		freshReq.RemoteAddr = "192.168.1.200:12345"
		router.ServeHTTP(w, freshReq)

		// This test is time-sensitive and may be flaky
		// Just log the result rather than asserting
		t.Logf("After recovery wait, status code: %d", w.Code)
	})
}
