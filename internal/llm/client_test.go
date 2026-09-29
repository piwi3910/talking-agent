package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamingToolCallAssembly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"wifi__status\",\"arguments\":\"{\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	c := OpenAI{BaseURL: server.URL + "/v1", Model: "configured-model"}
	out, e := c.Chat(context.Background(), Request{}, func(string) {})
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Message.ToolCalls) != 1 || out.Message.ToolCalls[0].Function.Name != "wifi__status" || out.Message.ToolCalls[0].Function.Arguments != "{}" || out.Usage.Total != 12 {
		t.Fatalf("%+v", out)
	}
}
func TestRetryOnlyBeforeStreaming(t *testing.T) {
	attempts := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
	}))
	defer s.Close()
	c := OpenAI{BaseURL: s.URL, Model: "test"}
	var text string
	_, e := c.Chat(context.Background(), Request{}, func(v string) { text += v })
	if e == nil || !strings.Contains(e.Error(), "without [DONE]") {
		t.Fatal(e)
	}
	if attempts != 2 || text != "hello" {
		t.Fatal(attempts, text)
	}
}
func TestCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer s.Close()
	c := OpenAI{BaseURL: s.URL, Model: "test"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.Chat(ctx, Request{}, func(string) {})
	if err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestSDKRequestMapping(t *testing.T) {
	t.Setenv("TEST_LLM_KEY", "test-secret")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing auth")
		}
		var body struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
			Stream   bool      `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "configured" || !body.Stream || len(body.Messages) != 3 {
			t.Errorf("bad request: %+v", body)
		}
		if len(body.Messages) == 3 && (body.Messages[1].ToolCalls[0].ID != "call1" || body.Messages[2].ToolCallID != "call1" || body.Messages[2].Content != `{"status":"ok"}`) {
			t.Errorf("tool roundtrip: %+v", body.Messages)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Connected\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer s.Close()
	c := OpenAI{BaseURL: s.URL, Model: "configured", APIKeyEnv: "TEST_LLM_KEY"}
	out, err := c.Chat(context.Background(), Request{Messages: []Message{
		{Role: "user", Content: "Check connection"},
		{Role: "assistant", ToolCalls: []Call{{ID: "call1", Type: "function", Function: Function{Name: "network__status", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call1", Content: `{"status":"ok"}`},
	}}, nil)
	if err != nil || out.Message.Content != "Connected" {
		t.Fatal(out, err)
	}
}

func TestGatewayFailureIsSafeAndNotRetried(t *testing.T) {
	attempts := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(502)
		fmt.Fprint(w, `{"error":{"message":"sensitive-provider-details"}}`)
	}))
	defer s.Close()
	c := OpenAI{BaseURL: s.URL, Model: "configured"}
	_, err := c.Chat(context.Background(), Request{}, nil)
	if err == nil || !strings.Contains(err.Error(), "model backend unavailable") || strings.Contains(err.Error(), "sensitive") || attempts != 1 {
		t.Fatal(err, attempts)
	}
}
