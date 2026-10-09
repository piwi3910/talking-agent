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

	"enterprise-ai-demo/internal/speech"
)

type qwenCall struct {
	Model    string
	Input    string
	Voice    string
	Options  map[string]string
	RefData  string
	RefText  string
	Streamed bool
}

type fakeQwen struct {
	*httptest.Server
	mu      sync.Mutex
	calls   []qwenCall
	failing bool
	gate    chan struct{}
}

func newFakeQwen(t *testing.T) *fakeQwen {
	t.Helper()
	f := &fakeQwen{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Model         string
			Input         string
			Voice         string
			Stream        bool
			Options       map[string]string
			VoiceRef      struct{ Data string } `json:"voice_ref"`
			ReferenceText string                `json:"reference_text"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.calls = append(f.calls, qwenCall{Model: b.Model, Input: b.Input, Voice: b.Voice, Options: b.Options, RefData: b.VoiceRef.Data, RefText: b.ReferenceText, Streamed: b.Stream})
		failing, gate := f.failing, f.gate
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
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
	t.Cleanup(f.Close)
	return f
}
func (f *fakeQwen) snapshot() []qwenCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]qwenCall{}, f.calls...)
}
func (f *fakeQwen) count(model string) int {
	n := 0
	for _, c := range f.snapshot() {
		if c.Model == model {
			n++
		}
	}
	return n
}
func (f *fakeQwen) client() *speech.Client {
	return &speech.Client{STTURL: f.URL, TTSURL: f.URL, Provider: speech.ProviderQwen3}
}

func TestPresetsAreListedOnlyUnderQwen3AndResolveToTheSpeaker(t *testing.T) {
	f := newFixture(t)
	breeze := f.open(t, &speech.Client{STTURL: "http://x", TTSURL: "http://x"})
	for _, v := range breeze.Snapshot().Voices {
		if v.Kind == KindPreset || strings.HasPrefix(v.ID, "preset-") {
			t.Fatalf("preset listed under breeze: %+v", v)
		}
	}
	if _, err := breeze.Save(request(breeze, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "preset-ryan"} })); err == nil {
		t.Fatal("preset assignment accepted under breeze")
	}
	if snap := breeze.Snapshot(); snap.Provider != "breeze" || !snap.Capabilities.VocalEvents {
		t.Fatalf("%+v", snap)
	}

	tts := newFakeQwen(t)
	s := f.open(t, tts.client())
	snap := s.Snapshot()
	if snap.Provider != "qwen3" || snap.Capabilities.VocalEvents {
		t.Fatalf("%q %+v", snap.Provider, snap.Capabilities)
	}
	names := map[string]string{}
	for _, v := range snap.Voices {
		if v.Kind == KindPreset {
			if !v.Builtin {
				t.Fatalf("preset must be builtin: %+v", v)
			}
			names[v.ID] = v.Name
		}
	}
	if len(names) != 9 || names["preset-ryan"] != "Ryan (preset, English)" || names["preset-vivian"] != "Vivian (preset, multilingual)" || names["preset-uncle-fu"] != "Uncle Fu (preset, multilingual)" {
		t.Fatalf("%v", names)
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Personas["a"] = Persona{VoiceID: "preset-uncle-fu", Direction: "Speak softly.", Events: true}
		in.Personas["b"] = Persona{VoiceID: "preset-aiden"}
	})); err != nil {
		t.Fatal(err)
	}
	v, events := s.Resolve("a")
	if v.Model != speech.ModelQwen3Custom || v.Speaker != "Uncle_Fu" || v.Instruct != "Speak softly." || v.Reference != nil || events {
		t.Fatalf("%+v events=%v", v, events)
	}
	if v, _ = s.Resolve("b"); v.Speaker != "Aiden" || v.Instruct != "" {
		t.Fatalf("%+v", v)
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "preset-nobody"} })); err == nil {
		t.Fatal("unknown preset accepted")
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Voices = []Custom{{ID: "preset-mine", Name: "n", Description: "d"}} })); err == nil {
		t.Fatal("custom voice shadowing the preset namespace accepted")
	}
}

func TestQwen3BuiltinAndCloneUseBaseAndIgnoreDirection(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, newFakeQwen(t).client())
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Personas["a"] = Persona{VoiceID: "ref-a", Direction: "Be brisk.", Events: true}
	})); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Resolve("a")
	if v.Model != speech.ModelQwen3Base || v.Reference == nil || v.Reference.AudioBase64 != "audio-a" || v.Instruct != "" || v.Instruction != "" {
		t.Fatalf("%+v", v)
	}
}

func TestSavingADesignVoiceRendersOneSampleThenEveryPhraseUsesBase(t *testing.T) {
	f := newFixture(t)
	tts := newFakeQwen(t)
	s := f.open(t, tts.client())
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "  Deep and slow.  "}}
		in.Personas["a"] = Persona{VoiceID: "gent", Direction: "Be brisk.", Events: true}
	})); err != nil {
		t.Fatal(err)
	}
	calls := tts.snapshot()
	if len(calls) != 1 || calls[0].Model != speech.ModelQwen3Design || calls[0].Options["instruct"] != "Deep and slow." || calls[0].Options["seed"] != "42" || calls[0].Input != designSampleText || calls[0].Streamed {
		t.Fatalf("%+v", calls)
	}
	for i := 0; i < 3; i++ {
		v, events := s.Resolve("a")
		if v.Model != speech.ModelQwen3Base || v.Reference == nil || v.Reference.Text != designSampleText || v.Reference.AudioBase64 == "" || events {
			t.Fatalf("%+v events=%v", v, events)
		}
	}
	if tts.count(speech.ModelQwen3Design) != 1 {
		t.Fatal("phrases triggered a design render")
	}
	dir := filepath.Join(f.dir, "var", "voices", "gent")
	for _, name := range []string{"reference.wav", "reference.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("sample not stored: %v", err)
		}
	}
	// Saving again without touching the description keeps the sample.
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["b"] = Persona{VoiceID: "gent"} })); err != nil {
		t.Fatal(err)
	}
	if tts.count(speech.ModelQwen3Design) != 1 {
		t.Fatal("unchanged description re-rendered")
	}
	// Changing the description renders a new sample.
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Voices[0].Description = "High and quick." })); err != nil {
		t.Fatal(err)
	}
	if tts.count(speech.ModelQwen3Design) != 2 {
		t.Fatal("changed description was not re-rendered")
	}

	// A restart keeps the sample: no render, same reference.
	before, _ := s.Resolve("a")
	restarted := f.open(t, tts.client())
	after, _ := restarted.Resolve("a")
	if after.Reference == nil || after.Reference.AudioBase64 != before.Reference.AudioBase64 || tts.count(speech.ModelQwen3Design) != 2 {
		t.Fatalf("sample lost across restart: %+v", after)
	}
	// A sample for a stale description is treated as missing.
	if err := os.WriteFile(filepath.Join(dir, designDescFile), []byte("something else"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := f.open(t, tts.client())
	if got := stale.Snapshot(); !voiceUnavailable(got, "gent") {
		t.Fatalf("stale sample accepted: %+v", got.Voices)
	}
}

func voiceUnavailable(s Snapshot, id string) bool {
	for _, v := range s.Voices {
		if v.ID == id {
			return v.Unavailable
		}
	}
	return false
}

func TestFailedDesignRenderPersistsNothing(t *testing.T) {
	f := newFixture(t)
	tts := newFakeQwen(t)
	tts.failing = true
	s := f.open(t, tts.client())
	_, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "Deep and slow."}}
		in.Personas["a"] = Persona{VoiceID: "gent"}
	}))
	if !errors.Is(err, ErrSynthesis) {
		t.Fatalf("err = %v", err)
	}
	snap := s.Snapshot()
	for _, v := range snap.Voices {
		if !v.Builtin {
			t.Fatalf("failed save kept a voice: %+v", v)
		}
	}
	if snap.Revision != 0 || snap.Personas["a"].VoiceID != "ref-a" {
		t.Fatalf("failed save changed state: %+v", snap)
	}
	if _, statErr := os.Stat(filepath.Join(f.dir, "var", "voices.json")); !os.IsNotExist(statErr) {
		t.Fatalf("settings file written: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(f.dir, "var", "voices", "gent")); !os.IsNotExist(statErr) {
		t.Fatalf("sample stored: %v", statErr)
	}
}

func TestMissingDesignSampleFallsBackThenIsRegeneratedAtStart(t *testing.T) {
	f := newFixture(t)
	tts := newFakeQwen(t)
	s := f.open(t, tts.client())
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "Deep and slow."}}
		in.Personas["a"] = Persona{VoiceID: "gent"}
	})); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.dir, "var", "voices", "gent")); err != nil {
		t.Fatal(err)
	}
	restarted := f.open(t, tts.client())
	// Until the sample exists the persona uses its builtin voice and the app keeps working.
	v, _ := restarted.Resolve("a")
	if v.Reference == nil || v.Reference.AudioBase64 != "audio-a" {
		t.Fatalf("no fallback: %+v", v)
	}
	if !voiceUnavailable(restarted.Snapshot(), "gent") {
		t.Fatal("voice not marked unavailable")
	}
	if _, err := restarted.Preview("gent", "", ""); err == nil {
		t.Fatal("previewed an unavailable voice")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restarted.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for voiceUnavailable(restarted.Snapshot(), "gent") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if v, _ = restarted.Resolve("a"); v.Reference == nil || v.Reference.Text != designSampleText {
		t.Fatalf("not regenerated: %+v", v)
	}
	if tts.count(speech.ModelQwen3Design) != 2 {
		t.Fatalf("design renders: %d", tts.count(speech.ModelQwen3Design))
	}
	// The cue set that waited for the sample renders now.
	waitState(t, restarted, "a", "ready")
}

func TestQwen3PreviewSources(t *testing.T) {
	f := newFixture(t)
	tts := newFakeQwen(t)
	s := f.open(t, tts.client())
	v, err := s.Preview("", "A warm old man.", "ignored")
	if err != nil || v.Model != speech.ModelQwen3Design || v.Instruct != "A warm old man." || v.Reference != nil {
		t.Fatalf("%+v %v", v, err)
	}
	if v, err = s.Preview("preset-ryan", "", "Cheerful."); err != nil || v.Model != speech.ModelQwen3Custom || v.Speaker != "Ryan" || v.Instruct != "Cheerful." {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err = s.Save(request(s, func(in *SaveRequest) { in.Voices = []Custom{{ID: "gent", Name: "Gent", Description: "Deep."}} })); err != nil {
		t.Fatal(err)
	}
	if v, err = s.Preview("gent", "", ""); err != nil || v.Model != speech.ModelQwen3Base || v.Reference == nil || v.Reference.Text != designSampleText {
		t.Fatalf("%+v %v", v, err)
	}
	if v, err = s.Preview("ref-a", "", ""); err != nil || v.Model != speech.ModelQwen3Base || v.Reference.AudioBase64 != "audio-a" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestQwen3StripsEventsAndSkipsThePromptAddendum(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, newFakeQwen(t).client())
	if p := s.Snapshot().Personas["a"]; !p.Events {
		t.Fatalf("events should default to true: %+v", p)
	}
	if _, events := s.Resolve("a"); events {
		t.Fatal("events allowed under qwen3 although the persona has them on")
	}
	if got := s.PromptAddendum("a"); got != "" {
		t.Fatalf("addendum under qwen3: %q", got)
	}
	// What callers do with the flag: every allowlisted event is removed.
	_, events := s.Resolve("a")
	if got := speech.VocalEvents("Sure (laugh) thing (sigh).", events); got != "Sure thing ." {
		t.Fatalf("%q", got)
	}
}

func TestCueKeyDiffersBetweenProviders(t *testing.T) {
	f := newFixture(t)
	breeze := f.open(t, &speech.Client{STTURL: "http://x", TTSURL: "http://x"})
	qwen := f.open(t, &speech.Client{STTURL: "http://x", TTSURL: "http://x", Provider: speech.ProviderQwen3})
	ref := &speech.Reference{AudioBase64: "x", Text: "t"}
	v := speech.Voice{Reference: ref, Model: speech.ModelQwen3Base}
	if breeze.cueKey(v) == qwen.cueKey(v) {
		t.Fatal("provider is not part of the cue key")
	}
	seen := map[string]string{qwen.cueKey(v): "base"}
	for name, other := range map[string]speech.Voice{
		"model":     {Reference: ref, Model: speech.ModelQwen3Custom},
		"speaker":   {Model: speech.ModelQwen3Custom, Speaker: "Ryan"},
		"speaker 2": {Model: speech.ModelQwen3Custom, Speaker: "Aiden"},
		"instruct":  {Model: speech.ModelQwen3Custom, Speaker: "Ryan", Instruct: "Calm."},
		"reference": {Reference: &speech.Reference{AudioBase64: "y", Text: "t"}, Model: speech.ModelQwen3Base},
		"text":      {Reference: &speech.Reference{AudioBase64: "x", Text: "u"}, Model: speech.ModelQwen3Base},
	} {
		k := qwen.cueKey(other)
		if prev, dup := seen[k]; dup {
			t.Fatalf("%s collides with %s", name, prev)
		}
		seen[k] = name
	}
	// Breeze keys are the ones sets were rendered under before this change.
	if breeze.cueKey(v) != cueKey(v) {
		t.Fatal("breeze cue key changed")
	}
}

func TestSwitchingProviderRerendersNonOriginalCues(t *testing.T) {
	f := newFixture(t)
	breezeTTS := newFakeTTS(t)
	s := f.open(t, &speech.Client{STTURL: breezeTTS.URL, TTSURL: breezeTTS.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "x", Events: true} })); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, "a", "ready")
	breezeKey := s.key("a")
	// Same data, now served by qwen3: the old set no longer matches.
	qwenTTS := newFakeQwen(t)
	q := f.open(t, qwenTTS.client())
	if q.key("a") == breezeKey || q.ready("a", q.key("a")) {
		t.Fatal("breeze cue set reused under qwen3")
	}
	if st := q.Snapshot().Cues["a"]; st.State != "queued" {
		t.Fatalf("%+v", st)
	}
	// Baked originals are untouched.
	if st := q.Snapshot().Cues["b"]; st.State != "original" {
		t.Fatalf("%+v", st)
	}
}

func TestCueRendererYieldsToLiveSpeech(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var inputs []string
	liveGate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Input string }
		json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		inputs = append(inputs, b.Input)
		mu.Unlock()
		if b.Input == "live phrase" {
			select {
			case <-liveGate:
			case <-r.Context().Done():
				return
			}
		}
		w.Write(make([]byte, 4800))
	}))
	defer server.Close()
	cues := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, in := range inputs {
			if in != "live phrase" {
				n++
			}
		}
		return n
	}
	client := &speech.Client{STTURL: server.URL, TTSURL: server.URL}
	s := f.open(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A live synthesis that stays open: a caller is being spoken to.
	liveDone := make(chan error, 1)
	go func() {
		liveDone <- client.Synthesize(ctx, "live phrase", speech.Voice{}, func([]byte) error { return nil })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for client.Live() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s.Start(ctx)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "ref-b", Direction: "x", Events: true} })); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, "a", "rendering")
	time.Sleep(700 * time.Millisecond)
	if n := cues(); n != 0 {
		t.Fatalf("%d cues were synthesised while a caller was being spoken to", n)
	}
	close(liveGate)
	if err := <-liveDone; err != nil {
		t.Fatal(err)
	}
	waitState(t, s, "a", "ready")
	if n := cues(); n != 3 {
		t.Fatalf("%d cues rendered after the caller finished", n)
	}
}
