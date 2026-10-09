package agent

import (
	"context"
	"strings"
	"testing"

	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/session"
)

func TestPromptAddendumOnlyWhenHookReturnsText(t *testing.T) {
	for name, hook := range map[string]func(string) string{
		"without hook":   nil,
		"empty addendum": func(string) string { return "" },
		"with addendum":  func(string) string { return "\nMARKER-ADDENDUM\n" },
	} {
		t.Run(name, func(t *testing.T) {
			r, agents, _ := setup(t)
			r.PromptAddendum = hook
			s := session.NewStore().Create(agents["telecom-support"], "C001")
			system := ""
			r.Clients["telecom-support"] = errorClient{respond: func(req llm.Request, delta func(string)) (llm.Response, error) {
				system = req.Messages[0].Content
				delta("Hello")
				return llm.Response{Message: llm.Message{Role: "assistant", Content: "Hello"}}, nil
			}}
			r.Run(context.Background(), s, "turn", Turn{Text: "Hi"})
			if got := strings.Contains(system, "MARKER-ADDENDUM"); got != (name == "with addendum") {
				t.Fatalf("addendum present=%v", got)
			}
			if name == "with addendum" {
				at, skills := strings.Index(system, "MARKER-ADDENDUM"), strings.Index(system, "Available skills")
				if skills < 0 || at < 0 || at > skills {
					t.Fatalf("addendum must come before the skills header: addendum=%d skills=%d", at, skills)
				}
				if !strings.Contains(system, "\nMARKER-ADDENDUM\n\nAvailable skills") {
					t.Fatalf("addendum must be its own paragraph: %q", system[at-10:skills+20])
				}
			}
		})
	}
}
