package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/session"
)

func TestSchedulingPolicyInSystemPromptForEveryShippedAgent(t *testing.T) {
	r, agents, _ := setup(t)
	if len(agents) < 5 {
		t.Fatalf("expected the shipped agents, got %d", len(agents))
	}
	for id, a := range agents {
		t.Run(id, func(t *testing.T) {
			s := session.NewStore().Create(a, "C001")
			var mu sync.Mutex
			system := ""
			r.Clients[id] = errorClient{respond: func(req llm.Request, delta func(string)) (llm.Response, error) {
				mu.Lock()
				if system == "" {
					system = req.Messages[0].Content
				}
				mu.Unlock()
				delta("Hello")
				return llm.Response{Message: llm.Message{Role: "assistant", Content: "Hello"}}, nil
			}}
			r.Run(context.Background(), s, "turn", Turn{Text: "Hello"})
			mu.Lock()
			defer mu.Unlock()
			if !strings.Contains(system, SchedulingPolicy) {
				t.Fatal("scheduling policy missing from system prompt")
			}
			for _, phrase := range []string{"Ask which day", "at most two or three options", "never a time zone or UTC", "no lists, bullets, bold or tables", "nearest days", "confirm the day, time"} {
				if !strings.Contains(system, phrase) {
					t.Fatalf("scheduling policy lost %q", phrase)
				}
			}
			if strings.Index(system, "Scheduling conversations") > strings.Index(system, "Available skills") {
				t.Fatal("policy must precede the skills list")
			}
			// Compatible with the scope policy: both present for scoped agents, in order.
			if a.ScopeEnforced() {
				scope, sched := strings.Index(system, "Role scope (overrides"), strings.Index(system, "Scheduling conversations")
				if scope < 0 || sched < scope || !strings.Contains(system, "Do not partially comply") {
					t.Fatal("scope and scheduling policies must both be present, scope first")
				}
			}
			if a.Timezone != "" {
				if !strings.Contains(system, "the local time is") || strings.Contains(system, "Current UTC date") {
					t.Fatal("agent with a timezone must be given its local date and time")
				}
			}
			// Nothing else in the prompt may tell the model to speak a zone.
			for _, bad := range []string{"described as UTC", "Dubai time", "calendars use UTC", "Calendars use UTC", "All times UTC"} {
				if strings.Contains(system, bad) {
					t.Fatalf("system prompt still says %q", bad)
				}
			}
		})
	}
}

func TestSchedulingPolicyIsShortAndPlainSpoken(t *testing.T) {
	if len(SchedulingPolicy) > 1500 {
		t.Fatalf("policy is %d bytes; keep it short", len(SchedulingPolicy))
	}
	for _, bad := range []string{"**", "- ", "| "} {
		if strings.Contains(SchedulingPolicy, bad) {
			t.Fatalf("policy itself uses markdown %q", bad)
		}
	}
}

func TestLocalClockUsesTheAgentTimezone(t *testing.T) {
	now := time.Date(2026, 10, 9, 22, 30, 0, 0, time.UTC) // already Saturday morning in Dubai
	a := &config.Agent{Timezone: "Asia/Dubai"}
	if got := localClock(a, now); got != "Today is Saturday 10 October 2026 and the local time is 2:30 am. Tomorrow is Sunday 11 October. This week's date anchors are Saturday 10 October; Sunday 11 October; Monday 12 October; Tuesday 13 October; Wednesday 14 October; Thursday 15 October; Friday 16 October; Saturday 17 October; Sunday 18 October; Monday 19 October; Tuesday 20 October; Wednesday 21 October; Thursday 22 October; Friday 23 October; Saturday 24 October. \"Next week\" means Monday 12 October to Sunday 18 October." {
		t.Fatal(got)
	}
	if got := localClock(&config.Agent{}, now); got != "" {
		t.Fatalf("no timezone, no local clock: %q", got)
	}
}
