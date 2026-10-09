package api

import (
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

func openingServer(t *testing.T) (*API, *httptest.Server) {
	t.Helper()
	agents, err := config.Load("../../agents")
	if err != nil {
		t.Fatal(err)
	}
	b, err := mock.Load("../../mock", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(b.Handler())
	t.Cleanup(backend.Close)
	ctx, cancel := context.WithCancel(context.Background())
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
	app := &API{Runtime: rt, Agents: agents, Sessions: session.NewStore(), BackendURL: backend.URL, Root: ctx}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		server.Close()
		app.Workers.Wait()
		cancel()
		rt.Wait()
	})
	return app, server
}

func openingPost(t *testing.T, server *httptest.Server, path, body string, want int) map[string]string {
	t.Helper()
	resp, err := http.Post(server.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("%s status %d, expected %d", path, resp.StatusCode, want)
	}
	var data map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&data)
	return data
}

func waitIdle(t *testing.T, s *session.Session) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.Busy.Load() {
		if time.Now().After(deadline) {
			t.Fatal("turn did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOpeningTurnOutboundAndInbound(t *testing.T) {
	app, server := openingServer(t)
	cases := []struct{ agent, user, want, instruction string }{
		{"aquila-outreach", "L001", "Sophie", "[Outbound call connected]"},
		{"aquila-admissions", "F003", "Amelia", "[Call answered]"},
		{"aquila-reception", "F001", "Noor", "[Call answered]"},
	}
	for _, tc := range cases {
		t.Run(tc.agent, func(t *testing.T) {
			created := openingPost(t, server, "/api/sessions", `{"agent_id":"`+tc.agent+`","user_id":"`+tc.user+`"}`, 201)
			s := app.Sessions.Get(created["id"])
			openingPost(t, server, "/api/sessions/"+created["id"]+"/open", "", 202)
			waitIdle(t, s)
			greeting := ""
			for _, e := range s.Events.Since(0) {
				raw, _ := json.Marshal(e)
				if strings.Contains(string(raw), tc.instruction) {
					t.Fatalf("internal instruction leaked in event %s", e.Type)
				}
				if e.Type == "agent.response.completed" {
					data, _ := e.Data.(map[string]any)
					greeting, _ = data["text"].(string)
				}
			}
			if !strings.Contains(greeting, tc.want) {
				t.Fatalf("opening greeting %q does not mention %s", greeting, tc.want)
			}
			if len(s.History) == 0 {
				t.Fatal("agent reply missing from history")
			}
			for _, m := range s.History {
				if m.Role == "user" || strings.Contains(m.Content, tc.instruction) {
					t.Fatalf("internal instruction stored as %s message", m.Role)
				}
			}
			// The conversation has begun: a second opening is refused.
			openingPost(t, server, "/api/sessions/"+created["id"]+"/open", "", 409)
			// Normal turns still work afterwards.
			openingPost(t, server, "/api/sessions/"+created["id"]+"/messages", `{"message":"hello"}`, 202)
			waitIdle(t, s)
		})
	}
}

func TestOpeningRejectedWithoutPersonaOpening(t *testing.T) {
	app, server := openingServer(t)
	created := openingPost(t, server, "/api/sessions", `{"agent_id":"school-services","user_id":"F001"}`, 201)
	openingPost(t, server, "/api/sessions/"+created["id"]+"/open", "", 400)
	if s := app.Sessions.Get(created["id"]); s.Busy.Load() || len(s.History) != 0 {
		t.Fatal("rejected opening changed the session")
	}
	openingPost(t, server, "/api/sessions/unknown/open", "", 404)
}

func TestOpeningConflictsWithExistingTurns(t *testing.T) {
	app, server := openingServer(t)
	created := openingPost(t, server, "/api/sessions", `{"agent_id":"aquila-admissions","user_id":"F003"}`, 201)
	s := app.Sessions.Get(created["id"])
	openingPost(t, server, "/api/sessions/"+created["id"]+"/messages", `{"message":"hello"}`, 202)
	waitIdle(t, s)
	openingPost(t, server, "/api/sessions/"+created["id"]+"/open", "", 409)
	if len(s.History) != 2 {
		t.Fatalf("history changed by refused opening: %d messages", len(s.History))
	}
}
