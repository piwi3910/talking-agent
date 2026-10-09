package voices

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"enterprise-ai-demo/internal/speech"
)

// KindPreset marks a Qwen3 CustomVoice speaker: builtin, read-only, qwen3 only.
const KindPreset = "preset"

const presetPrefix = "preset-"

// ErrSynthesis marks a failed voice sample render; the save is not persisted.
var ErrSynthesis = errors.New("could not generate the voice sample")

// designSampleText is the fixed neutral sentence (about 10 s) rendered once per
// designed voice. It is also the reference transcript.
const designSampleText = "Hello, and thank you for calling today. I would be glad to help you with your account, answer any questions you might have, and make sure everything is taken care of before we finish."

// designTimeout bounds one design sample render in the save path.
var designTimeout = 30 * time.Second

const designDescFile = "reference.desc"

// presetSpeakers are the Qwen3 CustomVoice speakers. Ryan and Aiden are native
// English; the others speak other languages.
var presetSpeakers = []struct{ slug, speaker, label string }{
	{"ryan", "Ryan", "English"},
	{"aiden", "Aiden", "English"},
	{"vivian", "Vivian", "multilingual"},
	{"serena", "Serena", "multilingual"},
	{"uncle-fu", "Uncle_Fu", "multilingual"},
	{"dylan", "Dylan", "multilingual"},
	{"eric", "Eric", "multilingual"},
	{"ono-anna", "Ono_Anna", "multilingual"},
	{"sohee", "Sohee", "multilingual"},
}

func presetVoices() []Voice {
	out := make([]Voice, 0, len(presetSpeakers))
	for _, p := range presetSpeakers {
		name := strings.ReplaceAll(p.speaker, "_", " ")
		out = append(out, Voice{ID: presetPrefix + p.slug, Name: fmt.Sprintf("%s (preset, %s)", name, p.label), Builtin: true, Kind: KindPreset})
	}
	return out
}

func presetSpeaker(voiceID string) (string, bool) {
	slug, ok := strings.CutPrefix(voiceID, presetPrefix)
	if !ok {
		return "", false
	}
	for _, p := range presetSpeakers {
		if p.slug == slug {
			return p.speaker, true
		}
	}
	return "", false
}

// designSample is the stored reference rendered from a design description.
type designSample struct {
	desc string
	ref  *speech.Reference
}

// Provider reports the TTS provider the store resolves voices for.
func (s *Store) Provider() string {
	if s != nil && s.tts.Qwen3() {
		return speech.ProviderQwen3
	}
	return speech.ProviderBreeze
}

func (s *Store) qwen() bool { return s.tts.Qwen3() }

// resolveQwen maps a voice to a Qwen3-TTS request. Direction only applies to
// presets: Base has no instruction support.
func (s *Store) resolveQwen(voiceID, direction string) speech.Voice {
	if agent, ok := strings.CutPrefix(voiceID, "ref-"); ok && s.refs[agent] != nil {
		return baseVoice(s.refs[agent])
	}
	if speaker, ok := presetSpeaker(voiceID); ok {
		return speech.Voice{Model: speech.ModelQwen3Custom, Speaker: speaker, Instruct: direction}
	}
	if ref := s.clones[voiceID]; ref != nil {
		return baseVoice(ref)
	}
	if ref := s.designReference(voiceID); ref != nil {
		return baseVoice(ref)
	}
	return speech.Voice{Model: speech.ModelQwen3Custom, Speaker: speech.DefaultQwen3Speaker, Instruct: direction}
}

func baseVoice(ref *speech.Reference) speech.Voice {
	return speech.Voice{Model: speech.ModelQwen3Base, Reference: ref}
}

// designReference returns the stored sample of a design voice whose description
// still matches it, or nil. Callers hold the lock.
func (s *Store) designReference(id string) *speech.Reference {
	d := s.designs[id]
	if d == nil {
		return nil
	}
	for _, c := range s.custom {
		if c.ID == id && c.Kind != KindClone {
			if c.Description == d.desc {
				return d.ref
			}
			return nil
		}
	}
	return nil
}

// unavailable reports whether a voice cannot be used right now: under qwen3, a
// design voice whose sample is missing. Callers hold the lock.
func (s *Store) unavailable(voiceID string) bool {
	if !s.qwen() {
		return false
	}
	for _, c := range s.custom {
		if c.ID == voiceID && c.Kind != KindClone {
			return s.designReference(voiceID) == nil
		}
	}
	return false
}

// effectiveVoiceID is the persona's voice, or its default while that voice is
// unavailable. Callers hold the lock.
func (s *Store) effectiveVoiceID(agentID string) string {
	id := s.personas[agentID].VoiceID
	if s.unavailable(id) {
		return s.defaultPersona(agentID).VoiceID
	}
	return id
}

