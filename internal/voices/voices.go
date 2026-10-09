// Package voices holds the runtime-editable TTS voice catalogue, each persona's
// voice assignment, and the background renderer for per-voice filler cues.
package voices

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/speech"
)

var ErrConflict = errors.New("voice settings changed; reload before saving")

// ErrStorage marks failures to persist settings. Its wrapped cause may contain
// file paths, so callers log it and show only a generic message.
var ErrStorage = errors.New("could not save voice settings")

var customID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

const (
	maxCustom = 50 // designed voices
	maxClones = 20
)

// Voice kinds. Builtin keeps the legacy Builtin flag for compatibility.
const (
	KindBuiltin = "builtin"
	KindDesign  = "design"
	KindClone   = "clone"
)

type Voice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Builtin     bool   `json:"builtin"`
	Kind        string `json:"kind"`
	Unavailable bool   `json:"unavailable,omitempty"` // a designed voice whose sample is not rendered yet
}
type Persona struct {
	VoiceID   string `json:"voice_id"`
	Direction string `json:"direction"`
	Events    bool   `json:"events"`
}
type CueStatus struct {
	State string `json:"state"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Error string `json:"error"`
}

// Capabilities tells the UI what the active TTS model supports.
type Capabilities struct {
	VocalEvents bool `json:"vocal_events"`
}
type Snapshot struct {
	Revision     uint64               `json:"revision"`
	Provider     string               `json:"provider"`
	Capabilities Capabilities         `json:"capabilities"`
	Voices       []Voice              `json:"voices"`
	Personas     map[string]Persona   `json:"personas"`
	Cues         map[string]CueStatus `json:"cues"`
}

// Custom is the editable part of a voice; builtin voices are derived at startup.
// Kind is empty or "design" for a designed voice, which needs a description,
// and "clone" for a cloned voice, which has none: its reference audio lives in
// its own directory and can only be created through CreateClone.
type Custom struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind,omitempty"`
}
type SaveRequest struct {
	Revision uint64             `json:"revision"`
	Voices   []Custom           `json:"voices"`
	Personas map[string]Persona `json:"personas"`
}
type disk struct {
	Revision uint64             `json:"revision"`
	Voices   []Custom           `json:"voices"`
	Personas map[string]Persona `json:"personas"`
}

// Store never changes memory unless the atomic write succeeded. Phrases resolve
// their voice from it on every call, so a save applies from the next phrase.
type Store struct {
	mu       sync.RWMutex
	path     string
	cueDir   string
	tts      *speech.Client
	agents   map[string]*config.Agent
	refs     map[string]*speech.Reference
	builtin  []Voice
	manifest map[string][]byte // baked cues.json per agent
	revision uint64
	custom   []Custom
	personas map[string]Persona
	jobs     map[string]*job
	queue    []string
	wake     chan struct{}

	cloneRoot string                       // <root>/<id>/reference.wav|txt, a sibling of the cue dir
	clones    map[string]*speech.Reference // loaded clone references by voice id
	designs   map[string]*designSample     // qwen3: rendered sample of each design voice, by voice id
	presets   map[string]*speech.Reference // omni: rendered sample of each preset voice, by voice id
}

