package telephony

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"enterprise-ai-demo/internal/session"
)

var cueID = regexp.MustCompile(`^[a-z]+-[0-9]+$`)

func (s *Server) playCue(ctx context.Context, w io.Writer, pt uint8, sess *session.Session, category, turn string) error {
	dir := filepath.Join(sess.Agent.Dir, "voice")
	if s.Voices != nil {
		// Cues are muted until the active voice's set is complete.
		var ok bool
		if dir, ok = s.Voices.CueDir(sess.Agent.ID); !ok {
			return nil
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cues.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Cues []struct{ ID, Category, Text string }
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return nil
	}
	for _, cue := range manifest.Cues {
		if cue.Category != category || !cueID.MatchString(cue.ID) {
			continue
		}
		wav, err := os.ReadFile(filepath.Join(dir, "cues", cue.ID+".wav"))
		if err != nil {
			continue
		}
		pcm, rate, err := wavPCM(wav)
		if err != nil {
			continue
		}
		// playback expects 24 kHz. Local cues may have a different integer sample rate.
		if rate != 24000 {
			samples := len(pcm) / 2
			out := make([]byte, 2*samples*24000/rate)
			for i := 0; i < len(out)/2; i++ {
				src := i * rate / 24000
				copy(out[i*2:], pcm[src*2:src*2+2])
			}
			pcm = out
		}
		sess.Events.Emit(turn, "voice.cue", map[string]any{"category": category, "text": cue.Text, "transport": "sip"})
		return playback(ctx, w, pt, func(emit func([]byte) error) error { return emit(pcm) })
	}
	if category != "waiting" {
		return s.playCue(ctx, w, pt, sess, "waiting", turn)
	}
	return nil
}
func wavPCM(wav []byte) ([]byte, int, error) {
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("invalid cue WAV")
	}
	rate := 0
	var pcm []byte
	for pos := 12; pos+8 <= len(wav); {
		kind := string(wav[pos : pos+4])
		n := int(binary.LittleEndian.Uint32(wav[pos+4:]))
		pos += 8
		if n > len(wav)-pos {
			return nil, 0, fmt.Errorf("truncated cue WAV")
		}
		chunk := wav[pos : pos+n]
		if kind == "fmt " {
			if n < 16 || binary.LittleEndian.Uint16(chunk) != 1 || binary.LittleEndian.Uint16(chunk[2:]) != 1 || binary.LittleEndian.Uint16(chunk[14:]) != 16 {
				return nil, 0, fmt.Errorf("unsupported cue WAV")
			}
			rate = int(binary.LittleEndian.Uint32(chunk[4:]))
			if rate < 8000 || rate > 48000 {
				return nil, 0, fmt.Errorf("unsupported cue sample rate")
			}
		}
		if kind == "data" {
			pcm = chunk
		}
		pos += n + n%2
	}
	if rate == 0 || len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil, 0, fmt.Errorf("missing cue PCM")
	}
	return pcm, rate, nil
}
