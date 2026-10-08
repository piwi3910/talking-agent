package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/telemetry"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// DefinitionSource is implemented by executors that discover tools at runtime.
// The runtime offers these tools in addition to the activated skills' tools.
type DefinitionSource interface {
	Definitions(ctx context.Context) []Definition
}

const (
	mcpPrefix        = "mcp"
	mcpMaxWireName   = 64
	mcpMaxSummary    = 8000
	mcpListTTL       = 5 * time.Minute
	mcpRetryBackoff  = 30 * time.Second
	mcpFirstListWait = 3 * time.Second
)

// MCP executes tools exposed by Model Context Protocol servers over Streamable
// HTTP. Tools are named mcp.<server>.<tool>; anything else goes to Fallback so
// the skills.yaml tools keep working through their HTTP executor.
type MCP struct {
	Fallback Executor

	servers []*mcpServer
	mu      sync.Mutex
	tools   map[string]mcpTool
	defs    []Definition
	loaded  time.Time
	retry   time.Time
	loading chan struct{}
}

type mcpTool struct {
	server *mcpServer
	name   string
	types  map[string]string // argument name -> JSON schema type
}

type mcpServer struct {
	name   string
	url    string
	client *http.Client
	allow  map[string]bool
	stale  func()

	mu      sync.Mutex
	session *mcp.ClientSession
}

// NewMCP builds an executor for one agent's MCP servers. Nothing is contacted
// until the first Definitions or Execute call (or Warm).
func NewMCP(servers []config.MCPServer, fallback Executor) (*MCP, error) {
	m := &MCP{Fallback: fallback, tools: map[string]mcpTool{}}
	for _, c := range servers {
		url := c.URL
		if c.URLEnv != "" && os.Getenv(c.URLEnv) != "" {
			url = os.Getenv(c.URLEnv)
		}
		hc, err := mcpHTTPClient(c.Auth)
		if err != nil {
			return nil, fmt.Errorf("mcp server %s: %w", c.Name, err)
		}
		s := &mcpServer{name: c.Name, url: url, client: hc, allow: map[string]bool{}, stale: m.invalidate}
		for _, t := range c.AllowTools {
			s.allow[t] = true
		}
		m.servers = append(m.servers, s)
	}
	return m, nil
}

type bearerTransport struct {
	base   http.RoundTripper
	source func() (string, error)
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tok, err := b.source()
	if err != nil {
		return nil, err
	}
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+tok)
	return b.base.RoundTrip(r)
}

func mcpHTTPClient(a config.MCPAuth) (*http.Client, error) {
	switch a.Type {
	case "", "none":
		return &http.Client{}, nil
	case "bearer":
		tok := os.Getenv(a.TokenEnv)
		if tok == "" {
			return nil, fmt.Errorf("environment variable %s is empty", a.TokenEnv)
		}
		return &http.Client{Transport: bearerTransport{base: http.DefaultTransport, source: func() (string, error) { return tok, nil }}}, nil
	case "client_credentials":
		secret := os.Getenv(a.ClientSecretEnv)
		if secret == "" {
			return nil, fmt.Errorf("environment variable %s is empty", a.ClientSecretEnv)
		}
		cc := clientcredentials.Config{ClientID: a.ClientID, ClientSecret: secret, TokenURL: a.TokenURL, Scopes: a.Scopes}
		// ReuseTokenSource caches the token and refreshes it before expiry.
		ts := oauth2.ReuseTokenSource(nil, cc.TokenSource(context.Background()))
		return &http.Client{Transport: bearerTransport{base: http.DefaultTransport, source: func() (string, error) {
			t, err := ts.Token()
			if err != nil {
				return "", err
			}
			return t.AccessToken, nil
		}}}, nil
	}
	return nil, fmt.Errorf("unknown auth type %q", a.Type)
}

func (s *mcpServer) connect(ctx context.Context) (*mcp.ClientSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		return s.session, nil
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "talking-agent", Version: "1.0.0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { s.stale() },
	})
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.url, HTTPClient: s.client}, nil)
	if err != nil {
		return nil, err
	}
	s.session = sess
	return sess, nil
}

func (s *mcpServer) drop(sess *mcp.ClientSession) {
	s.mu.Lock()
	if s.session == sess {
		s.session = nil
	}
	s.mu.Unlock()
	_ = sess.Close()
}

