package memory

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Scope struct {
	Tenant       string `json:"tenant"`
	Organization string `json:"organization"`
	Domain       string `json:"domain"`
	Namespace    string `json:"namespace"`
	User         string `json:"user"`
}
type Memory struct {
	Scope   Scope     `json:"scope"`
	Text    string    `json:"text"`
	Tags    []string  `json:"tags"`
	Created time.Time `json:"created"`
}
type RetrieveRequest struct {
	Scope Scope
	Query string
	Limit int
}
type Provider interface {
	Retrieve(context.Context, RetrieveRequest) ([]Memory, error)
	Store(context.Context, Memory) error
	Name() string
}

var ErrNotConfigured = errors.New("NovaMem is not configured: supply an adapter implementing the documented NovaMem interface")

// No NovaMem wire protocol is assumed. Inject an adapter only once its real contract is available.
type NovaMemProvider struct{ Adapter Provider }

func (n NovaMemProvider) Name() string { return "novamem" }
func (n NovaMemProvider) Retrieve(c context.Context, r RetrieveRequest) ([]Memory, error) {
	if n.Adapter == nil {
		return nil, ErrNotConfigured
	}
	return n.Adapter.Retrieve(c, r)
}
func (n NovaMemProvider) Store(c context.Context, m Memory) error {
	if n.Adapter == nil {
		return ErrNotConfigured
	}
	return n.Adapter.Store(c, m)
}

type InMemoryProvider struct {
	mu    sync.RWMutex
	items map[Scope][]Memory
}

func New() *InMemoryProvider           { return &InMemoryProvider{items: map[Scope][]Memory{}} }
func (*InMemoryProvider) Name() string { return "in-memory (development; process lifetime)" }
func (p *InMemoryProvider) Store(ctx context.Context, m Memory) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.Scope.Tenant == "" || m.Scope.Organization == "" || m.Scope.Domain == "" || m.Scope.Namespace == "" || m.Scope.User == "" {
		return errors.New("incomplete memory scope")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, old := range p.items[m.Scope] {
		if old.Text == m.Text {
			return nil
		}
	}
	m.Created = time.Now().UTC()
	p.items[m.Scope] = append(p.items[m.Scope], m)
	return nil
}
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}
func (p *InMemoryProvider) Retrieve(ctx context.Context, r RetrieveRequest) ([]Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	type scored struct {
		m Memory
		n int
	}
	rank := []scored{}
	for _, m := range p.items[r.Scope] {
		n := 0
		hay := " " + strings.Join(words(m.Text+" "+strings.Join(m.Tags, " ")), " ") + " "
		for _, w := range words(r.Query) {
			if len(w) > 2 && strings.Contains(hay, " "+w+" ") {
				n++
			}
		}
		if n > 0 {
			rank = append(rank, scored{m, n})
		}
	}
	sort.SliceStable(rank, func(i, j int) bool { return rank[i].n > rank[j].n })
	limit := r.Limit
	if limit <= 0 || limit > 5 {
		limit = 5
	}
	out := []Memory{}
	for i, x := range rank {
		if i >= limit {
			break
		}
		out = append(out, x.m)
	}
	return out, nil
}

// Rule defines an allowlisted service fact; agent configuration owns domain-specific wording.
type Rule struct {
	Pattern string   `yaml:"pattern" json:"pattern"`
	Text    string   `yaml:"text" json:"text"`
	Tags    []string `yaml:"tags" json:"tags"`
}

func Extract(scope Scope, text string, rules []Rule) []Memory {
	out := []Memory{}
	for _, rule := range rules {
		pattern, err := regexp.Compile(rule.Pattern)
		if err == nil && pattern.MatchString(text) {
			out = append(out, Memory{Scope: scope, Text: rule.Text, Tags: rule.Tags})
		}
	}
	return out
}
