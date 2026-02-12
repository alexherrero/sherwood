package api

import (
	"net/http"

	"github.com/alexherrero/sherwood/backend/tracing"
	"github.com/go-chi/chi/v5/middleware"
)

// TraceMiddleware injects a trace ID into the request context for
// structured logging correlation. If a chi RequestID is already present
// in the context, it is used as the trace ID. Otherwise, a new
// cryptographically random trace ID is generated.
//
// The trace ID is also set as a response header (X-Trace-ID) to allow
// clients and operators to correlate requests with backend logs.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use chi's RequestID if available, otherwise generate our own
		traceID := middleware.GetReqID(r.Context())
		if traceID == "" {
			traceID = tracing.NewTraceID()
		}

		// Inject into context
		ctx := tracing.WithTraceID(r.Context(), traceID)

		// Set response header for client-side correlation
		w.Header().Set("X-Trace-ID", traceID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
