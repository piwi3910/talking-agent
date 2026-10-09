package voices

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
)

const manifest = `{"reference_sha256":"baked","cues":[{"id":"waiting-1","category":"waiting","text":"One moment.","duration_ms":1},{"id":"waiting-2","category":"waiting","text":"Just a moment.","duration_ms":1},{"id":"lookup-1","category":"lookup","text":"Looking.","duration_ms":1}]}`

type fixture struct {
	dir    string
	agents map[string]*config.Agent
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{dir: root, agents: map[string]*config.Agent{}}
	for _, id := range []string{"a", "b"} {
		dir := filepath.Join(root, "agents", id)
		if err := os.MkdirAll(filepath.Join(dir, "voice"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "voice", "cues.json"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		f.agents[id] = &config.Agent{ID: id, Name: "Agent " + id, Dir: dir, Persona: map[string]string{"voice_style": "Warm " + id}}
		f.agents[id].Voice.Default = map[string]string{"a": "preset-ryan", "b": "preset-aiden"}[id]
	}
	return f
}

// only drops every agent but id, for tests that follow one persona's render.
func (f fixture) only(id string) fixture {
	for other := range f.agents {
		if other != id {
			delete(f.agents, other)
		}
	}
	return f
}

// open uses a fake audio.cpp worker unless the test brings its own client.
func (f fixture) open(t *testing.T, tts *speech.Client) *Store {
	t.Helper()
	if tts == nil {
		tts = newFakeQwen(t).client()
	}
	s, err := Open(filepath.Join(f.dir, "var", "voices.json"), filepath.Join(f.dir, "var", "cues"), f.agents, tts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(s *Store, mutate func(*SaveRequest)) SaveRequest {
	snap := s.Snapshot()
	in := SaveRequest{Revision: snap.Revision, Voices: []Custom{}, Personas: snap.Personas}
	for _, v := range snap.Voices {
		if !v.Builtin {
			in.Voices = append(in.Voices, Custom{ID: v.ID, Name: v.Name, Description: v.Description, Kind: v.Kind})
		}
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

func TestSaveLoadRoundTripKeepsVoicesAndAssignments(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, nil)
	saved, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: " British gent ", Description: "Deep and slow."}}
		in.Personas["a"] = Persona{VoiceID: "gent", Direction: " calm "}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || saved.Personas["a"].Direction != "calm" {
		t.Fatalf("%+v", saved)
	}
	reopened := f.open(t, nil)
	got := reopened.Snapshot()
	if got.Revision != 1 || got.Personas["a"] != (Persona{VoiceID: "gent", Direction: "calm"}) || got.Personas["b"].VoiceID != "preset-aiden" {
		t.Fatalf("round trip lost state: %+v", got)
	}
	n := len(builtinVoices)
	if len(got.Voices) != n+1 || got.Voices[n].Name != "British gent" || got.Voices[n].Builtin || got.Voices[0].Name != "Ryan (preset, English)" || !got.Voices[0].Builtin {
		t.Fatalf("voices %+v", got.Voices)
	}
}

func TestStaleRevisionIsConflict(t *testing.T) {
	s := newFixture(t).open(t, nil)
	in := request(s, nil)
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(in); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save accepted: %v", err)
	}
}

func TestAssignedVoiceCannotBeDeleted(t *testing.T) {
	s := newFixture(t).open(t, nil)
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "Deep."}}
		in.Personas["b"] = Persona{VoiceID: "gent"}
	})); err != nil {
		t.Fatal(err)
	}
	_, err := s.Save(request(s, func(in *SaveRequest) { in.Voices = nil }))
	if err == nil || !strings.Contains(err.Error(), "assigned to b") {
		t.Fatalf("deletion of assigned voice: %v", err)
	}
	if len(s.Snapshot().Voices) != len(builtinVoices)+1 {
		t.Fatal("rejected deletion changed state")
	}
	// Reassigning in the same request makes the deletion legitimate.
	if _, err = s.Save(request(s, func(in *SaveRequest) {
		in.Voices = nil
		in.Personas["b"] = Persona{VoiceID: "preset-aiden"}
	})); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidInputsAreRejected(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	many := make([]Custom, 51)
	for i := range many {
		many[i] = Custom{ID: "v" + strings.Repeat("a", i%30) + string(rune('a'+i/30)), Name: "n", Description: "d"}
	}
	for name, mutate := range map[string]func(*SaveRequest){
		"id with uppercase":       func(in *SaveRequest) { in.Voices = []Custom{{ID: "Gent", Name: "n", Description: "d"}} },
		"id starting with hyphen": func(in *SaveRequest) { in.Voices = []Custom{{ID: "-gent", Name: "n", Description: "d"}} },
		"id too long":             func(in *SaveRequest) { in.Voices = []Custom{{ID: long(41), Name: "n", Description: "d"}} },
		"id shadows ref":          func(in *SaveRequest) { in.Voices = []Custom{{ID: "ref-a", Name: "n", Description: "d"}} },
		"id shadows builtin":      func(in *SaveRequest) { in.Voices = []Custom{{ID: "preset-ryan", Name: "n", Description: "d"}} },
		"duplicate id": func(in *SaveRequest) {
			in.Voices = []Custom{{ID: "x", Name: "n", Description: "d"}, {ID: "x", Name: "n", Description: "d"}}
		},
		"empty name":           func(in *SaveRequest) { in.Voices = []Custom{{ID: "x", Name: " ", Description: "d"}} },
		"name too long":        func(in *SaveRequest) { in.Voices = []Custom{{ID: "x", Name: long(61), Description: "d"}} },
		"empty description":    func(in *SaveRequest) { in.Voices = []Custom{{ID: "x", Name: "n", Description: ""}} },
		"description too long": func(in *SaveRequest) { in.Voices = []Custom{{ID: "x", Name: "n", Description: long(501)}} },
		"too many voices":      func(in *SaveRequest) { in.Voices = many },
		"direction too long":   func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "preset-ryan", Direction: long(501)} },
		"unknown voice":        func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "nope"} },
		"missing persona":      func(in *SaveRequest) { delete(in.Personas, "b") },
		"unknown persona":      func(in *SaveRequest) { in.Personas["zzz"] = Persona{VoiceID: "preset-ryan"} },
	} {
		t.Run(name, func(t *testing.T) {
			s := newFixture(t).open(t, nil)
			in := request(s, mutate)
			if _, err := s.Save(in); err == nil {
				t.Fatal("accepted")
			}
			if s.Snapshot().Revision != 0 {
				t.Fatal("rejected save changed revision")
			}
		})
	}
}

