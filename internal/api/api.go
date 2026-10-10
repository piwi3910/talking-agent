package api

import (
	"bufio"
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/mcp"
	"enterprise-ai-demo/internal/metrics"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/telemetry"
	"enterprise-ai-demo/internal/telephony"
	"enterprise-ai-demo/internal/tools"
	"enterprise-ai-demo/internal/voices"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type API struct {
	MCP              *mcp.Manager
	PhoneGateway     *telephony.Gateway
	PhoneSettings    *telephony.Settings
	Voices           *voices.Store
	previewing       atomic.Bool
	Speech           *speech.Client
	VoiceActive      sync.Map
	speechMu         sync.Mutex
	speechActive     map[string]int // concurrent /speech requests per session
	Runtime          *agent.Runtime
	Agents           map[string]*config.Agent
	Sessions         *session.Store
	BackendURL       string
	BackendURLs      map[string]string
	Root             context.Context
	Workers          sync.WaitGroup
	WebDir           string
	Traces           *TraceStore
	limiter          *requestLimiter
	Metrics          *metrics.Registry
	metricSessionsMu sync.Mutex
	metricSessions   map[string]struct{}
}

const (
	maxJSONBodyBytes = 32 * 1024
	maxMessageBytes  = 8000
	ssePollInterval  = 40 * time.Millisecond
	sseHeartbeat     = 10 * time.Second
)

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"error": msg})
}

// API success statuses follow resource semantics: 201 for creation, 202 for
// accepted asynchronous work, 204 for completed operations without a body, and
// 200 for reads, synchronous updates, and streamed audio responses.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	return decodeLimit(w, r, v, maxJSONBodyBytes)
}
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
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
	a.voiceRoutes(m)
	a.settingsRoutes(m)
	a.mcpRoutes(m)
	a.voiceSettingsRoutes(m)
	a.openingRoutes(m)
	a.traceRoutes(m)
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"status": "ok"})
	})
	m.HandleFunc("GET /api/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		a.refreshSessionGauge()
		if a.Metrics != nil {
			a.Metrics.SetGauge("talking_agent_session_capacity", session.MaxSessions)
		}
		if a.Metrics != nil {
			_ = a.Metrics.WritePrometheus(w)
		}
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
			items = append(items, map[string]any{"config": c, "skills": a.Runtime.Catalogs[id], "users": users})
		}
		write(w, 200, items)
	})
	m.HandleFunc("GET /api/system/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		items := map[string]map[string]string{}
		for id, client := range a.Runtime.Clients {
			items[id] = map[string]string{"llm": client.Name(), "memory": a.Runtime.Memory.Name()}
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
			if a.Metrics != nil {
				a.Metrics.Add("talking_agent_session_capacity_rejections_total", 1)
			}
			fail(w, 503, "Session capacity reached")
			return
		}
		s.Events.Hook = a.metricsTraceHook(s.ID, c.ID)
		a.metricSessionsMu.Lock()
		if a.metricSessions == nil {
			a.metricSessions = map[string]struct{}{}
		}
		a.metricSessions[s.ID] = struct{}{}
		a.metricSessionsMu.Unlock()
		if a.Metrics != nil {
			a.Metrics.Add("talking_agent_sessions_created_total", 1)
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
		if (turn.Text == "" && turn.Confirmation == "") || len(turn.Text) > maxMessageBytes || (turn.Text != "" && turn.Confirmation != "") {
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
		w.WriteHeader(http.StatusNoContent)
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
		if a.Metrics != nil {
			a.Metrics.IncGauge("talking_agent_sse_streams", 1)
			defer a.Metrics.IncGauge("talking_agent_sse_streams", -1)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		id, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
		if raw := r.URL.Query().Get("after"); raw != "" {
			id, _ = strconv.ParseUint(raw, 10, 64)
		}
		fmt.Fprint(w, ": connected\n\n")
		f.Flush()
		tick := time.NewTicker(ssePollInterval)
		defer tick.Stop()
		heartbeat := time.NewTicker(sseHeartbeat)
		defer heartbeat.Stop()
		for {
			for _, event := range s.Events.Since(id) {
				// Session SSE also feeds the presenting Stage. Keep runtime model
				// identifiers in operator traces, but strip them from this stream.
				presented := event
				if data, ok := presented.Data.(map[string]any); ok {
					publicData := make(map[string]any, len(data))
					for key, value := range data {
						if key != "provider" && key != "model" && key != "llm" {
							publicData[key] = value
						}
					}
					presented.Data = publicData
				}
				raw, _ := json.Marshal(presented)
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
		if info, err := os.Stat(path); err != nil || info.IsDir() || filepath.Base(path) == "index.html" {
			// The page must always revalidate so a deploy reaches open phones and tabs;
			// its hashed assets never change and can be cached for good.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(a.WebDir, "index.html"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFile(w, r, path)
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" || r.Method == "PATCH" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
				fail(w, 403, "Cross-origin write rejected")
				return
			}
		}
		m.ServeHTTP(w, r)
	})
	if a.limiter == nil {
		a.limiter = newRequestLimiter()
	}
	return protected(a.metricsMiddleware(a.limiter.middleware(handler)), authToken())
}

// metricsMiddleware classifies routes without retaining path values such as
// session IDs. The wrapper preserves streaming and WebSocket response features.
func (a *API) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		mw := &metricResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(mw, r)
		if a.Metrics != nil {
			route := routeClass(r)
			status := strconv.Itoa(mw.status)
			a.Metrics.Add("talking_agent_http_requests_total", 1, route, status)
			a.Metrics.Observe("talking_agent_http_request_duration_seconds", time.Since(started).Seconds(), route)
		}
	})
}

type metricResponseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *metricResponseWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *metricResponseWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *metricResponseWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *metricResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response does not support hijacking")
	}
	w.status = http.StatusSwitchingProtocols
	w.wrote = true
	return h.Hijack()
}
func (w *metricResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func routeClass(r *http.Request) string {
	if r.URL.Path == "/api/metrics" {
		return "metrics"
	}
	if r.URL.Path == "/api/health" {
		return "health"
	}
	if strings.HasPrefix(r.URL.Path, "/api/sessions/") {
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			return "session_events"
		case strings.HasSuffix(r.URL.Path, "/messages"):
			return "session_messages"
		case strings.HasSuffix(r.URL.Path, "/transcribe"):
			return "speech_transcribe"
		case strings.HasSuffix(r.URL.Path, "/speech"):
			return "speech_synthesize"
		default:
			return "session_operation"
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/mcp/") {
		return "mcp"
	}
	if strings.HasPrefix(r.URL.Path, "/api/settings/") {
		return "settings"
	}
	if strings.HasPrefix(r.URL.Path, "/api/traces") {
		return "traces"
	}
	if strings.HasPrefix(r.URL.Path, "/api/agents/") {
		return "agent"
	}
	if r.URL.Path == "/api/agents" {
		return "agents"
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return "api_other"
	}
	return "web"
}

func (a *API) metricsTraceHook(sessionID, agentID string) func(telemetry.Event) {
	var trace func(telemetry.Event)
	if a.Traces != nil {
		trace = a.traceHook(sessionID, agentID)
	}
	var turnErrors sync.Map
	return func(e telemetry.Event) {
		if a.Metrics != nil {
			if e.Type == "agent.error" {
				turnErrors.Store(e.TurnID, true)
			}
			if e.Type == "turn.completed" {
				status := "success"
				if _, failed := turnErrors.LoadAndDelete(e.TurnID); failed {
					status = "error"
				}
				a.observeTurn(e, status)
			} else {
				a.observeEvent(e)
			}
		}
		if trace != nil {
			trace(e)
		}
	}
}

func (a *API) refreshSessionGauge() {
	if a.Metrics == nil {
		return
	}
	a.metricSessionsMu.Lock()
	defer a.metricSessionsMu.Unlock()
	active := int64(0)
	for id := range a.metricSessions {
		if a.Sessions.Get(id) == nil {
			delete(a.metricSessions, id)
		} else {
			active++
		}
	}
	a.Metrics.SetGauge("talking_agent_active_sessions", active)
}

func (a *API) observeEvent(e telemetry.Event) {
	data, _ := e.Data.(map[string]any)
	status := "success"
	if strings.HasSuffix(e.Type, ".failed") {
		status = "error"
	}
	if e.Type == "llm.completed" || e.Type == "llm.failed" {
		a.Metrics.Add("talking_agent_llm_calls_total", 1, status)
		if ms, ok := data["duration_ms"].(int64); ok {
			a.Metrics.Observe("talking_agent_llm_call_duration_seconds", float64(ms)/1000)
		} else if ms, ok := data["duration_ms"].(float64); ok {
			a.Metrics.Observe("talking_agent_llm_call_duration_seconds", ms/1000)
		}
	}
	if e.Type == "tool.completed" || e.Type == "tool.failed" {
		tool, _ := data["tool"].(string)
		if tool == "" {
			tool = "unknown"
		}
		a.Metrics.Add("talking_agent_tool_calls_total", 1, tool, status)
		if ms, ok := data["duration_ms"].(int64); ok {
			a.Metrics.Observe("talking_agent_tool_call_duration_seconds", float64(ms)/1000, tool)
		} else if ms, ok := data["duration_ms"].(float64); ok {
			a.Metrics.Observe("talking_agent_tool_call_duration_seconds", ms/1000, tool)
		}
	}
	if strings.HasPrefix(e.Type, "memory.") && strings.HasSuffix(e.Type, ".completed") || strings.HasPrefix(e.Type, "memory.") && strings.HasSuffix(e.Type, ".failed") {
		a.Metrics.Add("talking_agent_memory_operations_total", 1, e.Type)
	}
}

func (a *API) observeTurn(e telemetry.Event, status string) {
	a.Metrics.Add("talking_agent_turns_total", 1, status)
	data, _ := e.Data.(map[string]any)
	if ms, ok := data["duration_ms"].(int64); ok {
		a.Metrics.Observe("talking_agent_turn_duration_seconds", float64(ms)/1000, status)
	} else if ms, ok := data["duration_ms"].(float64); ok {
		a.Metrics.Observe("talking_agent_turn_duration_seconds", ms/1000, status)
	}
}
