package mock

import (
	"context"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"
)

// span is the simulated service time of one tool call: a uniform draw between
// min and max, so repeated calls feel different.
type span struct{ min, max time.Duration }

func ms(min, max int) span {
	return span{time.Duration(min) * time.Millisecond, time.Duration(max) * time.Millisecond}
}

// toolLatency lists simulated durations by exact tool name. Real diagnostics,
// bookings and CRM calls take time; instant answers make an agent that says
// "let me check" sound like it never looked.
var toolLatency = map[string]span{
	"network.diagnostics":  ms(3500, 6000),
	"network.speedtest":    ms(3000, 5500),
	"wifi.optimize":        ms(2500, 4500),
	"wifi.restart":         ms(4000, 6000),
	"wifi.diagnostics":     ms(2000, 3500),
	"network.status":       ms(800, 1600),
	"network.outages":      ms(800, 1500),
	"wifi.status":          ms(700, 1400),
	"wifi.devices":         ms(800, 1500),
	"plan.change":          ms(1500, 2500),
	"ticket.create":        ms(900, 1600),
	"admissions.fee_quote": ms(500, 1000),
	"transport.quote":      ms(500, 1000),
	"scholarship.check":    ms(600, 1100),
	"crm.note":             ms(300, 700),
	"outreach.log_outcome": ms(300, 700),
}

// DefaultLatency applies to tools without their own entry or family rule.
var DefaultLatency = ms(600, 1500)

func latencyFor(tool string) span {
	if s, ok := toolLatency[tool]; ok {
		return s
	}
	switch {
	case strings.HasSuffix(tool, ".availability"):
		return ms(1000, 2500)
	case strings.HasSuffix(tool, ".book"), strings.HasSuffix(tool, ".meeting_book"), strings.HasSuffix(tool, ".cancel"), tool == "callback.schedule", tool == "application.start":
		return ms(1200, 2500)
	}
	return DefaultLatency
}

// LatencyScaleFromEnv reads MOCK_LATENCY_SCALE: 1 (default) uses the table, 0
// disables simulated latency, 0.5 halves it and 2 doubles it.
func LatencyScaleFromEnv() float64 {
	raw := strings.TrimSpace(os.Getenv("MOCK_LATENCY_SCALE"))
	if raw == "" {
		return 1
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 10 {
		return 1
	}
	return v
}

// Delay is the simulated duration for tool at the given scale.
func Delay(tool string, scale float64) time.Duration {
	if scale <= 0 {
		return 0
	}
	s := latencyFor(tool)
	d := s.min
	if s.max > s.min {
		d += time.Duration(rand.Int64N(int64(s.max - s.min)))
	}
	return time.Duration(float64(d) * scale)
}

// wait sleeps for the tool's simulated duration. It returns early with the
// context error when the caller cancels (barge-in, stop, timeout).
func (b *Backend) wait(ctx context.Context, tool string) error {
	d := Delay(tool, b.LatencyScale)
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
