package api

import (
	"crypto/subtle"
	"net/http"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/rs/zerolog/log"
)

// AuthMiddleware creates a middleware that checks for a valid API Key.
// It requires the X-Sherwood-API-Key header to match the configured APIKey.
// Uses constant-time comparison to prevent timing attacks.
func AuthMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no API key is configured, allow all requests (dev mode)
			// In production, API_KEY should always be set
			if cfg.APIKey == "" {
				log.Warn().Msg("No API key configured - authentication disabled (dev mode only)")
				next.ServeHTTP(w, r)
				return
			}

			apiKey := r.Header.Get("X-Sherwood-API-Key")

			// Use constant-time comparison to prevent timing attacks
			// This prevents attackers from determining API key length/content
			// by measuring response time differences
			if subtle.ConstantTimeCompare([]byte(apiKey), []byte(cfg.APIKey)) != 1 {
				log.Warn().
					Str("ip", r.RemoteAddr).
					Str("path", r.URL.Path).
					Msg("Unauthorized access attempt: invalid API key")
				writeError(w, http.StatusUnauthorized, "Unauthorized", "UNAUTHORIZED")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
