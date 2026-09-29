package llm

import (
	"context"
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
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"wifi__\",\"arguments\":\"{\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"status\",\"arguments\":\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
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
	if e == nil || !strings.Contains(e.Error(), "before [DONE]") {
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
