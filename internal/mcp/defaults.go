package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// ZaiPresets are the Z.ai GLM MCP servers, enabled for the assistant agent.
// The credential is read from ZAI_API_KEY (a Kubernetes Secret on KW) and is
// never stored. zai-mcp-server is a stdio/npx package; the container has no
// Node runtime, so it is listed as unsupported.
func ZaiPresets() []Server {
	auth := map[string]string{"Authorization": "Bearer ${ZAI_API_KEY}"}
	mk := func(id, name, path string) Server {
		return Server{ID: id, Name: name, URL: "https://api.z.ai/api/mcp/" + path + "/mcp", Transport: TransportHTTP, Headers: auth, Enabled: true, Agents: []string{"assistant"}}
	}
	return []Server{
		mk("web-search-prime", "Z.ai Web Search", "web_search_prime"),
		mk("web-reader", "Z.ai Web Reader", "web_reader"),
		mk("zread", "Z.ai Zread (GitHub repositories)", "zread"),
		{ID: "zai-mcp-server", Name: "Z.ai Vision (stdio, not supported)", URL: "https://unsupported.invalid/stdio", Transport: TransportAuto, Enabled: false, Agents: []string{},
			Unsupported: "Runs as a local npx (stdio) process, which this container cannot start."},
	}
}

// Defaults returns the built-in presets followed by servers from the
// MCP_DEFAULT_SERVERS environment variable (a JSON array of servers, typically
// from a Secret; header values may use ${VAR}). Environment entries win on id.
func Defaults() ([]Server, error) {
	out := ZaiPresets()
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
