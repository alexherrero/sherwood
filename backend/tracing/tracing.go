// Package tracing provides trace ID generation and context propagation
// for structured logging across the Sherwood trading engine.
//
// Trace IDs are unique identifiers attached to operations (API requests,
// engine ticks, order executions) to enable tracing logic flow across
// components. They are propagated via context.Context and included in
// zerolog structured log fields.
package tracing

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const (
	// traceIDKey is the context key for the trace ID.
	traceIDKey contextKey = "trace_id"

	// TraceIDField is the zerolog field name used for trace IDs.
	TraceIDField = "trace_id"
)

// NewTraceID generates a cryptographically random trace ID.
// The ID is a 16-character lowercase hex string (64 bits of entropy).
//
// Returns:
//   - string: A unique trace ID
func NewTraceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback: this should never happen in practice
		return "0000000000000000"
	}
	return fmt.Sprintf("%x", b)
}

// WithTraceID returns a new context with the given trace ID attached.
//
// Args:
//   - ctx: Parent context
//   - traceID: The trace ID to attach
//
// Returns:
//   - context.Context: Context with trace ID
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceIDFromCtx extracts the trace ID from context.
// Returns an empty string if no trace ID is present.
//
// Args:
//   - ctx: Context to extract from
//
// Returns:
//   - string: The trace ID, or "" if not present
func TraceIDFromCtx(ctx context.Context) string {
	if id, ok := ctx.Value(traceIDKey).(string); ok {
		return id
	}
	return ""
}

// Logger returns a zerolog sub-logger with the trace ID from context.
// If no trace ID is present in the context, it returns the global logger
// without a trace_id field.
//
// Usage:
//
//	tracing.Logger(ctx).Info().Str("symbol", "AAPL").Msg("Processing symbol")
//
// Args:
//   - ctx: Context containing trace ID
//
// Returns:
//   - zerolog.Logger: Logger with trace_id field
func Logger(ctx context.Context) zerolog.Logger {
	traceID := TraceIDFromCtx(ctx)
	if traceID == "" {
		return log.Logger
	}
	return log.With().Str(TraceIDField, traceID).Logger()
}