func TestFailedWriteLeavesStateUnchanged(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, nil)
	if err := os.WriteFile(filepath.Join(f.dir, "var"), []byte("file in the way"), 0600); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	_, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "preset-aiden", Direction: "changed"} }))
	if err == nil {
		t.Fatal("write failure not reported")
	}
	after := s.Snapshot()
	if after.Revision != before.Revision || after.Personas["a"] != before.Personas["a"] {
		t.Fatalf("state changed after failed write: %+v", after)
	}
}

func TestResolveBuiltinVersusCustom(t *testing.T) {
	s := newFixture(t).open(t, nil)
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "Deep and slow."}}
		in.Personas["a"] = Persona{VoiceID: "preset-ryan"}
		in.Personas["b"] = Persona{VoiceID: "gent", Direction: "Be brisk."}
	})); err != nil {
		t.Fatal(err)
	}
	v := s.Resolve("a")
	if v.Model != speech.ModelQwen3Custom || v.Speaker != "Ryan" || v.Instruct != "" || v.Reference != nil {
		t.Fatalf("preset without direction: %+v", v)
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "preset-aiden", Direction: "Slow."} })); err != nil {
		t.Fatal(err)
	}
	if v = s.Resolve("a"); v.Speaker != "Aiden" || v.Instruct != "Slow." {
		t.Fatalf("preset with direction: %+v", v)
	}
	// A designed voice is its stored sample cloned; Base has no direction.
	v = s.Resolve("b")
	if v.Model != speech.ModelQwen3Base || v.Reference == nil || v.Reference.Text != designSampleText || v.Instruct != "" {
		t.Fatalf("custom: %+v", v)
	}
}

func TestDefaultsUseTheAgentVoiceAndVoiceStyle(t *testing.T) {
	s := newFixture(t).open(t, nil)
	if p := s.Snapshot().Personas["a"]; p != (Persona{VoiceID: "preset-ryan", Direction: "Warm a"}) {
		t.Fatalf("%+v", p)
	}
	if p := s.Snapshot().Personas["b"]; p.VoiceID != "preset-aiden" {
		t.Fatalf("%+v", p)
	}
}

