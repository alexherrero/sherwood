package api

import (
	"net/http"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/rs/zerolog/log"
)

// AuthMiddleware creates a middleware that checks for a valid API Key.
// It requires the X-Sherwood-API-Key header to match the configured APIKey.
func AuthMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no API key is configured, allow all requests (or log warning)
			// For security, if no key is set in config, we might want to default to deny,
			// but for now we'll assumes empty key means no auth (dev mode).
			// However, our config prints a warning if key is empty in live mode.
			if cfg.APIKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			apiKey := r.Header.Get("X-Sherwood-API-Key")
			if apiKey != cfg.APIKey {
				log.Warn().
					Str("ip", r.RemoteAddr).
					Str("path", r.URL.Path).
					Msg("Unauthorized access attempt: invalid API key")
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
