// Package security provides HTTP middleware for NornicDB security validation.
package security

import (
"fmt"
"net/http"
"os"
"strings"
)

// SecurityMiddleware wraps HTTP handlers with security validations.
type SecurityMiddleware struct {
	isDevelopment bool
	allowHTTP     bool
}

// NewSecurityMiddleware creates a new security middleware instance.
func NewSecurityMiddleware() *SecurityMiddleware {
	env := strings.ToLower(os.Getenv("NODE_ENV"))
	if env == "" {
		env = strings.ToLower(os.Getenv("NORNICDB_ENV"))
	}
	
	isDevelopment := env == "development" || env == "dev"
	allowHTTP := os.Getenv("NORNICDB_ALLOW_HTTP") == "true"
	
	return &SecurityMiddleware{
		isDevelopment: isDevelopment,
		allowHTTP:     allowHTTP,
	}
}

// ValidateRequest performs comprehensive security validation on incoming requests.
func (m *SecurityMiddleware) ValidateRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// Validate all header values for injection attacks
for name, values := range r.Header {
for _, value := range values {
if err := ValidateHeaderValue(value); err != nil {
					http.Error(w, fmt.Sprintf("Invalid header %s: %v", name, err), http.StatusBadRequest)
					return
				}
			}
		}
		
		// Validate Authorization header specifically
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 {
				token := strings.TrimSpace(parts[1])
				if err := ValidateToken(token); err != nil {
					http.Error(w, fmt.Sprintf("Invalid authorization token: %v", err), http.StatusUnauthorized)
					return
				}
			}
		}
		
		// Validate query parameter tokens (for SSE/WebSocket)
		if tokenParam := r.URL.Query().Get("token"); tokenParam != "" {
			if err := ValidateToken(tokenParam); err != nil {
				http.Error(w, fmt.Sprintf("Invalid token parameter: %v", err), http.StatusUnauthorized)
				return
			}
		}
		
		// Validate URL parameters
		urlParams := []string{"callback", "redirect", "redirect_uri", "url", "webhook"}
		for _, param := range urlParams {
			if urlValue := r.URL.Query().Get(param); urlValue != "" {
				if err := ValidateURL(urlValue, m.isDevelopment, m.allowHTTP); err != nil {
					http.Error(w, fmt.Sprintf("Invalid %s parameter: %v", param, err), http.StatusBadRequest)
					return
				}
			}
		}
		
		next.ServeHTTP(w, r)
	})
}

// Wrap is a convenience method for wrapping individual handlers.
func (m *SecurityMiddleware) Wrap(handler http.Handler) http.Handler {
	return m.ValidateRequest(handler)
}
