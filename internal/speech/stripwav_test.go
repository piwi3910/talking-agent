package speech

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripWAVHeader(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0}
	if got := stripWAVHeader(WAV(pcm)); !bytes.Equal(got, pcm) {
		t.Fatalf("header not removed: %v", got)
	}
	if got := stripWAVHeader(pcm); !bytes.Equal(got, pcm) {
		t.Fatalf("plain PCM changed: %v", got)
	}
	if got := stripWAVHeader(nil); len(got) != 0 {
		t.Fatalf("empty input changed: %v", got)
	}
}

// The audio.cpp render path answers with a WAV file; callers treat what the
// client emits as raw PCM, so its header must not reach them.
func TestSynthesizeDropsWAVHeaderOfRenderedSpeech(t *testing.T) {
	pcm := bytes.Repeat([]byte{7, 0}, 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(WAV(pcm))
	}))
	t.Cleanup(srv.Close)
	c := &Client{TTSURL: srv.URL}
	var got []byte
	err := c.Synthesize(context.Background(), "Hi.", Voice{Model: ModelQwen3Custom, Speaker: "Ryan"}, func(b []byte) error {
		got = append(got, b...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pcm) {
		t.Fatalf("emitted %d bytes starting %v, want the %d PCM bytes", len(got), got[:8], len(pcm))
	}
}
