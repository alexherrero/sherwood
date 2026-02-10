package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const (
	// auditIPKey is the context key for the requestor's IP address.
	auditIPKey contextKey = "audit_ip"
	// auditKeyIDKey is the context key for the API key identifier.
	auditKeyIDKey contextKey = "audit_key_id"
)

// AuditMiddleware injects audit context (IP address, API key identifier)
// into the request context for downstream logging.
// The API key identifier is a truncated SHA-256 hash of the key,
// safe for logging without exposing the full key.
func AuditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Extract client IP
		ip := r.RemoteAddr
		ctx = context.WithValue(ctx, auditIPKey, ip)

		// Extract API key identifier (hash prefix for safe logging)
		apiKey := r.Header.Get("X-Sherwood-API-Key")
		keyID := "dev-mode"
		if apiKey != "" {
			hash := sha256.Sum256([]byte(apiKey))
			keyID = fmt.Sprintf("%x", hash[:4]) // First 8 hex chars
		}
		ctx = context.WithValue(ctx, auditKeyIDKey, keyID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AuditIPFromCtx extracts the requestor IP from context.
// Returns "unknown" if not present.
func AuditIPFromCtx(ctx context.Context) string {
	if ip, ok := ctx.Value(auditIPKey).(string); ok {
		return ip
	}
	return "unknown"
}

// AuditKeyIDFromCtx extracts the API key identifier from context.
// Returns "unknown" if not present.
func AuditKeyIDFromCtx(ctx context.Context) string {
	if keyID, ok := ctx.Value(auditKeyIDKey).(string); ok {
		return keyID
	}
	return "unknown"
}
