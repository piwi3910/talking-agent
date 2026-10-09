package agent

import (
	"context"
	"strings"
	"testing"

	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/session"
)

func TestActionPolicyInSystemPromptForEveryShippedAgent(t *testing.T) {
	r, agents, _ := setup(t)
	for id, a := range agents {
		t.Run(id, func(t *testing.T) {
			s := session.NewStore().Create(a, "C001")
			system := ""
			r.Clients[id] = errorClient{respond: func(req llm.Request, delta func(string)) (llm.Response, error) {
				system = req.Messages[0].Content
				delta("Hello")
				return llm.Response{Message: llm.Message{Role: "assistant", Content: "Hello"}}, nil
			}}
			r.Run(context.Background(), s, "turn", Turn{Text: "Hello"})
			if !strings.Contains(system, ActionPolicy) {
				t.Fatal("action policy missing from system prompt")
			}
			if strings.Index(system, ActionPolicy) > strings.Index(system, "Available skills") {
				t.Fatal("policy must precede the skills list")
			}
		})
	}
	for _, phrase := range []string{"call the tool in that same turn", "wait for its result", "one short spoken line first", "Never describe, guess or promise results you have not received", "act on the real data"} {
		if !strings.Contains(ActionPolicy, phrase) {
			t.Fatalf("policy lost %q", phrase)
		}
	}
	if len(ActionPolicy) > 1000 {
		t.Fatalf("policy is %d bytes; keep it short", len(ActionPolicy))
	}
}

// Pre-tool speech is delivered as its own phrase before the tool starts, and the
// follow-up is generated from the real tool result.
func TestSpeakThenActSplitsPhrasesAndUsesRealResult(t *testing.T) {
	r, agents, _ := setup(t)
	s := session.NewStore().Create(agents["telecom-support"], "C001")
	calls := 0
	sawResult := false
	r.Clients[s.Agent.ID] = errorClient{respond: func(req llm.Request, delta func(string)) (llm.Response, error) {
		calls++
		if calls == 1 {
			delta("One moment, I'm checking the technician diary.")
			return llm.Response{Message: llm.Message{Role: "assistant", Content: "One moment, I'm checking the technician diary.", ToolCalls: []llm.Call{{ID: "c1", Type: "function", Function: llm.Function{Name: "technician__availability", Arguments: `{}`}}}}}, nil
		}
		for _, m := range req.Messages {
			if m.Role == "tool" && strings.Contains(m.Content, "Available technician visits") {
				sawResult = true
			}
		}
		delta("Right, I can see morning visits.")
		return llm.Response{Message: llm.Message{Role: "assistant", Content: "Right, I can see morning visits."}}, nil
	}}
	r.Run(context.Background(), s, "turn", Turn{Text: "Show technician visits"})
	order := []string{}
	for _, e := range s.Events.Since(0) {
		switch e.Type {
		case "agent.response.delta":
			text, _ := e.Data.(map[string]any)["text"].(string)
			order = append(order, "delta:"+strings.TrimSpace(text))
		case "tool.started", "tool.completed":
			order = append(order, e.Type)
		}
	}
	got := strings.Join(order, "|")
	want := "delta:One moment, I'm checking the technician diary.|delta:|tool.started|tool.completed|delta:Right, I can see morning visits."
	if got != want {
		t.Fatalf("event order\n got %s\nwant %s", got, want)
	}
	if !sawResult {
		t.Fatal("follow-up generation did not receive the real tool result")
	}
}
