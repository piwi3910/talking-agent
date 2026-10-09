package api

import (
	"bytes"
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// miniMCP is a minimal Streamable HTTP MCP server with one tool.
func miniMCP(t *testing.T, wantAuth string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "mini", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "ping", "description": "Ping", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPServersAPI(t *testing.T) {
	t.Setenv("MCP_API_TEST_KEY", "env-secret")
	upstream := miniMCP(t, "Bearer env-secret")
	preset := mcp.Server{ID: "preset", Name: "Preset", URL: upstream.URL, Transport: mcp.TransportHTTP, Enabled: true, Agents: []string{"assistant"},
		Headers: map[string]string{"Authorization": "Bearer ${MCP_API_TEST_KEY}"}}
	manager := &mcp.Manager{Registry: mcp.NewRegistry(filepath.Join(t.TempDir(), "mcp.json"), []mcp.Server{preset})}
	t.Cleanup(manager.Close)
	h := (&API{MCP: manager, Root: context.Background()}).Handler()
	do := func(method, path string, body any, origin string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://agent.test"+path, bytes.NewReader(raw))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Create with a literal secret header: never echoed back.
	create := map[string]any{"id": "mine", "name": "Mine", "url": upstream.URL, "transport": "http", "enabled": true, "agents": []string{"*"},
		"headers": map[string]string{"Authorization": "Bearer literal-token-123"}}
	if w := do("POST", "/api/mcp/servers", create, "https://evil.test"); w.Code != 403 {
		t.Fatalf("cross-origin create: %d", w.Code)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if w := do(method, "/api/mcp/servers/mine", create, "https://evil.test"); w.Code != 403 {
			t.Fatalf("cross-origin %s: %d", method, w.Code)
		}
	}
	w := do("POST", "/api/mcp/servers", create, "http://agent.test")
	if w.Code != 201 || strings.Contains(w.Body.String(), "literal-token-123") || !strings.Contains(w.Body.String(), mcp.MaskValue) {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := do("POST", "/api/mcp/servers", create, ""); w.Code != 409 {
		t.Fatalf("duplicate: %d", w.Code)
	}
	bad := map[string]any{"id": "bad", "name": "Bad", "url": "https://e.test", "headers": map[string]string{"X": "${LLM_API_KEY}"}}
	if w := do("POST", "/api/mcp/servers", bad, ""); w.Code != 400 {
		t.Fatalf("env exfiltration reference accepted: %d", w.Code)
	}

	// List: masked secrets, env references visible, status present.
	w = do("GET", "/api/mcp/servers", nil, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "literal-token-123") || strings.Contains(w.Body.String(), "env-secret") {
		t.Fatalf("list leaks secrets: %d %s", w.Code, w.Body.String())
	}
	var list []struct {
		ID      string            `json:"id"`
		Headers map[string]string `json:"headers"`
		Status  mcp.Status        `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, s := range list {
		byID[s.ID] = i
	}
	if list[byID["preset"]].Headers["Authorization"] != "Bearer ${MCP_API_TEST_KEY}" || list[byID["mine"]].Headers["Authorization"] != mcp.MaskValue {
		t.Fatalf("headers = %+v", list)
	}

	// Test connects: the preset authenticates through the environment variable.
	w = do("POST", "/api/mcp/servers/preset/test", nil, "")
	var tested struct {
		Status mcp.Status          `json:"status"`
		Tools  []map[string]string `json:"tools"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tested)
	if w.Code != 200 || tested.Status.State != "ok" || len(tested.Tools) != 1 || tested.Tools[0]["name"] != "mcp.preset.ping" {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	// "mine" has the wrong token: the failure is reported, without the secret.
	w = do("POST", "/api/mcp/servers/mine/test", nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"error"`) || strings.Contains(w.Body.String(), "literal-token-123") {
		t.Fatalf("failing test: %d %s", w.Code, w.Body.String())
	}
	w = do("GET", "/api/mcp/servers/preset/tools", nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"schema"`) {
		t.Fatalf("tools: %d %s", w.Code, w.Body.String())
	}
	if w := do("GET", "/api/mcp/servers/nope/tools", nil, ""); w.Code != 404 {
		t.Fatalf("unknown server tools: %d", w.Code)
	}

	// Update with the masked secret keeps it; fix by sending the real value.
	update := map[string]any{"name": "Mine renamed", "url": upstream.URL, "transport": "http", "enabled": true, "agents": []string{"assistant"},
		"headers": map[string]string{"Authorization": mcp.MaskValue}}
	if w := do("PUT", "/api/mcp/servers/mine", update, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Mine renamed") {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if s, _ := manager.Registry.Get("mine"); s.Headers["Authorization"] != "Bearer literal-token-123" {
		t.Fatalf("masked update overwrote the stored secret: %+v", s.Headers)
	}
	update["id"] = "other"
	if w := do("PUT", "/api/mcp/servers/mine", update, ""); w.Code != 400 {
		t.Fatalf("id change: %d", w.Code)
	}
	if w := do("PUT", "/api/mcp/servers/ghost", map[string]any{"name": "G", "url": upstream.URL}, ""); w.Code != 404 {
		t.Fatalf("update unknown: %d", w.Code)
	}
	if w := do("DELETE", "/api/mcp/servers/mine", nil, ""); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := do("DELETE", "/api/mcp/servers/mine", nil, ""); w.Code != 404 {
		t.Fatalf("second delete: %d", w.Code)
	}
}
