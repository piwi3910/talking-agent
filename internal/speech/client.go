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
)

type Client struct {
	STTURL, TTSURL string
	HTTP           *http.Client
}

func (c *Client) Enabled() bool { return c != nil && c.STTURL != "" && c.TTSURL != "" }
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
func (c *Client) Synthesize(ctx context.Context, text, instruction string, reference *Reference, emit func([]byte) error) error {
	body := map[string]any{"model": "breeze", "input": text, "stream": true, "stream_format": "audio", "response_format": "pcm", "options": map[string]string{"instruction": instruction, "seed": "42"}}
	if reference != nil {
		body["voice_ref"] = map[string]string{"type": "base64", "data": reference.AudioBase64}
		body["reference_text"] = reference.Text
	}
	raw, _ := json.Marshal(body)
	resp, err := c.do(ctx, strings.TrimRight(c.TTSURL, "/")+"/v1/audio/speech", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
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
