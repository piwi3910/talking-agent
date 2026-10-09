package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// WAVAudio is decoded mono signed 16-bit PCM.
type WAVAudio struct {
	Rate    int
	Samples []int16
}

// ParseWAV decodes a RIFF/WAVE file holding mono 16-bit PCM at 16 to 48 kHz.
// A data chunk that claims more bytes than the file holds is read up to the end
// of the file, as streamed recordings do.
func ParseWAV(raw []byte) (WAVAudio, error) {
	if len(raw) < 12 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return WAVAudio{}, errors.New("not a RIFF/WAVE file")
	}
	var out WAVAudio
	haveFmt := false
	pos := 12
	for pos+8 <= len(raw) {
		id := string(raw[pos : pos+4])
		size := uint64(binary.LittleEndian.Uint32(raw[pos+4:]))
		body := pos + 8
		avail := uint64(len(raw) - body)
		switch id {
		case "fmt ":
			if size < 16 || size > avail {
				return WAVAudio{}, errors.New("invalid WAV format chunk")
			}
			format := binary.LittleEndian.Uint16(raw[body:])
			channels := binary.LittleEndian.Uint16(raw[body+2:])
			rate := binary.LittleEndian.Uint32(raw[body+4:])
			bits := binary.LittleEndian.Uint16(raw[body+14:])
			if format != 1 {
				return WAVAudio{}, errors.New("WAV must be uncompressed PCM")
			}
			if channels != 1 {
				return WAVAudio{}, errors.New("WAV must be mono")
			}
			if bits != 16 {
				return WAVAudio{}, errors.New("WAV must be 16-bit")
			}
			if rate < 16000 || rate > 48000 {
				return WAVAudio{}, errors.New("WAV sample rate must be between 16 and 48 kHz")
			}
			out.Rate = int(rate)
			haveFmt = true
		case "data":
			if !haveFmt {
				return WAVAudio{}, errors.New("WAV data chunk before format chunk")
			}
			n := avail
			if size < n {
				n = size
			}
			n &^= 1
			if n == 0 {
				return WAVAudio{}, errors.New("WAV has no audio")
			}
			out.Samples = make([]int16, n/2)
			for i := range out.Samples {
				out.Samples[i] = int16(binary.LittleEndian.Uint16(raw[body+2*i:]))
			}
			return out, nil
		}
		if size > avail {
			break
		}
		pos = body + int(size) + int(size&1)
	}
	return WAVAudio{}, errors.New("WAV has no audio")
}

// Resample converts to another rate by averaging the source span that each
// output sample covers, which also low-passes when downsampling.
func (a WAVAudio) Resample(to int) []int16 {
	if a.Rate == to || a.Rate <= 0 || to <= 0 {
		return append([]int16(nil), a.Samples...)
	}
	ratio := float64(a.Rate) / float64(to)
	out := make([]int16, int(float64(len(a.Samples))/ratio))
	for j := range out {
		start := float64(j) * ratio
		end := start + ratio
		var sum, weight float64
		for i := int(start); i < len(a.Samples) && float64(i) < end; i++ {
			w := math.Min(float64(i+1), end) - math.Max(float64(i), start)
			if w <= 0 {
				continue
			}
			sum += float64(a.Samples[i]) * w
			weight += w
		}
		if weight > 0 {
			out[j] = int16(math.Max(-32768, math.Min(32767, math.Round(sum/weight))))
		}
	}
	return out
}

// PCMBytes encodes samples as little-endian signed 16-bit PCM.
func PCMBytes(samples []int16) []byte {
	b := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
	}
	return b
}

// WAVAt wraps mono signed 16-bit PCM at the given rate in a RIFF header.
func WAVAt(pcm []byte, rate int) []byte {
	h := make([]byte, 44, 44+len(pcm))
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return append(h, pcm...)
}

// STTEnabled reports whether the transcription worker is configured.
func (c *Client) STTEnabled() bool { return c != nil && c.STTURL != "" }

// TranscribeFile transcribes a short recording through the worker's file
// endpoint. The worker needs mono 16 kHz audio, so wav must already be in that
// format.
func (c *Client) TranscribeFile(ctx context.Context, wav []byte) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("model", "nemotron-3.5-asr")
	_ = form.WriteField("language", "en-US")
	part, err := form.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="recording.wav"`},
		"Content-Type":        {"audio/wav"},
	})
	if err != nil {
		return "", err
	}
	if _, err = part.Write(wav); err != nil {
		return "", err
	}
	if err = form.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.STTURL, "/")+"/v1/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("speech service HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("transcription service returned an unexpected response")
	}
	return strings.Join(strings.Fields(out.Text), " "), nil
}
