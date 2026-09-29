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
)

type Agent struct {
	Voice struct {
		ReferenceAudio string `yaml:"reference_audio" json:"reference_audio"`
		ReferenceText  string `yaml:"reference_text" json:"reference_text"`
	} `yaml:"voice" json:"voice"`
	Backend struct {
		URL    string `yaml:"url" json:"url,omitempty"`
		URLEnv string `yaml:"url_env" json:"url_env,omitempty"`
	} `yaml:"backend" json:"backend"`
	ID           string            `yaml:"id" json:"id"`
	Name         string            `yaml:"name" json:"name"`
	Organization string            `yaml:"organization" json:"organization"`
	Tenant       string            `yaml:"tenant" json:"tenant"`
	Role         string            `yaml:"role" json:"role"`
	Industry     string            `yaml:"industry" json:"industry"`
	Persona      map[string]string `yaml:"persona" json:"persona"`
	Memory       struct {
		Provider  string        `yaml:"provider" json:"provider"`
		Rules     []memory.Rule `yaml:"rules" json:"rules"`
		Namespace string        `yaml:"namespace" json:"namespace"`
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
