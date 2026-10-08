package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type addIn struct {
	A     int      `json:"a" jsonschema:"first addend"`
	B     int      `json:"b"`
	Fast  bool     `json:"fast,omitempty"`
	Items []string `json:"items,omitempty"`
}
type addOut struct {
	Sum int `json:"sum"`
}

func mcpTestServer(t *testing.T, wantAuth string) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "add", Description: "Add numbers", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, addOut, error) {
			return nil, addOut{Sum: in.A + in.B + len(in.Items)}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "fail", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return nil, nil, context.DeadlineExceeded
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "delete_all"}, // no read-only hint: a mutation
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "deleted"}}}, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "secret"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "nope"}}}, nil, nil
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantAuth != "" && r.Header.Get("Authorization") != wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
}

type stubFallback struct{ called atomic.Int32 }

func (s *stubFallback) Execute(context.Context, Definition, Request, telemetry.Sink) Result {
	s.called.Add(1)
	return Result{Summary: "fallback", Records: []Record{}}
}

func newTestMCP(t *testing.T, cfg config.MCPServer) (*MCP, *stubFallback) {
	t.Helper()
	fb := &stubFallback{}
	m, err := NewMCP([]config.MCPServer{cfg}, fb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, fb
}

func TestMCPDiscoveryHonoursAllowlistAndConvertsSchemas(t *testing.T) {
	ts := mcpTestServer(t, "")
	t.Cleanup(ts.Close)
	m, _ := newTestMCP(t, config.MCPServer{Name: "calc", URL: ts.URL, AllowTools: []string{"add", "delete_all"}})
	defs := m.Definitions(context.Background())
	if len(defs) != 2 {
		t.Fatalf("want 2 allow-listed tools, got %+v", defs)
	}
	byName := map[string]Definition{}
	for _, d := range defs {
		byName[d.Name] = d
	}
	add, ok := byName["mcp.calc.add"]
	if !ok {
		t.Fatalf("missing mcp.calc.add in %v", byName)
	}
	if add.Mutation || !byName["mcp.calc.delete_all"].Mutation {
		t.Fatal("read-only hint must decide Mutation (default is mutation)")
	}
	if add.Input.Type != "object" || add.Input.AdditionalProperties {
		t.Fatalf("schema %+v", add.Input)
	}
	for _, k := range []string{"a", "b", "fast", "items"} {
		if add.Input.Properties[k].Type != "string" {
			t.Fatalf("property %s must be exposed as string: %+v", k, add.Input.Properties)
		}
	}
	if !strings.Contains(add.Input.Properties["a"].Description, "integer") || !strings.Contains(add.Input.Properties["items"].Description, "JSON") {
		t.Fatalf("type hints missing: %+v", add.Input.Properties)
	}
	if strings.Join(add.Input.Required, ",") != "a,b" && strings.Join(add.Input.Required, ",") != "b,a" {
		t.Fatalf("required %v", add.Input.Required)
	}
	// The generated definition must satisfy the platform validator.
	if _, err := add.Validate(`{"a":"1","b":"2"}`); err != nil {
		t.Fatal(err)
	}
}

func TestMCPExecuteCoercesArgumentsAndMapsResults(t *testing.T) {
	ts := mcpTestServer(t, "")
	t.Cleanup(ts.Close)
	m, fb := newTestMCP(t, config.MCPServer{Name: "calc", URL: ts.URL, AllowTools: []string{"add", "fail"}})
	ctx := context.Background()
	byName := map[string]Definition{}
	for _, d := range m.Definitions(ctx) {
		byName[d.Name] = d
	}
	var events []string
	emit := func(kind string, _ any) { events = append(events, kind) }

	res := m.Execute(ctx, byName["mcp.calc.add"], Request{Arguments: map[string]string{"a": "2", "b": "3", "items": `["x","y"]`, "fast": ""}}, emit)
	if res.Error != nil || !strings.Contains(res.Summary, `"sum":7`) && !strings.Contains(res.Summary, "7") {
		t.Fatalf("result %+v", res)
	}
	if len(events) != 2 || events[0] != "tool.started" || events[1] != "tool.completed" {
		t.Fatalf("events %v", events)
	}
	if res := m.Execute(ctx, byName["mcp.calc.add"], Request{Arguments: map[string]string{"a": "two", "b": "3"}}, emit); res.Error == nil || res.Error.Code != "invalid_input" {
		t.Fatalf("bad integer must be rejected: %+v", res)
	}
	if res := m.Execute(ctx, byName["mcp.calc.fail"], Request{Arguments: map[string]string{}}, emit); res.Error == nil {
		t.Fatalf("tool error must map to failure: %+v", res)
	}
	// Tools that were not allow-listed or discovered are refused, never forwarded.
	if res := m.Execute(ctx, Definition{Name: "mcp.calc.secret"}, Request{}, emit); res.Error == nil || res.Error.Code != "tool_not_allowed" {
		t.Fatalf("unlisted tool: %+v", res)
	}
	// skills.yaml tools keep using the wrapped executor.
	if res := m.Execute(ctx, Definition{Name: "appointments.list"}, Request{}, emit); res.Summary != "fallback" || fb.called.Load() != 1 {
		t.Fatalf("fallback not used: %+v", res)
	}
}

func TestMCPBearerAuth(t *testing.T) {
	ts := mcpTestServer(t, "Bearer s3cret")
	t.Cleanup(ts.Close)
	t.Setenv("MCP_TEST_TOKEN", "s3cret")
	m, _ := newTestMCP(t, config.MCPServer{Name: "calc", URL: ts.URL, AllowTools: []string{"add"}, Auth: config.MCPAuth{Type: "bearer", TokenEnv: "MCP_TEST_TOKEN"}})
	if defs := m.Definitions(context.Background()); len(defs) != 1 {
		t.Fatalf("authorised discovery failed: %v", defs)
	}
	t.Setenv("MCP_BAD_TOKEN", "wrong")
	bad, _ := newTestMCP(t, config.MCPServer{Name: "calc", URL: ts.URL, AllowTools: []string{"add"}, Auth: config.MCPAuth{Type: "bearer", TokenEnv: "MCP_BAD_TOKEN"}})
	if defs := bad.Definitions(context.Background()); len(defs) != 0 {
		t.Fatalf("wrong token must expose no tools: %v", defs)
	}
}

func TestMCPClientCredentialsAuth(t *testing.T) {
	var grants atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" {
			http.Error(w, "bad grant", http.StatusBadRequest)
			return
		}
		user, pass, _ := r.BasicAuth()
		if user != "agent" || pass != "shh" {
			http.Error(w, "bad client", http.StatusUnauthorized)
			return
		}
		grants.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-1", "token_type": "Bearer", "expires_in": 3600})
	}))
	t.Cleanup(idp.Close)
	ts := mcpTestServer(t, "Bearer tok-1")
	t.Cleanup(ts.Close)
	t.Setenv("MCP_TEST_SECRET", "shh")
	m, _ := newTestMCP(t, config.MCPServer{Name: "calc", URL: ts.URL, AllowTools: []string{"add"},
		Auth: config.MCPAuth{Type: "client_credentials", TokenURL: idp.URL, ClientID: "agent", ClientSecretEnv: "MCP_TEST_SECRET"}})
	defs := m.Definitions(context.Background())
	if len(defs) != 1 {
		t.Fatalf("discovery with client credentials failed: %v", defs)
	}
	res := m.Execute(context.Background(), defs[0], Request{Arguments: map[string]string{"a": "1", "b": "1"}}, func(string, any) {})
	if res.Error != nil {
		t.Fatalf("%+v", res)
	}
	if grants.Load() != 1 {
		t.Fatalf("token must be cached, grants=%d", grants.Load())
	}
}

