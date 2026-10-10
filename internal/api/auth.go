package api

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

// authToken returns the configured operator token. Setting AUTH_TOKEN enables
// protection; AUTH_ENABLED=true can also enable it explicitly.
func authToken() string {
	token := os.Getenv("AUTH_TOKEN")
	enabled := strings.EqualFold(os.Getenv("AUTH_ENABLED"), "true") || os.Getenv("AUTH_ENABLED") == "1"
	if !enabled && token == "" {
		return ""
	}
	return token
}

func adminRoute(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/api/settings/") || strings.HasPrefix(p, "/api/mcp/") || strings.HasPrefix(p, "/api/traces") || p == "/api/system/info"
}

func protected(next http.Handler, token string) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !adminRoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(got), "bearer ") {
			got = strings.TrimSpace(got[7:])
		} else {
			got = ""
		}
		if got == "" {
			if c, err := r.Cookie("auth_token"); err == nil {
				got = c.Value
			}
		}
		if len(got) != len(token) || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