// Open loads saved settings and removes render leftovers from a previous run.
// refs holds each agent's reference voice (nil entries are agents without one).
func Open(path, cueDir string, agents map[string]*config.Agent, refs map[string]*speech.Reference, tts *speech.Client) (*Store, error) {
	s := &Store{path: path, cueDir: cueDir, tts: tts, agents: agents, refs: refs, manifest: map[string][]byte{}, personas: map[string]Persona{}, jobs: map[string]*job{}, wake: make(chan struct{}, 1),
		cloneRoot: filepath.Join(filepath.Dir(cueDir), "voices"), clones: map[string]*speech.Reference{}, designs: map[string]*designSample{}, presets: map[string]*speech.Reference{}}
	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := agents[id]
		if refs[id] != nil {
			name := a.Persona["name"]
			if name == "" {
				name = a.Name
			}
			s.builtin = append(s.builtin, Voice{ID: "ref-" + id, Name: name + " (original)", Builtin: true, Kind: KindBuiltin})
		}
		if raw, err := os.ReadFile(filepath.Join(a.Dir, "voice", "cues.json")); err == nil {
			s.manifest[id] = raw
		}
	}
	if tts.Qwen3() {
		// Preset speakers only exist for Qwen3-TTS CustomVoice.
		s.builtin = append(s.builtin, presetVoices()...)
	}
	for _, id := range ids {
		s.personas[id] = s.defaultPersona(id)
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		var d disk
		if err = json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("voice settings: %w", err)
		}
		s.revision, s.custom = d.Revision, s.loadClones(d.Voices)
		for id, p := range d.Personas {
			if agents[id] == nil {
				// A persona can vanish with its agent; never fail startup over it.
				slog.Warn("dropping saved voice settings for unknown persona", "persona", id)
				continue
			}
			s.personas[id] = p
		}
		if err = s.validate(s.custom, s.personas, nil); err != nil {
			// A builtin voice can vanish when a reference is removed; fall back to the default.
			for id, p := range s.personas {
				if !s.hasVoice(s.custom, p.VoiceID) {
					slog.Warn("saved voice does not exist; using the default", "persona", id, "voice", p.VoiceID)
					p.VoiceID = s.defaultPersona(id).VoiceID
					s.personas[id] = p
				}
			}
			if err = s.validate(s.custom, s.personas, nil); err != nil {
				return nil, fmt.Errorf("voice settings: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.loadDesigns()
	s.loadPresets()
	// Temp and aside dirs only exist when a render was interrupted; they are never visible as a set.
	for _, pattern := range []string{".tmp-*", ".old-*"} {
		leftovers, _ := filepath.Glob(filepath.Join(cueDir, "*", pattern))
		for _, d := range leftovers {
			os.RemoveAll(d)
		}
	}
	// A clone being stored when the process stopped never reached its final name.
	leftovers, _ := filepath.Glob(filepath.Join(s.cloneRoot, ".tmp-*"))
	for _, d := range leftovers {
		os.RemoveAll(d)
	}
	return s, nil
}
func (s *Store) defaultPersona(id string) Persona {
	p := Persona{Direction: s.agents[id].Persona["voice_style"], Events: true}
	if s.refs[id] != nil {
		p.VoiceID = "ref-" + id
	} else if len(s.builtin) > 0 {
		p.VoiceID = s.builtin[0].ID
	}
	return p
}
func (s *Store) hasVoice(custom []Custom, id string) bool {
	for _, v := range s.builtin {
		if v.ID == id {
			return true
		}
	}
	for _, v := range custom {
		if v.ID == id {
			return true
		}
	}
	return false
}

// validate checks a candidate state. previous lists the custom voices that
// exist today so a still-assigned deletion gets a precise message.
func (s *Store) validate(custom []Custom, personas map[string]Persona, previous []Custom) error {
	seen := map[string]bool{}
	designs, clones := 0, 0
	for _, v := range custom {
		if err := validID(v.ID); err != nil {
			return err
		}
		if seen[v.ID] {
			return fmt.Errorf("duplicate voice id %s", v.ID)
		}
		seen[v.ID] = true
		if err := validName(v.ID, v.Name); err != nil {
			return err
		}
		switch v.Kind {
		case KindClone:
			if v.Description != "" {
				return fmt.Errorf("voice %s: a cloned voice has no description", v.ID)
			}
			clones++
		case "", KindDesign:
			if n := utf8.RuneCountInString(v.Description); n < 1 || n > 500 || n != utf8.RuneCountInString(strings.TrimSpace(v.Description)) {
				return fmt.Errorf("voice %s: description must be 1 to 500 characters", v.ID)
			}
			designs++
		default:
			return fmt.Errorf("voice %s: unknown kind %q", v.ID, v.Kind)
		}
	}
	if designs > maxCustom {
		return fmt.Errorf("at most %d custom voices are allowed", maxCustom)
	}
	if clones > maxClones {
		return fmt.Errorf("at most %d cloned voices are allowed", maxClones)
	}
	for id := range s.agents {
		p, ok := personas[id]
		if !ok {
			return fmt.Errorf("missing voice settings for %s", id)
		}
		if utf8.RuneCountInString(p.Direction) > 500 {
			return fmt.Errorf("direction for %s must be at most 500 characters", id)
		}
		if p.VoiceID == "" && len(s.builtin)+len(custom) == 0 {
			continue
		}
		if !s.hasVoice(custom, p.VoiceID) {
			for _, old := range previous {
				if old.ID == p.VoiceID {
					return fmt.Errorf("voice %s is still assigned to %s; choose another voice first", p.VoiceID, id)
				}
			}
			return fmt.Errorf("unknown voice %q for %s", p.VoiceID, id)
		}
	}
	for id := range personas {
		if s.agents[id] == nil {
			return fmt.Errorf("unknown persona %s", id)
		}
	}
	return nil
}

func validID(id string) error {
	if !customID.MatchString(id) || strings.HasPrefix(id, "ref-") || strings.HasPrefix(id, presetPrefix) {
		return fmt.Errorf("invalid voice id %q: use lowercase letters, digits and hyphens, up to 40 characters, not starting with ref- or preset-", id)
	}
	return nil
}
func validName(id, name string) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > 60 || n != utf8.RuneCountInString(strings.TrimSpace(name)) {
		return fmt.Errorf("voice %s: name must be 1 to 60 characters", id)
	}
	return nil
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot()
}
func (s *Store) snapshot() Snapshot {
	out := Snapshot{Revision: s.revision, Provider: s.Provider(), Capabilities: Capabilities{VocalEvents: !s.qwen()}, Voices: append([]Voice{}, s.builtin...), Personas: map[string]Persona{}, Cues: map[string]CueStatus{}}
	for _, v := range s.custom {
		kind := KindDesign
		if v.Kind == KindClone {
			kind = KindClone
		}
		out.Voices = append(out.Voices, Voice{ID: v.ID, Name: v.Name, Description: v.Description, Kind: kind, Unavailable: s.unavailable(v.ID)})
	}
	for id, p := range s.personas {
		out.Personas[id] = p
		out.Cues[id] = s.status(id)
	}
	return out
}

// Save validates, persists atomically, then publishes and queues cue renders.
func (s *Store) Save(in SaveRequest) (Snapshot, error) {
	// Under qwen3 a design voice needs its reference sample before anything is
	// persisted; the render runs outside the lock so live speech never waits.
	fresh, err := s.prepareDesigns(in)
	if err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Revision != s.revision {
		return Snapshot{}, ErrConflict
	}
	custom := make([]Custom, len(in.Voices))
	for i, v := range in.Voices {
		custom[i] = Custom{ID: v.ID, Name: strings.TrimSpace(v.Name), Description: strings.TrimSpace(v.Description), Kind: v.Kind}
		if v.Kind == KindDesign {
			custom[i].Kind = ""
		}
	}
	if err := s.checkKinds(custom); err != nil {
		return Snapshot{}, err
	}
	personas := map[string]Persona{}
	for id, p := range in.Personas {
		p.Direction = strings.TrimSpace(p.Direction)
		personas[id] = p
	}
	if err := s.validate(custom, personas, s.custom); err != nil {
		return Snapshot{}, err
	}
	raw, err := json.MarshalIndent(disk{Revision: s.revision + 1, Voices: custom, Personas: personas}, "", "  ")
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrStorage, err)
	}
	if err = writeAtomic(s.path, raw); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrStorage, err)
	}
	s.revision++
	s.custom, s.personas = custom, personas
	s.dropRemovedClones(custom)
	s.adoptDesigns(custom, fresh)
	s.enqueueMissing(true)
	return s.snapshot(), nil
}
func writeAtomic(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".voice-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Resolve returns the voice to synthesize with for the persona's next phrase and
// whether inline vocal events are allowed. A nil store yields the plain default.
func (s *Store) Resolve(agentID string) (speech.Voice, bool) {
	if s == nil {
		return speech.Voice{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.personas[agentID]
	// Qwen3-TTS does not understand inline vocal events, whatever the setting.
	return s.resolve(s.effectiveVoiceID(agentID), p.Direction), p.Events && !s.qwen()
}

// Preview resolves a draft: a saved voice or an unsaved design description,
// each with an optional delivery direction.
func (s *Store) Preview(voiceID, description, direction string) (speech.Voice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if voiceID == "" {
		if s.qwen() {
			// An unsaved description previews straight from the design model.
			return speech.Voice{Model: speech.ModelQwen3Design, Instruct: description}, nil
		}
		return designVoice(description, direction), nil
	}
	if !s.hasVoice(s.custom, voiceID) {
		return speech.Voice{}, fmt.Errorf("unknown voice %q", voiceID)
	}
	if s.unavailable(voiceID) {
		return speech.Voice{}, fmt.Errorf("the sample of voice %q is not available yet; save the voice again", voiceID)
	}
	return s.resolve(voiceID, direction), nil
}
func (s *Store) resolve(voiceID, direction string) speech.Voice {
	if s.qwen() {
		return s.resolveQwen(voiceID, direction)
	}
	if agent, ok := strings.CutPrefix(voiceID, "ref-"); ok && s.refs[agent] != nil {
		return referenceVoice(s.refs[agent], direction)
	}
	if ref := s.clones[voiceID]; ref != nil {
		return referenceVoice(ref, direction)
	}
	for _, c := range s.custom {
		if c.ID == voiceID && c.Kind != KindClone {
			return designVoice(c.Description, direction)
		}
	}
	return speech.Voice{Instruction: direction}
}
func designVoice(description, direction string) speech.Voice {
	instruction := description
	if direction != "" {
		instruction += " " + direction
	}
	return speech.Voice{Instruction: instruction, Guidance: "4"}
}

// Reference returns the persona's own builtin reference voice, or nil.
func (s *Store) Reference(agentID string) *speech.Reference {
	if s == nil {
		return nil
	}
	return s.refs[agentID]
}

// PromptAddendum is the system prompt text for personas with vocal events enabled.
func (s *Store) PromptAddendum(agentID string) string {
	if _, events := s.Resolve(agentID); events {
		return speech.EventsPrompt
	}
	return ""
}

// cueKey identifies the audio a voice produces, so edits of any part re-render.
func cueKey(v speech.Voice) string {
	h := sha256.New()
	if v.Reference != nil {
		fmt.Fprintf(h, "%s\x00%s", v.Reference.AudioBase64, v.Reference.Text)
	}
	fmt.Fprintf(h, "\x01%s\x01%s", v.Instruction, v.Guidance)
	return hex.EncodeToString(h.Sum(nil))[:16]
}
