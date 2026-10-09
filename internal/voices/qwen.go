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

// KindPreset marks a built-in voice: a Qwen3 CustomVoice speaker or a voice
// designed in code. Both are read-only and listed for every provider.
const KindPreset = "preset"

const presetPrefix = "preset-"

// ErrSynthesis marks a failed voice sample render; the save is not persisted.
var ErrSynthesis = errors.New("could not generate the voice sample")

// designSampleText is the fixed neutral sentence (about 10 s) rendered once per
// designed voice. It is also the reference transcript.
const designSampleText = "Hello, and thank you for calling today. I would be glad to help you with your account, answer any questions you might have, and make sure everything is taken care of before we finish."

// designTimeout bounds one design sample render in the save path.
var designTimeout = 30 * time.Second

// presetSampleText is the fixed sentence rendered once per builtin voice; it is
// the reference transcript every builtin clones from.
const presetSampleText = designSampleText

const designDescFile = "reference.desc"

// builtinVoice is a read-only voice that ships with the server. A voice with a
// speaker is a Qwen3 CustomVoice preset; one with a design is described in words
// and rendered once with the VoiceDesign model.
type builtinVoice struct {
	slug    string
	name    string // display name
	label   string // shown in brackets after the name
	speaker string // CustomVoice speaker
	design  string // VoiceDesign description
}

// builtinVoices are the voices every deployment offers. The designed ones give
// each shipped agent its own default (see voice.default in agent.yaml). Ryan and
// Aiden are native English presets; the other presets speak other languages.
var builtinVoices = []builtinVoice{
	{slug: "ryan", name: "Ryan", label: "preset, English", speaker: "Ryan"},
	{slug: "aiden", name: "Aiden", label: "preset, English", speaker: "Aiden"},
	{slug: "vivian", name: "Vivian", label: "preset, multilingual", speaker: "Vivian"},
	{slug: "serena", name: "Serena", label: "preset, multilingual", speaker: "Serena"},
	{slug: "uncle-fu", name: "Uncle Fu", label: "preset, multilingual", speaker: "Uncle_Fu"},
	{slug: "dylan", name: "Dylan", label: "preset, multilingual", speaker: "Dylan"},
	{slug: "eric", name: "Eric", label: "preset, multilingual", speaker: "Eric"},
	{slug: "ono-anna", name: "Ono Anna", label: "preset, multilingual", speaker: "Ono_Anna"},
	{slug: "sohee", name: "Sohee", label: "preset, multilingual", speaker: "Sohee"},
	{slug: "amelia", name: "Amelia", label: "designed, warm British", design: "A warm, calm British woman in her mid-thirties with a refined, British-educated accent. Soft, reassuring and unhurried, like a trusted school admissions officer."},
	{slug: "noor", name: "Noor", label: "designed, crisp and friendly", design: "A crisp, friendly young woman in her late twenties with a clear, polished international English accent. Bright, efficient and welcoming, with quick, precise pacing, like a front-desk receptionist."},
	{slug: "sophie", name: "Sophie", label: "designed, energetic", design: "An energetic, cheerful young woman in her early twenties with a lively British accent. Enthusiastic, upbeat and quick, smiling as she speaks, like an outgoing outreach coordinator."},
	{slug: "emma", name: "Emma", label: "designed, warm American", design: "A warm, kind American woman in her early forties with a native General American accent. Relaxed, patient and conversational, like a caring school coordinator."},
	{slug: "sara", name: "Sara", label: "designed, relaxed American", design: "A confident, easygoing American woman in her late twenties with a slightly husky, relaxed voice and a native General American accent. Friendly and upbeat, like a helpful support specialist."},
	{slug: "nova", name: "Nova", label: "designed, bright and professional", design: "A friendly, articulate woman in her early thirties with a clear, neutral international English accent. Bright, natural and professional, with a medium pitch and an even, conversational pace, like a capable presenter or news anchor. Warm but never breathy or flirtatious."},
}

func presetVoices() []Voice {
	out := make([]Voice, 0, len(builtinVoices))
	for _, b := range builtinVoices {
		out = append(out, Voice{ID: presetPrefix + b.slug, Name: fmt.Sprintf("%s (%s)", b.name, b.label), Builtin: true, Kind: KindPreset})
	}
	return out
}

func builtinByID(voiceID string) (builtinVoice, bool) {
	slug, ok := strings.CutPrefix(voiceID, presetPrefix)
	if !ok {
		return builtinVoice{}, false
	}
	for _, b := range builtinVoices {
		if b.slug == slug {
			return b, true
		}
	}
	return builtinVoice{}, false
}

// cloned reports whether the voice is streamed from a stored sample. Designed
// voices always are, since a description alone does not keep one speaker across
// phrases; presets are only under omni, where every voice is a clone.
func (s *Store) cloned(b builtinVoice) bool { return b.design != "" || s.tts.Omni() }

// designSample is the stored reference rendered from a design description.
type designSample struct {
	desc string
	ref  *speech.Reference
}

// Provider reports the TTS provider the store resolves voices for.
func (s *Store) Provider() string {
	if s != nil && s.tts.Omni() {
		return speech.ProviderOmni
	}
	return speech.ProviderQwen3
}

