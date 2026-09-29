package skills

import (
	"bytes"
	"enterprise-ai-demo/internal/tools"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
)

type Skill struct {
	ID           string             `yaml:"id" json:"id"`
	Description  string             `yaml:"description" json:"description"`
	Instructions string             `yaml:"instructions" json:"instructions"`
	Keywords     []string           `yaml:"keywords" json:"keywords"`
	Tools        []tools.Definition `yaml:"tools" json:"tools"`
}
type Catalog map[string]Skill

func Load(dir string) (Catalog, error) {
	b, e := os.ReadFile(filepath.Join(dir, "skills.yaml"))
	if e != nil {
		return nil, e
	}
	var list []Skill
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if e = decoder.Decode(&list); e != nil {
		return nil, e
	}
	c := Catalog{}
	names := map[string]bool{}
	for _, s := range list {
		if s.ID == "" {
			return nil, fmt.Errorf("empty skill id")
		}
		if _, ok := c[s.ID]; ok {
			return nil, fmt.Errorf("duplicate skill %s", s.ID)
		}
		for _, t := range s.Tools {
			if t.Name == "" || names[t.Name] || strings.Contains(t.Name, "__") {
				return nil, fmt.Errorf("invalid/duplicate tool %s", t.Name)
			}
			names[t.Name] = true
			if t.ResponseMode != "" && (t.ResponseMode != "records" || t.Mutation) {
				return nil, fmt.Errorf("%s has invalid response_mode for a read tool", t.Name)
			}
			if t.Input.Type != "object" || t.Input.AdditionalProperties {
				return nil, fmt.Errorf("%s requires closed object input", t.Name)
			}
			for k, p := range t.Input.Properties {
				if p.Type != "string" {
					return nil, fmt.Errorf("%s.%s must be a string in Phase 1", t.Name, k)
				}
			}
		}
		c[s.ID] = s
	}
	return c, nil
}

// Skills are exposed as capabilities first. Detailed tools are loaded only on activation.
func (c Catalog) Select(ids []string) (Catalog, error) {
	out := Catalog{}
	for _, id := range ids {
		s, ok := c[id]
		if !ok {
			return nil, fmt.Errorf("unknown skill %s", id)
		}
		out[id] = s
	}
	return out, nil
}
