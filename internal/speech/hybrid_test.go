package speech

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeASR reads the whole body, then answers with a done event. delay postpones
// the answer; fail makes it return HTTP 500 immediately.
type fakeASR struct {
	*httptest.Server
	mu    sync.Mutex
	body  []byte
	model string
}

func newFakeASR(t *testing.T, partial, final string, delay time.Duration, fail bool) *fakeASR {
	f := &fakeASR{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.model = r.URL.Query().Get("model")
		f.mu.Unlock()
		if fail {
			http.Error(w, "boom", 500)
			return
		}
		http.NewResponseController(w).EnableFullDuplex()
		w.Header().Set("Content-Type", "text/event-stream")
		if partial != "" {
			fmt.Fprintf(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":%q}\n\n", partial)
			w.(http.Flusher).Flush()
		}
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.body = b
		f.mu.Unlock()
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		fmt.Fprintf(w, "data: {\"type\":\"transcript.text.done\",\"text\":%q}\n\ndata: [DONE]\n\n", final)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeASR) received() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body
}

type got struct {
	partials []string
	final    string
}

func runHybrid(t *testing.T, live, fin *fakeASR, audio []byte, timeout time.Duration) (got, []string, error) {
	c := &Client{STTURL: live.URL, FinalURL: fin.URL, FinalModel: "qwen3-asr-1.7b-utt", FinalTimeout: timeout}
	var events []string
	ctx := WithTrace(context.Background(), func(ev string, d map[string]any) {
		if r, ok := d["reason"].(string); ok {
			ev += ":" + r
		}
		events = append(events, ev)
	})
	var g got
	err := c.Transcribe(ctx, bytes.NewReader(audio), func(text string, final bool) error {
		if final {
			g.final = text
		} else {
			g.partials = append(g.partials, text)
		}
		return nil
	})
	return g, events, err
}

func TestHybridUsesFinalRecognizer(t *testing.T) {
	live := newFakeASR(t, "five dear hams", "five dear hams", 0, false)
	fin := newFakeASR(t, "", "five dirhams", 0, false)
	audio := make([]byte, 20000)
	g, events, err := runHybrid(t, live, fin, audio, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.partials) != 1 || g.partials[0] != "five dear hams" || g.final != "five dirhams" {
		t.Fatalf("%+v", g)
	}
	if len(live.received()) != len(audio) || len(fin.received()) != len(audio) {
		t.Fatalf("audio forwarded live=%d final=%d want %d", len(live.received()), len(fin.received()), len(audio))
	}
	if fin.model != "qwen3-asr-1.7b-utt" || live.model != "nemotron-3.5-asr" {
		t.Fatalf("models %q %q", fin.model, live.model)
	}
	if strings.Join(events, ",") != "stt.final.request,stt.final.done" {
		t.Fatalf("events %v", events)
	}
}

func TestHybridFallsBack(t *testing.T) {
	for name, tc := range map[string]struct {
		fin    *fakeASR
		reason string
	}{
		"timeout": {newFakeASR(t, "", "late", 2*time.Second, false), "timeout"},
		"error":   {newFakeASR(t, "", "", 0, true), "error"},
		"empty":   {newFakeASR(t, "", "  ", 0, false), "empty"},
	} {
		t.Run(name, func(t *testing.T) {
			live := newFakeASR(t, "hello", "hello there", 0, false)
			g, events, err := runHybrid(t, live, tc.fin, make([]byte, 2000), 150*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if g.final != "hello there" || len(g.partials) != 1 {
				t.Fatalf("%+v", g)
			}
			if events[len(events)-1] != "stt.final.fallback:"+tc.reason {
				t.Fatalf("events %v", events)
			}
		})
	}
}

func TestHybridLongUtteranceKeepsLiveFinal(t *testing.T) {
	live := newFakeASR(t, "", "live text", 0, false)
	fin := newFakeASR(t, "", "final text", 0, false)
	g, events, err := runHybrid(t, live, fin, make([]byte, finalWindowBytes+2), time.Second)
	if err != nil || g.final != "live text" {
		t.Fatalf("%v %+v", err, g)
	}
	if events[len(events)-1] != "stt.final.fallback:utterance_too_long" {
		t.Fatalf("events %v", events)
	}
}

func TestHybridLiveFailureStillFails(t *testing.T) {
	live := newFakeASR(t, "", "", 0, true)
	fin := newFakeASR(t, "", "final text", 0, false)
	g, _, err := runHybrid(t, live, fin, make([]byte, 2000), time.Second)
	if err != nil {
		t.Fatalf("final recognizer should rescue a failed live one: %v", err)
	}
	if g.final != "final text" {
		t.Fatalf("%+v", g)
	}
}

func TestSingleRecognizerWithoutFinalURL(t *testing.T) {
	c := &Client{STTURL: "x"}
	if c.HybridSTT() || c.STTLabel() != "Nemotron 3.5 ASR" {
		t.Fatal("hybrid enabled without config")
	}
	c.FinalURL, c.FinalModel = "y", "m"
	if !c.HybridSTT() || !strings.Contains(c.STTLabel(), "Qwen3-ASR") {
		t.Fatal("hybrid not enabled")
	}
}
