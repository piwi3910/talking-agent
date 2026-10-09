package api

import (
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestTraceStoreAppendListAndCaps(t *testing.T) {
	st := NewTraceStore(t.TempDir())
	st.MaxBytes = 400
	if err := st.Append("../etc/passwd", "a", []byte(`{"type":"x"}`)); err == nil {
		t.Fatal("unsafe session id accepted")
	}
	if err := st.Append("session-one", "agent-a", []byte(`{"type":"one"}`), []byte(`{"type":"two"}`)); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, st.path("session-one"))
	if len(lines) != 3 || lines[0]["type"] != "trace.start" || lines[2]["type"] != "two" {
		t.Fatalf("unexpected file: %v", lines)
	}
	list := st.List()
	if len(list) != 1 || list[0].SessionID != "session-one" || list[0].AgentID != "agent-a" || list[0].Size == 0 {
		t.Fatalf("unexpected list: %+v", list)
	}
	big := []byte(`{"type":"big","pad":"` + strings.Repeat("x", 400) + `"}`)
	if err := st.Append("session-one", "agent-a", big); err != errTraceFull {
		t.Fatalf("expected size cap, got %v", err)
	}
	if got := len(readLines(t, st.path("session-one"))); got != 3 {
		t.Fatalf("capped append modified file: %d lines", got)
	}
	// A new store instance learns existing size from disk.
	st2 := NewTraceStore(st.Dir)
	st2.MaxBytes = 400
	if err := st2.Append("session-one", "agent-a", big); err != errTraceFull {
		t.Fatalf("expected size cap after reopen, got %v", err)
	}
}

func TestTraceStorePrunesOldest(t *testing.T) {
	st := NewTraceStore(t.TempDir())
	st.MaxSessions = 3
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("session-%d", i)
		if err := st.Append(id, "a", []byte(`{"type":"x"}`)); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(st.path(id), base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	list := st.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 traces, got %d", len(list))
	}
	for _, gone := range []string{"session-0", "session-1"} {
		if _, err := os.Stat(st.path(gone)); !os.IsNotExist(err) {
			t.Fatalf("%s was not pruned", gone)
		}
	}
}

func TestTraceHandler(t *testing.T) {
	agents, err := config.Load("../../agents")
	if err != nil {
		t.Fatal(err)
	}
	var cfg *config.Agent
	for _, a := range agents {
		cfg = a
		break
	}
	store := session.NewStore()
	s := store.Create(cfg, "user")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := &API{Agents: agents, Sessions: store, Root: ctx, Traces: NewTraceStore(t.TempDir())}
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	post := func(path, body string) int {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post("/api/sessions/missing-session/trace", `[]`); c != 404 {
		t.Fatalf("unknown session: %d", c)
	}
	if c := post("/api/sessions/"+s.ID+"/trace", `{"not":"array"}`); c != 400 {
		t.Fatalf("bad body: %d", c)
	}
	if c := post("/api/sessions/"+s.ID+"/trace", `[{"type":"audio.state","t":1.5,"src":"spoof","data":{"state":"running"}},{"nope":1}]`); c != 204 {
		t.Fatalf("batch: %d", c)
	}
	s.Events.Hook = app.traceHook(s.ID, cfg.ID)
	s.Events.Emit("turn1", "llm.started", map[string]any{"model": "m"})
	s.Events.Emit("turn1", "agent.response.delta", map[string]any{"text": "hello"})

	resp, err := http.Get(srv.URL + "/api/traces/" + s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var lines []map[string]any
	d := json.NewDecoder(resp.Body)
	for d.More() {
		var m map[string]any
		if err := d.Decode(&m); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 4 || lines[1]["type"] != "audio.state" || lines[1]["src"] != "client" || lines[2]["type"] != "sse.llm.started" || lines[3]["src"] != "server" {
		t.Fatalf("unexpected trace: %v", lines)
	}
	if data := lines[3]["data"].(map[string]any); data["chars"] != float64(5) || data["text"] != nil {
		t.Fatalf("delta text not redacted: %v", data)
	}
	var list []TraceInfo
	lr, err := http.Get(srv.URL + "/api/traces")
	if err != nil {
		t.Fatal(err)
	}
	defer lr.Body.Close()
	if err := json.NewDecoder(lr.Body).Decode(&list); err != nil || len(list) != 1 || list[0].SessionID != s.ID || list[0].AgentID != cfg.ID {
		t.Fatalf("list: %v %+v", err, list)
	}
	if r, _ := http.Get(srv.URL + "/api/traces/..%2Fsecret"); r.StatusCode != 404 {
		t.Fatalf("traversal status %d", r.StatusCode)
	}
	big := `[{"type":"x","pad":"` + strings.Repeat("a", 5<<20) + `"}]`
	if c := post("/api/sessions/"+s.ID+"/trace", big); c != 413 {
		t.Fatalf("oversized body: %d", c)
	}
}
