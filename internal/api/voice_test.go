package api

import (
	"context"
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
