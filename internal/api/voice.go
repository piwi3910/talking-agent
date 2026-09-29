package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

func (a *API) voiceRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/agents/{id}/voice-reference", func(w http.ResponseWriter, r *http.Request) {
		ref := a.Voices[r.PathValue("id")]
		if ref == nil {
			fail(w, 404, "No voice reference configured")
			return
		}
		wav, err := base64.StdEncoding.DecodeString(ref.AudioBase64)
		if err != nil {
			fail(w, 500, "Invalid voice reference")
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(wav)
	})
	m.HandleFunc("GET /api/voice", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"enabled": a.Speech.Enabled(), "stt": "Nemotron 3.5 ASR", "tts": "Breeze TTS 2", "input_sample_rate": 16000, "output_sample_rate": 24000})
	})
	m.HandleFunc("GET /api/sessions/{id}/transcribe", a.transcribe)
	m.HandleFunc("POST /api/sessions/{id}/speech", a.synthesize)
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
				pw.Close()
				// Keep reading to detect browser aborts while final recognition is pending.
			} else {
				cancel()
				return
			}
		}
	}()
	first := true
	err = a.Speech.Transcribe(ctx, pr, func(text string, final bool) error {
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
	cancel()
	pr.Close()
	pw.Close()
	c.CloseNow()
	<-readerDone
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
	key := "tts:" + s.ID
	if _, loaded := a.VoiceActive.LoadOrStore(key, true); loaded {
		fail(w, 409, "Speech already active")
		return
	}
	defer a.VoiceActive.Delete(key)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.Root, cancel)
	defer stop()
	started := time.Now()
	first := true
	bytes := 0
	s.Events.Emit(in.TurnID, "tts.started", map[string]any{"characters": len(in.Text), "reference_voice": a.Voices[s.Agent.ID] != nil})
	instruction := "Speak in a warm, relaxed, conversational voice, with clear natural English. Avoid an announcer tone."
	if style := s.Agent.Persona["voice_style"]; style != "" {
		instruction = style
	}
	w.Header().Set("Content-Type", "audio/pcm")
	w.Header().Set("X-Audio-Sample-Rate", "24000")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Trailer", "X-Speech-Error")
	err := a.Speech.Synthesize(ctx, in.Text, instruction, a.Voices[s.Agent.ID], func(p []byte) error {
		if first {
			first = false
			s.Events.Emit(in.TurnID, "tts.first_audio", map[string]any{"ttfa_ms": time.Since(started).Milliseconds()})
		}
		bytes += len(p)
		_, err := w.Write(p)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return err
	})
	if err != nil {
		s.Events.Emit(in.TurnID, "tts.failed", map[string]any{"cancelled": ctx.Err() != nil, "duration_ms": time.Since(started).Milliseconds()})
		if first {
			fail(w, 502, "Speech generation failed")
		} else {
			w.Header().Set("X-Speech-Error", "generation-failed")
		}
		return
	}
	s.Events.Emit(in.TurnID, "tts.completed", map[string]any{"duration_ms": time.Since(started).Milliseconds(), "audio_ms": bytes * 1000 / 48000})
}
