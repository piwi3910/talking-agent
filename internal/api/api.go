package api

import (
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/tools"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type API struct {
	Runtime     *agent.Runtime
	Agents      map[string]*config.Agent
	Sessions    *session.Store
	BackendURL  string
	BackendURLs map[string]string
	Root        context.Context
	Workers     sync.WaitGroup
	WebDir      string
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func (a *API) users(ctx context.Context, config *config.Agent) ([]tools.Record, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	endpoint := a.BackendURL
	if specific := a.BackendURLs[config.ID]; specific != "" {
		endpoint = specific
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(endpoint, "/")+"/users/"+config.Industry, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("backend HTTP %d", resp.StatusCode)
	}
	var users []tools.Record
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&users)
	return users, err
}
func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"status": "ok", "memory": a.Runtime.Memory.Name()})
	})
	m.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		items := []any{}
		ids := []string{}
		for id := range a.Agents {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			c := a.Agents[id]
			users, err := a.users(r.Context(), c)
			if err != nil {
				fail(w, 502, "Mock backend unavailable")
				return
			}
			items = append(items, map[string]any{"config": c, "skills": a.Runtime.Catalogs[id], "users": users, "llm": a.Runtime.Clients[id].Name(), "memory": a.Runtime.Memory.Name()})
		}
		write(w, 200, items)
	})
	m.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			AgentID string `json:"agent_id"`
			UserID  string `json:"user_id"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, 400, "Invalid session request")
			return
		}
		c := a.Agents[in.AgentID]
		if c == nil {
			fail(w, 404, "Unknown agent")
			return
		}
		users, err := a.users(r.Context(), c)
		if err != nil {
			fail(w, 502, "Mock backend unavailable")
			return
		}
		ok := false
		for _, u := range users {
			if u.ID == in.UserID {
				ok = true
			}
		}
		if !ok {
			fail(w, 400, "Identity does not belong to selected agent")
			return
		}
		s := a.Sessions.Create(c, in.UserID)
		if s == nil {
			fail(w, 503, "Session capacity reached")
			return
		}
		s.Events.Emit("", "session.started", map[string]any{"agent_id": c.ID, "user_id": in.UserID, "memory_namespace": c.Memory.Namespace})
		write(w, 201, map[string]string{"id": s.ID})
	})
	m.HandleFunc("POST /api/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		s := a.Sessions.Get(r.PathValue("id"))
		if s == nil {
			fail(w, 404, "Session not found")
			return
		}
		var turn agent.Turn
		if err := decode(w, r, &turn); err != nil {
			fail(w, 400, "Invalid message")
			return
		}
		turn.Text = strings.TrimSpace(turn.Text)
		if (turn.Text == "" && turn.Confirmation == "") || len(turn.Text) > 8000 || (turn.Text != "" && turn.Confirmation != "") {
			fail(w, 400, "Supply a message (1–8000 bytes) or a confirmation ID")
			return
		}
		if !s.Busy.CompareAndSwap(false, true) {
			fail(w, 409, "A turn is already running")
			return
		}
		ctx, cancel := context.WithTimeout(a.Root, 2*time.Minute)
		s.SetCancel(cancel)
		id := session.ID()
		s.Events.Emit(id, "turn.started", map[string]any{"message": turn.Text})
		a.Workers.Add(1)
		go func() {
			defer a.Workers.Done()
			defer cancel()
			a.Runtime.Run(ctx, s, id, turn)
		}()
		write(w, 202, map[string]string{"turn_id": id})
	})
	m.HandleFunc("POST /api/sessions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		s := a.Sessions.Get(r.PathValue("id"))
		if s == nil {
			fail(w, 404, "Session not found")
			return
		}
		s.Cancel()
		write(w, 200, map[string]bool{"cancelled": true})
	})
	m.HandleFunc("GET /api/sessions/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		s := a.Sessions.Get(r.PathValue("id"))
		if s == nil {
			fail(w, 404, "Session not found")
			return
		}
		f, ok := w.(http.Flusher)
		if !ok {
			fail(w, 500, "Streaming unavailable")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		id, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
		if raw := r.URL.Query().Get("after"); raw != "" {
			id, _ = strconv.ParseUint(raw, 10, 64)
		}
		fmt.Fprint(w, ": connected\n\n")
		f.Flush()
		tick := time.NewTicker(40 * time.Millisecond)
		defer tick.Stop()
		heartbeat := time.NewTicker(10 * time.Second)
		defer heartbeat.Stop()
		for {
			for _, event := range s.Events.Since(id) {
				raw, _ := json.Marshal(event)
				if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, raw); err != nil {
					return
				}
				id = event.ID
			}
			f.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-a.Root.Done():
				return
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					return
				}
			case <-tick.C:
			}
		}
	})
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "API route not found") })
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			fail(w, 405, "Method not allowed")
			return
		}
		if a.WebDir == "" {
			fail(w, 404, "Frontend is not built; run npm run build in web or use Vite on port 5173")
			return
		}
		path := filepath.Join(a.WebDir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			http.ServeFile(w, r, filepath.Join(a.WebDir, "index.html"))
			return
		}
		http.ServeFile(w, r, path)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == "POST" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
				fail(w, 403, "Cross-origin write rejected")
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}
