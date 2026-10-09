package api

import (
	"context"
	"enterprise-ai-demo/internal/mcp"
	"errors"
	"net/http"
	"time"
)

type mcpServerView struct {
	mcp.Server
	Status mcp.Status `json:"status"`
}

func (a *API) mcpView(s mcp.Server) mcpServerView {
	return mcpServerView{Server: s.Masked(), Status: a.MCP.Status(s)}
}

func (a *API) mcpRoutes(m *http.ServeMux) {
	guard := func(h func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if a.MCP == nil {
				fail(w, 503, "MCP is not configured")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			h(w, r)
		}
	}
	m.HandleFunc("GET /api/mcp/servers", guard(func(w http.ResponseWriter, r *http.Request) {
		list, err := a.MCP.Registry.List()
		if err != nil {
			fail(w, 500, "Could not read MCP servers")
			return
		}
		out := make([]mcpServerView, 0, len(list))
		for _, s := range list {
			out = append(out, a.mcpView(s))
		}
		write(w, 200, out)
	}))
	m.HandleFunc("POST /api/mcp/servers", guard(func(w http.ResponseWriter, r *http.Request) {
		var in mcp.Server
		if decode(w, r, &in) != nil {
			fail(w, 400, "Invalid server definition")
			return
		}
		in.Deleted, in.Unsupported = false, ""
		if err := a.MCP.Registry.Create(in); err != nil {
			mcpFail(w, err)
			return
		}
		s, _ := a.MCP.Registry.Get(in.ID)
		write(w, 201, a.mcpView(s))
	}))
	m.HandleFunc("PUT /api/mcp/servers/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		var in mcp.Server
		if decode(w, r, &in) != nil {
			fail(w, 400, "Invalid server definition")
			return
		}
		in.Deleted, in.Unsupported = false, ""
		if in.ID != "" && in.ID != r.PathValue("id") {
			fail(w, 400, "The server id cannot be changed")
			return
		}
		if err := a.MCP.Registry.Update(r.PathValue("id"), in); err != nil {
			mcpFail(w, err)
			return
		}
		s, _ := a.MCP.Registry.Get(r.PathValue("id"))
		write(w, 200, a.mcpView(s))
	}))
	m.HandleFunc("DELETE /api/mcp/servers/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		if err := a.MCP.Registry.Delete(r.PathValue("id")); err != nil {
			mcpFail(w, err)
			return
		}
		write(w, 200, map[string]bool{"deleted": true})
	}))
	m.HandleFunc("POST /api/mcp/servers/{id}/test", guard(func(w http.ResponseWriter, r *http.Request) {
		s, ok := a.MCP.Registry.Get(r.PathValue("id"))
		if !ok {
			fail(w, 404, "Server not found")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		tools, status := a.MCP.Test(ctx, s)
		write(w, 200, map[string]any{"status": status, "tools": toolSummaries(tools)})
	}))
	m.HandleFunc("GET /api/mcp/servers/{id}/tools", guard(func(w http.ResponseWriter, r *http.Request) {
		s, ok := a.MCP.Registry.Get(r.PathValue("id"))
		if !ok {
			fail(w, 404, "Server not found")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		tools, err := a.MCP.Tools(ctx, s)
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		write(w, 200, tools)
	}))
}

func toolSummaries(tools []mcp.Tool) []map[string]string {
	out := make([]map[string]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]string{"name": t.Name, "tool": t.Tool, "description": t.Description})
	}
	return out
}

func mcpFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mcp.ErrNotFound):
		fail(w, 404, err.Error())
	case errors.Is(err, mcp.ErrExists):
		fail(w, 409, err.Error())
	default:
		fail(w, 400, err.Error())
	}
}
