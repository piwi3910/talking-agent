// Package mcp attaches Model Context Protocol servers to agents. Connections,
// JSON-RPC framing and the Streamable HTTP transport come from go-ai-sdk; this
// package adds the registry, the legacy SSE transport, tool naming, per-agent
// access control and result shaping.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// MaskValue replaces literal header values in API responses. Sending it back
// unchanged on update keeps the stored value.
const MaskValue = "********"

// Transport selectors.
const (
	TransportAuto = "auto"
	TransportHTTP = "http"
	TransportSSE  = "sse"
)

// Server is one configured MCP server.
type Server struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Transport string            `json:"transport"`
	Headers   map[string]string `json:"headers,omitempty"`
	Enabled   bool              `json:"enabled"`
	// Agents lists agent ids allowed to use the server, or ["*"] for all.
	Agents     []string `json:"agents"`
	AllowTools []string `json:"allow_tools,omitempty"`
	DenyTools  []string `json:"deny_tools,omitempty"`
	// Deleted is only meaningful in the file: it hides a default entry.
	Deleted bool `json:"deleted,omitempty"`
	// Unsupported explains why a preset cannot be connected (for example stdio).
	Unsupported string `json:"unsupported,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
var headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Validate checks a server definition (not its reachability).
func (s *Server) Validate() error {
	if !idPattern.MatchString(s.ID) || strings.Contains(s.ID, "--") {
		return errors.New("id must be 1-40 lowercase letters, digits or single hyphens")
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 80 {
		return errors.New("name is required (max 80 characters)")
	}
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url must be an absolute http or https URL")
	}
	switch s.Transport {
	case "", TransportAuto, TransportHTTP, TransportSSE:
	default:
		return errors.New("transport must be auto, http or sse")
	}
	if len(s.Headers) > 20 {
		return errors.New("too many headers")
	}
	for k, v := range s.Headers {
		if !headerName.MatchString(k) {
			return fmt.Errorf("invalid header name %q", k)
		}
		if len(v) > 4096 || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("invalid value for header %s", k)
		}
		for _, m := range envRef.FindAllStringSubmatch(v, -1) {
			if !EnvAllowed(m[1]) {
				return fmt.Errorf("header %s references ${%s}, which is not an allowed variable (use MCP_* or add it to MCP_ENV_ALLOW)", k, m[1])
			}
		}
	}
	for _, a := range s.Agents {
		if strings.TrimSpace(a) == "" {
			return errors.New("agents must not contain empty ids")
		}
	}
	return nil
}

// EnvAllowed limits which environment variables a header may reference, so
// that a registry editor cannot send arbitrary process secrets to a server of
// their choosing.
func EnvAllowed(name string) bool {
	if strings.HasPrefix(name, "MCP_") || name == "ZAI_API_KEY" {
		return true
	}
	for _, n := range strings.Split(os.Getenv("MCP_ENV_ALLOW"), ",") {
		if strings.TrimSpace(n) == name {
			return true
		}
	}
	return false
}

// Expand resolves ${VAR} references in a header value. missing lists the
// variables that are unset or empty.
func Expand(value string) (out string, missing []string) {
	out = envRef.ReplaceAllStringFunc(value, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		if !EnvAllowed(name) || os.Getenv(name) == "" {
			missing = append(missing, name)
			return ""
		}
		return os.Getenv(name)
	})
	return out, missing
}

// Resolved returns the headers with environment references expanded and the
// names of any variables that could not be resolved.
func (s Server) Resolved() (headers map[string]string, missing []string) {
	headers = map[string]string{}
	for k, v := range s.Headers {
		x, m := Expand(v)
		headers[k] = x
		missing = append(missing, m...)
	}
	sort.Strings(missing)
	return headers, missing
}

// AllowsAgent reports whether the agent may use this server.
func (s Server) AllowsAgent(agentID string) bool {
	for _, a := range s.Agents {
		if a == "*" || a == agentID {
			return true
		}
	}
	return false
}

// AllowsTool applies the allow and deny lists to a raw server tool name.
func (s Server) AllowsTool(tool string) bool {
	for _, d := range s.DenyTools {
		if d == tool {
			return false
		}
	}
	if len(s.AllowTools) == 0 {
		return true
	}
	for _, a := range s.AllowTools {
		if a == tool {
			return true
		}
	}
	return false
}

// Masked returns a copy that is safe to return from the API: references to
// environment variables are visible, literal values are replaced by MaskValue.
func (s Server) Masked() Server {
	out := s
	out.Headers = map[string]string{}
	for k, v := range s.Headers {
		if envRef.MatchString(v) && !secretLooking(envRef.ReplaceAllString(v, "")) {
			out.Headers[k] = v
		} else {
			out.Headers[k] = MaskValue
		}
	}
	return out
}

// secretLooking reports whether text left after removing ${VAR} references
// could contain a credential. Only short scheme words such as "Bearer" are safe.
func secretLooking(rest string) bool {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return false
	}
	switch strings.ToLower(rest) {
	case "bearer", "basic", "token":
		return false
	}
	return true
}

// Registry persists server definitions: built-in and environment defaults
// overlaid by a JSON file that the API edits.
type Registry struct {
	mu       sync.Mutex
	path     string
	defaults []Server
}

// NewRegistry reads the file lazily. defaults are fixed entries (presets and
// environment-provided servers); the file overrides them by id.
func NewRegistry(path string, defaults []Server) *Registry {
	return &Registry{path: path, defaults: defaults}
}

func (r *Registry) readFile() (map[string]Server, []string, error) {
	out := map[string]Server{}
	if r.path == "" {
		return out, nil, nil
	}
	raw, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Servers []Server `json:"servers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("mcp servers file: %w", err)
	}
	order := []string{}
	for _, s := range doc.Servers {
		out[s.ID] = s
		order = append(order, s.ID)
	}
	return out, order, nil
}

