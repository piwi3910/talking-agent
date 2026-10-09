package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/voices"
)

const defaultPreview = "Hello, thanks for calling. How can I help you today?"

func (a *API) voiceSettingsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/settings/voices", func(w http.ResponseWriter, r *http.Request) {
		if a.Voices == nil {
			fail(w, 503, "Voice settings unavailable")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, a.Voices.Snapshot())
	})
	m.HandleFunc("POST /api/settings/voices", func(w http.ResponseWriter, r *http.Request) {
		if a.Voices == nil {
			fail(w, 503, "Voice settings unavailable")
			return
		}
		var in voices.SaveRequest
		if err := decodeLimit(w, r, &in, 256<<10); err != nil {
			fail(w, 400, "Invalid voice settings")
			return
		}
		saved, err := a.Voices.Save(in)
		if errors.Is(err, voices.ErrConflict) {
			fail(w, 409, err.Error())
			return
		}
		if errors.Is(err, voices.ErrStorage) {
			// The cause can contain file paths; keep it in the log only.
			slog.Error("save voice settings", "error", err)
			fail(w, 500, "could not save voice settings")
			return
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, saved)
	})
	m.HandleFunc("POST /api/settings/voices/cues/{agent}", func(w http.ResponseWriter, r *http.Request) {
		if a.Voices == nil {
			fail(w, 503, "Voice settings unavailable")
			return
		}
		if a.Agents[r.PathValue("agent")] == nil {
			fail(w, 404, "Unknown agent")
			return
		}
		snapshot, err := a.Voices.Force(r.PathValue("agent"))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 202, snapshot)
	})
	m.HandleFunc("POST /api/settings/voices/preview", a.previewVoice)
}

func (a *API) previewVoice(w http.ResponseWriter, r *http.Request) {
	if a.Voices == nil || !a.Speech.Enabled() {
		fail(w, 503, "Speech is not configured")
		return
	}
	var in struct {
		VoiceID     string `json:"voice_id"`
		Description string `json:"description"`
		Direction   string `json:"direction"`
		Text        string `json:"text"`
	}
	if decode(w, r, &in) != nil {
		fail(w, 400, "Invalid preview request")
		return
	}
	in.Description, in.Direction, in.Text = strings.TrimSpace(in.Description), strings.TrimSpace(in.Direction), strings.TrimSpace(in.Text)
	if (in.VoiceID == "") == (in.Description == "") {
		fail(w, 400, "Provide exactly one of voice_id or description")
		return
	}
	if utf8.RuneCountInString(in.Description) > 500 || utf8.RuneCountInString(in.Direction) > 500 {
		fail(w, 400, "Description and direction must be at most 500 characters")
		return
	}
	if in.Text == "" {
		in.Text = defaultPreview
	}
	if utf8.RuneCountInString(in.Text) > 300 {
		fail(w, 400, "Preview text must be at most 300 characters")
		return
	}
	voice, err := a.Voices.Preview(in.VoiceID, in.Description, in.Direction)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !a.previewing.CompareAndSwap(false, true) {
		fail(w, 409, "Another preview is running")
		return
	}
	defer a.previewing.Store(false)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	stop := context.AfterFunc(a.Root, cancel)
	defer stop()
	var pcm bytes.Buffer
	err = a.Speech.Synthesize(ctx, speech.VocalEvents(in.Text, true), voice, func(p []byte) error { pcm.Write(p); return nil })
	if err != nil || pcm.Len()%2 != 0 {
		fail(w, 502, "Speech generation failed")
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(speech.WAV(pcm.Bytes()))
}
