package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"enterprise-ai-demo/internal/logx"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sdk "github.com/azrtydxb/go-ai-sdk/mcp"
)

// Limits shaping what reaches the model.
const (
	MaxResultRunes      = 500
	maxDescriptionRunes = 800
	maxWireName         = 64
)

// Tool is a server tool offered to the model as mcp.<server>.<tool>.
type Tool struct {
	Name        string          `json:"name"` // mcp.<server>.<tool>
	Server      string          `json:"server"`
	Tool        string          `json:"tool"` // the server's own name
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Status describes the last known state of a server.
type Status struct {
	State     string    `json:"state"` // ok, error, disabled, unconfigured, unsupported, unknown
	Error     string    `json:"error,omitempty"`
	Tools     int       `json:"tools"`
	Transport string    `json:"transport,omitempty"` // negotiated: http or sse
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

type conn struct {
	fp        string
	client    *sdk.Client
	transport string
	tools     []Tool
	fetched   time.Time
	failedAt  time.Time
	err       string
}

type indexedTool struct {
	server Server
	tool   Tool
	conn   *conn
}

// Manager keeps one connection per enabled server and caches its tool list.
type Manager struct {
	Registry    *Registry
	HTTP        *http.Client
	ToolTTL     time.Duration // how long a tool list is trusted; default 5 minutes
	FailureTTL  time.Duration // how long a failure suppresses reconnects; default 30 seconds
	CallTimeout time.Duration // per tools/call; default 30 seconds
	ConnTimeout time.Duration // connect + initialize + list; default 10 seconds

	mu         sync.Mutex
	conns      map[string]*conn
	locks      map[string]*sync.Mutex
	toolsIndex map[string]indexedTool
}

func (m *Manager) ttl() time.Duration  { return durOr(m.ToolTTL, 5*time.Minute) }
func (m *Manager) fttl() time.Duration { return durOr(m.FailureTTL, 30*time.Second) }
func (m *Manager) callTimeout() time.Duration {
	return durOr(m.CallTimeout, 30*time.Second)
}
func (m *Manager) connTimeout() time.Duration { return durOr(m.ConnTimeout, 10*time.Second) }
func durOr(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
func (m *Manager) httpClient() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return sseHTTPClient()
}

// fingerprint changes when anything affecting the connection changes.
func fingerprint(s Server, headers map[string]string) string {
	h := sha256.New()
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprint(h, s.URL, "|", s.Transport, "|", strings.Join(s.AllowTools, ","), "|", strings.Join(s.DenyTools, ","))
	for _, k := range keys {
		fmt.Fprint(h, "|", k, "=", headers[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *Manager) lock(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks == nil {
		m.locks = map[string]*sync.Mutex{}
	}
	if m.locks[id] == nil {
		m.locks[id] = &sync.Mutex{}
	}
	return m.locks[id]
}

// scrub removes credential values from an error message.
func scrub(msg string, headers map[string]string) string {
	msg = logx.Redact(msg)
	for _, v := range headers {
		if len(v) >= 6 {
			msg = strings.ReplaceAll(msg, v, "***")
		}
		if i := strings.LastIndex(v, " "); i > 0 && len(v)-i-1 >= 6 {
			msg = strings.ReplaceAll(msg, v[i+1:], "***")
		}
	}
	if len(msg) > 400 {
		msg = msg[:400] + "..."
	}
	return msg
}

func (m *Manager) open(ctx context.Context, s Server, headers map[string]string) (*sdk.Client, string, error) {
	try := func(kind string) (*sdk.Client, error) {
		var tr sdk.Transport
		if kind == TransportSSE {
			t, err := newSSETransport(ctx, s.URL, headers, m.httpClient())
			if err != nil {
				return nil, err
			}
			tr = t
		} else {
			tr = sdk.NewStreamableHTTPTransport(s.URL, headers)
		}
		c := sdk.NewClient(tr)
		if err := c.Initialize(ctx); err != nil {
			_ = c.Close()
			return nil, err
		}
		return c, nil
	}
	switch s.Transport {
	case TransportSSE:
		c, err := try(TransportSSE)
		return c, TransportSSE, err
	case TransportHTTP:
		c, err := try(TransportHTTP)
		return c, TransportHTTP, err
	}
	c, err := try(TransportHTTP)
	if err == nil {
		return c, TransportHTTP, nil
	}
	// Legacy servers answer the Streamable HTTP POST with 400, 404 or 405.
	if ctx.Err() == nil && legacyStatus(err) {
		if c2, err2 := try(TransportSSE); err2 == nil {
			return c2, TransportSSE, nil
		}
	}
	return nil, "", err
}

var wireChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
var multiUnderscore = regexp.MustCompile(`_{2,}`)

// ToolName builds the model-facing name mcp.<server>.<tool>. Characters
// outside [A-Za-z0-9_-] become underscores and runs of underscores collapse,
// because the wire format turns "." into "__".
func ToolName(server, tool string) string {
	t := multiUnderscore.ReplaceAllString(wireChars.ReplaceAllString(tool, "_"), "_")
	name := "mcp." + server + "." + t
	if wire := strings.ReplaceAll(name, ".", "__"); len(wire) > maxWireName {
		sum := sha256.Sum256([]byte(tool))
		keep := maxWireName - len("mcp__"+server+"__") - 9
		if keep < 4 {
			keep = 4
		}
		if keep < len(t) {
			t = t[:keep]
		}
		name = "mcp." + server + "." + t + "_" + hex.EncodeToString(sum[:4])
	}
	return name
}

func convert(s Server, defs []sdk.ToolDef) []Tool {
	out := []Tool{}
	seen := map[string]bool{}
	for _, d := range defs {
		if !s.AllowsTool(d.Name) {
			continue
		}
		name := ToolName(s.ID, d.Name)
		if seen[name] {
			continue
		}
		seen[name] = true
		schema := d.InputSchema
		var probe struct {
			Type string `json:"type"`
		}
		if len(schema) == 0 || json.Unmarshal(schema, &probe) != nil || probe.Type != "object" {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		desc := truncateRunes(strings.TrimSpace(d.Description), maxDescriptionRunes)
		if desc == "" {
			desc = d.Name
		}
		out = append(out, Tool{Name: name, Server: s.ID, Tool: d.Name, Description: desc + " (via " + s.Name + ")", Schema: schema})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

// connect (re)establishes the connection and tool list for s. The caller
// holds the per-server lock.
func (m *Manager) connect(ctx context.Context, s Server, headers map[string]string) (*conn, error) {
	ctx, cancel := context.WithTimeout(ctx, m.connTimeout())
	defer cancel()
	client, kind, err := m.open(ctx, s, headers)
	if err != nil {
		return nil, err
	}
	defs, err := client.ListTools(ctx)
	var capErr *sdk.CapabilityError
	if errors.As(err, &capErr) {
		defs, err = nil, nil
	}
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &conn{fp: fingerprint(s, headers), client: client, transport: kind, tools: convert(s, defs), fetched: time.Now()}, nil
}

func (m *Manager) drop(id string) {
	m.mu.Lock()
	c := m.conns[id]
	delete(m.conns, id)
	for key, item := range m.toolsIndex {
		if item.server.ID == id {
			delete(m.toolsIndex, key)
		}
	}
	m.mu.Unlock()
	if c != nil && c.client != nil {
		go func() { _ = c.client.Close() }()
	}
}

func (m *Manager) unindexServer(id string) {
	m.mu.Lock()
	for key, item := range m.toolsIndex {
		if item.server.ID == id {
			delete(m.toolsIndex, key)
		}
	}
	m.mu.Unlock()
}

// ensure returns a usable connection, refreshing an expired tool list.
// force bypasses the failure back-off and the cache.
func (m *Manager) ensure(ctx context.Context, s Server, force bool) (*conn, error) {
	headers, missing := s.Resolved()
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing credential: %s is not set", strings.Join(missing, ", "))
	}
	l := m.lock(s.ID)
	l.Lock()
	defer l.Unlock()
	fp := fingerprint(s, headers)
	m.mu.Lock()
	c := m.conns[s.ID]
	m.mu.Unlock()
	if c != nil && c.fp != fp {
		m.drop(s.ID)
		c = nil
	}
	if c != nil && c.client != nil && !force && time.Since(c.fetched) < m.ttl() {
		return c, nil
	}
	if c != nil && c.client == nil && !force && time.Since(c.failedAt) < m.fttl() {
		return nil, errors.New(c.err)
	}
	if c != nil && c.client != nil {
		m.drop(s.ID)
	}
	nc, err := m.connect(ctx, s, headers)
	m.mu.Lock()
	if m.conns == nil {
		m.conns = map[string]*conn{}
	}
	if err != nil {
		msg := scrub(err.Error(), headers)
		m.conns[s.ID] = &conn{fp: fp, failedAt: time.Now(), err: msg}
		m.mu.Unlock()
		return nil, errors.New(msg)
	}
	m.conns[s.ID] = nc
	if m.toolsIndex == nil {
		m.toolsIndex = map[string]indexedTool{}
	}
	for _, tool := range nc.tools {
		m.toolsIndex[tool.Name] = indexedTool{server: s, tool: tool, conn: nc}
	}
	m.mu.Unlock()
	return nc, nil
}

// Status reports the last known state of a server without connecting.
func (m *Manager) Status(s Server) Status {
	if s.Unsupported != "" {
		return Status{State: "unsupported", Error: s.Unsupported}
	}
	if !s.Enabled {
		return Status{State: "disabled"}
	}
	if _, missing := s.Resolved(); len(missing) > 0 {
		return Status{State: "unconfigured", Error: "missing credential: " + strings.Join(missing, ", ")}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.conns[s.ID]
	if c == nil {
		return Status{State: "unknown"}
	}
	if c.client == nil {
		return Status{State: "error", Error: c.err, CheckedAt: c.failedAt}
	}
	return Status{State: "ok", Tools: len(c.tools), Transport: c.transport, CheckedAt: c.fetched}
}

// Test connects fresh and lists tools; used by the test endpoint.
func (m *Manager) Test(ctx context.Context, s Server) ([]Tool, Status) {
	if s.Unsupported != "" {
		return nil, Status{State: "unsupported", Error: s.Unsupported}
	}
	c, err := m.ensure(ctx, s, true)
	if err != nil {
		return nil, Status{State: "error", Error: logx.Error(err), CheckedAt: time.Now()}
	}
	return c.tools, Status{State: "ok", Tools: len(c.tools), Transport: c.transport, CheckedAt: c.fetched}
}

// Tools returns the server's tools, using the cache when fresh.
func (m *Manager) Tools(ctx context.Context, s Server) ([]Tool, error) {
	if s.Unsupported != "" || !s.Enabled {
		return nil, errors.New("server is not enabled")
	}
	c, err := m.ensure(ctx, s, false)
	if err != nil {
		return nil, err
	}
	return c.tools, nil
}

// ToolsFor returns the tools of every enabled server the agent may use. It is
// called at the start of every model iteration, so it serves the cache and
// refreshes slow or expired servers in parallel, bounded by the connect
// timeout. Failing servers are skipped (and not retried for FailureTTL).
func (m *Manager) ToolsFor(ctx context.Context, agentID string) []Tool {
	list, err := m.Registry.List()
	if err != nil {
		return nil
	}
	servers := make(map[string]Server, len(list))
	for _, s := range list {
		servers[s.ID] = s
	}
	m.mu.Lock()
	for key, item := range m.toolsIndex {
		s, ok := servers[item.server.ID]
		if !ok || !s.Enabled || s.Unsupported != "" {
			delete(m.toolsIndex, key)
		}
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var out []Tool
	for _, s := range list {
		if !s.Enabled || s.Unsupported != "" || !s.AllowsAgent(agentID) {
			continue
		}
		wg.Add(1)
		go func(s Server) {
			defer wg.Done()
			if tools, err := m.Tools(ctx, s); err == nil {
				mu.Lock()
				out = append(out, tools...)
				mu.Unlock()
			}
		}(s)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Result is the outcome of a tool call, shaped for the model.
type Result struct {
	Text    string
	IsError bool
}

// Call runs a tool for an agent. The agent's access, the server's allow and
// deny lists and the tool's existence are re-checked here regardless of what
// the model was offered. Protocol and transport failures are returned as an
// error result, never a panic.
func (m *Manager) Call(ctx context.Context, agentID, fullName string, args json.RawMessage) Result {
	m.mu.Lock()
	item, ok := m.toolsIndex[fullName]
	m.mu.Unlock()
	if !ok {
		list, err := m.Registry.List()
		if err != nil {
			return Result{Text: "MCP registry unavailable", IsError: true}
		}
		for _, s := range list {
			if s.Enabled && s.Unsupported == "" && s.AllowsAgent(agentID) && strings.HasPrefix(fullName, "mcp."+s.ID+".") {
				if _, err := m.Tools(ctx, s); err != nil {
					return Result{Text: "Tool server unavailable: " + logx.Error(err), IsError: true}
				}
				m.mu.Lock()
				item, ok = m.toolsIndex[fullName]
				m.mu.Unlock()
				break
			}
		}
	}
	if ok && item.server.AllowsAgent(agentID) {
		s, exists := m.Registry.Get(item.server.ID)
		if exists && s.Enabled && s.Unsupported == "" && s.AllowsAgent(agentID) && s.AllowsTool(item.tool.Tool) {
			if _, err := m.ensure(ctx, s, false); err != nil {
				return Result{Text: "Tool server unavailable: " + logx.Error(err), IsError: true}
			}
			m.mu.Lock()
			item, ok = m.toolsIndex[fullName]
			m.mu.Unlock()
			if ok && item.server.ID == s.ID {
				return m.call(ctx, s, item.tool, args)
			}
		}
		m.unindexServer(item.server.ID)
	}
	return Result{Text: "Unknown or unavailable tool " + fullName, IsError: true}
}

func (m *Manager) call(ctx context.Context, s Server, t Tool, args json.RawMessage) Result {
	var obj map[string]json.RawMessage
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if json.Unmarshal(args, &obj) != nil || obj == nil {
		return Result{Text: "Tool arguments must be a JSON object", IsError: true}
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		m.mu.Lock()
		c := m.conns[s.ID]
		m.mu.Unlock()
		if c == nil || c.client == nil {
			var err error
			if c, err = m.ensure(ctx, s, false); err != nil {
				return Result{Text: "Tool server unavailable: " + logx.Error(err), IsError: true}
			}
		}
		cctx, cancel := context.WithTimeout(ctx, m.callTimeout())
		res, err := c.client.CallTool(cctx, t.Tool, args)
		cancel()
		if err == nil {
			return shape(res)
		}
		var rpc *sdk.RPCError
		if errors.As(err, &rpc) {
			// The server understood and refused: report it, do not reconnect.
			return untrustedResult(fmt.Sprintf("Tool error: %s", rpc.Message), true)
		}
		last = err
		if ctx.Err() != nil {
			break
		}
		// Transport failure or expired session: reconnect once.
		m.drop(s.ID)
	}
	headers, _ := s.Resolved()
	return Result{Text: "Tool call failed: " + scrub(last.Error(), headers), IsError: true}
}

// shape flattens MCP content into text for the model and bounds its size.
func shape(r *sdk.ToolResult) Result {
	var b strings.Builder
	for _, c := range r.Content {
		switch c.Type {
		case "text":
			b.WriteString(c.Text)
		case "image", "audio":
			fmt.Fprintf(&b, "[%s content omitted]", c.Type)
		default:
			var res struct {
				Resource struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				} `json:"resource"`
			}
			if json.Unmarshal(c.Raw, &res) == nil && res.Resource.Text != "" {
				b.WriteString(res.Resource.Text)
			} else {
				fmt.Fprintf(&b, "[%s content omitted]", c.Type)
			}
		}
		b.WriteString("\n")
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		text = "(the tool returned no content)"
	}
	if n := utf8.RuneCountInString(text); n > MaxResultRunes {
		text = truncateRunes(text, MaxResultRunes) + fmt.Sprintf("\n[truncated: %d of %d characters shown]", MaxResultRunes, n)
	}
	return untrustedResult(text, r.IsError)
}

func untrustedResult(text string, isError bool) Result {
	text = filterUntrusted(text)
	text = strings.ReplaceAll(text, "<<< BEGIN UNTRUSTED MCP TOOL RESULT >>>", "[result delimiter escaped]")
	text = strings.ReplaceAll(text, "<<< END UNTRUSTED MCP TOOL RESULT >>>", "[result delimiter escaped]")
	return Result{Text: "<<< BEGIN UNTRUSTED MCP TOOL RESULT >>>\n" + text + "\n<<< END UNTRUSTED MCP TOOL RESULT >>>", IsError: isError}
}

var injectionPatterns = regexp.MustCompile(`(?i)(ignore (all |any |the )?(previous|prior|above) instructions|system prompt|developer message|reveal (the )?(system|developer) prompt|you are chatgpt|<\|system\|>)`)

func filterUntrusted(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if injectionPatterns.MatchString(line) {
			lines[i] = "[instruction-like content removed]"
		}
	}
	s = strings.Join(lines, "\n")
	if i := strings.Index(strings.ToLower(s), "system prompt"); i >= 0 {
		s = s[:i] + "[system prompt reference removed]"
	}
	if n := utf8.RuneCountInString(s); n > MaxResultRunes {
		s = truncateRunes(s, MaxResultRunes) + " [truncated]"
	}
	return s
}

// legacyStatus reports whether a Streamable HTTP failure looks like a legacy
// SSE-only server rejecting the POST (400, 404 or 405).
func legacyStatus(err error) bool {
	msg := err.Error()
	for _, code := range []string{"status 400", "status 404", "status 405"} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}

// Close drops every connection.
func (m *Manager) Close() {
	m.mu.Lock()
	conns := m.conns
	m.conns = nil
	m.toolsIndex = nil
	m.mu.Unlock()
	for _, c := range conns {
		if c.client != nil {
			_ = c.client.Close()
		}
	}
}
