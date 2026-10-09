package speech

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOmniStreamsCloneRequestAndFlushesChunks(t *testing.T) {
	release := make(chan struct{})
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v1/audio/speech" {
			t.Errorf("path %s", r.URL.Path)
		}
		bodies <- body
		w.Write([]byte{1, 0, 2, 0})
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Write([]byte{3, 0})
	}))
	t.Cleanup(server.Close)
	c := &Client{TTSURL: server.URL, Provider: ProviderOmni}
	ref := &Reference{AudioBase64: "QUJD", Text: "reference words"}
	first := make(chan int, 1)
	done := make(chan error, 1)
	total := 0
	go func() {
		done <- c.Synthesize(context.Background(), "Hello there.", Voice{Model: ModelQwen3Base, Reference: ref, Instruct: "ignored"}, func(p []byte) error {
			if total == 0 {
				first <- len(p)
			}
			total += len(p)
			return nil
		})
	}()
	select {
	case n := <-first:
		if n != 4 {
			t.Fatalf("first chunk %d bytes", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first chunk was not delivered before the server finished")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if total != 6 {
		t.Fatalf("total %d", total)
	}
	b := <-bodies
	want := map[string]any{
		"model": "qwen3-tts-base", "input": "Hello there.", "task_type": "Base", "language": "English",
		"response_format": "pcm", "stream": true, "stream_format": "audio",
		"ref_audio": "data:audio/wav;base64,QUJD", "ref_text": "reference words", "initial_codec_chunk_frames": float64(8),
	}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("%s = %v, want %v", k, b[k], v)
		}
	}
	for _, k := range []string{"voice", "voice_ref", "options", "instruct"} {
		if _, ok := b[k]; ok {
			t.Errorf("request has a %s key", k)
		}
	}
}

func TestOmniRendersPresetsAndDesignsOnTheRenderWorker(t *testing.T) {
	hits := make(chan string, 2)
	handler := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			hits <- name + ":" + body["model"].(string)
			w.Write([]byte{1, 0})
		})
	}
	stream := httptest.NewServer(handler("stream"))
	render := httptest.NewServer(handler("render"))
	t.Cleanup(stream.Close)
	t.Cleanup(render.Close)
	c := &Client{TTSURL: stream.URL, RenderURL: render.URL, Provider: ProviderOmni}
	for voice, want := range map[string]struct {
		v   Voice
		hit string
	}{
		"preset": {Voice{Model: ModelQwen3Custom, Speaker: "Ryan"}, "render:qwen3-tts-custom"},
		"design": {Voice{Model: ModelQwen3Design, Instruct: "Calm."}, "render:qwen3-tts-design"},
		"clone":  {Voice{Reference: &Reference{AudioBase64: "QUJD", Text: "x"}}, "stream:qwen3-tts-base"},
	} {
		if err := c.SynthesizeBackground(context.Background(), "Hi.", want.v, func([]byte) error { return nil }); err != nil {
			t.Fatalf("%s: %v", voice, err)
		}
		if got := <-hits; got != want.hit {
			t.Fatalf("%s went to %s, want %s", voice, got, want.hit)
		}
	}
	c.RenderURL = ""
	if err := c.SynthesizeBackground(context.Background(), "Hi.", Voice{Model: ModelQwen3Custom}, func([]byte) error { return nil }); err == nil {
		t.Fatal("render without TTS_RENDER_URL succeeded")
	}
	if !c.Qwen3() || c.EventsSupported() || !c.Omni() {
		t.Fatal("omni must behave as a Qwen3 provider without vocal events")
	}
}
