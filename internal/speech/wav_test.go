package speech

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func ramp(n int) []int16 {
	s := make([]int16, n)
	for i := range s {
		s[i] = int16(i % 1000)
	}
	return s
}

func TestWAVRoundTripAndResample(t *testing.T) {
	in := ramp(24000)
	a, err := ParseWAV(WAVAt(PCMBytes(in), 24000))
	if err != nil || a.Rate != 24000 || len(a.Samples) != len(in) || a.Samples[999] != in[999] {
		t.Fatalf("%v %+v", err, a.Rate)
	}
	if got := a.Resample(16000); len(got) != 16000 {
		t.Fatalf("24 kHz to 16 kHz gave %d samples", len(got))
	}
	if got := a.Resample(24000); len(got) != 24000 || got[5] != in[5] {
		t.Fatal("same-rate resample changed the audio")
	}
	// A constant signal stays constant when averaged.
	flat := WAVAudio{Rate: 48000, Samples: make([]int16, 4800)}
	for i := range flat.Samples {
		flat.Samples[i] = 1000
	}
	for i, v := range flat.Resample(16000) {
		if v != 1000 {
			t.Fatalf("sample %d is %d", i, v)
		}
	}
	if WAV(PCMBytes(in))[24] != 0xc0 { // 24000 Hz = 0x5dc0
		t.Fatal("WAV is no longer 24 kHz")
	}
}

func TestTranscribeFileSendsMonoSixteenKilohertzMultipart(t *testing.T) {
	var got struct {
		path, model, language, contentType string
		wav                                []byte
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		got.model, got.language = r.FormValue("model"), r.FormValue("language")
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		got.contentType = header.Header.Get("Content-Type")
		got.wav, _ = io.ReadAll(file)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"  Hello   there. "}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := &Client{STTURL: server.URL + "/"}
	text, err := c.TranscribeFile(ctx, WAVAt(PCMBytes(ramp(16000)), 16000))
	if err != nil || text != "Hello there." {
		t.Fatalf("%q %v", text, err)
	}
	if got.path != "/v1/audio/transcriptions" || got.model != "nemotron-3.5-asr" || got.language != "en-US" || got.contentType != "audio/wav" {
		t.Fatalf("%+v", got)
	}
	a, err := ParseWAV(got.wav)
	if err != nil || a.Rate != 16000 || len(a.Samples) != 16000 {
		t.Fatalf("uploaded audio: %v %d Hz", err, a.Rate)
	}
}

func TestTranscribeFileReportsFailures(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"http error":   func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) },
		"not json":     func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) },
		"empty object": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) },
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			text, err := (&Client{STTURL: server.URL}).TranscribeFile(context.Background(), WAVAt(nil, 16000))
			if name == "empty object" {
				if err != nil || text != "" {
					t.Fatalf("%q %v", text, err)
				}
				return
			}
			if err == nil {
				t.Fatal("failure not reported")
			}
		})
	}
}
