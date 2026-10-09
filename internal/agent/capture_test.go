package agent

import (
	"context"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/memory"
	"enterprise-ai-demo/internal/session"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// captureClient answers normal turns with a fixed reply and routes the
// extraction call (identified by its system prompt) to extraction.
type captureClient struct {
	inner      llm.Client
	extraction func(context.Context) (string, error)
	calls      atomic.Int32
}

func (c *captureClient) Name() string { return "capture-test" }
func (c *captureClient) Chat(ctx context.Context, req llm.Request, delta func(string)) (llm.Response, error) {
	if len(req.Messages) > 0 && req.Messages[0].Content == captureSystemPrompt {
		c.calls.Add(1)
		if len(req.Tools) != 0 {
			return llm.Response{}, errors.New("extraction must not offer tools")
		}
		out, err := c.extraction(ctx)
		return llm.Response{Message: llm.Message{Role: "assistant", Content: out}}, err
	}
	if c.inner != nil {
		return c.inner.Chat(ctx, req, delta)
	}
	if delta != nil {
		delta("Noted, thank you.")
	}
	return llm.Response{Message: llm.Message{Role: "assistant", Content: "Noted, thank you."}}, nil
}

func captureAgent(agents map[string]*config.Agent, id string, enabled bool) *config.Agent {
	a := *agents[id]
	a.Memory.AutoCapture = enabled
	return &a
}

func capEventTypes(s *session.Session) []string {
	out := []string{}
	for _, e := range s.Events.Since(0) {
		out = append(out, e.Type)
	}
	return out
}

func capHasEvent(s *session.Session, kind string) bool {
	for _, k := range capEventTypes(s) {
		if k == kind {
			return true
		}
	}
	return false
}

func capWaitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(time.Millisecond)
	}
}

const zaraFact = "2026-10-09: Aisha's daughter Zara is 6."

func TestAutoCaptureStoresFactsInScopeWithAutoTag(t *testing.T) {
	r, agents, _ := setup(t)
	a := captureAgent(agents, "hospital-services", true)
	client := &captureClient{extraction: func(context.Context) (string, error) {
		return "```json\n[\"" + zaraFact + "\", 7, \"\", \"" + strings.Repeat("x", 301) + "\"]\n```", nil
	}}
	r.Clients[a.ID] = client
	s := session.NewStore().Create(a, "P001")
	r.Run(context.Background(), s, "t1", Turn{Text: "My daughter Zara is 6 and loves swimming."})
	capWaitFor(t, "capture event", func() bool { return capHasEvent(s, "memory.capture.completed") })
	capWaitFor(t, "stored fact", func() bool {
		ms, _ := r.Memory.Retrieve(context.Background(), memory.RetrieveRequest{Scope: Scope(s), Query: "Zara"})
		return len(ms) == 1
	})
	ms, _ := r.Memory.Retrieve(context.Background(), memory.RetrieveRequest{Scope: Scope(s), Query: "Zara"})
	if ms[0].Text != zaraFact || !reflect.DeepEqual(ms[0].Tags, []string{"auto"}) || ms[0].Scope != Scope(s) {
		t.Fatalf("unexpected memory %+v", ms[0])
	}
	other := session.NewStore().Create(a, "P002")
	if leaked, _ := r.Memory.Retrieve(context.Background(), memory.RetrieveRequest{Scope: Scope(other), Query: "Zara"}); len(leaked) != 0 {
		t.Fatal("captured fact leaked to another user")
	}
	if client.calls.Load() != 1 {
		t.Fatal("expected exactly one extraction call", client.calls.Load())
	}
}

func TestAutoCaptureDisabledDoesNothing(t *testing.T) {
	r, agents, _ := setup(t)
	a := captureAgent(agents, "hospital-services", false)
	client := &captureClient{extraction: func(context.Context) (string, error) { return "[\"" + zaraFact + "\"]", nil }}
	r.Clients[a.ID] = client
	s := session.NewStore().Create(a, "P001")
	r.Run(context.Background(), s, "t1", Turn{Text: "My daughter Zara is 6."})
	time.Sleep(50 * time.Millisecond)
	if client.calls.Load() != 0 {
		t.Fatal("extraction ran while disabled")
	}
	if ms, _ := r.Memory.Retrieve(context.Background(), memory.RetrieveRequest{Scope: Scope(s), Query: "Zara"}); len(ms) != 0 {
		t.Fatal("fact stored while disabled")
	}
}

