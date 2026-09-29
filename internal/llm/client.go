package llm

import (
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/tools"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/providers/openai"
)

type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type Call struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content,omitempty"`
	ToolCalls  []Call `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}
type ToolFunction struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Parameters  tools.Schema `json:"parameters"`
}
type Request struct {
	Messages []Message
	Tools    []Tool
}
type Usage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
}
type Response struct {
	Message Message
	Usage   Usage
}
type Client interface {
	Chat(context.Context, Request, func(string)) (Response, error)
	Name() string
}
type OpenAI struct {
	BaseURL, Model, APIKeyEnv string
	HTTP                      *http.Client
	Timeout                   time.Duration
}

func (c *OpenAI) Name() string      { return "openai-compatible / " + c.Model }
func WireName(name string) string   { return strings.ReplaceAll(name, ".", "__") }
func DomainName(name string) string { return strings.ReplaceAll(name, "__", ".") }

// Chat adapts the platform contract to go-ai-sdk. Tool execution remains in
// the runtime so identity checks, policies, timeouts and telemetry stay shared.
func (c *OpenAI) Chat(ctx context.Context, in Request, delta func(string)) (Response, error) {
	if c.Model == "" || c.BaseURL == "" {
		return Response{}, errors.New("LLM_BASE_URL and LLM_MODEL are required")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Retry a completed but empty generation once. No text or tool request has
	// reached the caller, so this cannot duplicate a visible response or action.
	var usage Usage
	for attempt := 0; attempt < 2; attempt++ {
		out, err := c.chatOnce(ctx, in, delta)
		usage.Prompt += out.Usage.Prompt
		usage.Completion += out.Usage.Completion
		usage.Total += out.Usage.Total
		out.Usage = usage
		if !errors.Is(err, errEmptyResponse) {
			return out, err
		}
		if attempt == 1 {
			return out, err
		}
	}
	return Response{}, errEmptyResponse
}

var errEmptyResponse = errors.New("LLM returned no answer or tool calls; please try again")

func (c *OpenAI) chatOnce(ctx context.Context, in Request, delta func(string)) (Response, error) {
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	model := openai.New(openai.WithBaseURL(c.BaseURL), openai.WithAPIKey(os.Getenv(c.APIKeyEnv)), openai.WithHTTPClient(client)).Model(c.Model)
	call := provider.Call{}
	// Some compatible model templates accept only one leading system message.
	// Combine the runtime's instructions and retrieved context before conversion.
	messages := make([]Message, 0, len(in.Messages))
	for _, m := range in.Messages {
		if m.Role == "system" && len(messages) == 1 && messages[0].Role == "system" {
			messages[0].Content += "\n\n" + m.Content
		} else {
			messages = append(messages, m)
		}
	}
	for _, m := range messages {
		msg := provider.Message{Role: provider.Role(m.Role)}
		if m.Role == "tool" {
			var result any
			if err := json.Unmarshal([]byte(m.Content), &result); err != nil {
				result = m.Content
			}
			msg.Content = append(msg.Content, provider.ToolResultPart{ToolCallID: m.ToolCallID, Result: result})
		} else {
			if m.Content != "" {
				msg.Content = append(msg.Content, provider.TextPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				msg.Content = append(msg.Content, provider.ToolCallPart{ID: tc.ID, Name: tc.Function.Name, Args: json.RawMessage(tc.Function.Arguments)})
			}
		}
		call.Messages = append(call.Messages, msg)
	}
	for _, tool := range in.Tools {
		schema, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			return Response{}, err
		}
		call.Tools = append(call.Tools, provider.ToolDef{Name: tool.Function.Name, Description: tool.Function.Description, Schema: schema})
	}
	var stream provider.StreamResponse
	var err error
	// Only retry explicit transient rejections before any stream is consumed.
	for attempt := 0; attempt < 3; attempt++ {
		stream, err = model.Stream(ctx, call)
		if err == nil {
			break
		}
		var apiErr *ai.APICallError
		if !errors.As(err, &apiErr) || (apiErr.StatusCode != 429 && apiErr.StatusCode != 503) || attempt == 2 {
			return Response{}, safeError(err)
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Response{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer stream.Close()
	out := Response{Message: Message{Role: "assistant"}}
	finished := false
	seen := map[string]bool{}
	for part := range stream.Parts() {
		switch p := part.(type) {
		case provider.TextDelta:
			out.Message.Content += p.Text
			if delta != nil {
				delta(p.Text)
			}
		case provider.ToolCallEnd:
			tc := p.Call
			if len(seen) >= 32 || tc.ID == "" || tc.Name == "" || seen[tc.ID] || !json.Valid(tc.Args) {
				return out, errors.New("invalid streamed tool call")
			}
			seen[tc.ID] = true
			out.Message.ToolCalls = append(out.Message.ToolCalls, Call{ID: tc.ID, Type: "function", Function: Function{Name: tc.Name, Arguments: string(tc.Args)}})
		case provider.FinishPart:
			if p.Reason == provider.FinishLength || p.Reason == provider.FinishContentFilter || p.Reason == provider.FinishError {
				return out, errors.New("LLM response truncated, filtered or failed")
			}
			finished = true
			out.Usage = Usage{Prompt: p.Usage.InputTokens, Completion: p.Usage.OutputTokens, Total: p.Usage.TotalTokens}
		}
	}
	if err := stream.Err(); err != nil {
		return out, safeError(err)
	}
	if !finished {
		return out, errors.New("LLM stream ended unexpectedly; not retrying a partial response")
	}
	if out.Message.Content == "" && len(out.Message.ToolCalls) == 0 {
		return out, errEmptyResponse
	}
	return out, nil
}

// Do not expose arbitrary provider response bodies or credential-bearing URLs.
func safeError(err error) error {
	var apiErr *ai.APICallError
	if errors.As(err, &apiErr) {
		hint := "provider request failed"
		switch apiErr.StatusCode {
		case 401, 403:
			hint = "gateway rejected credentials or access"
		case 429:
			hint = "gateway rate limit reached"
		case 502, 503, 504:
			hint = "gateway model backend unavailable"
		}
		return fmt.Errorf("LLM HTTP %d (%s)", apiErr.StatusCode, hint)
	}
	return err
}
