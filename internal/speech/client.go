// Package speech adapts audio.cpp's duplex ASR and streaming PCM synthesis.
// It has no dependency on agent logic or a browser transport.
package speech

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// TTS providers. Both speak Qwen3-TTS.
const (
	// ProviderOmni (the server default) streams Qwen3-TTS Base clones from a
	// vLLM-Omni server. Presets and designed voices are rendered once through the
	// audio.cpp worker at RenderURL and then cloned from that sample.
	ProviderOmni = "omni"
	// ProviderQwen3 renders every phrase offline on the audio.cpp worker at
	// TTSURL: presets, designs and clones, without streaming. It is the zero value.
	ProviderQwen3 = "qwen3"
)

// Qwen3-TTS model ids served by the audio.cpp worker.
const (
	ModelQwen3Base   = "qwen3-tts-base"   // voice cloning from a reference
	ModelQwen3Custom = "qwen3-tts-custom" // preset speakers
	ModelQwen3Design = "qwen3-tts-design" // voice from a description
)

// DefaultQwen3Speaker is used when a CustomVoice request names no speaker, which
// the server would otherwise reject.
const DefaultQwen3Speaker = "Ryan"

type Client struct {
	STTURL, TTSURL string
	HTTP           *http.Client

	// Provider selects the TTS request format: "omni" or "qwen3" (the zero value).
	Provider string

	// RenderURL is the audio.cpp Qwen3-TTS worker that renders preset and design
	// samples under the omni provider. TTSURL is then the streaming server.
	RenderURL string

	live atomic.Int64 // live syntheses in flight (web and SIP), not cue renders
}

func (c *Client) Enabled() bool { return c != nil && c.STTURL != "" && c.TTSURL != "" }

// Omni reports whether clone voices stream from the vLLM-Omni server.
func (c *Client) Omni() bool { return c != nil && c.Provider == ProviderOmni }

// Live is the number of live (caller-facing) syntheses currently in flight.
func (c *Client) Live() int64 {
	if c == nil {
		return 0
	}
	return c.live.Load()
}

// idlePoll is how often WaitIdle re-checks for live speech.
const idlePoll = 200 * time.Millisecond

// WaitIdle blocks while any live synthesis is in flight, so background renders
// never delay a caller. It returns the context's error if it ends first.
func (c *Client) WaitIdle(ctx context.Context) error {
	for c.Live() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(idlePoll):
		}
	}
	return ctx.Err()
}
func (c *Client) do(ctx context.Context, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.Contains(endpoint, "/live?") {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("speech service HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// Transcribe reads partials while the caller is still writing PCM to audio.
func (c *Client) Transcribe(ctx context.Context, audio io.Reader, emit func(string, bool) error) error {
	resp, err := c.do(ctx, strings.TrimRight(c.STTURL, "/")+"/v1/audio/transcriptions/live?model=nemotron-3.5-asr&sample_rate=16000&channels=1&sample_format=s16le&language=en-US", audio)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	final := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			continue
		}
		var event struct{ Type, Text, Delta string }
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		switch event.Type {
		case "transcript.text.delta":
			if err := emit(event.Delta, false); err != nil {
				return err
			}
		case "transcript.text.done":
			final = true
			if err := emit(event.Text, true); err != nil {
				return err
			}
		case "error":
			return fmt.Errorf("transcription service failed")
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !final {
		return fmt.Errorf("transcription ended without a final result")
	}
	return nil
}

// Voice is the resolved voice for one phrase. Model is one of the ModelQwen3*
// ids (empty means Base with a Reference, otherwise CustomVoice), Speaker names
// a CustomVoice preset and Instruct is a style or, for the design model, the
// voice description.
type Voice struct {
	Reference *Reference

	Model    string
	Speaker  string
	Instruct string
}

// qwen3Body builds the offline Qwen3-TTS request: no streaming, raw PCM, fixed seed.
func qwen3Body(text string, v Voice) map[string]any {
	model := v.Model
	if model == "" {
		if v.Reference != nil {
			model = ModelQwen3Base
		} else {
			model = ModelQwen3Custom
		}
	}
	options := map[string]string{"seed": "42"}
	body := map[string]any{"model": model, "input": text, "stream": false, "response_format": "pcm", "options": options}
	switch model {
	case ModelQwen3Base:
		// Base has no instruction support: only the reference shapes the voice.
		if v.Reference != nil {
			body["voice_ref"] = map[string]string{"type": "base64", "data": v.Reference.AudioBase64}
			body["reference_text"] = v.Reference.Text
		}
	case ModelQwen3Design:
		options["instruct"] = v.Instruct
	default:
		speaker := v.Speaker
		if speaker == "" {
			speaker = DefaultQwen3Speaker
		}
		body["voice"] = speaker
		if v.Instruct != "" {
			options["instruct"] = v.Instruct
		}
	}
	return body
}

// Synthesize streams the phrase's PCM to emit as it arrives. It counts as live
// speech: background renders wait for it.
func (c *Client) Synthesize(ctx context.Context, text string, v Voice, emit func([]byte) error) error {
	c.live.Add(1)
	defer c.live.Add(-1)
	return c.synthesize(ctx, text, v, emit)
}

// SynthesizeBackground is Synthesize for background renders, which do not count
// as live speech.
func (c *Client) SynthesizeBackground(ctx context.Context, text string, v Voice, emit func([]byte) error) error {
	return c.synthesize(ctx, text, v, emit)
}

// omniBody builds the streaming vLLM-Omni request. Every voice is a clone.
func omniBody(text string, ref *Reference) map[string]any {
	return map[string]any{
		"model": ModelQwen3Base, "input": text, "task_type": "Base", "language": "English",
		"response_format": "pcm", "stream": true, "stream_format": "audio",
		"ref_audio": ref.DataURI(), "ref_text": ref.Text, "initial_codec_chunk_frames": 8,
	}
}

// rendersOffline reports whether an omni-provider voice needs the audio.cpp
// worker: presets and designs, which have no reference yet.
func rendersOffline(v Voice) bool {
	return v.Reference == nil || v.Model == ModelQwen3Custom || v.Model == ModelQwen3Design
}

func (c *Client) synthesize(ctx context.Context, text string, v Voice, emit func([]byte) error) error {
	var body map[string]any
	base := c.TTSURL
	switch {
	case c.Omni() && rendersOffline(v):
		if c.RenderURL == "" {
			return fmt.Errorf("TTS_RENDER_URL is not configured")
		}
		base = c.RenderURL
		body = qwen3Body(text, v)
	case c.Omni():
		body = omniBody(text, v.Reference)
	default:
		body = qwen3Body(text, v)
	}
	raw, _ := json.Marshal(body)
	trace(ctx, "upstream.request", map[string]any{"base": base, "request_bytes": len(raw)})
	resp, err := c.do(ctx, strings.TrimRight(base, "/")+"/v1/audio/speech", bytes.NewReader(raw))
	if err != nil {
		trace(ctx, "upstream.error", map[string]any{"error": err.Error()})
		return err
	}
	defer resp.Body.Close()
	trace(ctx, "upstream.headers", map[string]any{"status": resp.StatusCode})
	buf := make([]byte, 8192)
	total := 0
	for {
		n, e := resp.Body.Read(buf)
		if n > 0 {
			total += n
			if total > 24_000*2*120 {
				return fmt.Errorf("speech output too long")
			}
			if err := emit(buf[:n]); err != nil {
				return err
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if total == 0 {
		return fmt.Errorf("speech service returned no audio")
	}
	return nil
}
