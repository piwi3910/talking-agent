package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/voices"
)

// qwenEnv runs the real routes against a fake Qwen3-TTS server.
type qwenEnv struct {
	app     *API
	server  *httptest.Server
	tts     *httptest.Server
	session *session.Session
	mu      sync.Mutex
	bodies  []map[string]any
	failing bool
	hold    chan struct{} // requests whose input contains "hold" wait for it
}

func newQwenEnv(t *testing.T) *qwenEnv {
	t.Helper()
	e := &qwenEnv{}
	e.tts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		e.mu.Lock()
		e.bodies = append(e.bodies, b)
		failing, hold := e.failing, e.hold
		e.mu.Unlock()
		if input, _ := b["input"].(string); hold != nil && strings.Contains(input, "hold") {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		if failing {
			http.Error(w, "boom", 500)
			return
		}
		w.Write(make([]byte, 4800))
	}))
	t.Cleanup(e.tts.Close)
	root := t.TempDir()
	dir := filepath.Join(root, "voice")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cues.json"), []byte(bakedCues), 0600); err != nil {
		t.Fatal(err)
	}
	agent := &config.Agent{ID: "a", Name: "Sara", Dir: root}
	agent.Voice.Default = "preset-ryan"
	agents := map[string]*config.Agent{"a": agent}
	client := &speech.Client{STTURL: e.tts.URL, TTSURL: e.tts.URL, Provider: speech.ProviderQwen3}
	store, err := voices.Open(filepath.Join(root, "var", "voices.json"), filepath.Join(root, "var", "cues"), agents, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sessions := session.NewStore()
	e.session = sessions.Create(agent, "C001")
	e.app = &API{Root: ctx, Agents: agents, Speech: client, Voices: store, Sessions: sessions}
	e.server = httptest.NewServer(e.app.Handler())
	t.Cleanup(e.server.Close)
	return e
}
func (e *qwenEnv) do(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}
func (e *qwenEnv) requests() []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]map[string]any{}, e.bodies...)
}

func TestQwen3ReportsProviderAndBuiltinVoices(t *testing.T) {
	e := newQwenEnv(t)
	resp := e.do(t, "GET", "/api/settings/voices", "")
	var snap struct {
		Provider string `json:"provider"`
		Voices   []struct{ ID, Name, Kind string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	presets := 0
	for _, v := range snap.Voices {
		if v.Kind == "preset" && strings.HasPrefix(v.ID, "preset-") {
			presets++
		}
	}
	if snap.Provider != "qwen3" || presets != 14 {
		t.Fatalf("%+v presets=%d", snap, presets)
	}
	var info struct{ TTS string }
	resp = e.do(t, "GET", "/api/voice", "")
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.TTS != "Qwen3-TTS 1.7B" {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestSpeechUsesTheAgentDefaultVoiceAndDropsStageDirections(t *testing.T) {
	e := newQwenEnv(t)
	resp := e.do(t, "POST", "/api/sessions/"+e.session.ID+"/speech", `{"text":"Sure (laugh) thing."}`)
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store, no-transform" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	sent := e.requests()
	if len(sent) != 1 {
		t.Fatalf("%d requests", len(sent))
	}
	b := sent[0]
	if b["input"] != "Sure thing." || b["model"] != "qwen3-tts-custom" || b["voice"] != "Ryan" || b["stream"] != false || b["response_format"] != "pcm" {
		t.Fatalf("%v", b)
	}
	if _, ok := b["voice_ref"]; ok {
		t.Fatalf("a preset must not send a reference: %v", b)
	}
}

func TestSpeechAllowsTwoConcurrentRequestsPerSession(t *testing.T) {
	e := newQwenEnv(t)
	e.hold = make(chan struct{})
	path := "/api/sessions/" + e.session.ID + "/speech"
	done := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, err := http.Post(e.server.URL+path, "application/json", strings.NewReader(`{"text":"hold on"}`))
			if err != nil {
				done <- -1
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			done <- resp.StatusCode
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(e.requests()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(e.requests()) != 2 {
		t.Fatalf("only %d requests were in flight", len(e.requests()))
	}
	if third := e.do(t, "POST", path, `{"text":"third"}`); third.StatusCode != 409 {
		t.Fatalf("third concurrent request: %d", third.StatusCode)
	}
	close(e.hold)
	for i := 0; i < 2; i++ {
		if status := <-done; status != 200 {
			t.Fatalf("held request: %d", status)
		}
	}
	// Every slot is released on every path.
	for i := 0; i < 3; i++ {
		resp := e.do(t, "POST", path, `{"text":"after"}`)
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("slot leaked: %d", resp.StatusCode)
		}
	}
	// A request that fails upstream also gives its slot back.
	e.mu.Lock()
	e.failing = true
	e.mu.Unlock()
	for i := 0; i < 3; i++ {
		resp := e.do(t, "POST", path, `{"text":"fails"}`)
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != 502 {
			t.Fatalf("failed request %d: %d", i, resp.StatusCode)
		}
	}
}

func TestSavingADesignVoiceWhoseSampleFailsReturns502AndPersistsNothing(t *testing.T) {
	e := newQwenEnv(t)
	e.mu.Lock()
	e.failing = true
	e.mu.Unlock()
	body := `{"revision":0,"voices":[{"id":"gent","name":"Gent","description":"Deep and slow."}],"personas":{"a":{"voice_id":"gent","direction":""}}}`
	resp := e.do(t, "POST", "/api/settings/voices", body)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 502 || !strings.Contains(string(raw), "Nothing was saved") {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	snap := e.app.Voices.Snapshot()
	if snap.Revision != 0 || snap.Personas["a"].VoiceID != "preset-ryan" {
		t.Fatalf("%+v", snap)
	}
	// Once the service recovers the same save succeeds with a single design render.
	e.mu.Lock()
	e.failing = false
	e.bodies = nil
	e.mu.Unlock()
	resp = e.do(t, "POST", "/api/settings/voices", body)
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	sent := e.requests()
	if len(sent) != 1 || sent[0]["model"] != "qwen3-tts-design" {
		t.Fatalf("%v", sent)
	}
}

func TestQwen3PreviewUsesPresetAndDesignModels(t *testing.T) {
	e := newQwenEnv(t)
	resp := e.do(t, "POST", "/api/settings/voices/preview", `{"voice_id":"preset-ryan","direction":"Calm.","text":"Hi (laugh) there"}`)
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp = e.do(t, "POST", "/api/settings/voices/preview", `{"description":"A warm old man."}`)
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	sent := e.requests()
	if len(sent) != 2 {
		t.Fatalf("%d requests", len(sent))
	}
	options := func(b map[string]any) map[string]any { o, _ := b["options"].(map[string]any); return o }
	if sent[0]["model"] != "qwen3-tts-custom" || sent[0]["voice"] != "Ryan" || options(sent[0])["instruct"] != "Calm." || sent[0]["input"] != "Hi there" {
		t.Fatalf("%v", sent[0])
	}
	if sent[1]["model"] != "qwen3-tts-design" || options(sent[1])["instruct"] != "A warm old man." {
		t.Fatalf("%v", sent[1])
	}
}