// cueKeyQwen identifies the audio a Qwen3 voice produces. The provider is part
// of it, so switching provider re-renders every non-original cue set.
func cueKeyQwen(v speech.Voice) string {
	h := sha256.New()
	fmt.Fprintf(h, "qwen3\x00%s\x00%s\x00%s", v.Model, v.Speaker, v.Instruct)
	if v.Reference != nil {
		fmt.Fprintf(h, "\x01%s\x00%s", v.Reference.AudioBase64, v.Reference.Text)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (s *Store) cueKey(v speech.Voice) string {
	if s.qwen() {
		return cueKeyQwen(v)
	}
	return cueKey(v)
}

// renderDesignSample renders the fixed sentence with the design model and
// returns it as a WAV reference. It does not touch the store.
func (s *Store) renderDesignSample(ctx context.Context, description string) (*speech.Reference, []byte, error) {
	if !s.tts.Enabled() {
		return nil, nil, fmt.Errorf("speech is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, designTimeout)
	defer cancel()
	var pcm []byte
	err := s.tts.Synthesize(ctx, designSampleText, speech.Voice{Model: speech.ModelQwen3Design, Instruct: description}, func(b []byte) error { pcm = append(pcm, b...); return nil })
	if err != nil {
		return nil, nil, err
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil, nil, fmt.Errorf("invalid design sample audio")
	}
	wav := speech.WAV(pcm)
	return &speech.Reference{AudioBase64: base64.StdEncoding.EncodeToString(wav), Text: designSampleText}, wav, nil
}

// freshSample is a rendered design sample waiting to be stored.
type freshSample struct {
	designSample
	wav []byte
}

// prepareDesigns renders, outside the store lock, a sample for every design
// voice of a save request that has none for its current description. Under
// breeze it does nothing.
func (s *Store) prepareDesigns(in SaveRequest) (map[string]*freshSample, error) {
	if !s.qwen() {
		return nil, nil
	}
	s.mu.RLock()
	if in.Revision != s.revision {
		s.mu.RUnlock()
		return nil, ErrConflict
	}
	type todo struct{ id, desc string }
	var need []todo
	for _, v := range in.Voices {
		desc := strings.TrimSpace(v.Description)
		if (v.Kind != "" && v.Kind != KindDesign) || validID(v.ID) != nil || desc == "" || len([]rune(desc)) > 500 {
			continue // validation reports it
		}
		if d := s.designs[v.ID]; d != nil && d.desc == desc {
			continue
		}
		need = append(need, todo{v.ID, desc})
	}
	s.mu.RUnlock()
	out := map[string]*freshSample{}
	for _, n := range need {
		ref, wav, err := s.renderDesignSample(context.Background(), n.desc)
		if err != nil {
			slog.Warn("voice design sample render failed", "voice", n.id, "error", err)
			return nil, fmt.Errorf("%w for %q: %s", ErrSynthesis, n.id, trimError(err))
		}
		out[n.id] = &freshSample{designSample{desc: n.desc, ref: ref}, wav}
	}
	return out, nil
}

// adoptDesigns publishes fresh samples, stores their files and drops the
// samples of removed voices. A file failure only costs a lazy re-render at the
// next start. Callers hold the write lock.
func (s *Store) adoptDesigns(kept []Custom, fresh map[string]*freshSample) {
	keep := map[string]bool{}
	for _, v := range kept {
		keep[v.ID] = true
	}
	for id := range s.designs {
		if !keep[id] || s.designs[id] == nil {
			delete(s.designs, id)
			if !s.hasClone(id) {
				if err := os.RemoveAll(s.cloneDir(id)); err != nil {
					slog.Warn("could not remove design sample", "voice", id, "error", err)
				}
			}
		}
	}
	for id, f := range fresh {
		if !keep[id] {
			continue
		}
		s.designs[id] = &designSample{desc: f.desc, ref: f.ref}
		if err := s.writeReference(id, f.wav, f.ref.Text, map[string]string{designDescFile: f.desc}); err != nil {
			slog.Warn("could not store design sample", "voice", id, "error", err)
		}
	}
}

func (s *Store) hasClone(id string) bool { _, ok := s.clones[id]; return ok }

// loadDesigns reads the stored samples of design voices whose description still
// matches. Missing or stale ones are regenerated by Start. Qwen3 only.
func (s *Store) loadDesigns() {
	if !s.qwen() {
		return
	}
	for _, v := range s.custom {
		if v.Kind == KindClone || validID(v.ID) != nil {
			continue
		}
		dir := s.cloneDir(v.ID)
		ref, err := speech.LoadReference(dir, cloneAudioFile, cloneTextFile)
		desc, derr := os.ReadFile(filepath.Join(dir, designDescFile))
		if err != nil || ref == nil || derr != nil || string(desc) != v.Description {
			slog.Info("design voice sample missing; it will be regenerated", "voice", v.ID)
			continue
		}
		s.designs[v.ID] = &designSample{desc: v.Description, ref: ref}
	}
}

// missingDesigns lists design voices without a usable sample. Callers hold the lock.
func (s *Store) missingDesigns() []Custom {
	var out []Custom
	if !s.qwen() {
		return nil
	}
	for _, v := range s.custom {
		if v.Kind != KindClone && s.designReference(v.ID) == nil {
			out = append(out, v)
		}
	}
	return out
}

// regenDesigns renders missing samples in the background, then queues the cue
// sets that were waiting for them. It never fails startup.
func (s *Store) regenDesigns(ctx context.Context, missing []Custom) {
	for _, v := range missing {
		if ctx.Err() != nil {
			return
		}
		ref, wav, err := s.renderDesignSample(ctx, v.Description)
		if err != nil {
			slog.Warn("could not regenerate design voice sample; the voice stays unavailable", "voice", v.ID, "error", err)
			continue
		}
		s.mu.Lock()
		still := false
		for _, c := range s.custom {
			if c.ID == v.ID && c.Description == v.Description && c.Kind != KindClone {
				still = true
			}
		}
		if still {
			s.designs[v.ID] = &designSample{desc: v.Description, ref: ref}
			if err := s.writeReference(v.ID, wav, ref.Text, map[string]string{designDescFile: v.Description}); err != nil {
				slog.Warn("could not store design sample", "voice", v.ID, "error", err)
			}
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.enqueueMissing(false)
	s.mu.Unlock()
}
