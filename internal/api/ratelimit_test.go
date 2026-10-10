package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenBucketTable(t *testing.T) {
	for _, tc := range []struct {
		name        string
		calls       int
		rate, burst float64
		accepted    int
	}{
		{"burst honored", 4, 1, 3, 3},
		{"steady tokens", 2, 5, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b tokenBuckets
			n := 0
			for i := 0; i < tc.calls; i++ {
				ok, _ := b.allow("client", tc.rate, tc.burst)
				if ok {
					n++
				}
			}
			if n != tc.accepted {
				t.Fatalf("accepted %d, want %d", n, tc.accepted)
			}
		})
	}
}

func TestRateLimitResponseTable(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/sessions/s1/messages", 429},
		{http.MethodPost, "/api/sessions/s1/speech", 429},
		{http.MethodGet, "/api/sessions/s1/events", 204},
	} {
		t.Run(tc.path, func(t *testing.T) {
			l := &requestLimiter{ipRate: 100, ipBurst: 100, messageRate: 1, messageBurst: 1, speechRate: 1, speechBurst: 1}
			h := l.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.SetPathValue("id", "s1")
			if tc.want == 429 {
				key := "speech:s1"
				if strings.HasSuffix(tc.path, "/messages") {
					key = "messages:s1"
				}
				_, _ = l.buckets.allow(key, 1, 1)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
			if tc.want == 429 && (w.Header().Get("Retry-After") == "" || w.Header().Get("Content-Type") != "application/json") {
				t.Fatalf("missing retry/error headers: %v", w.Header())
			}
		})
	}
}
