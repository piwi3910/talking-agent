package config

import (
	"bytes"
	"enterprise-ai-demo/internal/memory"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // zone data for minimal container images
)

type Agent struct {
	Voice struct {
		// Default is the id of the builtin voice this agent speaks with until
		// someone assigns another, for example "preset-amelia".
		Default string `yaml:"default" json:"default"`
	} `yaml:"voice" json:"voice"`
	Backend struct {
		URL    string `yaml:"url" json:"url,omitempty"`
		URLEnv string `yaml:"url_env" json:"url_env,omitempty"`
	} `yaml:"backend" json:"backend"`
	ID           string `yaml:"id" json:"id"`
	Name         string `yaml:"name" json:"name"`
	Organization string `yaml:"organization" json:"organization"`
	Tenant       string `yaml:"tenant" json:"tenant"`
	// Timezone is the IANA zone of the organisation (for example "Asia/Dubai").
	// Scheduling tools publish times in it and the agent speaks in it.
	Timezone string   `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	Role     string   `yaml:"role" json:"role"`
	Scope    []string `yaml:"scope" json:"scope,omitempty"` // topics the role-scope policy allows
	// ScopePolicy opts an agent out of the shared role-scope policy with
	// `scope_policy: false` (an open, general-purpose agent). Unset means on.
	ScopePolicy *bool `yaml:"scope_policy,omitempty" json:"scope_policy,omitempty"`

	Industry string            `yaml:"industry" json:"industry"`
	Persona  map[string]string `yaml:"persona" json:"persona"`
	Memory   struct {
		Provider  string        `yaml:"provider" json:"-"`
		Rules     []memory.Rule `yaml:"rules" json:"rules"`
		Namespace string        `yaml:"namespace" json:"namespace"`
		Domain    string        `yaml:"domain" json:"domain,omitempty"`

		// AutoCapture opts in to a background LLM extraction of durable facts
		// the person stated, after each normal conversational turn.
		AutoCapture bool `yaml:"auto_capture" json:"auto_capture,omitempty"`
	} `yaml:"memory" json:"memory"`
	Skills    []string `yaml:"skills" json:"skills"`
	Knowledge []string `yaml:"knowledge" json:"knowledge"`
	Safety    struct {
		Emergency        []string `yaml:"emergency" json:"emergency"`
		Response         string   `yaml:"response" json:"response"`
		Clinical         []string `yaml:"clinical" json:"clinical"`
		ClinicalResponse string   `yaml:"clinical_response" json:"clinical_response"`
	} `yaml:"safety" json:"safety"`
	Future   map[string]map[string]bool `yaml:"future" json:"future"`
	Branding map[string]string          `yaml:"branding" json:"branding"`
	Prompt   string                     `yaml:"-" json:"-"`
	Dir      string                     `yaml:"-" json:"-"`
}

var memoryDomain = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ScopeEnforced reports whether the shared role-scope policy applies.
func (a *Agent) ScopeEnforced() bool { return a.ScopePolicy == nil || *a.ScopePolicy }

// Location is the organisation's time zone, or UTC when none is configured.
func (a *Agent) Location() *time.Location {
	if l, err := time.LoadLocation(a.Timezone); err == nil && a.Timezone != "" {
		return l
	}
	return time.UTC
}

// MemoryDomain is the domain used in memory scopes: the configured shared
// domain, or the agent ID when none is set.
func (a *Agent) MemoryDomain() string {
	if a.Memory.Domain != "" {
		return a.Memory.Domain
	}
	return a.ID
}

func Load(root string) (map[string]*Agent, error) {
	paths, err := filepath.Glob(filepath.Join(root, "*", "agent.yaml"))
	if err != nil {
		return nil, err
	}
	out := map[string]*Agent{}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, e
		}
		var a Agent
		decoder := yaml.NewDecoder(bytes.NewReader(b))
		decoder.KnownFields(true)
		if e = decoder.Decode(&a); e != nil {
			return nil, fmt.Errorf("%s: %w", p, e)
		}
		if a.ID == "" || a.Tenant == "" || a.Organization == "" || a.Memory.Namespace == "" || len(a.Skills) == 0 {
			return nil, fmt.Errorf("%s: missing identity, namespace or skills", p)
		}
		if _, ok := out[a.ID]; ok {
			return nil, fmt.Errorf("duplicate agent %s", a.ID)
		}
		if a.Timezone != "" {
			if _, e := time.LoadLocation(a.Timezone); e != nil {
				return nil, fmt.Errorf("%s: invalid timezone %q", p, a.Timezone)
			}
		}
		if a.Memory.Domain != "" && !memoryDomain.MatchString(a.Memory.Domain) {
			return nil, fmt.Errorf("%s: invalid memory domain %q", p, a.Memory.Domain)
		}
		for _, rule := range a.Memory.Rules {
			if _, e := regexp.Compile(rule.Pattern); e != nil {
				return nil, fmt.Errorf("%s: invalid memory rule: %w", p, e)
			}
		}
		a.Dir = filepath.Dir(p)
		b, e = os.ReadFile(filepath.Join(a.Dir, "prompt.md"))
		if e != nil {
			return nil, e
		}
		a.Prompt = string(b)
		for _, k := range a.Knowledge {
			if filepath.IsAbs(k) || strings.Contains(k, "..") {
				return nil, fmt.Errorf("invalid knowledge path %s", k)
			}
		}
		out[a.ID] = &a
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no agents found in %s", root)
	}
	return out, nil
}
