package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/knowledge"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/memory"
	"enterprise-ai-demo/internal/mock"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/skills"
	"enterprise-ai-demo/internal/tools"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPAPIAndSSE(t *testing.T) {
	agents, err := config.Load("../../agents")
	if err != nil {
		t.Fatal(err)
	}
	b, err := mock.Load("../../mock", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(b.Handler())
	defer backend.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &agent.Runtime{Catalogs: map[string]skills.Catalog{}, Clients: map[string]llm.Client{}, Memory: memory.New(), Knowledge: knowledge.Local{}, Tools: tools.HTTPExecutor{BaseURL: backend.URL}}
	for id, a := range agents {
		rt.Catalogs[id], err = skills.Load(a.Dir)
		if err != nil {
			t.Fatal(err)
		}
		rt.Clients[id], err = llm.LoadDemo(filepath.Join(a.Dir, "demo.json"))
		if err != nil {
			t.Fatal(err)
		}
	}
	rt.Start(ctx)
	defer func() { cancel(); rt.Wait() }()
	app := &API{Runtime: rt, Agents: agents, Sessions: session.NewStore(), BackendURL: backend.URL, Root: ctx}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	defer app.Workers.Wait()
	post := func(path, body string, want int) map[string]string {
		t.Helper()
		resp, e := http.Post(server.URL+path, "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s status %d expected %d", path, resp.StatusCode, want)
		}
		var data map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&data)
		return data
	}
	post("/api/sessions", `{"agent_id":"telecom-support","user_id":"P001"}`, 400)
	s := post("/api/sessions", `{"agent_id":"telecom-support","user_id":"C001"}`, 201)
	post("/api/sessions/"+s["id"]+"/messages", `{"message":"Explain my bill"}`, 202)
	post("/api/sessions/"+s["id"]+"/messages", `{"message":"Another request"}`, 409)
	streamctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	req, _ := http.NewRequestWithContext(streamctx, "GET", server.URL+"/api/sessions/"+s["id"]+"/events", nil)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(resp.Header)
	}
	scanner := bufio.NewScanner(resp.Body)
	seen := map[string]bool{}
	var lastID uint64
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			ID   uint64 `json:"id"`
			Type string `json:"type"`
		}
		if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.ID <= lastID {
			t.Fatal("non-monotonic event ID")
		}
		lastID = event.ID
		seen[event.Type] = true
		if event.Type == "turn.completed" {
			break
		}
	}
	resp.Body.Close()
	for _, kind := range []string{"session.started", "tool.started", "tool.completed", "agent.response.delta", "agent.response.completed", "turn.completed"} {
		if !seen[kind] {
			t.Error("missing event", kind)
		}
	}
	bad, _ := http.NewRequest("POST", server.URL+"/api/sessions", bytes.NewBufferString(`{}`))
	bad.Header.Set("Origin", "https://external.example")
	res, e := http.DefaultClient.Do(bad)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cross-origin write allowed")
	}
}