// resolve maps a voice to a Qwen3-TTS request. Direction only applies to presets
// that still render offline: a Base clone has no instruction support.
func (s *Store) resolve(voiceID, direction string) speech.Voice {
	if b, ok := builtinByID(voiceID); ok {
		// Until the stored sample exists a builtin renders offline through the
		// audio.cpp worker.
		if ref := s.presets[voiceID]; ref != nil {
			return baseVoice(ref)
		}
		if b.design != "" {
			return speech.Voice{Model: speech.ModelQwen3Design, Instruct: b.design}
		}
		return speech.Voice{Model: speech.ModelQwen3Custom, Speaker: b.speaker, Instruct: direction}
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
// of it, so switching provider re-renders every cue set.
func cueKeyQwen(v speech.Voice) string {
	h := sha256.New()
	fmt.Fprintf(h, "qwen3\x00%s\x00%s\x00%s", v.Model, v.Speaker, v.Instruct)
	if v.Reference != nil {
		fmt.Fprintf(h, "\x01%s\x00%s", v.Reference.AudioBase64, v.Reference.Text)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (s *Store) cueKey(v speech.Voice) string {
	if s.tts.Omni() {
		return "omni" + cueKeyQwen(v)[:12]
	}
	return cueKeyQwen(v)
}

// pending reports whether the voice is a builtin whose stored sample is still
// being rendered. Its cues wait for the sample, so they are rendered once, in the
// voice callers will hear. Callers hold the lock.
func (s *Store) pending(voiceID string) bool {
	b, ok := builtinByID(voiceID)
	return ok && s.cloned(b) && s.presets[voiceID] == nil
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
// voice of a save request that has none for its current description.
func (s *Store) prepareDesigns(in SaveRequest) (map[string]*freshSample, error) {
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
// matches. Missing or stale ones are regenerated by Start.
func (s *Store) loadDesigns() {
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

// loadPresets reads the stored sample of every builtin that streams from one:
// all of them under omni, the designed ones otherwise. A designed sample only
// counts while its description is the one it was rendered from. Missing ones are
// rendered by regenPresets.
func (s *Store) loadPresets() {
	for _, b := range builtinVoices {
		if !s.cloned(b) {
			continue
		}
		id := presetPrefix + b.slug
		dir := s.cloneDir(id)
		ref, err := speech.LoadReference(dir, cloneAudioFile, cloneTextFile)
		if err != nil || ref == nil {
			continue
		}
		if b.design != "" {
			if desc, err := os.ReadFile(filepath.Join(dir, designDescFile)); err != nil || string(desc) != b.design {
				continue
			}
		}
		s.presets[id] = ref
	}
}

// missingPresets lists builtin voice ids without a stored sample. Callers hold the lock.
func (s *Store) missingPresets() []string {
	var out []string
	for _, b := range builtinVoices {
		if id := presetPrefix + b.slug; s.cloned(b) && s.presets[id] == nil {
			out = append(out, id)
		}
	}
	return out
}

// presetRetry is how long regenPresets waits before trying the failed samples again.
var presetRetry = 30 * time.Second

// regenPresets renders each missing builtin sample once through the audio.cpp
// worker, never while a caller is being spoken to, and persists it. Until then
// the voice renders offline. Failed samples are retried every presetRetry, since
// the worker may simply not be up yet. It never fails startup.
func (s *Store) regenPresets(ctx context.Context, missing []string) {
	for len(missing) > 0 {
		var failed []string
		for _, id := range missing {
			if !s.renderPreset(ctx, id) {
				if ctx.Err() != nil {
					return
				}
				failed = append(failed, id)
			}
		}
		s.mu.Lock()
		s.enqueueMissing(false)
		s.mu.Unlock()
		missing = failed
		if len(missing) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(presetRetry):
		}
	}
}

// renderPreset renders and stores one builtin sample and reports success.
func (s *Store) renderPreset(ctx context.Context, id string) bool {
	b, ok := builtinByID(id)
	if !ok || s.tts.WaitIdle(ctx) != nil {
		return false
	}
	v := speech.Voice{Model: speech.ModelQwen3Custom, Speaker: b.speaker}
	var extra map[string]string
	if b.design != "" {
		v = speech.Voice{Model: speech.ModelQwen3Design, Instruct: b.design}
		extra = map[string]string{designDescFile: b.design}
	}
	rctx, cancel := context.WithTimeout(ctx, designTimeout)
	defer cancel()
	var pcm []byte
	err := s.tts.SynthesizeBackground(rctx, presetSampleText, v, func(p []byte) error { pcm = append(pcm, p...); return nil })
	if err != nil || len(pcm) == 0 || len(pcm)%2 != 0 {
		slog.Warn("could not render builtin voice sample; the voice stays offline for now", "voice", id, "error", err)
		return false
	}
	wav := speech.WAV(pcm)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeReference(id, wav, presetSampleText, extra); err != nil {
		slog.Warn("could not store builtin voice sample", "voice", id, "error", err)
	}
	s.presets[id] = &speech.Reference{AudioBase64: base64.StdEncoding.EncodeToString(wav), Text: presetSampleText}
	return true
}
