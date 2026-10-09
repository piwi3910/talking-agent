package telephony

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

	"enterprise-ai-demo/internal/config"
)

var ErrSettingsConflict = errors.New("settings changed; reload before saving")
var phoneNumber = regexp.MustCompile(`^\+?[0-9]{1,20}$`)

type PersonaSettings struct {
	Numbers []string `json:"numbers"`
	UserID  string   `json:"user_id"`
	Cues    bool     `json:"cues"`
}
type PhoneSettings struct {
	Revision uint64                     `json:"revision"`
	Personas map[string]PersonaSettings `json:"personas"`
	Enabled  bool                       `json:"enabled"`
}
type diskPersona struct {
	Numbers []string `json:"numbers"`
	UserID  string   `json:"user_id"`
	Cues    bool     `json:"cues"`
}
type diskSettings struct {
	Revision uint64                 `json:"revision"`
	Personas map[string]diskPersona `json:"personas"`
}

// Settings publishes routes only after a durable atomic save succeeds.
// Active calls retain the persona/account snapshot taken when they arrived.
type Settings struct {
	mu      sync.RWMutex
	path    string
	agents  map[string]*config.Agent
	state   PhoneSettings
	enabled bool
}

func OpenSettings(path string, agents map[string]*config.Agent, numbers map[string]string) (*Settings, error) {
	s := &Settings{path: path, agents: agents, state: PhoneSettings{Personas: map[string]PersonaSettings{}}}
	for id := range agents {
		s.state.Personas[id] = PersonaSettings{Numbers: []string{}, Cues: true}
	}
	for n, id := range numbers {
		p, ok := s.state.Personas[id]
		if !ok {
			return nil, fmt.Errorf("unknown phone persona %s", id)
		}
		p.Numbers = append(p.Numbers, n)
		sort.Strings(p.Numbers)
		s.state.Personas[id] = p
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err = s.validate(s.state); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var d diskSettings
	if err = json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	for id, p := range d.Personas {
		if agents[id] == nil {
			// A persona can vanish with its agent; never fail startup over it.
			slog.Warn("dropping saved phone settings for unknown persona", "persona", id)
			continue
		}
		s.state.Personas[id] = PersonaSettings{Numbers: p.Numbers, UserID: p.UserID, Cues: p.Cues}
	}
	s.state.Revision = d.Revision
	if err = s.validate(s.state); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Settings) SetEnabled(v bool) { s.mu.Lock(); defer s.mu.Unlock(); s.enabled = v }
func (s *Settings) Snapshot() PhoneSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := PhoneSettings{Revision: s.state.Revision, Enabled: s.enabled, Personas: map[string]PersonaSettings{}}
	for id, p := range s.state.Personas {
		p.Numbers = append([]string{}, p.Numbers...)
		out.Personas[id] = p
	}
	return out
}
func (s *Settings) Route(number string) (*config.Agent, PersonaSettings) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, p := range s.state.Personas {
		for _, n := range p.Numbers {
			if n == number {
				p.Numbers = append([]string{}, p.Numbers...)
				return s.agents[id], p
			}
		}
	}
	return nil, PersonaSettings{}
}
func (s *Settings) validate(state PhoneSettings) error {
	seen := map[string]string{}
	for id, p := range state.Personas {
		if s.agents[id] == nil {
			return fmt.Errorf("unknown persona %s", id)
		}
		if len(p.Numbers) > 100 {
			return fmt.Errorf("too many phone numbers for %s", id)
		}
		for _, n := range p.Numbers {
			if !phoneNumber.MatchString(n) {
				return fmt.Errorf("invalid phone number %q: use digits with an optional leading +", n)
			}
			if other, ok := seen[n]; ok {
				return fmt.Errorf("number %s is already assigned to %s", n, other)
			}
			seen[n] = id
		}
	}
	return nil
}
func (s *Settings) Save(in PhoneSettings) (PhoneSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Revision != s.state.Revision {
		return PhoneSettings{}, ErrSettingsConflict
	}
	next := PhoneSettings{Revision: s.state.Revision + 1, Personas: map[string]PersonaSettings{}}
	for id := range s.agents {
		p, ok := in.Personas[id]
		if !ok {
			return PhoneSettings{}, fmt.Errorf("missing phone settings for %s", id)
		}
		p.Numbers = append([]string{}, p.Numbers...)
		for i := range p.Numbers {
			p.Numbers[i] = strings.TrimSpace(p.Numbers[i])
		}
		p.UserID = strings.TrimSpace(p.UserID)
		next.Personas[id] = p
	}
	for id := range in.Personas {
		if s.agents[id] == nil {
			return PhoneSettings{}, fmt.Errorf("unknown persona %s", id)
		}
	}
	if err := s.validate(next); err != nil {
		return PhoneSettings{}, err
	}
	disk := diskSettings{Revision: next.Revision, Personas: map[string]diskPersona{}}
	for id, p := range next.Personas {
		disk.Personas[id] = diskPersona{Numbers: p.Numbers, UserID: p.UserID, Cues: p.Cues}
	}
	raw, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return PhoneSettings{}, err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return PhoneSettings{}, fmt.Errorf("phone settings storage unavailable: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".phone-settings-*")
	if err != nil {
		return PhoneSettings{}, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return PhoneSettings{}, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return PhoneSettings{}, err
	}
	if err = f.Close(); err != nil {
		return PhoneSettings{}, err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return PhoneSettings{}, err
	}
	s.state = next
	out := PhoneSettings{Revision: next.Revision, Personas: map[string]PersonaSettings{}, Enabled: s.enabled}
	for id, p := range next.Personas {
		p.Numbers = append([]string{}, p.Numbers...)
		out.Personas[id] = p
	}
	return out, nil
}
