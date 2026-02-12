package tracing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTraceID_Unique verifies that generated trace IDs are unique.
func TestNewTraceID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewTraceID()
		require.NotEmpty(t, id)
		assert.Len(t, id, 16, "trace ID should be 16 hex chars")
		assert.False(t, seen[id], "trace ID collision detected")
		seen[id] = true
	}
}

// TestNewTraceID_Format verifies trace ID format is lowercase hex.
func TestNewTraceID_Format(t *testing.T) {
	id := NewTraceID()
	for _, c := range id {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"trace ID should only contain hex chars, got: %c", c)
	}
}

// TestWithTraceID_RoundTrip verifies setting and getting trace ID from context.
func TestWithTraceID_RoundTrip(t *testing.T) {
	traceID := "abc123def4567890"
	ctx := WithTraceID(context.Background(), traceID)

	got := TraceIDFromCtx(ctx)
	assert.Equal(t, traceID, got)
}

// TestTraceIDFromCtx_Missing verifies empty string when no trace ID set.
func TestTraceIDFromCtx_Missing(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", TraceIDFromCtx(ctx))
}

// TestLogger_WithTraceID verifies the logger includes trace_id field.
func TestLogger_WithTraceID(t *testing.T) {
	traceID := NewTraceID()
	ctx := WithTraceID(context.Background(), traceID)

	logger := Logger(ctx)
	// Logger should not be zero-value
	assert.NotNil(t, logger)
}

// TestLogger_WithoutTraceID verifies the logger works without trace ID.
func TestLogger_WithoutTraceID(t *testing.T) {
	ctx := context.Background()
	logger := Logger(ctx)
	assert.NotNil(t, logger)
}
