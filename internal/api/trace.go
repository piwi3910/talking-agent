package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/telemetry"
)

// Per-conversation diagnostic traces: one JSONL file per session holding both
// the browser's timeline (src "client") and the server's (src "server").
const (
	traceBodyLimit   = 4 << 20
	traceEventLimit  = 1 << 20
	defaultTraceMax  = 20 << 20
	defaultTraceKeep = 50
)

var traceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

var errTraceFull = errors.New("trace size limit reached")

// TraceStore appends events to $dir/<session>.jsonl and keeps the newest files.
type TraceStore struct {
	Dir         string
	MaxSessions int
	MaxBytes    int64
	mu          sync.Mutex
	sizes       map[string]int64
}

func NewTraceStore(dir string) *TraceStore {
	return &TraceStore{Dir: dir, MaxSessions: defaultTraceKeep, MaxBytes: defaultTraceMax, sizes: map[string]int64{}}
}

func (t *TraceStore) path(id string) string { return filepath.Join(t.Dir, id+".jsonl") }

// Append writes complete JSON lines to the session's file. The first write of a
// session adds a trace.start line and prunes the oldest sessions.
func (t *TraceStore) Append(id, agent string, lines ...[]byte) error {
	if t == nil || !traceIDPattern.MatchString(id) {
		return errors.New("invalid trace session")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := os.MkdirAll(t.Dir, 0o750); err != nil {
		return err
	}
	size, known := t.sizes[id]
	if !known {
		if info, err := os.Stat(t.path(id)); err == nil {
			size = info.Size()
		}
	}
	var buf bytes.Buffer
	if size == 0 {
		start, _ := json.Marshal(map[string]any{"src": "server", "type": "trace.start", "wall": wallMS(time.Now()), "data": map[string]any{"session_id": id, "agent_id": agent}})
		buf.Write(start)
		buf.WriteByte('\n')
	}
	for _, l := range lines {
		buf.Write(bytes.TrimSpace(l))
		buf.WriteByte('\n')
	}
	if size+int64(buf.Len()) > t.MaxBytes {
		return errTraceFull
	}
	f, err := os.OpenFile(t.path(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	n, err := f.Write(buf.Bytes())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	t.sizes[id] = size + int64(n)
	if size == 0 {
		t.prune(id)
	}
	return err
}

// prune removes the oldest files beyond MaxSessions, never keep. Called with mu held.
func (t *TraceStore) prune(keep string) {
	entries, err := os.ReadDir(t.Dir)
	if err != nil {
		return
	}
	type item struct {
		id  string
		mod time.Time
	}
	var items []item
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{strings.TrimSuffix(name, ".jsonl"), info.ModTime()})
	}
	if len(items) <= t.MaxSessions {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.Before(items[j].mod) })
	removable := len(items) - t.MaxSessions
	for _, it := range items {
		if removable == 0 {
			break
		}
		if it.id == keep {
			continue
		}
		_ = os.Remove(t.path(it.id))
		delete(t.sizes, it.id)
		removable--
	}
}

// TraceInfo describes one stored trace.
type TraceInfo struct {
	SessionID string    `json:"session_id"`
	AgentID   string    `json:"agent_id"`
	Started   time.Time `json:"started"`
	Size      int64     `json:"size"`
}

// List returns stored traces, newest first.
func (t *TraceStore) List() []TraceInfo {
	out := []TraceInfo{}
	if t == nil {
		return out
	}
	entries, _ := os.ReadDir(t.Dir)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		item := TraceInfo{SessionID: strings.TrimSuffix(name, ".jsonl"), Started: info.ModTime().UTC(), Size: info.Size()}
		if f, err := os.Open(filepath.Join(t.Dir, name)); err == nil {
			line, _ := bufio.NewReader(f).ReadBytes('\n')
			f.Close()
			var head struct {
				Wall float64 `json:"wall"`
				Data struct {
					Agent string `json:"agent_id"`
				} `json:"data"`
			}
			if json.Unmarshal(line, &head) == nil {
				item.AgentID = head.Data.Agent
				if head.Wall > 0 {
					item.Started = time.UnixMicro(int64(head.Wall * 1000)).UTC()
				}
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// Open returns the JSONL file of one session.
func (t *TraceStore) Open(id string) (*os.File, error) {
	if t == nil || !traceIDPattern.MatchString(id) {
		return nil, os.ErrNotExist
	}
	return os.Open(t.path(id))
}

func wallMS(tm time.Time) float64 { return float64(tm.UnixMicro()) / 1000 }

// trace records one server-side event for a session. It never fails the caller.
func (a *API) trace(session, agent, typ string, data map[string]any) {
	if a.Traces == nil || session == "" {
		return
	}
	line, err := json.Marshal(map[string]any{"src": "server", "type": typ, "wall": wallMS(time.Now()), "data": data})
	if err == nil {
		_ = a.Traces.Append(session, agent, line)
	}
}

// traceHook mirrors the session's telemetry events (turn, LLM, tool, STT, TTS) into the trace.
func (a *API) traceHook(session, agent string) func(telemetry.Event) {
	return func(e telemetry.Event) {
		data := map[string]any{"turn_id": e.TurnID, "seq": e.ID}
		if m, ok := e.Data.(map[string]any); ok {
			for k, v := range m {
				data[k] = v
			}
		} else if e.Data != nil {
			data["value"] = e.Data
		}
		// Streamed text is large and not timing data.
		if e.Type == "agent.response.delta" {
			s, _ := data["text"].(string)
			data["chars"] = len(s)
			delete(data, "text")
		}
		a.trace(session, agent, "sse."+e.Type, data)
	}
}

// speechTrace returns the upstream hook for one /speech request.
func (a *API) speechTrace(session, agent, req string) speech.TraceFunc {
	started := time.Now()
	return func(event string, data map[string]any) {
		d := map[string]any{"req": req, "since_ms": float64(time.Since(started).Microseconds()) / 1000}
		for k, v := range data {
			d[k] = v
		}
		a.trace(session, agent, "speech."+event, d)
	}
}

func (a *API) traceRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/sessions/{id}/trace", func(w http.ResponseWriter, r *http.Request) {
		s := a.Sessions.Get(r.PathValue("id"))
		if s == nil {
			fail(w, 404, "Session not found")
			return
		}
		if a.Traces == nil {
			fail(w, 404, "Tracing is not enabled")
			return
		}
		var batch []map[string]json.RawMessage
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, traceBodyLimit))
		if err := d.Decode(&batch); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				fail(w, 413, "Trace batch too large")
				return
			}
			fail(w, 400, "Invalid trace batch")
			return
		}
		if len(batch) > 5000 {
			fail(w, 400, "Too many trace events")
			return
		}
		lines := make([][]byte, 0, len(batch))
		for _, ev := range batch {
			if len(ev["type"]) == 0 {
				continue
			}
			ev["src"] = json.RawMessage(`"client"`)
			line, err := json.Marshal(ev)
			if err != nil || len(line) > traceEventLimit {
				continue
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			if err := a.Traces.Append(s.ID, s.Agent.ID, lines...); err != nil {
				if errors.Is(err, errTraceFull) {
					fail(w, 413, "Trace size limit reached for this session")
					return
				}
				fail(w, 500, "Trace write failed")
				return
			}
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /api/traces", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, a.Traces.List())
	})
	m.HandleFunc("GET /api/traces/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, err := a.Traces.Open(r.PathValue("id"))
		if err != nil {
			fail(w, 404, "Trace not found")
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.Copy(w, f)
	})
}
