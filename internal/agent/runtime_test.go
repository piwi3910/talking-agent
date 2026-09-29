package agent

import (
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/knowledge"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/memory"
	"enterprise-ai-demo/internal/mock"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/skills"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (*Runtime, map[string]*config.Agent, *mock.Backend) {
	t.Helper()
	agents, err := config.Load("../../agents")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := mock.Load("../../mock", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(backend.Handler())
	t.Cleanup(server.Close)
	r := &Runtime{Catalogs: map[string]skills.Catalog{}, Clients: map[string]llm.Client{}, Memory: memory.New(), Knowledge: knowledge.Local{}, Tools: newHTTPExecutor(server.URL), MaxIterations: 12}
	for id, a := range agents {
		c, e := skills.Load(a.Dir)
		if e != nil {
			t.Fatal(e)
		}
		r.Catalogs[id] = c
		client, e := llm.LoadDemo(filepath.Join(a.Dir, "demo.json"))
		if e != nil {
			t.Fatal(e)
		}
		r.Clients[id] = client
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	t.Cleanup(func() { cancel(); r.Wait() })
	return r, agents, backend
}
func conversation(s *session.Session) string {
	out := ""
	for _, m := range s.History {
		if m.Role == "assistant" {
			out += m.Content + "\n"
		}
	}
	return out
}
func TestMemoryAcrossSessionsAndIndustrySwitch(t *testing.T) {
	r, agents, _ := setup(t)
	store := session.NewStore()
	first := store.Create(agents["hospital-services"], "P001")
	r.Run(context.Background(), first, "t1", Turn{Text: "Mornings normally work better for me."})
	deadline := time.Now().Add(time.Second)
	for {
		ms, _ := r.Memory.Retrieve(context.Background(), memory.RetrieveRequest{Scope: Scope(first), Query: "appointment"})
		if len(ms) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preference not stored")
		}
		time.Sleep(time.Millisecond)
	}
	second := store.Create(agents["hospital-services"], "P001")
	r.Run(context.Background(), second, "t2", Turn{Text: "Can I make another appointment with Dr. Ahmed?"})
	if !strings.Contains(conversation(second), "prefer mornings") {
		t.Fatal(conversation(second))
	}
	for _, e := range second.Events.Since(0) {
		if e.Type == "tool.completed" {
			raw, _ := json.Marshal(e.Data)
			if strings.Contains(string(raw), "T14:") {
				t.Fatal("afternoon returned despite preference")
			}
		}
	}
	tele := store.Create(agents["telecom-support"], "P001")
	mems, _ := r.Memory.Retrieve(context.Background(), memory.RetrieveRequest{Scope: Scope(tele), Query: "appointment"})
	if len(mems) != 0 {
		t.Fatal("hospital memory leaked to telecom")
	}
}
func TestConfirmationAndOutageProtection(t *testing.T) {
	r, agents, b := setup(t)
	s := session.NewStore().Create(agents["telecom-support"], "C001")
	r.Run(context.Background(), s, "t1", Turn{Text: "Optimize my Wi-Fi channel"})
	if len(s.Pending) != 1 {
		t.Fatalf("missing confirmation: %s", conversation(s))
	}
	if !strings.Contains(b.Execute(request("telecom", "C001", "wifi.diagnostics")).Summary, "High interference") {
		t.Fatal("mutation executed before confirmation")
	}
	var id string
	for key := range s.Pending {
		id = key
	}
	r.Run(context.Background(), s, "t2", Turn{Confirmation: id})
	if strings.Contains(b.Execute(request("telecom", "C001", "wifi.diagnostics")).Summary, "High interference") {
		t.Fatal("confirmed action did not execute")
	}
	r.Run(context.Background(), s, "t3", Turn{Confirmation: id})
	if len(s.Pending) != 0 {
		t.Fatal("confirmation replay accepted")
	}
	outage := session.NewStore().Create(agents["telecom-support"], "C002")
	r.Run(context.Background(), outage, "t4", Turn{Text: "Restart my router"})
	if len(outage.Pending) != 0 {
		t.Fatal("offered restart during outage")
	}
	if !strings.Contains(conversation(outage), "regional outage") {
		t.Fatal(conversation(outage))
	}
}
func TestHospitalSafetyBypassesModelAndTools(t *testing.T) {
	r, agents, _ := setup(t)
	s := session.NewStore().Create(agents["hospital-services"], "P001")
	r.Clients[s.Agent.ID] = panicClient{}
	r.Run(context.Background(), s, "t1", Turn{Text: "I have chest pain and can't breathe; book an appointment"})
	r.Run(context.Background(), s, "t2", Turn{Text: "Book an appointment now"})
	if !strings.Contains(conversation(s), "emergency services") {
		t.Fatal(conversation(s))
	}
	for _, e := range s.Events.Since(0) {
		if e.Type == "tool.started" || e.Type == "llm.started" || e.Type == "memory.store.started" {
			t.Fatal("normal processing during emergency", e.Type)
		}
	}
}

type panicClient struct{}

func (panicClient) Name() string { return "panic" }
func (panicClient) Chat(context.Context, llm.Request, func(string)) (llm.Response, error) {
	panic("model must not be called")
}

type testClient struct {
	respond func(llm.Request, func(string)) llm.Response
}

func (testClient) Name() string { return "test" }
func (c testClient) Chat(_ context.Context, r llm.Request, delta func(string)) (llm.Response, error) {
	return c.respond(r, delta), nil
}
func TestMultipleCallsAndBoundedLoop(t *testing.T) {
	r, agents, _ := setup(t)
	s := session.NewStore().Create(agents["telecom-support"], "C001")
	calls := 0
	r.Clients[s.Agent.ID] = testClient{respond: func(_ llm.Request, delta func(string)) llm.Response {
		calls++
		if calls == 1 {
			return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.Call{{ID: "one", Type: "function", Function: llm.Function{Name: "customer__profile", Arguments: "{}"}}, {ID: "two", Type: "function", Function: llm.Function{Name: "wifi__status", Arguments: "{}"}}}}}
		}
		delta("Checked both services.")
		return llm.Response{Message: llm.Message{Role: "assistant", Content: "Checked both services."}}
	}}
	r.Run(context.Background(), s, "multi", Turn{Text: "Check my account and wifi"})
	count := 0
	for _, e := range s.Events.Since(0) {
		if e.Type == "tool.completed" {
			count++
		}
	}
	if count != 2 || calls != 2 {
		t.Fatal(count, calls)
	}
	s = session.NewStore().Create(agents["telecom-support"], "C001")
	r.MaxIterations = 3
	calls = 0
	r.Clients[s.Agent.ID] = testClient{respond: func(_ llm.Request, _ func(string)) llm.Response {
		calls++
		return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.Call{{ID: session.ID(), Type: "function", Function: llm.Function{Name: "patient__profile", Arguments: "{}"}}}}}
	}}
	r.Run(context.Background(), s, "loop", Turn{Text: "Check my account"})
	if calls != 3 {
		t.Fatal("loop not bounded", calls)
	}
	failed := false
	for _, e := range s.Events.Since(0) {
		if e.Type == "tool.started" {
			t.Fatal("cross-domain tool executed")
		}
		if e.Type == "agent.error" {
			failed = true
		}
	}
	if !failed {
		t.Fatal("loop error not observable")
	}
}
