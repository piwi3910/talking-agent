package voices

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"enterprise-ai-demo/internal/speech"
)

// MaxCloneAudio caps an uploaded or stored reference recording.
const MaxCloneAudio = 2 << 20

const maxRawSeconds = 30

// Check is one quality check of a reference recording.
type Check struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"` // pass, warn or fail
	Detail string `json:"detail"`
}

// CloneReport is what the check endpoint returns; Transcript is filled in by the caller.
type CloneReport struct {
	DurationMS    int     `json:"duration_ms"`
	RMSdBFS       float64 `json:"rms_dbfs"`
	ClippingRatio float64 `json:"clipping_ratio"`
	Checks        []Check `json:"checks"`
	Transcript    string  `json:"transcript"`
}

// Analysis is a validated recording with its silence trimmed.
type Analysis struct {
	Report CloneReport
	Audio  speech.WAVAudio // trimmed to the speech, at the recording's own rate
	Speech bool            // false when no speech was found
}

// Failed lists the details of every failed check.
func (a *Analysis) Failed() []string {
	var out []string
	for _, c := range a.Report.Checks {
		if c.Status == "fail" {
			out = append(out, c.Detail)
		}
	}
	return out
}

func dbfs(samples []int16) float64 {
	if len(samples) == 0 {
		return -120
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms < 1 {
		return -120
	}
	return math.Max(-120, 20*math.Log10(rms/32768))
}

// AnalyzeClone validates a mono 16-bit PCM WAV and measures its quality. It
// keeps nothing: the trimmed audio is returned to the caller.
func AnalyzeClone(raw []byte) (*Analysis, error) {
	if len(raw) > MaxCloneAudio {
		return nil, errors.New("recording is larger than 2 MiB")
	}
	a, err := speech.ParseWAV(raw)
	if err != nil {
		return nil, err
	}
	n, rate := len(a.Samples), a.Rate
	if n > maxRawSeconds*rate {
		return nil, fmt.Errorf("recording is longer than %d seconds", maxRawSeconds)
	}
	// Voiced span: 20 ms frames within 30 dB of the loudest frame (never below -60 dBFS).
	frame := rate / 50
	var levels []float64
	peak := -120.0
	for i := 0; i+frame <= n; i += frame {
		l := dbfs(a.Samples[i : i+frame])
		levels = append(levels, l)
		peak = math.Max(peak, l)
	}
	threshold := math.Max(-60, peak-30)
	first, last := -1, -1
	for i, l := range levels {
		if l >= threshold {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	spoken := a
	lead, trail := 0.0, 0.0
	if first >= 0 {
		pad := rate * 150 / 1000
		start := max(0, first*frame-pad)
		end := min(n, (last+1)*frame+pad)
		spoken.Samples = a.Samples[start:end]
		lead = float64(first*frame) / float64(rate)
		trail = math.Max(0, float64(n-(last+1)*frame)/float64(rate))
	}
	clipped := 0
	for _, s := range spoken.Samples {
		if s >= 32767 || s <= -32768 {
			clipped++
		}
	}
	seconds := float64(len(spoken.Samples)) / float64(rate)
	level := dbfs(spoken.Samples)
	ratio := float64(clipped) / float64(len(spoken.Samples))
	total := float64(n) / float64(rate)

	report := CloneReport{
		DurationMS:    int(math.Round(seconds * 1000)),
		RMSdBFS:       math.Round(level*10) / 10,
		ClippingRatio: math.Round(ratio*1e5) / 1e5,
	}
	duration := Check{ID: "duration", Label: "Length", Status: "pass", Detail: fmt.Sprintf("%.1f s of speech (4 to 25 s works best)", seconds)}
	switch {
	case seconds < 3:
		duration.Status, duration.Detail = "fail", fmt.Sprintf("Only %.1f s of speech; record at least 4 s", seconds)
	case seconds < 4:
		duration.Status, duration.Detail = "warn", fmt.Sprintf("%.1f s of speech; 4 to 25 s works best", seconds)
	case seconds > 25.2:
		duration.Status, duration.Detail = "fail", fmt.Sprintf("%.1f s of speech; keep it under 25 s", seconds)
	}
	quiet := Check{ID: "level", Label: "Volume", Status: "pass", Detail: fmt.Sprintf("Average level %.1f dBFS", level)}
	switch {
	case level <= -50:
		quiet.Status, quiet.Detail = "fail", fmt.Sprintf("Too quiet (%.1f dBFS); move closer to the microphone", level)
	case level <= -40:
		quiet.Status, quiet.Detail = "warn", fmt.Sprintf("Rather quiet (%.1f dBFS); move closer to the microphone", level)
	}
	clip := Check{ID: "clipping", Label: "Clipping", Status: "pass", Detail: fmt.Sprintf("%.2f%% of samples at full scale", ratio*100)}
	switch {
	case ratio >= 0.01:
		clip.Status, clip.Detail = "fail", fmt.Sprintf("%.1f%% of samples are clipped; speak softer or move back", ratio*100)
	case ratio >= 0.001:
		clip.Status, clip.Detail = "warn", fmt.Sprintf("%.2f%% of samples are clipped", ratio*100)
	}
	silence := Check{ID: "silence", Label: "Silence", Status: "pass", Detail: fmt.Sprintf("Trimmed %.1f s of leading and %.1f s of trailing silence", lead, trail)}
	switch {
	case first < 0:
		silence.Status, silence.Detail = "fail", "No speech was detected"
	case (lead+trail)/total > 0.5:
		silence.Status = "warn"
		silence.Detail += "; most of the recording was silence"
	}
	report.Checks = []Check{duration, quiet, clip, silence}
	return &Analysis{Report: report, Audio: spoken, Speech: first >= 0}, nil
}

func cleanTranscript(t string) (string, error) {
	t = strings.Join(strings.Fields(t), " ")
	if n := utf8.RuneCountInString(t); n < 1 || n > 500 {
		return "", errors.New("transcript must be 1 to 500 characters")
	}
	return t, nil
}

// PrepareClone validates a recording and its transcript for use as a reference
// and returns the WAV to store: speech only, mono 16-bit at the original rate.
func PrepareClone(raw []byte, transcript string) (wav []byte, text string, err error) {
	if text, err = cleanTranscript(transcript); err != nil {
		return nil, "", err
	}
	an, err := AnalyzeClone(raw)
	if err != nil {
		return nil, "", err
	}
	if failed := an.Failed(); len(failed) > 0 {
		return nil, "", fmt.Errorf("recording is not usable: %s", strings.Join(failed, "; "))
	}
	return speech.WAVAt(speech.PCMBytes(an.Audio.Samples), an.Audio.Rate), text, nil
}

// InlineClone builds the voice of an unsaved recording for a preview.
func InlineClone(raw []byte, transcript, direction string) (speech.Voice, error) {
	wav, text, err := PrepareClone(raw, transcript)
	if err != nil {
		return speech.Voice{}, err
	}
	return referenceVoice(&speech.Reference{AudioBase64: base64.StdEncoding.EncodeToString(wav), Text: text}, direction), nil
}

// referenceVoice is Clone mode, or Direction mode when a delivery instruction is set.
func referenceVoice(ref *speech.Reference, direction string) speech.Voice {
	v := speech.Voice{Reference: ref, Instruction: direction, Guidance: "1"}
	if direction != "" {
		v.Guidance = "4"
	}
	return v
}
