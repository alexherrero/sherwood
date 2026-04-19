package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAuditMiddleware_InjectsContext verifies that the audit middleware
// injects IP and key ID into the request context.
func TestAuditMiddleware_InjectsContext(t *testing.T) {
	var capturedCtx context.Context

	// Create a handler that captures the context
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	handler := AuditMiddleware(inner)

	t.Run("WithAPIKey", func(t *testing.T) {
		apiKey := "test-api-key-12345"
		expectedHash := sha256.Sum256([]byte(apiKey))
		expectedKeyID := fmt.Sprintf("%x", expectedHash[:4])

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Sherwood-API-Key", apiKey)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		ip := AuditIPFromCtx(capturedCtx)
		keyID := AuditKeyIDFromCtx(capturedCtx)

		assert.NotEmpty(t, ip)
		assert.NotEqual(t, "unknown", ip)
		assert.Equal(t, expectedKeyID, keyID)
	})

	t.Run("DevMode_NoAPIKey", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		// No API key header
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		keyID := AuditKeyIDFromCtx(capturedCtx)
		assert.Equal(t, "dev-mode", keyID)
	})

	t.Run("RemoteAddr_Captured", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.168.1.100:54321"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		ip := AuditIPFromCtx(capturedCtx)
		assert.Equal(t, "192.168.1.100:54321", ip)
	})
}

// TestAuditHelpers_MissingContext verifies that helper functions return
// "unknown" when context does not contain audit values.
func TestAuditHelpers_MissingContext(t *testing.T) {
	ctx := context.Background()

	assert.Equal(t, "unknown", AuditIPFromCtx(ctx))
	assert.Equal(t, "unknown", AuditKeyIDFromCtx(ctx))
}

// TestAuditKeyID_Deterministic verifies that the same API key always
// produces the same key ID.
func TestAuditKeyID_Deterministic(t *testing.T) {
	var capturedKeyIDs []string

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKeyIDs = append(capturedKeyIDs, AuditKeyIDFromCtx(r.Context()))
		w.WriteHeader(http.StatusOK)
	})

	handler := AuditMiddleware(inner)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Sherwood-API-Key", "same-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	assert.Len(t, capturedKeyIDs, 3)
	assert.Equal(t, capturedKeyIDs[0], capturedKeyIDs[1])
	assert.Equal(t, capturedKeyIDs[1], capturedKeyIDs[2])
}

// TestAuditKeyID_DifferentKeys verifies that different API keys produce
// different key IDs.
func TestAuditKeyID_DifferentKeys(t *testing.T) {
	var capturedKeyIDs []string

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKeyIDs = append(capturedKeyIDs, AuditKeyIDFromCtx(r.Context()))
		w.WriteHeader(http.StatusOK)
	})

	handler := AuditMiddleware(inner)

	keys := []string{"key-alpha", "key-beta", "key-gamma"}
	for _, key := range keys {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Sherwood-API-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	assert.Len(t, capturedKeyIDs, 3)
	assert.NotEqual(t, capturedKeyIDs[0], capturedKeyIDs[1])
	assert.NotEqual(t, capturedKeyIDs[1], capturedKeyIDs[2])
}
