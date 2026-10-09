package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/speech"
	"github.com/coder/websocket"
)

// The browser asks for speech with the chat text; the TTS service must receive
// the spoken form while the request text itself is untouched.
func TestSynthesizeSendsSpokenText(t *testing.T) {
	got := make(chan string, 1)
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		select {
		case got <- body.Input:
		default:
		}
		w.Write([]byte{1, 0, 2, 0})
	}))
	defer tts.Close()
	store := session.NewStore()
	s := store.Create(&config.Agent{ID: "telecom"}, "C001")
	app := &API{Root: context.Background(), Sessions: store, Speech: &speech.Client{STTURL: "http://unused", TTSURL: tts.URL}}
	mux := http.NewServeMux()
	app.voiceRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	response, err := http.Post(server.URL+"/api/sessions/"+s.ID+"/speech", "application/json", strings.NewReader(`{"text":"The fee is AED 51,917 per year."}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	if response.StatusCode != 200 {
		t.Fatalf("status %d", response.StatusCode)
	}
	select {
	case input := <-got:
		if want := "The fee is fifty-one thousand nine hundred and seventeen dirhams per year."; input != want {
			t.Fatalf("TTS received %q, want %q", input, want)
		}
	default:
		t.Fatal("TTS service was not called")
	}
}

func TestVoiceSessionAndOriginBoundaries(t *testing.T) {
	store := session.NewStore()
	s := store.Create(&config.Agent{ID: "telecom"}, "C001")
	app := &API{Root: context.Background(), Sessions: store, Speech: &speech.Client{STTURL: "http://unused", TTSURL: "http://unused"}}
	mux := http.NewServeMux()
	app.voiceRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, tc := range []struct {
		path, origin string
		status       int
	}{{"missing", "", 404}, {s.ID, "https://untrusted.example", 403}} {
		c, r, err := websocket.Dial(ctx, strings.Replace(server.URL, "http:", "ws:", 1)+"/api/sessions/"+tc.path+"/transcribe", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{tc.origin}}})
		if c != nil {
			c.CloseNow()
		}
		if err == nil || r == nil || r.StatusCode != tc.status {
			t.Fatalf("status=%v err=%v", r, err)
		}
	}
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+s.ID+"/speech", strings.NewReader(`{"text":"`+strings.Repeat("a", 1801)+`"}`))
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	if response.StatusCode != 400 {
		t.Fatalf("excessive speech accepted: %d", response.StatusCode)
	}
}
