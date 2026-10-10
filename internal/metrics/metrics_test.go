package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrometheusOutput(t *testing.T) {
	r := New()
	r.Add("talking_agent_http_requests_total", 2, "session_events", "200")
	r.Observe("talking_agent_http_request_duration_seconds", .25, "session_events")
	r.IncGauge("talking_agent_sse_streams", 1)
	var out bytes.Buffer
	if err := r.WritePrometheus(&out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`talking_agent_http_requests_total{route="session_events",status="200"} 2`,
		`talking_agent_http_request_duration_seconds_count{route="session_events"} 1`,
		`talking_agent_http_request_duration_seconds_sum{route="session_events"} 0.25`,
		`talking_agent_sse_streams 1`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"session_id", "user_id", "prompt", "conversation text"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("output contains %q", forbidden)
		}
	}
}
