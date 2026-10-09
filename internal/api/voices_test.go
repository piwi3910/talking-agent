package api

import (
	"context"
	"encoding/binary"
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
	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/voices"
)

const bakedCues = `{"reference_sha256":"baked","cues":[{"id":"waiting-1","category":"waiting","text":"One moment.","duration_ms":1},{"id":"waiting-2","category":"waiting","text":"Hold on.","duration_ms":1}]}`

type voiceEnv struct {
	app    *API
	server *httptest.Server
	tts    *httptest.Server
	store  *voices.Store
	root   string
	mu     sync.Mutex
	inputs []string
	gate   chan struct{}
}

func newVoiceEnv(t *testing.T) *voiceEnv {
	t.Helper()
	e := &voiceEnv{}
	e.tts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Input string }
		json.NewDecoder(r.Body).Decode(&b)
		e.mu.Lock()
		e.inputs = append(e.inputs, b.Input)
		gate := e.gate
		e.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		w.Write(make([]byte, 4800))
	}))
	t.Cleanup(e.tts.Close)
	root := t.TempDir()
	e.root = root
	dir := filepath.Join(root, "voice")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cues.json"), []byte(bakedCues), 0600); err != nil {
		t.Fatal(err)
	}
	agents := map[string]*config.Agent{"a": {ID: "a", Name: "Sara", Dir: root}}
	client := &speech.Client{STTURL: e.tts.URL, TTSURL: e.tts.URL}
	store, err := voices.Open(filepath.Join(root, "var", "voices.json"), filepath.Join(root, "var", "cues"), agents, map[string]*speech.Reference{"a": {AudioBase64: "x", Text: "y"}}, client)
	if err != nil {
		t.Fatal(err)
	}
	e.store = store
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.app = &API{Root: ctx, Agents: agents, Speech: client, Voices: store}
	e.server = httptest.NewServer(e.app.Handler())
	t.Cleanup(e.server.Close)
	return e
}
func (e *voiceEnv) post(t *testing.T, path, body, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", e.server.URL+path, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPreviewReturnsWAVAndValidatesRequests(t *testing.T) {
	e := newVoiceEnv(t)
	resp := e.post(t, "/api/settings/voices/preview", `{"description":"A deep, slow British man.","text":"Hi (LAUGH) there"}`, "")
	wav, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/wav" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	if len(wav) != 44+4800 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || binary.LittleEndian.Uint32(wav[24:]) != 24000 || binary.LittleEndian.Uint32(wav[40:]) != 4800 {
		t.Fatalf("bad WAV header (%d bytes)", len(wav))
	}
	if e.inputs[0] != "Hi (laugh) there" {
		t.Fatalf("preview did not normalise vocal events: %q", e.inputs[0])
	}
	resp = e.post(t, "/api/settings/voices/preview", `{"voice_id":"ref-a"}`, "")
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 || e.inputs[1] != defaultPreview {
		t.Fatalf("default text: %d %q", resp.StatusCode, e.inputs[1])
	}
	for name, body := range map[string]string{
		"both voice_id and description": `{"voice_id":"ref-a","description":"x"}`,
		"neither":                       `{"text":"hi"}`,
		"unknown voice":                 `{"voice_id":"nope"}`,
		"text too long":                 `{"voice_id":"ref-a","text":"` + strings.Repeat("a", 301) + `"}`,
		"direction too long":            `{"voice_id":"ref-a","direction":"` + strings.Repeat("a", 501) + `"}`,
		"unknown field":                 `{"voice_id":"ref-a","seed":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			resp := e.post(t, "/api/settings/voices/preview", body, "")
			if resp.StatusCode != 400 {
				t.Fatalf("status %d", resp.StatusCode)
			}
		})
	}
}

func TestPreviewRejectsCrossOriginAndConcurrentRequests(t *testing.T) {
	e := newVoiceEnv(t)
	before := len(e.inputs)
	resp := e.post(t, "/api/settings/voices/preview", `{"voice_id":"ref-a"}`, "https://evil.example")
	if resp.StatusCode != 403 || len(e.inputs) != before {
		t.Fatalf("cross-origin preview ran: %d", resp.StatusCode)
	}
	e.gate = make(chan struct{})
	done := make(chan int, 1)
	go func() {
		r := e.post(t, "/api/settings/voices/preview", `{"voice_id":"ref-a"}`, "")
		done <- r.StatusCode
	}()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		e.mu.Lock()
		n := len(e.inputs)
		e.mu.Unlock()
		if n > before {
			break
		}
	}
	if r := e.post(t, "/api/settings/voices/preview", `{"voice_id":"ref-a"}`, ""); r.StatusCode != 409 {
		t.Fatalf("concurrent preview: %d", r.StatusCode)
	}
	close(e.gate)
	if code := <-done; code != 200 {
		t.Fatalf("first preview: %d", code)
	}
}

func TestVoiceSettingsStatusCodes(t *testing.T) {
	e := newVoiceEnv(t)
	save := func(rev int, voice string) int {
		body := `{"revision":` + string(rune('0'+rev)) + `,"voices":[{"id":"gent","name":"Gent","description":"Deep."}],"personas":{"a":{"voice_id":"` + voice + `","direction":"","events":true}}}`
		return e.post(t, "/api/settings/voices", body, "").StatusCode
	}
	if c := save(0, "gent"); c != 200 {
		t.Fatalf("save %d", c)
	}
	if c := save(0, "gent"); c != 409 {
		t.Fatalf("stale revision %d", c)
	}
	if c := save(1, "missing"); c != 400 {
		t.Fatalf("unknown voice %d", c)
	}
	if r := e.post(t, "/api/settings/voices", `{"revision":1,"voices":[{"id":"x","name":"n","description":"d","builtin":true}],"personas":{}}`, ""); r.StatusCode != 400 {
		t.Fatalf("builtin flag accepted in custom voice: %d", r.StatusCode)
	}
	if r := e.post(t, "/api/settings/voices/cues/nobody", `{}`, ""); r.StatusCode != 404 {
		t.Fatalf("unknown persona %d", r.StatusCode)
	}
	resp, err := http.Get(e.server.URL + "/api/settings/voices")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snap voices.Snapshot
	if err = json.NewDecoder(resp.Body).Decode(&snap); err != nil || resp.Header.Get("Cache-Control") != "no-store" || snap.Revision != 1 || len(snap.Voices) != 2 {
		t.Fatalf("%v %+v", err, snap)
	}
}

func TestCueEndpointsAre404UntilRenderedSetIsComplete(t *testing.T) {
	e := newVoiceEnv(t)
	get := func(path string) int {
		resp, err := http.Get(e.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if get("/api/agents/a/voice-cues") != 200 {
		t.Fatal("baked manifest missing for original voice")
	}
	resp := e.post(t, "/api/settings/voices", `{"revision":0,"voices":[{"id":"gent","name":"Gent","description":"Deep."}],"personas":{"a":{"voice_id":"gent","direction":"","events":true}}}`, "")
	if resp.StatusCode != 200 {
		t.Fatalf("save %d", resp.StatusCode)
	}
	// No worker is running yet, so the set cannot exist.
	if get("/api/agents/a/voice-cues") != 404 || get("/api/agents/a/voice-cues/waiting-1") != 404 {
		t.Fatal("cues served before the set was ready")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.store.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for get("/api/agents/a/voice-cues") != 200 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if get("/api/agents/a/voice-cues/waiting-1") != 200 || get("/api/agents/a/voice-cues/waiting-2") != 200 {
		t.Fatal("rendered cues not served once ready")
	}
}

func TestStorageFailureIsServerErrorWithoutPaths(t *testing.T) {
	e := newVoiceEnv(t)
	if err := os.WriteFile(filepath.Join(e.root, "var"), []byte("file in the way"), 0600); err != nil {
		t.Fatal(err)
	}
	resp := e.post(t, "/api/settings/voices", `{"revision":0,"voices":[],"personas":{"a":{"voice_id":"ref-a","direction":"","events":true}}}`, "")
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 500 || !strings.Contains(string(raw), "could not save voice settings") || strings.Contains(string(raw), e.root) {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
}