func TestAutoCaptureMalformedOutputStoresNothing(t *testing.T) {
	for name, output := range map[string]string{
		"prose":  "Zara is 6.",
		"object": `{"facts": ["x"]}`,
		"cut":    `["2026-10-09: Zara`,
	} {
		t.Run(name, func(t *testing.T) {
			r, agents, _ := setup(t)
			a := captureAgent(agents, "hospital-services", true)
			r.Clients[a.ID] = &captureClient{extraction: func(context.Context) (string, error) { return output, nil }}
			s := session.NewStore().Create(a, "P001")
			r.Run(context.Background(), s, "t1", Turn{Text: "My daughter Zara is 6."})
			capWaitFor(t, "failure event", func() bool { return capHasEvent(s, "memory.capture.failed") })
			if capHasEvent(s, "memory.store.started") || capHasEvent(s, "memory.capture.completed") {
				t.Fatal("something was stored", capEventTypes(s))
			}
		})
	}
	t.Run("llm error", func(t *testing.T) {
		r, agents, _ := setup(t)
		a := captureAgent(agents, "hospital-services", true)
		r.Clients[a.ID] = &captureClient{extraction: func(context.Context) (string, error) { return "", errors.New("boom") }}
		s := session.NewStore().Create(a, "P001")
		r.Run(context.Background(), s, "t1", Turn{Text: "My daughter Zara is 6."})
		capWaitFor(t, "failure event", func() bool { return capHasEvent(s, "memory.capture.failed") })
		if capHasEvent(s, "memory.store.started") {
			t.Fatal("something was stored")
		}
	})
}

func TestAutoCaptureSkipsOpeningAndConfirmationTurns(t *testing.T) {
	r, agents, _ := setup(t)
	a := captureAgent(agents, "telecom-support", true)
	client := &captureClient{inner: r.Clients[a.ID], extraction: func(context.Context) (string, error) { return "[\"" + zaraFact + "\"]", nil }}
	r.Clients[a.ID] = client
	s := session.NewStore().Create(a, "C001")
	r.Run(context.Background(), s, "open", Turn{Opening: "Greet the customer.", MemoryQuery: "greeting"})
	if client.calls.Load() != 0 {
		t.Fatal("extraction ran for an opening turn")
	}
	r.Run(context.Background(), s, "t1", Turn{Text: "Optimize my Wi-Fi channel"})
	if len(s.Pending) != 1 {
		t.Fatalf("missing confirmation: %s", conversation(s))
	}
	var id string
	for key := range s.Pending {
		id = key
	}
	r.Run(context.Background(), s, "t2", Turn{Confirmation: id})
	r.Run(context.Background(), s, "t3", Turn{Confirmation: "missing", Reject: true})
	time.Sleep(50 * time.Millisecond)
	if n := client.calls.Load(); n != 0 {
		t.Fatal("extraction ran for a confirmation, rejection or failed turn", n)
	}
}

func TestAutoCaptureDoesNotDelayTurnCompletion(t *testing.T) {
	r, agents, _ := setup(t)
	a := captureAgent(agents, "hospital-services", true)
	release := make(chan struct{})
	client := &captureClient{extraction: func(ctx context.Context) (string, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "[]", nil
	}}
	r.Clients[a.ID] = client
	s := session.NewStore().Create(a, "P001")
	done := make(chan struct{})
	go func() {
		r.Run(context.Background(), s, "t1", Turn{Text: "My daughter Zara is 6."})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("turn blocked on extraction")
	}
	if !capHasEvent(s, "turn.completed") {
		t.Fatal("turn.completed missing", capEventTypes(s))
	}
	if capHasEvent(s, "memory.capture.completed") {
		t.Fatal("capture finished before it was released")
	}
	close(release)
	capWaitFor(t, "capture event", func() bool { return capHasEvent(s, "memory.capture.completed") })
}

func TestParseFacts(t *testing.T) {
	many := `["a","b","c","d","e","f","g"]`
	for name, tc := range map[string]struct {
		in   string
		want []string
		bad  bool
	}{
		"plain":      {in: `["x"]`, want: []string{"x"}},
		"fence":      {in: "```json\n[\"x\"]\n```", want: []string{"x"}},
		"bare fence": {in: "```\n[\"x\"]\n```", want: []string{"x"}},
		"empty":      {in: `[]`, want: []string{}},
		"mixed":      {in: `["x", 1, null, "  ", {"a":1}, "y"]`, want: []string{"x", "y"}},
		"cap":        {in: many, want: []string{"a", "b", "c", "d", "e"}},
		"long":       {in: `["` + strings.Repeat("x", 301) + `"]`, want: []string{}},
		"object":     {in: `{"a":1}`, bad: true},
		"text":       {in: `nothing`, bad: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseFacts(tc.in)
			if (err != nil) != tc.bad {
				t.Fatal(got, err)
			}
			if !tc.bad && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
