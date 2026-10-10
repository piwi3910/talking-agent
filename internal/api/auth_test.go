package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtectedRouteAuthTable(t *testing.T) {
	for _, tc := range []struct {
		name, path, auth string
		want             int
	}{
		{"admin missing", "/api/settings/sip", "", 401},
		{"admin bearer", "/api/settings/sip", "Bearer secret", 204},
		{"admin cookie", "/api/mcp/servers", "cookie", 204},
		{"conversation public", "/api/sessions/x/events", "", 204},
		{"health public", "/api/health", "", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := httpHandler204{}
			h := protected(next, "secret")
			r := httptest.NewRequest("GET", tc.path, nil)
			if tc.auth == "cookie" {
				r.AddCookie(&http.Cookie{Name: "auth_token", Value: "secret"})
			} else if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == 401 && w.Header().Get("Content-Type") != "application/json" {
				t.Fatal("unauthorized error is not JSON")
			}
		})
	}
}

type httpHandler204 struct{}

func (httpHandler204) ServeHTTP(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }
