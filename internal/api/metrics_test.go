package api

import (
	"enterprise-ai-demo/internal/metrics"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsEndpointScrapesWithoutRequestIdentifiers(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("AUTH_TOKEN", "")
	a := &API{Metrics: metrics.New()}
	h := a.Handler()
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("GET", "/api/metrics", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("metrics status %d: %s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain; version=0.0.4") {
			t.Fatalf("metrics content type %q", got)
		}
		if i == 1 {
			for _, want := range []string{"talking_agent_http_requests_total{route=\"metrics\",status=\"200\"} 1", "talking_agent_active_sessions 0", "talking_agent_session_capacity 1000"} {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("scrape missing %q:\n%s", want, w.Body.String())
				}
			}
		}
	}
}
