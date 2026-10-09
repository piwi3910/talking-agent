package voices

import (
	"context"
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
	refs   map[string]*speech.Reference
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{dir: root, agents: map[string]*config.Agent{}, refs: map[string]*speech.Reference{}}
	for _, id := range []string{"a", "b"} {
		dir := filepath.Join(root, "agents", id)
		if err := os.MkdirAll(filepath.Join(dir, "voice"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "voice", "cues.json"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		f.agents[id] = &config.Agent{ID: id, Name: "Agent " + id, Dir: dir, Persona: map[string]string{"voice_style": "Warm " + id}}
		f.refs[id] = &speech.Reference{AudioBase64: "audio-" + id, Text: "text " + id}
	}
	return f
}
func (f fixture) open(t *testing.T, tts *speech.Client) *Store {
	t.Helper()
	s, err := Open(filepath.Join(f.dir, "var", "voices.json"), filepath.Join(f.dir, "var", "cues"), f.agents, f.refs, tts)
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
		in.Personas["a"] = Persona{VoiceID: "gent", Direction: " calm ", Events: false}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || saved.Personas["a"].Direction != "calm" {
		t.Fatalf("%+v", saved)
	}
	reopened := f.open(t, nil)
	got := reopened.Snapshot()
	if got.Revision != 1 || got.Personas["a"] != (Persona{VoiceID: "gent", Direction: "calm", Events: false}) || got.Personas["b"].VoiceID != "ref-b" {
		t.Fatalf("round trip lost state: %+v", got)
	}
	if len(got.Voices) != 3 || got.Voices[2].Name != "British gent" || got.Voices[2].Builtin || got.Voices[0].Name != "Agent a (original)" || !got.Voices[0].Builtin {
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
		in.Personas["b"] = Persona{VoiceID: "gent", Events: true}
	})); err != nil {
		t.Fatal(err)
	}
	_, err := s.Save(request(s, func(in *SaveRequest) { in.Voices = nil }))
	if err == nil || !strings.Contains(err.Error(), "assigned to b") {
		t.Fatalf("deletion of assigned voice: %v", err)
	}
	if len(s.Snapshot().Voices) != 3 {
		t.Fatal("rejected deletion changed state")
	}
	// Reassigning in the same request makes the deletion legitimate.
	if _, err = s.Save(request(s, func(in *SaveRequest) {
		in.Voices = nil
		in.Personas["b"] = Persona{VoiceID: "ref-b", Events: true}
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
		"id shadows builtin":      func(in *SaveRequest) { in.Voices = []Custom{{ID: "ref-a", Name: "n", Description: "d"}} },
		"duplicate id": func(in *SaveRequest) {
			in.Voices = []Custom{{ID: "x", Name: "n", Description: "d"}, {ID: "x", Name: "n", Description: "d"}}
		},
		"empty name":           func(in *SaveRequest) { in.Voices = []Custom{{ID: "x", Name: " ", Description: "d"}} },
		"name too long":        func(in *SaveRequest) { in.Voices = []Custom{{ID: "x", Name: long(61), Description: "d"}} },
		"empty description":    func(in *SaveRequest) { in.Voices = []Custom{{ID: "x", Name: "n", Description: ""}} },
		"description too long": func(in *SaveRequest) { in.Voices = []Custom{{ID: "x", Name: "n", Description: long(501)}} },
		"too many voices":      func(in *SaveRequest) { in.Voices = many },
		"direction too long":   func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-a", Direction: long(501)} },
		"unknown voice":        func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "nope"} },
		"missing persona":      func(in *SaveRequest) { delete(in.Personas, "b") },
		"unknown persona":      func(in *SaveRequest) { in.Personas["zzz"] = Persona{VoiceID: "ref-a"} },
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
	_, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "changed"} }))
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
		in.Personas["a"] = Persona{VoiceID: "ref-a", Direction: "", Events: true}
		in.Personas["b"] = Persona{VoiceID: "gent", Direction: "Be brisk.", Events: false}
	})); err != nil {
		t.Fatal(err)
	}
	v, events := s.Resolve("a")
	if v.Reference == nil || v.Reference.AudioBase64 != "audio-a" || v.Instruction != "" || v.Guidance != "1" || !events {
		t.Fatalf("builtin without direction: %+v", v)
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "Slow.", Events: true} })); err != nil {
		t.Fatal(err)
	}
	if v, _ = s.Resolve("a"); v.Reference.AudioBase64 != "audio-b" || v.Instruction != "Slow." || v.Guidance != "4" {
		t.Fatalf("builtin with direction: %+v", v)
	}
	v, events = s.Resolve("b")
	if v.Reference != nil || v.Instruction != "Deep and slow. Be brisk." || v.Guidance != "4" || events {
		t.Fatalf("custom: %+v events=%v", v, events)
	}
}

func TestDefaultsUseReferenceAndVoiceStyle(t *testing.T) {
	s := newFixture(t).open(t, nil)
	if p := s.Snapshot().Personas["a"]; p != (Persona{VoiceID: "ref-a", Direction: "Warm a", Events: true}) {
		t.Fatalf("%+v", p)
	}
}

