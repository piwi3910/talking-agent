package speech

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type qwenRef struct{ Data string }

type qwenRequest struct {
	Model          string            `json:"model"`
	Input          string            `json:"input"`
	Voice          string            `json:"voice"`
	Stream         *bool             `json:"stream"`
	ResponseFormat string            `json:"response_format"`
	VoiceRef       *qwenRef          `json:"voice_ref"`
	ReferenceText  string            `json:"reference_text"`
	Options        map[string]string `json:"options"`
	Raw            map[string]any    `json:"-"`
}

func qwenServer(t *testing.T, got chan<- qwenRequest) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		var body qwenRequest
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &body); err != nil {
			t.Error(err)
		}
		_ = json.Unmarshal(data, &raw)
		body.Raw = raw
		if r.URL.Path != "/v1/audio/speech" {
			t.Errorf("path %s", r.URL.Path)
		}
		got <- body
		w.Write([]byte{1, 0, 2, 0})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestQwen3RequestBodies(t *testing.T) {
	got := make(chan qwenRequest, 1)
	server := qwenServer(t, got)
	c := &Client{TTSURL: server.URL, Provider: ProviderQwen3}
	ref := &Reference{AudioBase64: "QUJD", Text: "reference words"}
	for name, tc := range map[string]struct {
		voice Voice
		check func(t *testing.T, b qwenRequest)
	}{
		"base": {Voice{Model: ModelQwen3Base, Reference: ref}, func(t *testing.T, b qwenRequest) {
			if b.Model != "qwen3-tts-base" || b.VoiceRef == nil || b.VoiceRef.Data != "QUJD" || b.ReferenceText != "reference words" || b.Voice != "" {
				t.Fatalf("%+v", b)
			}
			if _, ok := b.Options["instruct"]; ok {
				t.Fatal("base has no instruction support")
			}
		}},
		"base from a bare reference": {Voice{Reference: ref}, func(t *testing.T, b qwenRequest) {
			if b.Model != "qwen3-tts-base" || b.VoiceRef == nil {
				t.Fatalf("%+v", b)
			}
		}},
		"custom": {Voice{Model: ModelQwen3Custom, Speaker: "Aiden", Instruct: "Warm and calm."}, func(t *testing.T, b qwenRequest) {
			if b.Model != "qwen3-tts-custom" || b.Voice != "Aiden" || b.Options["instruct"] != "Warm and calm." || b.VoiceRef != nil {
				t.Fatalf("%+v", b)
			}
		}},
		"custom without a speaker still names one": {Voice{}, func(t *testing.T, b qwenRequest) {
			if b.Model != "qwen3-tts-custom" || b.Voice != DefaultQwen3Speaker {
				t.Fatalf("%+v", b)
			}
			if _, ok := b.Options["instruct"]; ok {
				t.Fatal("empty instruct was sent")
			}
		}},
		"design": {Voice{Model: ModelQwen3Design, Instruct: "A deep, slow British man."}, func(t *testing.T, b qwenRequest) {
			if b.Model != "qwen3-tts-design" || b.Options["instruct"] != "A deep, slow British man." || b.VoiceRef != nil || b.Voice != "" {
				t.Fatalf("%+v", b)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := c.Synthesize(context.Background(), "Hello there.", tc.voice, func([]byte) error { return nil }); err != nil {
				t.Fatal(err)
			}
			b := <-got
			if b.Input != "Hello there." || b.Stream == nil || *b.Stream || b.ResponseFormat != "pcm" || b.Options["seed"] != "42" {
				t.Fatalf("%+v", b)
			}
			for _, key := range []string{"instruction", "guidance_scale", "stream_format"} {
				if _, ok := b.Raw[key]; ok {
					t.Fatalf("request has a %s key", key)
				}
				if _, ok := b.Options[key]; ok {
					t.Fatalf("options has a %s key", key)
				}
			}
			tc.check(t, b)
		})
	}
}

func TestLiveCounterIgnoresBackgroundSynthesis(t *testing.T) {
	var mu sync.Mutex
	gate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		g := gate
		mu.Unlock()
		select {
		case <-g:
		case <-r.Context().Done():
			return
		}
		w.Write([]byte{1, 0})
	}))
	defer server.Close()
	c := &Client{TTSURL: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() {
		done <- c.SynthesizeBackground(ctx, "bg", Voice{}, func([]byte) error { return nil })
	}()
	time.Sleep(50 * time.Millisecond)
	if c.Live() != 0 {
		t.Fatalf("background synthesis counted as live: %d", c.Live())
	}
	go func() { done <- c.Synthesize(ctx, "live", Voice{}, func([]byte) error { return nil }) }()
	deadline := time.Now().Add(time.Second)
	for c.Live() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Live() != 1 {
		t.Fatalf("live = %d", c.Live())
	}
	waited := make(chan error, 1)
	go func() { waited <- c.WaitIdle(ctx) }()
	select {
	case <-waited:
		t.Fatal("WaitIdle returned while live speech was running")
	case <-time.After(300 * time.Millisecond):
	}
	close(gate)
	if err := <-waited; err != nil {
		t.Fatal(err)
	}
	<-done
	<-done
	if c.Live() != 0 {
		t.Fatalf("live = %d after completion", c.Live())
	}
}