func TestDefaultVoiceFallsBackToTheFirstBuiltin(t *testing.T) {
	f := newFixture(t)
	f.agents["a"].Voice.Default = "preset-nobody"
	f.agents["b"].Voice.Default = ""
	s := f.open(t, nil)
	for _, id := range []string{"a", "b"} {
		if p := s.Snapshot().Personas[id]; p.VoiceID != "preset-ryan" {
			t.Fatalf("%s: %+v", id, p)
		}
	}
}

func TestEveryShippedDesignedVoiceHasADescription(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range builtinVoices {
		if seen[b.slug] {
			t.Fatalf("duplicate builtin %s", b.slug)
		}
		seen[b.slug] = true
		if (b.speaker == "") == (b.design == "") {
			t.Fatalf("builtin %s must be exactly one of a preset speaker or a design", b.slug)
		}
	}
}

func TestCueKeyChangesWhenVoiceChanges(t *testing.T) {
	ref := &speech.Reference{AudioBase64: "x", Text: "t"}
	base := speech.Voice{Model: speech.ModelQwen3Base, Reference: ref}
	seen := map[string]string{cueKeyQwen(base): "base"}
	for name, v := range map[string]speech.Voice{
		"model":     {Model: speech.ModelQwen3Custom, Reference: ref},
		"speaker":   {Model: speech.ModelQwen3Custom, Speaker: "Ryan"},
		"instruct":  {Model: speech.ModelQwen3Custom, Speaker: "Ryan", Instruct: "Calm."},
		"reference": {Model: speech.ModelQwen3Base, Reference: &speech.Reference{AudioBase64: "y", Text: "t"}},
		"text":      {Model: speech.ModelQwen3Base, Reference: &speech.Reference{AudioBase64: "x", Text: "u"}},
	} {
		k := cueKeyQwen(v)
		if other, dup := seen[k]; dup || len(k) != 16 {
			t.Fatalf("%s collides with %s", name, other)
		}
		seen[k] = name
	}
	if cueKeyQwen(base) != cueKeyQwen(speech.Voice{Model: speech.ModelQwen3Base, Reference: &speech.Reference{AudioBase64: "x", Text: "t"}}) {
		t.Fatal("key is not deterministic")
	}
}

// fakeTTS returns 100 ms of PCM per request and records the inputs. Every PCM
// byte is the first byte of the request's instruct option or, for a clone, the
// last byte of its reference sample, so tests can tell which voice produced a clip.
type fakeTTS struct {
	*httptest.Server
	mu      sync.Mutex
	inputs  []string
	release chan struct{} // when set, requests wait for it
	fail    bool
}

