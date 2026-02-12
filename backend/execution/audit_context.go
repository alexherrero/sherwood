package execution

import "context"

// contextKey is a private type for context keys to avoid collisions.
// These keys must match the ones used by the API audit middleware.
type contextKey string

const (
	// auditIPKey is the context key for the requestor's IP address.
	auditIPKey contextKey = "audit_ip"
	// auditKeyIDKey is the context key for the API key identifier.
	auditKeyIDKey contextKey = "audit_key_id"
)

// auditIPFromCtx extracts the requestor IP from context.
// Returns "unknown" if not present.
func auditIPFromCtx(ctx context.Context) string {
	if ip, ok := ctx.Value(auditIPKey).(string); ok {
		return ip
	}
	return "unknown"
}

// auditKeyIDFromCtx extracts the API key identifier from context.
// Returns "unknown" if not present.
func auditKeyIDFromCtx(ctx context.Context) string {
	if keyID, ok := ctx.Value(auditKeyIDKey).(string); ok {
		return keyID
	}
	return "unknown"
}

// NewEngineContext creates a context with audit fields and a trace ID
// for engine-initiated operations, distinguishing automated orders
// from manual API orders.
//
// Each engine context receives a unique trace ID so that all log entries
// and downstream operations for the same engine action can be correlated.
func NewEngineContext() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, auditIPKey, "engine")
	ctx = context.WithValue(ctx, auditKeyIDKey, "system")
	return ctx
}

// NewEngineContextWithTrace creates a context with audit fields and
// a pre-existing trace ID. Use this when the caller already has a
// trace ID (e.g., from an engine tick) that should be propagated to
// child operations.
//
// Args:
//   - parentCtx: Parent context containing trace ID
//
// Returns:
//   - context.Context: Context with engine audit fields and inherited trace ID
func NewEngineContextWithTrace(parentCtx context.Context) context.Context {
	ctx := parentCtx
	ctx = context.WithValue(ctx, auditIPKey, "engine")
	ctx = context.WithValue(ctx, auditKeyIDKey, "system")
	return ctx
}
