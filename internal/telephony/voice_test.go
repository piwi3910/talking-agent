package telephony

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/voices"
)

type phoneBody struct {
	Model   string            `json:"model"`
	Input   string            `json:"input"`
	Voice   string            `json:"voice"`
	Options map[string]string `json:"options"`
	Ref     map[string]string `json:"voice_ref"`
}

func TestSayPhoneUsesStoreResolvedVoiceAndDownsamplesTo8kHz(t *testing.T) {
	var mu sync.Mutex
	var bodies []phoneBody
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b phoneBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		// 960 bytes are 480 samples, 20 ms of 24 kHz audio.
		w.Write(make([]byte, 960))
	}))
	defer tts.Close()
	agents := map[string]*config.Agent{"a": {ID: "a", Name: "Alice", Dir: t.TempDir()}}
	agents["a"].Voice.Default = "preset-aiden"
	client := &speech.Client{STTURL: tts.URL, TTSURL: tts.URL}
	dir := t.TempDir()
	store, err := voices.Open(filepath.Join(dir, "v.json"), filepath.Join(dir, "cues"), agents, client)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Speech: client, Voices: store}
	sess := session.NewStore().Create(agents["a"], "u")
	var wire bytes.Buffer
	say := func(text string) {
		t.Helper()
		wire.Reset()
		if err := s.sayPhone(context.Background(), &wire, 0, sess, text, "turn"); err != nil {
			t.Fatal(err)
		}
	}
	say("Hello (laugh) there")
	if b := bodies[0]; b.Model != speech.ModelQwen3Custom || b.Voice != "Aiden" || b.Input != "Hello there" {
		t.Fatalf("agent default voice, stage direction stripped: %+v", b)
	}
	// 20 ms of 24 kHz PCM become exactly one 160-byte G.711 frame at 8 kHz.
	if wire.Len() != 160 {
		t.Fatalf("sent %d bytes, want one 160 byte frame", wire.Len())
	}
	snap := store.Snapshot()
	if _, err = store.Save(voices.SaveRequest{Revision: snap.Revision, Voices: []voices.Custom{{ID: "gent", Name: "Gent", Description: "Deep and slow."}}, Personas: map[string]voices.Persona{"a": {VoiceID: "gent", Direction: "Brisk."}}}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1].Model != speech.ModelQwen3Design {
		t.Fatalf("saving a designed voice renders one sample: %+v", bodies)
	}
	// The very next phrase uses the saved voice; no restart or new call needed.
	say("Hello (laugh) there")
	if b := bodies[2]; b.Model != speech.ModelQwen3Base || b.Ref["data"] == "" || b.Voice != "" || b.Input != "Hello there" {
		t.Fatalf("designed voice is cloned from its sample: %+v", b)
	}
	say("(sigh)")
	if len(bodies) != 3 {
		t.Fatal("a phrase that was only a stripped stage direction reached the TTS")
	}
}
