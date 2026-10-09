// Package voices holds the runtime-editable TTS voice catalogue, each persona's
// voice assignment, and the background renderer for per-voice filler cues.
package voices

import (
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
}
type CueStatus struct {
	State string `json:"state"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Error string `json:"error"`
}

type Snapshot struct {
	Revision uint64               `json:"revision"`
	Provider string               `json:"provider"`
	Voices   []Voice              `json:"voices"`
	Personas map[string]Persona   `json:"personas"`
	Cues     map[string]CueStatus `json:"cues"`
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
	designs   map[string]*designSample     // rendered sample of each design voice, by voice id
	presets   map[string]*speech.Reference // rendered sample of each builtin voice that streams from one, by voice id
}

// Open loads saved settings and removes render leftovers from a previous run.
func Open(path, cueDir string, agents map[string]*config.Agent, tts *speech.Client) (*Store, error) {
	s := &Store{path: path, cueDir: cueDir, tts: tts, agents: agents, builtin: presetVoices(), manifest: map[string][]byte{}, personas: map[string]Persona{}, jobs: map[string]*job{}, wake: make(chan struct{}, 1),
		cloneRoot: filepath.Join(filepath.Dir(cueDir), "voices"), clones: map[string]*speech.Reference{}, designs: map[string]*designSample{}, presets: map[string]*speech.Reference{}}
	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := agents[id]
		if raw, err := os.ReadFile(filepath.Join(a.Dir, "voice", "cues.json")); err == nil {
			s.manifest[id] = raw
		}
		if d := a.Voice.Default; d != "" && !s.hasVoice(nil, d) {
			slog.Warn("agent default voice is not a builtin voice; using the first builtin", "agent", id, "voice", d)
		}
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
		migrated := false
		for id, p := range d.Personas {
			if agents[id] == nil {
				// A persona can vanish with its agent; never fail startup over it.
				slog.Warn("dropping saved voice settings for unknown persona", "persona", id)
				continue
			}
			if strings.HasPrefix(p.VoiceID, legacyRefPrefix) {
				// The agent's own reference sample was a voice of the previous TTS
				// model. Move the persona to the agent's current default, with that
				// voice's delivery.
				slog.Info("moving persona from its retired reference voice to its default", "persona", id, "voice", p.VoiceID)
				p = s.defaultPersona(id)
				migrated = true
			}
			s.personas[id] = p
		}
		if err = s.validate(s.custom, s.personas, nil); err != nil {
			// A voice can vanish; fall back to the default.
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
		if migrated {
			// Persist the move so the browser sees a new revision. A failure only
			// means it is redone at the next start.
			out, merr := json.MarshalIndent(disk{Revision: s.revision + 1, Voices: s.custom, Personas: s.personas}, "", "  ")
			if merr == nil {
				merr = writeAtomic(path, out)
			}
			if merr != nil {
				slog.Warn("could not store migrated voice settings", "error", merr)
			}
			s.revision++
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

// legacyRefPrefix named the per-agent reference voices of the retired first TTS model.
const legacyRefPrefix = "ref-"

// defaultPersona is the agent's own voice (voice.default in agent.yaml) with its
// delivery style, or the first builtin voice when it names none.
func (s *Store) defaultPersona(id string) Persona {
	p := Persona{Direction: s.agents[id].Persona["voice_style"]}
	if d := s.agents[id].Voice.Default; d != "" && s.hasVoice(nil, d) {
		p.VoiceID = d
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
	if !customID.MatchString(id) || strings.HasPrefix(id, legacyRefPrefix) || strings.HasPrefix(id, presetPrefix) {
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
	out := Snapshot{Revision: s.revision, Provider: s.Provider(), Voices: append([]Voice{}, s.builtin...), Personas: map[string]Persona{}, Cues: map[string]CueStatus{}}
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
	// A design voice needs its reference sample before anything is persisted; the
	// render runs outside the lock so live speech never waits.
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

// Resolve returns the voice to synthesize with for the persona's next phrase. A
// nil store yields the zero voice, the offline default speaker.
func (s *Store) Resolve(agentID string) speech.Voice {
	if s == nil {
		return speech.Voice{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolve(s.effectiveVoiceID(agentID), s.personas[agentID].Direction)
}

// Preview resolves a draft: a saved voice or an unsaved design description,
// each with an optional delivery direction.
func (s *Store) Preview(voiceID, description, direction string) (speech.Voice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if voiceID == "" {
		// An unsaved description previews straight from the design model.
		return speech.Voice{Model: speech.ModelQwen3Design, Instruct: description}, nil
	}
	if !s.hasVoice(s.custom, voiceID) {
		return speech.Voice{}, fmt.Errorf("unknown voice %q", voiceID)
	}
	if s.unavailable(voiceID) {
		return speech.Voice{}, fmt.Errorf("the sample of voice %q is not available yet; save the voice again", voiceID)
	}
	return s.resolve(voiceID, direction), nil
}

// Current describes the persona's voice right now: its id, display name and the
// stored sample it is cloned from, which is nil while the voice still renders
// offline.
func (s *Store) Current(agentID string) (id, name string, ref *speech.Reference) {
	if s == nil {
		return "", "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id = s.effectiveVoiceID(agentID)
	for _, v := range s.builtin {
		if v.ID == id {
			name = v.Name
		}
	}
	for _, v := range s.custom {
		if v.ID == id {
			name = v.Name
		}
	}
	return id, name, s.resolve(id, "").Reference
}