func newFakeTTS(t *testing.T) *fakeTTS {
	f := &fakeTTS{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Input    string
			Options  map[string]string
			VoiceRef struct{ Data string } `json:"voice_ref"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.inputs = append(f.inputs, b.Input)
		gate, fail := f.release, f.fail
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if fail {
			http.Error(w, "boom", 500)
			return
		}
		pcm := make([]byte, 4800)
		var tint byte
		if in := b.Options["instruct"]; in != "" {
			tint = in[0]
		} else if raw, err := base64.StdEncoding.DecodeString(b.VoiceRef.Data); err == nil && len(raw) > 44 {
			tint = raw[len(raw)-1]
		}
		for i := range pcm {
			pcm[i] = tint
		}
		w.Write(pcm)
	}))
	t.Cleanup(f.Close)
	return f
}
func (f *fakeTTS) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.inputs) }

func waitState(t *testing.T, s *Store, agent, state string) CueStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c := s.Snapshot().Cues[agent]; c.State == state {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never reached %s: %+v", agent, state, s.Snapshot().Cues[agent])
	return CueStatus{}
}

func TestRenderPublishesCompleteSetAtomicallyAndMutesUntilReady(t *testing.T) {
	f := newFixture(t).only("a")
	tts := newFakeTTS(t)
	tts.release = make(chan struct{})
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// No cue audio ships with an agent: nothing is served before a render.
	if _, ok := s.CueDir("a"); ok {
		t.Fatal("cues served before any render")
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "preset-uncle-fu", Direction: "Deep."} })); err != nil {
		t.Fatal(err)
	}
	s.Start(ctx)
	// Mid-render: partial synthesis exists, but no set is visible.
	tts.release <- struct{}{}
	waitState(t, s, "a", "rendering")
	if _, ok := s.CueDir("a"); ok {
		t.Fatal("cues served before the set was complete")
	}
	if st := s.Snapshot().Cues["a"]; st.Total != 3 {
		t.Fatalf("%+v", st)
	}
	close(tts.release)
	st := waitState(t, s, "a", "ready")
	if st.Done != 3 || st.Total != 3 {
		t.Fatalf("%+v", st)
	}
	dir, ok := s.CueDir("a")
	if !ok {
		t.Fatal("ready set not served")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cues.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Ref  string `json:"reference_sha256"`
		Cues []struct {
			ID       string
			Text     string
			Duration int `json:"duration_ms"`
		}
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Ref, filepath.Base(dir)+"-") || len(m.Cues) != 3 || m.Cues[0].Text != "One moment." || m.Cues[0].Duration != 100 {
		t.Fatalf("manifest %+v", m)
	}
	for _, c := range m.Cues {
		wav, err := os.ReadFile(filepath.Join(dir, "cues", c.ID+".wav"))
		if err != nil || len(wav) != 44+4800 || string(wav[:4]) != "RIFF" || wav[44] != 'D' {
			t.Fatalf("cue %s: %v", c.ID, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(f.dir, "var", "cues", "a", ".tmp-*")); len(left) != 0 {
		t.Fatalf("temp dir left behind: %v", left)
	}
	if tts.count() != 3 {
		t.Fatalf("synthesised %d phrases", tts.count())
	}
}

func TestExistingSetIsNotRerenderedUnlessForced(t *testing.T) {
	f := newFixture(t).only("a")
	tts := newFakeTTS(t)
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	// The persona's default voice is rendered at start: no cue audio ships.
	waitState(t, s, "a", "ready")
	if tts.count() != 3 {
		t.Fatalf("default voice synthesised %d phrases", tts.count())
	}
	// A save that leaves the voice alone does not render again.
	if _, err := s.Save(request(s, nil)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if tts.count() != 3 {
		t.Fatalf("unforced save re-rendered: %d", tts.count())
	}
	if _, err := s.Force("a"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for tts.count() < 6 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	waitState(t, s, "a", "ready")
	if tts.count() != 6 {
		t.Fatalf("force synthesised %d", tts.count())
	}
	if _, ok := s.CueDir("a"); !ok {
		t.Fatal("forced re-render left no set")
	}
	if _, err := s.Force("nobody"); err == nil {
		t.Fatal("forced an unknown persona")
	}
}

func TestFailedRenderKeepsErrorAndNoSet(t *testing.T) {
	f := newFixture(t).only("a")
	tts := newFakeTTS(t)
	tts.fail = true
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	st := waitState(t, s, "a", "failed")
	if !strings.Contains(st.Error, "500") {
		t.Fatalf("%+v", st)
	}
	if _, ok := s.CueDir("a"); ok {
		t.Fatal("partial set served after failure")
	}
	if left, _ := filepath.Glob(filepath.Join(f.dir, "var", "cues", "a", "*")); len(left) != 0 {
		t.Fatalf("failed render left %v", left)
	}
}

func TestRestartRequeuesMissingSetAndCleansTempDirs(t *testing.T) {
	f := newFixture(t)
	tts := newFakeTTS(t)
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "preset-aiden", Direction: "x"} })); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-render: no worker ran, and a temp dir is left over.
	leftover := filepath.Join(f.dir, "var", "cues", "a", ".tmp-123")
	if err := os.MkdirAll(filepath.Join(leftover, "cues"), 0700); err != nil {
		t.Fatal(err)
	}
	restarted := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatal("leftover temp dir survived startup")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restarted.Start(ctx)
	waitState(t, restarted, "a", "ready")
}

func TestRenderStopsOnContextCancel(t *testing.T) {
	f := newFixture(t).only("a")
	tts := newFakeTTS(t)
	tts.release = make(chan struct{})
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	waitState(t, s, "a", "rendering")
	cancel()
	waitState(t, s, "a", "failed")
	if _, ok := s.CueDir("a"); ok {
		t.Fatal("cancelled render published a set")
	}
	if left, _ := filepath.Glob(filepath.Join(f.dir, "var", "cues", "a", "*")); len(left) != 0 {
		t.Fatalf("cancel left %v", left)
	}
}

func TestQueuedJobRendersTheVoiceItWasQueuedWith(t *testing.T) {
	f := newFixture(t).only("a")
	tts := newFakeTTS(t)
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "Deep and slow."}}
		in.Personas["a"] = Persona{VoiceID: "gent"}
	})); err != nil {
		t.Fatal(err)
	}
	keyX := s.key("a")
	// The worker takes the job, then the voice changes before it renders.
	id, j := s.next()
	if id != "a" || j == nil || j.key != keyX {
		t.Fatalf("unexpected job %q %+v", id, j)
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "High and quick."}}
	})); err != nil {
		t.Fatal(err)
	}
	keyY := s.key("a")
	if keyX == keyY {
		t.Fatal("a new description must change the cue key")
	}
	if st := s.Snapshot().Cues["a"]; st.State != "queued" {
		t.Fatalf("the current key must be queued on its own: %+v", st)
	}
	s.render(context.Background(), id, j)
	dir := filepath.Join(f.dir, "var", "cues", "a", keyX)
	for _, c := range []string{"waiting-1", "waiting-2", "lookup-1"} {
		wav, err := os.ReadFile(filepath.Join(dir, "cues", c+".wav"))
		if err != nil || len(wav) != 44+4800 || wav[44] != 'D' || wav[len(wav)-1] != 'D' {
			t.Fatalf("set at the old key holds audio from the wrong voice (%s): %v", c, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, "var", "cues", "a", keyY)); !os.IsNotExist(err) {
		t.Fatal("the new key must not be published by the old job")
	}
	if _, ok := s.CueDir("a"); ok {
		t.Fatal("the old set must not be served for the new voice")
	}
	// The current key is still queued, and it renders with its own voice.
	id, j = s.next()
	if id != "a" || j.key != keyY {
		t.Fatalf("current key not queued: %q %+v", id, j)
	}
	s.render(context.Background(), id, j)
	wav, err := os.ReadFile(filepath.Join(f.dir, "var", "cues", "a", keyY, "cues", "waiting-1.wav"))
	if err != nil || wav[44] != 'H' {
		t.Fatalf("new set: %v", err)
	}
	if _, err = os.Stat(filepath.Join(f.dir, "var", "cues", "a", keyX)); !os.IsNotExist(err) {
		t.Fatal("superseded set was not pruned after the current key rendered")
	}
}

func TestSaveRetriesFailedRenderButWorkerDoesNot(t *testing.T) {
	f := newFixture(t).only("a")
	tts := newFakeTTS(t)
	tts.mu.Lock()
	tts.fail = true
	tts.mu.Unlock()
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, "a", "failed")
	time.Sleep(50 * time.Millisecond)
	failed := tts.count()
	if failed != 1 {
		t.Fatalf("a failed render looped: %d requests", failed)
	}
	tts.mu.Lock()
	tts.fail = false
	tts.mu.Unlock()
	// An unrelated save retries the failed set for the same key.
	if _, err := s.Save(request(s, nil)); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, "a", "ready")
}

func TestForcedRerenderReplacesSetWithNewGeneration(t *testing.T) {
	f := newFixture(t).only("a")
	tts := newFakeTTS(t)
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, "a", "ready")
	dir, _ := s.CueDir("a")
	// The manifest is briefly absent while the old set is moved aside; treat that as "not yet".
	generation := func() string {
		raw, err := os.ReadFile(filepath.Join(dir, "cues.json"))
		if err != nil {
			return ""
		}
		var m struct {
			Ref string `json:"reference_sha256"`
		}
		if json.Unmarshal(raw, &m) != nil {
			return ""
		}
		return m.Ref
	}
	before := generation()
	if before == "" {
		t.Fatal("no manifest for the ready set")
	}
	revision := s.Snapshot().Revision
	forced, err := s.Force("a")
	if err != nil {
		t.Fatal(err)
	}
	if forced.Revision != revision || s.Snapshot().Revision != revision {
		t.Fatal("force must not change the settings revision")
	}
	deadline := time.Now().Add(5 * time.Second)
	for g := generation(); (g == "" || g == before) && time.Now().Before(deadline); g = generation() {
		time.Sleep(5 * time.Millisecond)
	}
	if g := generation(); g == "" || g == before || !strings.HasPrefix(g, filepath.Base(dir)+"-") {
		t.Fatalf("forced render kept the cache-buster %q (now %q)", before, g)
	}
	waitState(t, s, "a", "ready")
	if left, _ := filepath.Glob(filepath.Join(f.dir, "var", "cues", "a", ".*")); len(left) != 0 {
		t.Fatalf("aside or temp dirs left behind: %v", left)
	}
}

func TestStartupRemovesAsideDirs(t *testing.T) {
	f := newFixture(t)
	aside := filepath.Join(f.dir, "var", "cues", "a", ".old-abc-1")
	if err := os.MkdirAll(filepath.Join(aside, "cues"), 0700); err != nil {
		t.Fatal(err)
	}
	f.open(t, nil)
	if _, err := os.Stat(aside); !os.IsNotExist(err) {
		t.Fatal("aside dir survived startup")
	}
}

func TestOpenDropsUnknownPersonasAndVoices(t *testing.T) {
	f := newFixture(t)
	saved := `{"revision":4,"voices":[],"personas":{` +
		`"a":{"voice_id":"gone","direction":"keep me","events":false},` +
		`"removed-agent":{"voice_id":"preset-ryan","direction":"","events":true}}}`
	path := filepath.Join(f.dir, "var", "voices.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	s := f.open(t, nil)
	snap := s.Snapshot()
	if snap.Revision != 4 || len(snap.Personas) != 2 {
		t.Fatalf("%+v", snap)
	}
	if p := snap.Personas["a"]; p.VoiceID != "preset-ryan" || p.Direction != "keep me" {
		t.Fatalf("missing voice did not fall back to the default: %+v", p)
	}
	if p := snap.Personas["b"]; p.VoiceID != "preset-aiden" {
		t.Fatalf("%+v", p)
	}
}

func TestStorageFailureIsDistinguishedFromValidation(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, nil)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "nope"} })); err == nil || errors.Is(err, ErrStorage) {
		t.Fatalf("validation error classified as storage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "var"), []byte("file in the way"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(request(s, nil)); !errors.Is(err, ErrStorage) {
		t.Fatalf("write failure not classified as storage: %v", err)
	}
}

func TestTrimErrorHidesURLsAndPaths(t *testing.T) {
	_, urlErr := http.Get("http://127.0.0.1:1/secret-path")
	if urlErr == nil {
		t.Fatal("expected a connection error")
	}
	for name, err := range map[string]error{
		"url":  urlErr,
		"path": &os.PathError{Op: "open", Path: "/app/var/voice-cues/x", Err: os.ErrPermission},
	} {
		msg := trimError(err)
		if strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "/app") || strings.Contains(msg, "secret-path") {
			t.Fatalf("%s leaked: %q", name, msg)
		}
	}
	if got := trimError(errors.New("speech service HTTP 500")); got != "speech service HTTP 500" {
		t.Fatalf("%q", got)
	}
}

func TestRetiredReferenceVoicesMoveToTheAgentDefaultAndArePersisted(t *testing.T) {
	f := newFixture(t)
	saved := `{"revision":7,"voices":[{"id":"mine","name":"Mine","description":"Deep."}],"personas":{` +
		`"a":{"voice_id":"ref-a","direction":"Preserve the reference woman's voice.","events":true},` +
		`"b":{"voice_id":"mine","direction":"Keep this.","events":true}}}`
	path := filepath.Join(f.dir, "var", "voices.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	snap := f.open(t, nil).Snapshot()
	if snap.Revision != 8 {
		t.Fatalf("migration must bump the revision: %d", snap.Revision)
	}
	if p := snap.Personas["a"]; p != (Persona{VoiceID: "preset-ryan", Direction: "Warm a"}) {
		t.Fatalf("persona a: %+v", p)
	}
	if p := snap.Personas["b"]; p.VoiceID != "mine" || p.Direction != "Keep this." {
		t.Fatalf("a user-chosen voice must stay: %+v", p)
	}
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), "ref-a") || !strings.Contains(string(raw), `"revision": 8`) {
		t.Fatalf("migration not stored: %v %s", err, raw)
	}
	if again := f.open(t, nil).Snapshot(); again.Revision != 8 {
		t.Fatalf("migration repeated: %d", again.Revision)
	}
}