func (r *Registry) writeFile(m map[string]Server) error {
	if r.path == "" {
		return errors.New("no MCP servers file configured")
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	doc := struct {
		Servers []Server `json:"servers"`
	}{Servers: []Server{}}
	for _, id := range ids {
		doc.Servers = append(doc.Servers, m[id])
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".mcp-servers-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}

func normalize(s *Server) {
	if s.Transport == "" {
		s.Transport = TransportAuto
	}
	if len(s.Agents) == 0 {
		s.Agents = []string{}
	}
}

// List returns the effective servers sorted by id.
func (r *Registry) List() ([]Server, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.list()
}

func (r *Registry) list() ([]Server, error) {
	file, _, err := r.readFile()
	if err != nil {
		return nil, err
	}
	byID := map[string]Server{}
	for _, d := range r.defaults {
		byID[d.ID] = d
	}
	for id, s := range file {
		if s.Deleted {
			delete(byID, id)
			continue
		}
		// A preset that cannot be connected keeps that note when overridden.
		if d, ok := byID[id]; ok && d.Unsupported != "" {
			s.Unsupported = d.Unsupported
		}
		byID[id] = s
	}
	out := make([]Server, 0, len(byID))
	for _, s := range byID {
		normalize(&s)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get returns one effective server.
func (r *Registry) Get(id string) (Server, bool) {
	list, err := r.List()
	if err != nil {
		return Server{}, false
	}
	for _, s := range list {
		if s.ID == id {
			return s, true
		}
	}
	return Server{}, false
}

// Create adds a server; the id must not exist.
func (r *Registry) Create(s Server) error {
	normalize(&s)
	if err := s.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	list, err := r.list()
	if err != nil {
		return err
	}
	for _, e := range list {
		if e.ID == s.ID {
			return ErrExists
		}
	}
	file, _, err := r.readFile()
	if err != nil {
		return err
	}
	s.Deleted = false
	file[s.ID] = s
	return r.writeFile(file)
}

// Update replaces a server. Header values equal to MaskValue keep the stored
// value of the same header.
func (r *Registry) Update(id string, s Server) error {
	s.ID = id
	normalize(&s)
	r.mu.Lock()
	defer r.mu.Unlock()
	list, err := r.list()
	if err != nil {
		return err
	}
	var old *Server
	for i := range list {
		if list[i].ID == id {
			old = &list[i]
		}
	}
	if old == nil {
		return ErrNotFound
	}
	headers := map[string]string{}
	for k, v := range s.Headers {
		if v == MaskValue {
			if prev, ok := old.Headers[k]; ok {
				v = prev
			} else {
				return fmt.Errorf("header %s has no stored value to keep", k)
			}
		}
		headers[k] = v
	}
	s.Headers = headers
	s.Unsupported = ""
	if err := s.Validate(); err != nil {
		return err
	}
	file, _, err := r.readFile()
	if err != nil {
		return err
	}
	s.Deleted = false
	file[id] = s
	return r.writeFile(file)
}

// Delete removes a server. Defaults are hidden with a tombstone.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list, err := r.list()
	if err != nil {
		return err
	}
	found := false
	for _, e := range list {
		found = found || e.ID == id
	}
	if !found {
		return ErrNotFound
	}
	file, _, err := r.readFile()
	if err != nil {
		return err
	}
	isDefault := false
	for _, d := range r.defaults {
		isDefault = isDefault || d.ID == id
	}
	if isDefault {
		file[id] = Server{ID: id, Deleted: true}
	} else {
		delete(file, id)
	}
	return r.writeFile(file)
}

// Errors returned by the registry.
var (
	ErrExists   = errors.New("a server with this id already exists")
	ErrNotFound = errors.New("server not found")
)
