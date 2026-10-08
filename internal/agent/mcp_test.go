package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type pair struct {
	A int `json:"a"`
	B int `json:"b"`
}

func TestMCPToolsAreOfferedCalledAndMutationsNeedApproval(t *testing.T) {
	r, agents, _ := setup(t)
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "add", Description: "Add", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in pair) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "sum is " + string(rune('0'+in.A+in.B))}}}, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "wipe"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		t.Error("a mutation ran without operator approval")
		return nil, nil, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	a := agents["telecom-support"]
	m, err := tools.NewMCP([]config.MCPServer{{Name: "calc", URL: ts.URL, AllowTools: []string{"add", "wipe"}}}, r.Tools)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close) // runs before ts.Close, which would otherwise wait on the event stream
	r.Executors = map[string]tools.Executor{a.ID: m}

	s := session.NewStore().Create(a, "C001")
	var offered []string
	step := 0
	r.Clients[a.ID] = testClient{respond: func(req llm.Request, delta func(string)) llm.Response {
		step++
		switch step {
		case 1:
			for _, tl := range req.Tools {
				offered = append(offered, tl.Function.Name)
			}
			return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.Call{
				{ID: "1", Type: "function", Function: llm.Function{Name: "mcp__calc__add", Arguments: `{"a":"2","b":"3"}`}},
				{ID: "2", Type: "function", Function: llm.Function{Name: "mcp__calc__wipe", Arguments: `{}`}},
			}}}
		}
		delta("done")
		return llm.Response{Message: llm.Message{Role: "assistant", Content: "done"}}
	}}
	r.Run(context.Background(), s, "mcp", Turn{Text: "add"})
	if !strings.Contains(strings.Join(offered, ","), "mcp__calc__add") || !strings.Contains(strings.Join(offered, ","), "skills__activate") {
		t.Fatalf("MCP tools not offered next to skills: %v", offered)
	}
	var completed, confirm bool
	for _, e := range s.Events.Since(0) {
		switch e.Type {
		case "tool.completed":
			completed = true
		case "action.confirmation.required":
			confirm = true
		}
	}
	if !completed || !confirm {
		t.Fatalf("read tool completed=%v, mutation held for approval=%v", completed, confirm)
	}
}
