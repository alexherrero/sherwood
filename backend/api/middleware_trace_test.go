package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexherrero/sherwood/backend/tracing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTraceMiddleware_InjectsTraceID verifies that the middleware injects
// a trace ID into the request context and response header.
func TestTraceMiddleware_InjectsTraceID(t *testing.T) {
	var capturedTraceID string

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTraceID = tracing.TraceIDFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := TraceMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, capturedTraceID, "trace ID should be set in context")
	assert.Len(t, capturedTraceID, 16, "generated trace ID should be 16 hex chars")

	// Verify response header
	headerTraceID := rec.Header().Get("X-Trace-ID")
	assert.Equal(t, capturedTraceID, headerTraceID, "response header should match context trace ID")
}

// TestTraceMiddleware_UniquePerRequest verifies that each request gets
// a unique trace ID.
func TestTraceMiddleware_UniquePerRequest(t *testing.T) {
	var traceIDs []string

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceIDs = append(traceIDs, tracing.TraceIDFromCtx(r.Context()))
		w.WriteHeader(http.StatusOK)
	})

	handler := TraceMiddleware(inner)

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// All trace IDs should be unique
	seen := make(map[string]bool)
	for _, id := range traceIDs {
		assert.False(t, seen[id], "trace ID collision detected")
		seen[id] = true
	}
}

// TestTraceMiddleware_ResponseHeader verifies X-Trace-ID is set on responses.
func TestTraceMiddleware_ResponseHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := TraceMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	headerVal := rec.Header().Get("X-Trace-ID")
	assert.NotEmpty(t, headerVal, "X-Trace-ID header should be set")
}
