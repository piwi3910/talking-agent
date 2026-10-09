package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/speech"
	"github.com/coder/websocket"
)

func (a *API) voiceRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/agents/{id}/voice-cues", func(w http.ResponseWriter, r *http.Request) {
		c := a.Agents[r.PathValue("id")]
		if c == nil {
			fail(w, 404, "Unknown agent")
			return
		}
		dir, ok := a.cueDir(c.ID)
		if !ok {
			fail(w, 404, "No cues configured")
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, "cues.json"))
		if err != nil {
			fail(w, 404, "No cues configured")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	})
	m.HandleFunc("GET /api/agents/{id}/voice-cues/{cue}", func(w http.ResponseWriter, r *http.Request) {
		c := a.Agents[r.PathValue("id")]
		cue := r.PathValue("cue")
		if c == nil || !regexp.MustCompile(`^[a-z]+-[0-9]+$`).MatchString(cue) {
			fail(w, 404, "Unknown cue")
			return
		}
		dir, ok := a.cueDir(c.ID)
		if !ok {
			fail(w, 404, "No cues configured")
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "cues", cue+".wav"))
	})
	m.HandleFunc("GET /api/agents/{id}/voice", func(w http.ResponseWriter, r *http.Request) {
		c := a.Agents[r.PathValue("id")]
		if c == nil {
			fail(w, 404, "Unknown agent")
			return
		}
		id, name, ref := a.Voices.Current(c.ID)
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, map[string]any{"voice_id": id, "name": name, "sample": ref != nil})
	})
	m.HandleFunc("GET /api/agents/{id}/voice-sample", func(w http.ResponseWriter, r *http.Request) {
		if a.Agents[r.PathValue("id")] == nil {
			fail(w, 404, "Unknown agent")
			return
		}
		_, _, ref := a.Voices.Current(r.PathValue("id"))
		if ref == nil {
			fail(w, 404, "No voice sample yet")
			return
		}
		wav, err := base64.StdEncoding.DecodeString(ref.AudioBase64)
		if err != nil {
			fail(w, 500, "Invalid voice sample")
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(wav)
	})
	m.HandleFunc("GET /api/voice", func(w http.ResponseWriter, r *http.Request) {
		tts := "Qwen3-TTS 1.7B"
		if a.Speech.Omni() {
			tts = "Qwen3-TTS 1.7B (streaming)"
		}
		write(w, 200, map[string]any{"enabled": a.Speech.Enabled(), "stt": a.Speech.STTLabel(), "tts": tts, "input_sample_rate": 16000, "output_sample_rate": 24000})
	})
	m.HandleFunc("GET /api/sessions/{id}/transcribe", a.transcribe)
	m.HandleFunc("POST /api/sessions/{id}/speech", a.synthesize)
}

// cueDir is the active cue set: baked, a completed render, or none.
func (a *API) cueDir(agentID string) (string, bool) {
	if a.Voices == nil {
		c := a.Agents[agentID]
		return filepath.Join(c.Dir, "voice"), true
	}
	return a.Voices.CueDir(agentID)
}
func (a *API) transcribe(w http.ResponseWriter, r *http.Request) {
	s := a.Sessions.Get(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "Session not found")
		return
	}
	if !a.Speech.Enabled() {
		fail(w, 503, "Speech is not configured")
		return
	}
	key := "stt:" + s.ID
	if _, loaded := a.VoiceActive.LoadOrStore(key, true); loaded {
		fail(w, 409, "Transcription already active")
		return
	}
	defer a.VoiceActive.Delete(key)
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(65536)
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.Root, cancel)
	defer stop()
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	started := time.Now()
	var ended atomic.Int64
	id := r.URL.Query().Get("utterance")
	if len(id) > 64 {
		id = ""
	}
	s.Events.Emit(id, "stt.started", map[string]any{"sample_rate": 16000})
	a.trace(s.ID, s.Agent.ID, "stt.socket", map[string]any{"utterance": id})
	var sttBytes atomic.Int64
	defer func() {
		a.trace(s.ID, s.Agent.ID, "stt.socket.closed", map[string]any{"utterance": id, "audio_bytes": sttBytes.Load(), "duration_ms": time.Since(started).Milliseconds()})
	}()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		total := 0
		finished := false
		for {
			kind, data, e := c.Read(ctx)
			if e != nil {
				pw.CloseWithError(e)
				cancel()
				return
			}
			if kind == websocket.MessageBinary && !finished {
				total += len(data)
				sttBytes.Store(int64(total))
				if len(data)%2 != 0 || total > 16000*2*60 {
					pw.CloseWithError(fmt.Errorf("invalid or excessive PCM"))
					cancel()
					return
				}
				if _, e = pw.Write(data); e != nil {
					return
				}
			} else if kind == websocket.MessageText && !finished {
				var cmd struct{ Type string }
				if json.Unmarshal(data, &cmd) != nil || cmd.Type != "finish" {
					cancel()
					return
				}
				finished = true
				ended.Store(time.Now().UnixMilli())
				a.trace(s.ID, s.Agent.ID, "stt.finish", map[string]any{"utterance": id, "audio_bytes": total, "since_start_ms": time.Since(started).Milliseconds()})
				pw.Close()
				// Keep reading to detect browser aborts while final recognition is pending.
			} else {
				cancel()
				return
			}
		}
	}()
	first := true
	tctx := speech.WithTrace(ctx, func(ev string, data map[string]any) {
		data["utterance"] = id
		a.trace(s.ID, s.Agent.ID, ev, data)
	})
	err = a.Speech.Transcribe(tctx, pr, func(text string, final bool) error {
		typ := "stt.partial"
		data := map[string]any{"text": text, "duration_ms": time.Since(started).Milliseconds()}
		if first {
			first = false
			s.Events.Emit(id, "stt.first_partial", data)
		}
		if final {
			typ = "stt.final"
			if end := ended.Load(); end > 0 {
				data["finalization_ms"] = time.Now().UnixMilli() - end
			}
		}
		s.Events.Emit(id, typ, data)
		raw, _ := json.Marshal(map[string]any{"type": typ, "text": text})
		return c.Write(ctx, websocket.MessageText, raw)
	})
	if err != nil && ctx.Err() == nil {
		s.Events.Emit(id, "stt.failed", map[string]any{"message": "Transcription failed; please try again."})
		raw, _ := json.Marshal(map[string]string{"type": "error", "message": "Transcription failed; please try again."})
		_ = c.Write(ctx, websocket.MessageText, raw)
	}
	pr.Close()
	pw.Close()
	// A normal close handshake; an abrupt close reads as a failure (1006) in the browser.
	_ = c.Close(websocket.StatusNormalClosure, "")
	cancel()
	<-readerDone
}

