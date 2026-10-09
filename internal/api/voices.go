package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"mime"
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
	m.HandleFunc("POST /api/settings/voices/clone/check", a.checkClone)
	m.HandleFunc("POST /api/settings/voices/clone", a.createClone)
}

// checkClone validates a reference recording, measures its quality and
// transcribes it. Nothing is kept. It shares the preview slot so that only one
// speech request of this kind runs at a time.
func (a *API) checkClone(w http.ResponseWriter, r *http.Request) {
	if a.Voices == nil || !a.Speech.STTEnabled() {
		fail(w, 503, "Speech recognition is not configured")
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || (mt != "audio/wav" && mt != "audio/x-wav" && mt != "audio/wave") {
		fail(w, 415, "Send the recording as audio/wav")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, voices.MaxCloneAudio))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, 413, "Recording is larger than 2 MiB")
			return
		}
		fail(w, 400, "Could not read the recording")
		return
	}
	analysis, err := voices.AnalyzeClone(raw)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !a.previewing.CompareAndSwap(false, true) {
		fail(w, 409, "Another check or preview is running")
		return
	}
	defer a.previewing.Store(false)
	report := analysis.Report
	if analysis.Speech {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		stop := context.AfterFunc(a.Root, cancel)
		defer stop()
		// The recognizer takes mono 16 kHz; the recording is usually 24 kHz.
		mono := speech.WAVAudio{Rate: analysis.Audio.Rate, Samples: analysis.Audio.Samples}.Resample(16000)
		report.Transcript, err = a.Speech.TranscribeFile(ctx, speech.WAVAt(speech.PCMBytes(mono), 16000))
		if err != nil {
			slog.Warn("clone transcription failed", "error", err)
			fail(w, 502, "Speech recognition failed")
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, report)
}

func (a *API) createClone(w http.ResponseWriter, r *http.Request) {
	if a.Voices == nil {
		fail(w, 503, "Voice settings unavailable")
		return
	}
	var in struct {
		Revision    uint64 `json:"revision"`
		ID          string `json:"id"`
		Name        string `json:"name"`
		Transcript  string `json:"transcript"`
		AudioBase64 string `json:"audio_base64"`
		Consent     bool   `json:"consent"`
	}
	if err := decodeLimit(w, r, &in, 3<<20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, 413, "Clone request is too large")
			return
		}
		fail(w, 400, "Invalid clone request")
		return
	}
	if !in.Consent {
		fail(w, 400, "Confirm that this is your own voice or that you have the speaker's permission")
		return
	}
	audio, err := base64.StdEncoding.DecodeString(in.AudioBase64)
	if err != nil || len(audio) == 0 || len(audio) > voices.MaxCloneAudio {
		fail(w, 400, "Invalid recording")
		return
	}
	saved, err := a.Voices.CreateClone(voices.CloneRequest{Revision: in.Revision, ID: in.ID, Name: in.Name, Transcript: in.Transcript, Audio: audio})
	if errors.Is(err, voices.ErrConflict) {
		fail(w, 409, err.Error())
		return
	}
	if errors.Is(err, voices.ErrStorage) {
		slog.Error("save cloned voice", "error", err)
		fail(w, 500, "could not save voice settings")
		return
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, saved)
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
		// An unsaved clone: the recording and its exact transcript.
		CloneAudio      string `json:"clone_audio_base64"`
		CloneTranscript string `json:"clone_transcript"`
	}
	if decodeLimit(w, r, &in, 3<<20) != nil {
		fail(w, 400, "Invalid preview request")
		return
	}
	in.Description, in.Direction, in.Text = strings.TrimSpace(in.Description), strings.TrimSpace(in.Direction), strings.TrimSpace(in.Text)
	sources := 0
	for _, s := range []string{in.VoiceID, in.Description, in.CloneAudio} {
		if s != "" {
			sources++
		}
	}
	if sources != 1 {
		fail(w, 400, "Provide exactly one of voice_id, description or clone_audio_base64")
		return
	}
	if (in.CloneAudio == "") != (in.CloneTranscript == "") {
		fail(w, 400, "An inline clone needs both clone_audio_base64 and clone_transcript")
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
	var voice speech.Voice
	var err error
	if in.CloneAudio != "" {
		var audio []byte
		if audio, err = base64.StdEncoding.DecodeString(in.CloneAudio); err != nil || len(audio) > voices.MaxCloneAudio {
			fail(w, 400, "Invalid recording")
			return
		}
		voice, err = voices.InlineClone(audio, in.CloneTranscript, in.Direction)
	} else {
		voice, err = a.Voices.Preview(in.VoiceID, in.Description, in.Direction)
	}
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
