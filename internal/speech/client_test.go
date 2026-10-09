package speech

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTranscriptionIsDuplex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Error(err)
		}
		b := make([]byte, 2)
		if _, err := io.ReadFull(r.Body, b); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"Hello\"}\n\n")
		w.(http.Flusher).Flush()
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"Hello\"}\n\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	partial := make(chan struct{})
	done := make(chan error, 1)
	c := Client{STTURL: server.URL}
	go func() {
		done <- c.Transcribe(ctx, pr, func(text string, final bool) error {
			if text != "Hello" {
				t.Errorf("text %q", text)
			}
			if !final {
				close(partial)
			}
			return nil
		})
	}()
	pw.Write([]byte{0, 0})
	select {
	case <-partial:
	case <-ctx.Done():
		t.Fatal("partial blocked behind upload completion")
	}
	pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestSynthesisStreamsAndCancels(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte{1, 0})
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := Client{TTSURL: server.URL}
	chunks := 0
	err := c.Synthesize(ctx, "Hello", Voice{Instruction: "Natural"}, func(b []byte) error { chunks++; cancel(); return nil })
	if chunks != 1 || err == nil {
		t.Fatalf("chunks=%d err=%v", chunks, err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream not cancelled")
	}
}
func TestMissingFinalFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"Hi\"}\n\n")
	}))
	defer server.Close()
	c := Client{STTURL: server.URL}
	if err := c.Transcribe(context.Background(), strings.NewReader("pcm"), func(string, bool) error { return nil }); err == nil {
		t.Fatal("accepted truncated transcription")
	}
}

func TestReferenceIsIdenticalAcrossPhrases(t *testing.T) {
	ref, err := LoadReference("../../agents/telecom", "voice/reference.wav", "voice/reference.txt")
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reference struct{ Type, Data string } `json:"voice_ref"`
			Text      string                      `json:"reference_text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Reference.Type != "base64" || body.Reference.Data != ref.AudioBase64 || body.Text != ref.Text {
			t.Error("reference changed or missing")
		}
		requests++
		w.Write([]byte{1, 0})
	}))
	defer server.Close()
	c := Client{TTSURL: server.URL}
	for _, phrase := range []string{"Let me check.", "Here are your options."} {
		if err := c.Synthesize(context.Background(), phrase, Voice{Instruction: "Natural", Reference: ref, Guidance: "4"}, func([]byte) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 2 {
		t.Fatal(requests)
	}
	if _, err := LoadReference("../../agents/telecom", "../hospital/voice/reference.wav", "voice/reference.txt"); err == nil {
		t.Fatal("accepted outside reference")
	}
}
