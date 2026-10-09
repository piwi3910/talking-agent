package memory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	novamem "github.com/azrtydxb/novamem/clients/go"
)

const novaSource = "enterprise-ai-demo/v1"

// ScopedCredential is deployment-owned. It is never returned by the frontend API.
// Each complete scope gets its own NovaMem account: namespaces alone are not ACLs.
type ScopedCredential struct {
	Scope Scope  `json:"scope"`
	Token string `json:"token"`
}

type NovaMemConfig struct {
	// Nil uses 0.5; calibrate for the deployed embedding model.
	MinVectorScore *float64
	BaseURL        string
	Credentials    []ScopedCredential
	HTTPClient     *http.Client
	Timeout        time.Duration
}

type novaAdapter struct {
	clients        map[Scope]*novamem.Client
	minVectorScore float64
}

func validateScope(s Scope) error {
	for _, value := range []string{s.Tenant, s.Organization, s.Domain, s.Namespace, s.User} {
		if strings.TrimSpace(value) == "" || len(value) > 512 {
			return errors.New("incomplete or oversized memory scope")
		}
	}
	return nil
}

// ScopeKey uses length-prefixed UTF-8 fields to avoid delimiter collisions.
// The same versioned algorithm is used by the deployment provisioning script.
func ScopeKey(s Scope) string {
	h := sha256.New()
	for _, value := range []string{s.Tenant, s.Organization, s.Domain, s.Namespace, s.User} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(value)))
		h.Write(size[:])
		h.Write([]byte(value))
	}
	return "enterprise-demo-v1-" + hex.EncodeToString(h.Sum(nil))
}

func LoadNovaMemCredentials(path string) ([]ScopedCredential, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read NovaMem credential file: %w", err)
	}
	if len(raw) > 1<<20 {
		return nil, errors.New("NovaMem credential file too large")
	}
	var out []ScopedCredential
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("invalid NovaMem credential file")
	}
	return out, nil
}

func NewNovaMem(cfg NovaMemConfig) (NovaMemProvider, error) {
	if cfg.BaseURL == "" || len(cfg.Credentials) == 0 {
		return NovaMemProvider{}, ErrNotConfigured
	}
	minScore := 0.5
	if cfg.MinVectorScore != nil {
		minScore = *cfg.MinVectorScore
	}
	if math.IsNaN(minScore) || minScore < 0 || minScore > 1 {
		return NovaMemProvider{}, errors.New("NovaMem minimum vector score must be 0–1")
	}
	a := &novaAdapter{clients: map[Scope]*novamem.Client{}, minVectorScore: minScore}
	tokens := map[string]bool{}
	for _, credential := range cfg.Credentials {
		if err := validateScope(credential.Scope); err != nil {
			return NovaMemProvider{}, err
		}
		if strings.TrimSpace(credential.Token) == "" {
			return NovaMemProvider{}, errors.New("empty NovaMem scoped token")
		}
		if a.clients[credential.Scope] != nil || tokens[credential.Token] {
			return NovaMemProvider{}, errors.New("duplicate NovaMem scope or reused token")
		}
		c, err := novamem.New(novamem.Config{BaseURL: cfg.BaseURL, Token: credential.Token, HTTPClient: cfg.HTTPClient, Timeout: cfg.Timeout})
		if err != nil {
			return NovaMemProvider{}, err
		}
		a.clients[credential.Scope] = c
		tokens[credential.Token] = true
	}
	return NovaMemProvider{Adapter: a}, nil
}

func (*novaAdapter) Name() string { return "novamem" }
func (a *novaAdapter) client(scope Scope) (*novamem.Client, error) {
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	c := a.clients[scope]
	if c == nil {
		return nil, errors.New("NovaMem identity is not provisioned for this memory scope")
	}
	return c, nil
}

type novaMetadata struct {
	Version int       `json:"version"`
	Scope   Scope     `json:"scope"`
	Tags    []string  `json:"tags"`
	Created time.Time `json:"created"`
}

func (a *novaAdapter) Store(ctx context.Context, m Memory) error {
	c, err := a.client(m.Scope)
	if err != nil {
		return err
	}
	if strings.TrimSpace(m.Text) == "" {
		return errors.New("empty memory fact")
	}
	if m.Created.IsZero() {
		m.Created = time.Now().UTC()
	}
	// Facts are already allowlisted by agent rules; do not send transcripts through
	// NovaMem extraction. Remember's content-hash dedup makes seed replay idempotent.
	result, err := c.Remember(ctx, novamem.CaptureRequest{Content: m.Text, Namespace: ScopeKey(m.Scope), Source: novaSource,
		Sensitivity: novamem.Sensitivity("internal"), SourceType: "service_fact",
		Metadata: map[string]any{"enterprise_demo": novaMetadata{1, m.Scope, m.Tags, m.Created}}})
	if err != nil {
		return err
	}
	if !result.Saved() {
		return errors.New("NovaMem did not acknowledge storing the memory")
	}
	return nil
}

func (a *novaAdapter) Retrieve(ctx context.Context, r RetrieveRequest) ([]Memory, error) {
	c, err := a.client(r.Scope)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Query) == "" {
		return []Memory{}, nil
	}
	limit := r.Limit
	if limit <= 0 || limit > 5 {
		limit = 5
	}
	keyword, vector, zero := 0.35, 0.65, 0.0
	results, err := c.Search(ctx, novamem.SearchRequest{Query: r.Query, K: limit, Namespace: ScopeKey(r.Scope),
		Weights: &novamem.Weights{Keyword: &keyword, Vector: &vector, Graph: &zero, Recency: &zero, Entity: &zero}})
	if err != nil {
		return nil, err
	}
	// A partial store view is not a claim that we recalled all relevant facts.
	if results.Degraded {
		return nil, errors.New("NovaMem retrieval is degraded")
	}
	out := []Memory{}
	for _, entry := range results.Entries {
		if entry.Namespace != ScopeKey(r.Scope) || entry.Project != nil || entry.Source != novaSource {
			return nil, errors.New("NovaMem returned a memory outside the requested scope or format")
		}
		// NovaMem derives typed facts ("[event] …") from stored content without copying
		// our metadata. Account and namespace already bind them to this scope; metadata,
		// when present, must still name it.
		var meta novaMetadata
		if tagged, ok := entry.Metadata["enterprise_demo"]; ok {
			raw, err := json.Marshal(tagged)
			if err != nil || json.Unmarshal(raw, &meta) != nil || meta.Version != 1 || meta.Scope != r.Scope {
				return nil, errors.New("NovaMem returned a memory outside the requested scope or format")
			}
		}
		if entry.Score <= 0 || strings.TrimSpace(entry.Content) == "" || entry.Signals == nil || (entry.Signals.Keyword <= 0 && entry.Signals.Vector < a.minVectorScore) {
			continue
		}
		if len(out) < limit {
			out = append(out, Memory{Scope: r.Scope, Text: entry.Content, Tags: meta.Tags, Created: meta.Created})
		}
	}
	return out, nil
}
