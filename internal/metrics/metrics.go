// Package metrics provides a small, dependency-free Prometheus text registry.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Registry stores in-memory counters and duration sums. Labels are constrained
// by callers to route classes and operation names; request and conversation data
// must never be used as labels.
type Registry struct {
	mu        sync.Mutex
	counters  map[string]float64
	durations map[string]duration
	gauges    map[string]int64
}

type duration struct {
	Count uint64
	Sum   float64
}

func New() *Registry {
	return &Registry{counters: map[string]float64{}, durations: map[string]duration{}, gauges: map[string]int64{}}
}

func key(name string, labels ...string) string { return name + "\x00" + strings.Join(labels, "\x00") }
func (r *Registry) Add(name string, value float64, labels ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.counters[key(name, labels...)] += value
	r.mu.Unlock()
}
func (r *Registry) Observe(name string, seconds float64, labels ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	k := key(name, labels...)
	d := r.durations[k]
	d.Count++
	d.Sum += seconds
	r.durations[k] = d
	r.mu.Unlock()
}
func (r *Registry) IncGauge(name string, delta int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.gauges[name] += delta
	r.mu.Unlock()
}
func (r *Registry) SetGauge(name string, value int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.gauges[name] = value
	r.mu.Unlock()
}

// WritePrometheus emits stable scrape-friendly text without exposing any
// request, session, user, prompt, or credential values.
func (r *Registry) WritePrometheus(w io.Writer) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	counters := make(map[string]float64, len(r.counters))
	for k, v := range r.counters {
		counters[k] = v
	}
	durations := make(map[string]duration, len(r.durations))
	for k, v := range r.durations {
		durations[k] = v
	}
	gauges := make(map[string]int64, len(r.gauges))
	for k, v := range r.gauges {
		gauges[k] = v
	}
	r.mu.Unlock()
	var names []string
	for k := range counters {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		parts := strings.Split(k, "\x00")
		if _, err := fmt.Fprintf(w, "%s%s %g\n", parts[0], formatLabels(parts[0], parts[1:]), counters[k]); err != nil {
			return err
		}
	}
	names = names[:0]
	for k := range durations {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		parts := strings.Split(k, "\x00")
		d := durations[k]
		base := parts[0]
		labels := formatLabels(base, parts[1:])
		if _, err := fmt.Fprintf(w, "%s_count%s %d\n%s_sum%s %g\n", base, labels, d.Count, base, labels, d.Sum); err != nil {
			return err
		}
	}
	names = names[:0]
	for k := range gauges {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if _, err := fmt.Fprintf(w, "%s %d\n", k, gauges[k]); err != nil {
			return err
		}
	}
	return nil
}

var labelNames = map[string][]string{
	"talking_agent_http_requests_total":           {"route", "status"},
	"talking_agent_http_request_duration_seconds": {"route"},
	"talking_agent_llm_calls_total":               {"status"},
	"talking_agent_llm_call_duration_seconds":     {},
	"talking_agent_tool_calls_total":              {"tool", "status"},
	"talking_agent_tool_call_duration_seconds":    {"tool"},
	"talking_agent_turns_total":                   {"status"},
	"talking_agent_turn_duration_seconds":         {"status"},
	"talking_agent_memory_operations_total":       {"operation"},
	"talking_agent_speech_requests_total":         {"operation", "status"},
	"talking_agent_speech_duration_seconds":       {"operation"},
}

func formatLabels(metric string, labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	names := labelNames[metric]
	for i, v := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		name := fmt.Sprintf("label%d", i)
		if i < len(names) {
			name = names[i]
		}
		fmt.Fprintf(&b, "%s=%q", name, v)
	}
	b.WriteByte('}')
	return b.String()
}
