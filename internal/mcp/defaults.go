package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// Presets are the built-in servers, enabled for the assistant agent. Brave
// Search is the official brave-search-mcp-server deployed next to the app
// (deploy/kw/manifests/brave-search-mcp.yaml); its API key lives in that
// server, so no headers are sent.
func Presets() []Server {
	return []Server{
		{ID: "brave-search", Name: "Brave Search", URL: "http://brave-search-mcp.enterprise-ai-demo.svc.cluster.local:8080/mcp", Transport: TransportHTTP, Enabled: true, Agents: []string{"assistant"}},
	}
}

// Defaults returns the built-in presets followed by servers from the
// MCP_DEFAULT_SERVERS environment variable (a JSON array of servers, typically
// from a Secret; header values may use ${VAR}). Environment entries win on id.
func Defaults() ([]Server, error) {
	out := Presets()
	raw := os.Getenv("MCP_DEFAULT_SERVERS")
	if raw == "" {
		return out, nil
	}
	var extra []Server
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return out, fmt.Errorf("MCP_DEFAULT_SERVERS: %w", err)
	}
	for _, s := range extra {
		normalize(&s)
		if err := s.Validate(); err != nil {
			return out, fmt.Errorf("MCP_DEFAULT_SERVERS %q: %w", s.ID, err)
		}
		replaced := false
		for i := range out {
			if out[i].ID == s.ID {
				out[i], replaced = s, true
			}
		}
		if !replaced {
			out = append(out, s)
		}
	}
	return out, nil
}