// maxSpeechPerSession bounds concurrent /speech requests of one session.
const maxSpeechPerSession = 2

// acquireSpeech takes one of the session's speech slots; false when all are taken.
func (a *API) acquireSpeech(session string) bool {
	a.speechMu.Lock()
	defer a.speechMu.Unlock()
	if a.speechActive == nil {
		a.speechActive = map[string]int{}
	}
	if a.speechActive[session] >= maxSpeechPerSession {
		return false
	}
	a.speechActive[session]++
	return true
}
func (a *API) releaseSpeech(session string) {
	a.speechMu.Lock()
	defer a.speechMu.Unlock()
	if a.speechActive[session] <= 1 {
		delete(a.speechActive, session)
		return
	}
	a.speechActive[session]--
}
func (a *API) synthesize(w http.ResponseWriter, r *http.Request) {
	s := a.Sessions.Get(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "Session not found")
		return
	}
	if !a.Speech.Enabled() {
		fail(w, 503, "Speech is not configured")
		return
	}
	var in struct {
		Text   string `json:"text"`
		TurnID string `json:"turn_id"`
	}
	if decode(w, r, &in) != nil || len(strings.TrimSpace(in.Text)) == 0 || len(in.Text) > 1800 || len(in.TurnID) > 64 {
		fail(w, 400, "Invalid speech request")
		return
	}
	// The browser pipelines the next phrase while the current one plays.
	if !a.acquireSpeech(s.ID) {
		fail(w, 409, "Speech already active")
		return
	}
	defer a.releaseSpeech(s.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.Root, cancel)
	defer stop()
	started := time.Now()
	first := true
	bytes := 0
	voice := a.Voices.Resolve(s.Agent.ID)
	// Only the audio text is normalised; the chat text stays as written.
	text := speech.Spoken(in.Text)
	s.Events.Emit(in.TurnID, "tts.started", map[string]any{"characters": len(text), "reference_voice": voice.Reference != nil})
	req := session.ID()[:8]
	if a.Traces != nil {
		ctx = speech.WithTrace(ctx, a.speechTrace(s.ID, s.Agent.ID, req))
		a.trace(s.ID, s.Agent.ID, "speech.received", map[string]any{"req": req, "turn_id": in.TurnID, "text": in.Text, "spoken": text, "provider": a.Speech.Provider, "reference": voice.Reference != nil})
	}
	w.Header().Set("Content-Type", "audio/pcm")
	w.Header().Set("X-Audio-Sample-Rate", "24000")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Trailer", "X-Speech-Error")
	if text == "" {
		return
	}
	chunks := 0
	err := a.Speech.Synthesize(ctx, text, voice, func(p []byte) error {
		chunks++
		a.trace(s.ID, s.Agent.ID, "speech.chunk", map[string]any{"req": req, "n": chunks, "bytes": len(p), "since_ms": time.Since(started).Milliseconds(), "total": bytes + len(p)})
		if first {
			first = false
			s.Events.Emit(in.TurnID, "tts.first_audio", map[string]any{"ttfa_ms": time.Since(started).Milliseconds()})
		}
		bytes += len(p)
		_, err := w.Write(p)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		a.trace(s.ID, s.Agent.ID, "speech.flushed", map[string]any{"req": req, "n": chunks, "bytes": len(p), "since_ms": time.Since(started).Milliseconds(), "error": err != nil})
		return err
	})
	if err != nil {
		a.trace(s.ID, s.Agent.ID, "speech.error", map[string]any{"req": req, "error": err.Error(), "cancelled": ctx.Err() != nil, "bytes": bytes, "duration_ms": time.Since(started).Milliseconds()})
		s.Events.Emit(in.TurnID, "tts.failed", map[string]any{"cancelled": ctx.Err() != nil, "duration_ms": time.Since(started).Milliseconds()})
		if first {
			fail(w, 502, "Speech generation failed")
		} else {
			w.Header().Set("X-Speech-Error", "generation-failed")
		}
		return
	}
	a.trace(s.ID, s.Agent.ID, "speech.end", map[string]any{"req": req, "bytes": bytes, "chunks": chunks, "audio_ms": bytes * 1000 / 48000, "duration_ms": time.Since(started).Milliseconds()})
	s.Events.Emit(in.TurnID, "tts.completed", map[string]any{"duration_ms": time.Since(started).Milliseconds(), "audio_ms": bytes * 1000 / 48000})
}
