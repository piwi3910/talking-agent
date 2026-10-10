package telephony

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/knowledge"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/memory"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/skills"
	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/telemetry"
	"enterprise-ai-demo/internal/tools"
	"github.com/emiago/diago"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/zaf/g711"
	"path/filepath"
	"sync/atomic"
)

// Real SIP dialogs exercise number routing, concurrent calls, capacity and BYE cleanup.
func TestSIPConcurrentCalls(t *testing.T) {
	setTestSIPDigest(t, "agent.test", map[string]sipDigestUser{"handset-a": {HA1: testSIPHA1("handset-a", "agent.test", "fixture-a"), Persona: "a"}, "handset-b": {HA1: testSIPHA1("handset-b", "agent.test", "fixture-b"), Persona: "b"}})
	greetings := make(chan string, 30)
	transcripts := make(chan string, 10)
	for _, text := range []string{"hello", "change my plan", "confirm", "confirm"} {
		transcripts <- text
	}
	mutations := make(chan tools.Request, 1)
	var blockReply atomic.Bool
	var blockProposal atomic.Bool
	blockProposal.Store(true)
	playbackCanceled := make(chan struct{}, 1)
	speechHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "transcriptions") {
			raw, err := io.ReadAll(r.Body)
			if err != nil || len(raw) < 16000 {
				t.Errorf("bad transcription PCM: %d, %v", len(raw), err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			select {
			case text := <-transcripts:
				raw, _ := json.Marshal(map[string]string{"type": "transcript.text.done", "text": text})
				fmt.Fprintf(w, "data: %s\n", raw)
			case <-r.Context().Done():
				return
			}
			return
		}
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		greetings <- body.Input
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write(make([]byte, 960)) // one 20 ms G.711 frame after resampling
		if strings.Contains(body.Input, "Say confirm") && blockProposal.CompareAndSwap(true, false) {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
		if body.Input == "Hello back from Alice." && blockReply.Load() {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			playbackCanceled <- struct{}{}
		}
	}))
	defer speechHTTP.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	agents := map[string]*config.Agent{"a": {ID: "a", Name: "Alice"}, "b": {ID: "b", Name: "Bob"}}
	s := &Server{Config: Config{BindHost: "127.0.0.1", Port: port, AdvertiseIP: "127.0.0.1", RTPStart: 28000, RTPEnd: 28040, MaxCalls: 3, AllowedPeers: []string{"127.0.0.1/32"}, Numbers: map[string]string{"500": "a", "501": "b"}}, Agents: agents, Sessions: session.NewStore(), Speech: &speech.Client{STTURL: speechHTTP.URL, TTSURL: speechHTTP.URL}}
	settings, err := OpenSettings(filepath.Join(t.TempDir(), "settings.json"), agents, s.Config.Numbers)
	if err != nil {
		t.Fatal(err)
	}
	state := settings.Snapshot()
	profile := state.Personas["a"]
	profile.UserID = "customer-a"
	state.Personas["a"] = profile
	if _, err = settings.Save(state); err != nil {
		t.Fatal(err)
	}
	s.Settings = settings
	s.Runtime = &agent.Runtime{Memory: memory.New(), Knowledge: knowledge.Local{}, Clients: map[string]llm.Client{"a": &llm.Demo{Rules: []llm.DemoRule{{Keywords: []string{"hello"}, Reply: "Hello back from Alice."}, {Keywords: []string{"change"}, Steps: []llm.DemoStep{{Tool: "plan.change", Arguments: map[string]string{"plan_id": "fiber"}}}}}}}}
	s.Runtime.Catalogs = map[string]skills.Catalog{"a": {"plans": skills.Skill{ID: "plans", Keywords: []string{"change"}, Tools: []tools.Definition{{Name: "plan.change", Mutation: true, Input: tools.Schema{Type: "object", Properties: map[string]tools.Property{"plan_id": {Type: "string"}}, Required: []string{"plan_id"}}}}}}}
	s.Runtime.Tools = phoneExecutor{mutations: mutations}
	s.Runtime.Clients["b"] = &llm.Demo{Rules: []llm.DemoRule{{Keywords: []string{"hello"}, Reply: "Hello back from Bob."}}}
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("SIP shutdown did not release calls")
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SIP listener did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	phone := diago.NewDiago(ua, diago.WithTransport(diago.Transport{Transport: "udp", BindHost: "127.0.0.1", BindPort: 0}), diago.WithTransport(diago.Transport{Transport: "tcp", BindHost: "127.0.0.1", BindPort: 0}))
	if err := phone.ServeBackground(ctx, func(*diago.DialogServerSession) {}); err != nil {
		t.Fatal(err)
	}
	var callMedia []*diago.DialogMedia
	dial := func(number, transport string) (*diago.DialogClientSession, error) {
		dialctx, done := context.WithTimeout(ctx, 3*time.Second)
		defer done()
		username, password := "handset-a", "fixture-a"
		if number == "501" {
			username, password = "handset-b", "fixture-b"
		}
		call, m, err := phone.Invite(dialctx, sip.Uri{User: number, Host: "127.0.0.1", Port: port}, diago.InviteOptions{Transport: transport, Username: username, Password: password})
		if err == nil {
			callMedia = append(callMedia, m)
		}
		return call, err
	}
	var calls []*diago.DialogClientSession
	defer func() {
		for _, call := range calls {
			call.Close()
		}
	}()
	for _, number := range []string{"500", "500", "501"} {
		call, err := dial(number, "udp")
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	counts := map[string]int{}
	for range 3 {
		select {
		case text := <-greetings:
			if strings.Contains(text, "Alice") {
				counts["Alice"]++
			} else if strings.Contains(text, "Bob") {
				counts["Bob"]++
			} else {
				t.Fatalf("wrong greeting %q", text)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no agent greeting")
		}
	}
	if counts["Alice"] != 2 || counts["Bob"] != 1 {
		t.Fatalf("wrong route counts: %v", counts)
	}
	if call, err := dial("500", "udp"); err == nil {
		call.Close()
		t.Fatal("capacity limit ignored")
	} else if !strings.Contains(err.Error(), "486") {
		t.Fatalf("wanted 486: %v", err)
	}
	if call, err := dial("999", "udp"); err == nil {
		call.Close()
		t.Fatal("unknown number accepted")
	} else if !strings.Contains(err.Error(), "404") {
		t.Fatalf("wanted 404: %v", err)
	}
	// Exercise both directions of RTP and the full STT -> agent -> TTS turn.
	input, err := callMedia[0].AudioReader()
	if err != nil {
		t.Fatal(err)
	}
	audio := make([]byte, 2048)
	n, err := input.Read(audio)
	if err != nil || n != 160 {
		t.Fatalf("bad greeting RTP: %d %v", n, err)
	}
	output, err := callMedia[0].AudioWriter()
	if err != nil {
		t.Fatal(err)
	}
	for range 25 {
		if _, err := output.Write(bytes.Repeat([]byte{g711.EncodeUlawFrame(3000)}, 160)); err != nil {
			t.Fatal(err)
		}
	}
	for range 40 {
		if _, err := output.Write(bytes.Repeat([]byte{g711.EncodeUlawFrame(0)}, 160)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case text := <-greetings:
		if text != "Hello back from Alice." {
			t.Fatalf("wrong agent reply: %q", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("caller speech did not generate reply")
	}
	// A proposal is read aloud and remains unexecuted until an explicit voice confirmation.
	sendUtterance := func(w io.Writer) {
		t.Helper()
		for range 25 {
			if _, err := w.Write(bytes.Repeat([]byte{g711.EncodeUlawFrame(3000)}, 160)); err != nil {
				t.Fatal(err)
			}
		}
		for range 40 {
			if _, err := w.Write(bytes.Repeat([]byte{g711.EncodeUlawFrame(0)}, 160)); err != nil {
				t.Fatal(err)
			}
		}
	}
	sendUtterance(output)
	for {
		select {
		case text := <-greetings:
			if strings.Contains(text, "Say confirm") {
				if !strings.Contains(text, "plan id: fiber") {
					t.Fatalf("proposal missing details: %s", text)
				}
				goto prompted
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no spoken proposal")
		}
	}
prompted:
	select {
	case <-mutations:
		t.Fatal("action executed without approval")
	default:
	}
	// Confirming during an unfinished prompt must only replay its details.
	sendUtterance(output)
	replayed := false
	for !replayed {
		select {
		case text := <-greetings:
			replayed = strings.Contains(text, "Say confirm")
		case <-time.After(3 * time.Second):
			t.Fatal("interrupted proposal was not replayed")
		}
	}
	select {
	case <-mutations:
		t.Fatal("unfinished proposal approved by voice")
	default:
	}
	time.Sleep(50 * time.Millisecond)
	sendUtterance(output)
	select {
	case req := <-mutations:
		if req.UserID != "customer-a" || req.Arguments["plan_id"] != "fiber" {
			t.Fatalf("wrong confirmed account/action: %+v", req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("voice confirmation did not execute action")
	}
	// Keep Alice's reply open while a different persona processes its own call.
	blockReply.Store(true)
	transcripts <- "hello"
	sendUtterance(output)
	waitFor := func(expected string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case text := <-greetings:
				if text == expected {
					return
				}
			case <-deadline:
				t.Fatalf("did not hear %q", expected)
			}
		}
	}
	waitFor("Hello back from Alice.")
	bobOutput, err := callMedia[2].AudioWriter()
	if err != nil {
		t.Fatal(err)
	}
	transcripts <- "hello"
	sendUtterance(bobOutput)
	waitFor("Hello back from Bob.")
	// The new caller speech cancels synthesis before ASR has finalized "stop".
	transcripts <- "stop"
	for range 8 {
		if _, err := output.Write(bytes.Repeat([]byte{g711.EncodeUlawFrame(3000)}, 160)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-playbackCanceled:
	case <-time.After(600 * time.Millisecond):
		t.Fatal("speech onset did not cancel old playback")
	}
	for range 17 {
		if _, err := output.Write(bytes.Repeat([]byte{g711.EncodeUlawFrame(3000)}, 160)); err != nil {
			t.Fatal(err)
		}
	}
	for range 40 {
		if _, err := output.Write(bytes.Repeat([]byte{g711.EncodeUlawFrame(0)}, 160)); err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range calls {
		h, c := context.WithTimeout(ctx, time.Second)
		err := call.Hangup(h)
		c()
		if err != nil {
			t.Error(err)
		}
	}
	var tcpCall *diago.DialogClientSession
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		tcpCall, err = dial("501", "tcp")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("TCP call after BYE: %v", err)
	}
	calls = append(calls, tcpCall)
	h, c := context.WithTimeout(ctx, time.Second)
	err = tcpCall.Hangup(h)
	c()
	if err != nil {
		t.Error(err)
	}

}

type phoneExecutor struct{ mutations chan tools.Request }

func (e phoneExecutor) Execute(ctx context.Context, d tools.Definition, r tools.Request, emit telemetry.Sink) tools.Result {
	e.mutations <- r
	return tools.Result{Summary: "Your plan has been changed."}
}