// Close ends all MCP sessions.
func (m *MCP) Close() {
	for _, s := range m.servers {
		s.mu.Lock()
		if s.session != nil {
			_ = s.session.Close()
			s.session = nil
		}
		s.mu.Unlock()
	}
}

func (m *MCP) invalidate() {
	m.mu.Lock()
	m.loaded = time.Time{}
	m.mu.Unlock()
}

// Warm discovers tools ahead of the first call so no caller waits for it.
func (m *MCP) Warm(ctx context.Context) { m.Definitions(ctx) }

// Definitions returns the allow-listed tools. The first call waits (bounded)
// for discovery; afterwards the cache is served and refreshed in the background
// so a slow or failing MCP server never adds latency to a turn.
func (m *MCP) Definitions(ctx context.Context) []Definition {
	m.mu.Lock()
	fresh := !m.loaded.IsZero() && time.Since(m.loaded) < mcpListTTL
	if fresh || time.Now().Before(m.retry) {
		defs := m.defs
		m.mu.Unlock()
		return defs
	}
	if m.loading == nil {
		m.loading = make(chan struct{})
		go m.refresh(m.loading)
	}
	done, have := m.loading, !m.loaded.IsZero()
	defs := m.defs
	m.mu.Unlock()
	if have {
		return defs
	}
	select {
	case <-done:
	case <-time.After(mcpFirstListWait):
	case <-ctx.Done():
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.defs
}

func (m *MCP) refresh(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	table := map[string]mcpTool{}
	var defs []Definition
	var failed bool
	for _, s := range m.servers {
		list, err := s.list(ctx)
		if err != nil {
			slog.Warn("MCP tool discovery failed", "server", s.name, "error", err)
			failed = true
			continue
		}
		for _, t := range list {
			d, types, ok := convertMCPTool(s.name, t)
			if !ok {
				slog.Warn("MCP tool skipped", "server", s.name, "tool", t.Name)
				continue
			}
			table[d.Name] = mcpTool{server: s, name: t.Name, types: types}
			defs = append(defs, d)
		}
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	m.mu.Lock()
	if !failed || len(defs) > 0 {
		m.tools, m.defs, m.loaded = table, defs, time.Now()
	}
	if failed {
		m.retry = time.Now().Add(mcpRetryBackoff)
	}
	m.loading = nil
	m.mu.Unlock()
	close(done)
}

func (s *mcpServer) list(ctx context.Context) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	for attempt := 0; attempt < 2; attempt++ {
		sess, err := s.connect(ctx)
		if err != nil {
			return nil, err
		}
		out = out[:0]
		err = nil
		for t, e := range sess.Tools(ctx, nil) {
			if e != nil {
				err = e
				break
			}
			if s.allow[t.Name] {
				out = append(out, t)
			}
		}
		if err == nil {
			return out, nil
		}
		s.drop(sess)
		if ctx.Err() != nil || attempt == 1 {
			return nil, err
		}
	}
	return nil, errors.New("unreachable")
}

var mcpNameReplacer = strings.NewReplacer(".", "-", " ", "-", "/", "-", ":", "-")

func convertMCPTool(server string, t *mcp.Tool) (Definition, map[string]string, bool) {
	name := mcpPrefix + "." + server + "." + mcpNameReplacer.Replace(t.Name)
	if strings.Contains(name, "__") || len(llmWire(name)) > mcpMaxWireName {
		return Definition{}, nil, false
	}
	schema, types := convertMCPSchema(t.InputSchema)
	desc := t.Description
	if desc == "" && t.Annotations != nil {
		desc = t.Annotations.Title
	}
	readOnly := t.Annotations != nil && t.Annotations.ReadOnlyHint
	return Definition{Name: name, Description: desc, Input: schema, Mutation: !readOnly, TimeoutMS: 10000}, types, true
}

func llmWire(n string) string { return strings.ReplaceAll(n, ".", "__") }

// convertMCPSchema maps a JSON Schema object onto the string-only schema the
// platform validates and gives the model. Non-string types are described in the
// property text and coerced back to their real type when the call is made.
func convertMCPSchema(in any) (Schema, map[string]string) {
	out := Schema{Type: "object", Properties: map[string]Property{}, Required: []string{}}
	types := map[string]string{}
	raw, _ := json.Marshal(in)
	var src struct {
		Properties map[string]struct {
			Type        any    `json:"type"`
			Description string `json:"description"`
			Enum        []any  `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(raw, &src) != nil {
		return out, types
	}
	for k, p := range src.Properties {
		typ := "string"
		switch v := p.Type.(type) {
		case string:
			typ = v
		case []any: // ["integer","null"]
			for _, e := range v {
				if s, ok := e.(string); ok && s != "null" {
					typ = s
					break
				}
			}
		}
		prop := Property{Type: "string", Description: p.Description}
		switch typ {
		case "integer", "number", "boolean":
			prop.Description = strings.TrimSpace(prop.Description + " (" + typ + " as text)")
		case "array", "object":
			prop.Description = strings.TrimSpace(prop.Description + " (" + typ + " as a JSON string)")
		default:
			typ = "string"
		}
		for _, e := range p.Enum {
			prop.Enum = append(prop.Enum, fmt.Sprint(e))
		}
		types[k] = typ
		out.Properties[k] = prop
	}
	for _, r := range src.Required {
		if _, ok := out.Properties[r]; ok {
			out.Required = append(out.Required, r)
		}
	}
	return out, types
}

func coerceMCPArgs(args map[string]string, types map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(args))
	for k, v := range args {
		typ := types[k]
		if typ != "string" && strings.TrimSpace(v) == "" {
			continue
		}
		switch typ {
		case "integer":
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("argument %s must be an integer", k)
			}
			out[k] = n
		case "number":
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("argument %s must be a number", k)
			}
			out[k] = f
		case "boolean":
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("argument %s must be true or false", k)
			}
			out[k] = b
		case "array", "object":
			var x any
			if err := json.Unmarshal([]byte(v), &x); err != nil {
				return nil, fmt.Errorf("argument %s must be valid JSON", k)
			}
			out[k] = x
		default:
			out[k] = v
		}
	}
	return out, nil
}

// Execute implements Executor.
func (m *MCP) Execute(ctx context.Context, d Definition, req Request, emit telemetry.Sink) (out Result) {
	m.mu.Lock()
	tool, ok := m.tools[d.Name]
	m.mu.Unlock()
	if !ok {
		if m.Fallback != nil && !strings.HasPrefix(d.Name, mcpPrefix+".") {
			return m.Fallback.Execute(ctx, d, req, emit)
		}
		return Failure("tool_not_allowed", "MCP tool is not available")
	}
	start := time.Now()
	emit("tool.started", map[string]any{"tool": d.Name})
	defer func() {
		kind := "tool.completed"
		if out.Error != nil {
			kind = "tool.failed"
		}
		emit(kind, map[string]any{"tool": d.Name, "duration_ms": time.Since(start).Milliseconds(), "result": out})
	}()
	args, err := coerceMCPArgs(req.Arguments, tool.types)
	if err != nil {
		return Failure("invalid_input", err.Error())
	}
	ms := d.TimeoutMS
	if ms <= 0 {
		ms = 10000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	defer cancel()
	var res *mcp.CallToolResult
	for attempt := 0; attempt < 2; attempt++ {
		sess, cerr := tool.server.connect(ctx)
		if cerr != nil {
			err = cerr
			break
		}
		if res, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: tool.name, Arguments: args}); err == nil {
			break
		}
		var rpc *jsonrpc.Error
		if errors.As(err, &rpc) { // the server answered; the session is healthy
			return Failure("tool_error", rpc.Message)
		}
		tool.server.drop(sess)
		// A dead session is retried once on a fresh one, but a mutation that
		// may already have been applied is never replayed.
		if ctx.Err() != nil || d.Mutation {
			break
		}
	}
	if err != nil {
		return Failure("backend_unavailable", err.Error())
	}
	return mapMCPResult(res)
}

func mapMCPResult(res *mcp.CallToolResult) Result {
	if res.NeedsInput() {
		return Failure("input_required", "The MCP server requested additional input, which is not supported")
	}
	var parts []string
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, v.Text)
		case *mcp.ImageContent:
			parts = append(parts, "[image omitted]")
		case *mcp.AudioContent:
			parts = append(parts, "[audio omitted]")
		default:
			parts = append(parts, "[unsupported content omitted]")
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			text = string(b)
		}
	}
	if len(text) > mcpMaxSummary {
		text = text[:mcpMaxSummary] + "…"
	}
	if res.IsError {
		if text == "" {
			text = "tool reported an error"
		}
		return Failure("tool_error", text)
	}
	if text == "" {
		text = "Tool completed with no output."
	}
	return Result{Summary: text, Records: []Record{}}
}