func TestMCPRecoversFromDroppedSession(t *testing.T) {
	ts := mcpTestServer(t, "")
	t.Cleanup(ts.Close)
	m, _ := newTestMCP(t, config.MCPServer{Name: "calc", URL: ts.URL, AllowTools: []string{"add"}})
	ctx := context.Background()
	defs := m.Definitions(ctx)
	m.servers[0].mu.Lock()
	_ = m.servers[0].session.Close() // simulate the connection dying
	m.servers[0].mu.Unlock()
	res := m.Execute(ctx, defs[0], Request{Arguments: map[string]string{"a": "1", "b": "2"}}, func(string, any) {})
	if res.Error != nil {
		t.Fatalf("read tool should reconnect: %+v", res)
	}
}

func TestConvertMCPSchemaEdgeCases(t *testing.T) {
	s, types := convertMCPSchema(map[string]any{
		"type":     "object",
		"required": []any{"n", "ghost"},
		"properties": map[string]any{
			"n":    map[string]any{"type": []any{"integer", "null"}},
			"mode": map[string]any{"type": "string", "enum": []any{"a", "b"}},
			"any":  map[string]any{"anyOf": []any{}},
		},
	})
	if types["n"] != "integer" || types["any"] != "string" || len(s.Properties["mode"].Enum) != 2 {
		t.Fatalf("%+v %+v", s, types)
	}
	if len(s.Required) != 1 || s.Required[0] != "n" {
		t.Fatalf("required must only list known properties: %v", s.Required)
	}
	if s, _ := convertMCPSchema(nil); s.Type != "object" {
		t.Fatal("nil schema must still be a closed object")
	}
}
