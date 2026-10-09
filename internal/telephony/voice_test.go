package telephony

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/voices"
)

func TestSayPhoneUsesStoreResolvedVoiceAndEventPolicy(t *testing.T) {
	var bodies []struct {
		Input   string            `json:"input"`
		Options map[string]string `json:"options"`
		Ref     map[string]string `json:"voice_ref"`
	}
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Input   string            `json:"input"`
			Options map[string]string `json:"options"`
			Ref     map[string]string `json:"voice_ref"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, b)
		w.Write(make([]byte, 960))
	}))
	defer tts.Close()
	agents := map[string]*config.Agent{"a": {ID: "a", Name: "Alice", Dir: t.TempDir()}}
	client := &speech.Client{STTURL: tts.URL, TTSURL: tts.URL}
	dir := t.TempDir()
	store, err := voices.Open(filepath.Join(dir, "v.json"), filepath.Join(dir, "cues"), agents, map[string]*speech.Reference{"a": {AudioBase64: "ref-audio", Text: "ref text"}}, client)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Speech: client, Voices: store}
	sess := session.NewStore().Create(agents["a"], "u")
	say := func(text string) {
		t.Helper()
		if err := s.sayPhone(context.Background(), io.Discard, 0, sess, text, "turn"); err != nil {
			t.Fatal(err)
		}
	}
	say("Hello (laugh) there")
	if b := bodies[0]; b.Ref["data"] != "ref-audio" || b.Options["guidance_scale"] != "1" || b.Input != "Hello (laugh) there" {
		t.Fatalf("original voice with events: %+v", b)
	}
	snap := store.Snapshot()
	if _, err = store.Save(voices.SaveRequest{Revision: snap.Revision, Voices: []voices.Custom{{ID: "gent", Name: "Gent", Description: "Deep and slow."}}, Personas: map[string]voices.Persona{"a": {VoiceID: "gent", Direction: "Brisk.", Events: false}}}); err != nil {
		t.Fatal(err)
	}
	// The very next phrase uses the saved voice; no restart or new call needed.
	say("Hello (laugh) there")
	if b := bodies[1]; b.Ref != nil || b.Options["instruction"] != "Deep and slow. Brisk." || b.Options["guidance_scale"] != "4" || b.Input != "Hello there" {
		t.Fatalf("custom voice without events: %+v", b)
	}
	say("(sigh)")
	if len(bodies) != 2 {
		t.Fatal("a phrase that was only a stripped event reached the TTS")
	}
}