func TestPromptAddendumOnlyWhenEventsEnabled(t *testing.T) {
	s := newFixture(t).open(t, nil)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["b"] = Persona{VoiceID: "ref-b", Events: false} })); err != nil {
		t.Fatal(err)
	}
	if got := s.PromptAddendum("a"); !strings.Contains(got, "(laugh)") || !strings.Contains(got, "spoken aloud") {
		t.Fatalf("addendum missing when events=true: %q", got)
	}
	if got := s.PromptAddendum("b"); got != "" {
		t.Fatalf("addendum present when events=false: %q", got)
	}
}

func TestCueKeyChangesWhenVoiceChanges(t *testing.T) {
	ref := &speech.Reference{AudioBase64: "x", Text: "t"}
	base := speech.Voice{Instruction: "i", Guidance: "4", Reference: ref}
	seen := map[string]string{cueKey(base): "base"}
	for name, v := range map[string]speech.Voice{
		"instruction": {Instruction: "j", Guidance: "4", Reference: ref},
		"guidance":    {Instruction: "i", Guidance: "1", Reference: ref},
		"reference":   {Instruction: "i", Guidance: "4", Reference: &speech.Reference{AudioBase64: "y", Text: "t"}},
		"no ref":      {Instruction: "i", Guidance: "4"},
	} {
		k := cueKey(v)
		if other, dup := seen[k]; dup || len(k) != 16 {
			t.Fatalf("%s collides with %s", name, other)
		}
		seen[k] = name
	}
	if cueKey(base) != cueKey(speech.Voice{Instruction: "i", Guidance: "4", Reference: &speech.Reference{AudioBase64: "x", Text: "t"}}) {
		t.Fatal("key is not deterministic")
	}
}

// fakeTTS returns 100 ms of PCM per request and records the inputs. Every PCM
// byte is the first byte of the request's instruction, so tests can tell which
// voice produced a clip.
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
			Input   string
			Options map[string]string
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
		if in := b.Options["instruction"]; in != "" {
			for i := range pcm {
				pcm[i] = in[0]
			}
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
	f := newFixture(t)
	tts := newFakeTTS(t)
	tts.release = make(chan struct{})
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	if dir, ok := s.CueDir("a"); !ok || dir != filepath.Join(f.agents["a"].Dir, "voice") {
		t.Fatal("original voice must use baked cues")
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "Deep and slow."}}
		in.Personas["a"] = Persona{VoiceID: "gent", Events: true}
	})); err != nil {
		t.Fatal(err)
	}
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
		if err != nil || len(wav) != 44+4800 || string(wav[:4]) != "RIFF" {
			t.Fatalf("cue %s: %v", c.ID, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(f.dir, "var", "cues", "a", ".tmp-*")); len(left) != 0 {
		t.Fatalf("temp dir left behind: %v", left)
	}
	if tts.count() != 3 {
		t.Fatalf("synthesised %d phrases", tts.count())
	}
	// Persona b still uses its own baked set and was never rendered.
	if st := s.Snapshot().Cues["b"]; st.State != "original" {
		t.Fatalf("%+v", st)
	}
}

func TestExistingSetIsNotRerenderedUnlessForced(t *testing.T) {
	f := newFixture(t)
	tts := newFakeTTS(t)
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "x", Events: true} })); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, "a", "ready")
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Personas["b"] = Persona{VoiceID: "ref-b", Direction: "Warm b", Events: false}
	})); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if tts.count() != 3 {
		t.Fatalf("unforced save re-rendered: %d", tts.count())
	}
	if _, err := s.Force("b"); err == nil {
		t.Fatal("forced an original set")
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
}

func TestFailedRenderKeepsErrorAndNoSet(t *testing.T) {
	f := newFixture(t)
	tts := newFakeTTS(t)
	tts.fail = true
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Events: true} })); err != nil {
		t.Fatal(err)
	}
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
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "x", Events: true} })); err != nil {
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
	f := newFixture(t)
	tts := newFakeTTS(t)
	tts.release = make(chan struct{})
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "x", Events: true} })); err != nil {
		t.Fatal(err)
	}
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
	f := newFixture(t)
	tts := newFakeTTS(t)
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "Deep and slow."}}
		in.Personas["a"] = Persona{VoiceID: "gent", Events: true}
	})); err != nil {
		t.Fatal(err)
	}
	keyX := cueKey(designVoice("Deep and slow.", ""))
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
	keyY := cueKey(designVoice("High and quick.", ""))
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
	f := newFixture(t)
	tts := newFakeTTS(t)
	tts.mu.Lock()
	tts.fail = true
	tts.mu.Unlock()
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "x", Events: true} })); err != nil {
		t.Fatal(err)
	}
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
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["b"] = Persona{VoiceID: "ref-b", Direction: "y", Events: true} })); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, "a", "ready")
}

func TestForcedRerenderReplacesSetWithNewGeneration(t *testing.T) {
	f := newFixture(t)
	tts := newFakeTTS(t)
	s := f.open(t, &speech.Client{STTURL: tts.URL, TTSURL: tts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "x", Events: true} })); err != nil {
		t.Fatal(err)
	}
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
		`"removed-agent":{"voice_id":"ref-a","direction":"","events":true}}}`
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
	if p := snap.Personas["a"]; p.VoiceID != "ref-a" || p.Direction != "keep me" || p.Events {
		t.Fatalf("missing voice did not fall back to the default: %+v", p)
	}
	if p := snap.Personas["b"]; p.VoiceID != "ref-b" {
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
