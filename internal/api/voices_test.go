package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"io/fs"
	"math"
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
	app     *API
	server  *httptest.Server
	tts     *httptest.Server
	store   *voices.Store
	root    string
	mu      sync.Mutex
	inputs  []string
	refs    [][2]string
	options []map[string]string
	gate    chan struct{}
}

func newVoiceEnv(t *testing.T) *voiceEnv {
	t.Helper()
	e := &voiceEnv{}
	e.tts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Input         string                `json:"input"`
			ReferenceText string                `json:"reference_text"`
			VoiceRef      struct{ Data string } `json:"voice_ref"`
			Options       map[string]string     `json:"options"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		e.mu.Lock()
		e.inputs = append(e.inputs, b.Input)
		e.refs = append(e.refs, [2]string{b.VoiceRef.Data, b.ReferenceText})
		e.options = append(e.options, b.Options)
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
	agents["a"].Voice.Default = "preset-ryan"
	client := &speech.Client{STTURL: e.tts.URL, TTSURL: e.tts.URL}
	store, err := voices.Open(filepath.Join(root, "var", "voices.json"), filepath.Join(root, "var", "cues"), agents, client)
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
	if e.inputs[0] != "Hi there" {
		t.Fatalf("preview did not drop the stage direction: %q", e.inputs[0])
	}
	resp = e.post(t, "/api/settings/voices/preview", `{"voice_id":"preset-ryan"}`, "")
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 || e.inputs[1] != defaultPreview {
		t.Fatalf("default text: %d %q", resp.StatusCode, e.inputs[1])
	}
	for name, body := range map[string]string{
		"both voice_id and description": `{"voice_id":"preset-ryan","description":"x"}`,
		"neither":                       `{"text":"hi"}`,
		"unknown voice":                 `{"voice_id":"nope"}`,
		"text too long":                 `{"voice_id":"preset-ryan","text":"` + strings.Repeat("a", 301) + `"}`,
		"direction too long":            `{"voice_id":"preset-ryan","direction":"` + strings.Repeat("a", 501) + `"}`,
		"unknown field":                 `{"voice_id":"preset-ryan","seed":1}`,
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
	resp := e.post(t, "/api/settings/voices/preview", `{"voice_id":"preset-ryan"}`, "https://evil.example")
	if resp.StatusCode != 403 || len(e.inputs) != before {
		t.Fatalf("cross-origin preview ran: %d", resp.StatusCode)
	}
	e.gate = make(chan struct{})
	done := make(chan int, 1)
	go func() {
		r := e.post(t, "/api/settings/voices/preview", `{"voice_id":"preset-ryan"}`, "")
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
	if r := e.post(t, "/api/settings/voices/preview", `{"voice_id":"preset-ryan"}`, ""); r.StatusCode != 409 {
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
		body := `{"revision":` + string(rune('0'+rev)) + `,"voices":[{"id":"gent","name":"Gent","description":"Deep."}],"personas":{"a":{"voice_id":"` + voice + `","direction":""}}}`
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
	if err = json.NewDecoder(resp.Body).Decode(&snap); err != nil || resp.Header.Get("Cache-Control") != "no-store" || snap.Revision != 1 || len(snap.Voices) != 15 {
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
	// Cue wording ships with the agent but its audio never does: nothing is
	// served until a set has been rendered in the persona's voice.
	if get("/api/agents/a/voice-cues") != 404 || get("/api/agents/a/voice-cues/waiting-1") != 404 {
		t.Fatal("cues served before any render")
	}
	resp := e.post(t, "/api/settings/voices", `{"revision":0,"voices":[{"id":"gent","name":"Gent","description":"Deep."}],"personas":{"a":{"voice_id":"gent","direction":""}}}`, "")
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
	resp := e.post(t, "/api/settings/voices", `{"revision":0,"voices":[],"personas":{"a":{"voice_id":"preset-ryan","direction":""}}}`, "")
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 500 || !strings.Contains(string(raw), "could not save voice settings") || strings.Contains(string(raw), e.root) {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
}

// tone is a 220 Hz sine of the given peak amplitude padded with digital silence.
func tone(rate int, lead, body, trail, amp float64) []byte {
	n := func(sec float64) int { return int(sec * float64(rate)) }
	samples := make([]int16, n(lead)+n(body)+n(trail))
	for i := 0; i < n(body); i++ {
		v := math.Round(amp * math.Sin(2*math.Pi*220*float64(i)/float64(rate)))
		samples[n(lead)+i] = int16(math.Max(-32768, math.Min(32767, v)))
	}
	return speech.WAVAt(speech.PCMBytes(samples), rate)
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func countFiles(root string) int {
	n := 0
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func decodeSnapshot(t *testing.T, resp *http.Response) voices.Snapshot {
	t.Helper()
	var snap voices.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	return snap
}

func voiceKind(snap voices.Snapshot, id string) (kind, name string) {
	for _, v := range snap.Voices {
		if v.ID == id {
			return v.Kind, v.Name
		}
	}
	return "", ""
}

func (e *voiceEnv) postBytes(t *testing.T, path, contentType string, body []byte, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", e.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
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

// fakeSTT stands in for the speech-to-text worker's file endpoint.
type fakeSTT struct {
	mu       sync.Mutex
	calls    int
	model    string
	language string
	wav      []byte
	reply    string
	status   int
	gate     chan struct{}
}

func (f *fakeSTT) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (e *voiceEnv) withSTT(t *testing.T) *fakeSTT {
	t.Helper()
	f := &fakeSTT{reply: "the quick brown fox"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer file.Close()
		wav, _ := io.ReadAll(file)
		f.mu.Lock()
		f.calls++
		f.model, f.language, f.wav = r.FormValue("model"), r.FormValue("language"), wav
		gate, status, reply := f.gate, f.status, f.reply
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if status != 0 {
			http.Error(w, "boom", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"text": reply})
	}))
	t.Cleanup(server.Close)
	e.app.Speech.STTURL = server.URL
	return f
}

const checkPath = "/api/settings/voices/clone/check"

func TestCloneCheckReturnsQualityChecksAndTranscript(t *testing.T) {
	e := newVoiceEnv(t)
	stt := e.withSTT(t)
	files := countFiles(e.root)
	resp := e.postBytes(t, checkPath, "audio/wav", tone(24000, 1, 6, 1, 8000), "")
	var report voices.CloneReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil || resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %v", resp.StatusCode, err, resp.Header)
	}
	if report.Transcript != "the quick brown fox" || len(report.Checks) != 4 || report.DurationMS < 6200 || report.DurationMS > 6400 {
		t.Fatalf("%+v", report)
	}
	for i, id := range []string{"duration", "level", "clipping", "silence"} {
		if c := report.Checks[i]; c.ID != id || c.Status != "pass" || c.Label == "" || c.Detail == "" {
			t.Fatalf("check %d: %+v", i, c)
		}
	}
	if math.Abs(report.RMSdBFS+15.5) > 0.7 || report.ClippingRatio != 0 {
		t.Fatalf("%+v", report)
	}
	// The worker takes the trimmed speech as mono 16 kHz in a multipart file.
	stt.mu.Lock()
	model, language, wav := stt.model, stt.language, stt.wav
	stt.mu.Unlock()
	a, err := speech.ParseWAV(wav)
	if model != "nemotron-3.5-asr" || language != "en-US" || err != nil || a.Rate != 16000 || len(a.Samples) != 100800 {
		t.Fatalf("STT request: %q %q %v %+v", model, language, err, a.Rate)
	}
	// Nothing was kept.
	if countFiles(e.root) != files {
		t.Fatal("the check wrote files")
	}
	if _, err = os.Stat(filepath.Join(e.root, "var", "voices")); err == nil {
		t.Fatal("the check created a voice directory")
	}
}

func TestCloneCheckReportsBadQualityWithoutFailing(t *testing.T) {
	e := newVoiceEnv(t)
	e.withSTT(t)
	resp := e.postBytes(t, checkPath, "audio/wav", tone(24000, 0.5, 6, 0.5, 40000), "")
	var report voices.CloneReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	if report.Checks[2].ID != "clipping" || report.Checks[2].Status != "fail" || report.ClippingRatio < 0.2 {
		t.Fatalf("%+v", report)
	}
	silent := e.postBytes(t, checkPath, "audio/x-wav", speech.WAVAt(make([]byte, 24000*2*5), 24000), "")
	report = voices.CloneReport{}
	if err := json.NewDecoder(silent.Body).Decode(&report); err != nil || silent.StatusCode != 200 || report.Checks[3].Status != "fail" || report.RMSdBFS != -120 || report.Transcript != "" {
		t.Fatalf("silent recording: %d %v %+v", silent.StatusCode, err, report)
	}
}

func TestCloneCheckRejectsInvalidRequests(t *testing.T) {
	e := newVoiceEnv(t)
	stt := e.withSTT(t)
	good := tone(24000, 0.5, 6, 0.5, 8000)
	stereo := append([]byte{}, good...)
	stereo[22] = 2
	for _, tc := range []struct {
		name        string
		contentType string
		body        []byte
		status      int
	}{
		{"not a wav", "audio/wav", []byte("this is not audio at all"), 400},
		{"empty", "audio/wav", nil, 400},
		{"stereo", "audio/wav", stereo, 400},
		{"too large", "audio/wav", make([]byte, 2<<20+1024), 413},
		{"wrong content type", "application/json", good, 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if resp := e.postBytes(t, checkPath, tc.contentType, tc.body, ""); resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.status)
			}
		})
	}
	if resp := e.postBytes(t, checkPath, "audio/wav", good, "https://evil.example"); resp.StatusCode != 403 {
		t.Fatalf("cross-origin check: %d", resp.StatusCode)
	}
	if n := stt.count(); n != 0 {
		t.Fatalf("%d rejected requests reached the recognizer", n)
	}
}

func TestCloneCheckSpeechRecognitionErrors(t *testing.T) {
	e := newVoiceEnv(t)
	good := tone(24000, 0.5, 6, 0.5, 8000)
	e.app.Speech.STTURL = ""
	if resp := e.postBytes(t, checkPath, "audio/wav", good, ""); resp.StatusCode != 503 {
		t.Fatalf("unconfigured: %d", resp.StatusCode)
	}
	stt := e.withSTT(t)
	stt.status = 500
	if resp := e.postBytes(t, checkPath, "audio/wav", good, ""); resp.StatusCode != 502 {
		t.Fatalf("recognizer failure: %d", resp.StatusCode)
	}
	// The slot is released after a failure.
	stt.mu.Lock()
	stt.status = 0
	stt.mu.Unlock()
	if resp := e.postBytes(t, checkPath, "audio/wav", good, ""); resp.StatusCode != 200 {
		t.Fatalf("after a failure: %d", resp.StatusCode)
	}
}

func TestCloneCheckIsExclusiveWithOtherChecksAndPreviews(t *testing.T) {
	e := newVoiceEnv(t)
	stt := e.withSTT(t)
	good := tone(24000, 0.5, 6, 0.5, 8000)
	stt.gate = make(chan struct{})
	done := make(chan int, 1)
	go func() {
		done <- e.postBytes(t, checkPath, "audio/wav", good, "").StatusCode
	}()
	for deadline := time.Now().Add(2 * time.Second); stt.count() == 0 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
	}
	if resp := e.postBytes(t, checkPath, "audio/wav", good, ""); resp.StatusCode != 409 {
		t.Fatalf("concurrent check: %d", resp.StatusCode)
	}
	if resp := e.post(t, "/api/settings/voices/preview", `{"voice_id":"preset-ryan"}`, ""); resp.StatusCode != 409 {
		t.Fatalf("preview during a check: %d", resp.StatusCode)
	}
	close(stt.gate)
	if code := <-done; code != 200 {
		t.Fatalf("first check: %d", code)
	}
}

func (e *voiceEnv) cloneBody(t *testing.T, override map[string]any) string {
	t.Helper()
	body := map[string]any{"revision": 0, "id": "my-voice", "name": "My voice", "transcript": "Hello there.", "audio_base64": b64(tone(24000, 0.5, 6, 0.5, 8000)), "consent": true}
	for k, v := range override {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

const clonePath = "/api/settings/voices/clone"

func TestCloneLifecycleThroughTheAPI(t *testing.T) {
	e := newVoiceEnv(t)
	dir := filepath.Join(e.root, "var", "voices", "my-voice")
	exists := func() bool { _, err := os.Stat(filepath.Join(dir, "reference.wav")); return err == nil }

	resp := e.post(t, clonePath, e.cloneBody(t, map[string]any{"name": " My voice "}), "")
	snap := decodeSnapshot(t, resp)
	if kind, name := voiceKind(snap, "my-voice"); resp.StatusCode != 200 || snap.Revision != 1 || kind != "clone" || name != "My voice" {
		t.Fatalf("create: %d %+v", resp.StatusCode, snap)
	}
	if kind, _ := voiceKind(snap, "preset-ryan"); kind != "preset" || !exists() {
		t.Fatalf("kind of the builtin voice %q, files present %v", kind, exists())
	}
	if txt, _ := os.ReadFile(filepath.Join(dir, "reference.txt")); string(txt) != "Hello there." {
		t.Fatalf("transcript %q", txt)
	}

	assigned := `"personas":{"a":{"voice_id":"my-voice","direction":""}}}`
	resp = e.post(t, "/api/settings/voices", `{"revision":1,"voices":[{"id":"my-voice","name":"My voice","kind":"clone"}],`+assigned, "")
	snap = decodeSnapshot(t, resp)
	if resp.StatusCode != 200 || snap.Personas["a"].VoiceID != "my-voice" || snap.Cues["a"].State != "queued" {
		t.Fatalf("assign: %d %+v", resp.StatusCode, snap)
	}

	// A clone that is still assigned cannot be removed, and nothing is deleted.
	resp = e.post(t, "/api/settings/voices", `{"revision":2,"voices":[],`+assigned, "")
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 400 || !strings.Contains(string(raw), "still assigned") || !exists() {
		t.Fatalf("delete while assigned: %d %s", resp.StatusCode, raw)
	}
	// Entries stay strict: no audio smuggled through the save, no description on a clone.
	for _, entry := range []string{
		`{"id":"my-voice","name":"x","kind":"clone","audio_base64":"AAAA"}`,
		`{"id":"my-voice","name":"x","kind":"clone","description":"Deep."}`,
		`{"id":"fresh","name":"x","kind":"clone"}`,
	} {
		if resp = e.post(t, "/api/settings/voices", `{"revision":2,"voices":[`+entry+`],`+assigned, ""); resp.StatusCode != 400 {
			t.Fatalf("%s: %d", entry, resp.StatusCode)
		}
	}

	resp = e.post(t, "/api/settings/voices", `{"revision":2,"voices":[{"id":"my-voice","name":"Renamed","kind":"clone"}],`+assigned, "")
	snap = decodeSnapshot(t, resp)
	if kind, name := voiceKind(snap, "my-voice"); resp.StatusCode != 200 || kind != "clone" || name != "Renamed" || !exists() {
		t.Fatalf("rename: %d %+v", resp.StatusCode, snap.Voices)
	}

	// Unassigned and omitted: the voice and its files go.
	resp = e.post(t, "/api/settings/voices", `{"revision":3,"voices":[],"personas":{"a":{"voice_id":"preset-ryan","direction":""}}}`, "")
	snap = decodeSnapshot(t, resp)
	if kind, _ := voiceKind(snap, "my-voice"); resp.StatusCode != 200 || kind != "" || exists() {
		t.Fatalf("delete: %d %+v files present %v", resp.StatusCode, snap.Voices, exists())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("clone directory left behind: %v", err)
	}
}

func TestCloneCreateRejectsInvalidRequests(t *testing.T) {
	e := newVoiceEnv(t)
	for _, tc := range []struct {
		name     string
		override map[string]any
		origin   string
		status   int
	}{
		{"consent false", map[string]any{"consent": false}, "", 400},
		{"consent missing", map[string]any{"consent": nil}, "", 400},
		{"stale revision", map[string]any{"revision": 5}, "", 409},
		{"unknown field", map[string]any{"extra": 1}, "", 400},
		{"bad base64", map[string]any{"audio_base64": "!!!"}, "", 400},
		{"empty audio", map[string]any{"audio_base64": ""}, "", 400},
		{"recording too short", map[string]any{"audio_base64": b64(tone(24000, 0.5, 1, 0.5, 8000))}, "", 400},
		{"bad id", map[string]any{"id": "Bad Id"}, "", 400},
		{"reserved id", map[string]any{"id": "preset-ryan"}, "", 400},
		{"empty name", map[string]any{"name": " "}, "", 400},
		{"empty transcript", map[string]any{"transcript": ""}, "", 400},
		{"long transcript", map[string]any{"transcript": strings.Repeat("a", 501)}, "", 400},
		{"cross-origin", nil, "https://evil.example", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if resp := e.post(t, clonePath, e.cloneBody(t, tc.override), tc.origin); resp.StatusCode != tc.status {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.status, raw)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(e.root, "var", "voices")); err == nil {
		t.Fatal("a rejected request left clone files")
	}
	if e.store.Snapshot().Revision != 0 {
		t.Fatal("a rejected request changed the settings")
	}
}

func TestPreviewWithInlineCloneUsesItsReferenceAndDirection(t *testing.T) {
	e := newVoiceEnv(t)
	audio := b64(tone(24000, 0.5, 6, 0.5, 8000))
	inline := func(fields string) string {
		return `{"clone_audio_base64":"` + audio + `","clone_transcript":"Hello there."` + fields + `}`
	}
	resp := e.post(t, "/api/settings/voices/preview", inline(`,"direction":"Calm.","text":"Hi (SIGH) there"`), "")
	wav, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/wav" || len(wav) != 44+4800 {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	last := len(e.inputs) - 1
	ref, err := base64.StdEncoding.DecodeString(e.refs[last][0])
	if err != nil {
		t.Fatal(err)
	}
	if a, err := speech.ParseWAV(ref); err != nil || a.Rate != 24000 || e.refs[last][1] != "Hello there." {
		t.Fatalf("reference sent to TTS: %v %q", err, e.refs[last][1])
	}
	if e.inputs[last] != "Hi (sigh) there" || e.options[last]["guidance_scale"] != "4" || e.options[last]["instruction"] != "Calm." {
		t.Fatalf("%q %v", e.inputs[last], e.options[last])
	}
	// Without a direction it is plain Clone mode.
	if resp = e.post(t, "/api/settings/voices/preview", inline(``), ""); resp.StatusCode != 200 {
		t.Fatalf("%d", resp.StatusCode)
	}
	last = len(e.inputs) - 1
	if _, has := e.options[last]["instruction"]; has || e.options[last]["guidance_scale"] != "1" || e.inputs[last] != defaultPreview {
		t.Fatalf("%q %v", e.inputs[last], e.options[last])
	}

	// Exactly one source, and a usable recording; none of these may reach the TTS.
	stereo := tone(24000, 0.5, 6, 0.5, 8000)
	stereo[22] = 2
	before := len(e.inputs)
	for name, body := range map[string]string{
		"clone and voice_id":      inline(`,"voice_id":"preset-ryan"`),
		"clone and description":   inline(`,"description":"Deep."`),
		"audio without text":      `{"clone_audio_base64":"` + audio + `"}`,
		"transcript without it":   `{"clone_transcript":"Hello there."}`,
		"bad base64":              `{"clone_audio_base64":"!!!","clone_transcript":"Hello there."}`,
		"too short":               `{"clone_audio_base64":"` + b64(tone(24000, 0.5, 1, 0.5, 8000)) + `","clone_transcript":"Hello there."}`,
		"stereo":                  `{"clone_audio_base64":"` + b64(stereo) + `","clone_transcript":"Hello there."}`,
		"unknown field":           inline(`,"seed":1`),
		"direction too long":      inline(`,"direction":"` + strings.Repeat("a", 501) + `"`),
		"transcript out of range": `{"clone_audio_base64":"` + audio + `","clone_transcript":"` + strings.Repeat("a", 501) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if resp := e.post(t, "/api/settings/voices/preview", body, ""); resp.StatusCode != 400 {
				t.Fatalf("status %d", resp.StatusCode)
			}
		})
	}
	if len(e.inputs) != before {
		t.Fatalf("%d rejected previews reached the TTS", len(e.inputs)-before)
	}
}

func TestSavedCloneCanBePreviewedByID(t *testing.T) {
	e := newVoiceEnv(t)
	if resp := e.post(t, clonePath, e.cloneBody(t, nil), ""); resp.StatusCode != 200 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	resp := e.post(t, "/api/settings/voices/preview", `{"voice_id":"my-voice","direction":"Slow."}`, "")
	io.Copy(io.Discard, resp.Body)
	last := len(e.inputs) - 1
	stored, err := os.ReadFile(filepath.Join(e.root, "var", "voices", "my-voice", "reference.wav"))
	if err != nil || resp.StatusCode != 200 || e.refs[last][0] != b64(stored) || e.refs[last][1] != "Hello there." || e.options[last]["instruct"] != "" {
		t.Fatalf("%d %v %q", resp.StatusCode, err, e.refs[last][1])
	}
}
