package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/tools"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
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
	payload := struct {
		Model         string          `json:"model"`
		Messages      []Message       `json:"messages"`
		Tools         []Tool          `json:"tools,omitempty"`
		Stream        bool            `json:"stream"`
		StreamOptions map[string]bool `json:"stream_options"`
	}{c.Model, in.Messages, in.Tools, true, map[string]bool{"include_usage": true}}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		r, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
		if e != nil {
			return Response{}, e
		}
		r.Header.Set("Content-Type", "application/json")
		if key := os.Getenv(c.APIKeyEnv); key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err = client.Do(r)
		if err != nil {
			return Response{}, fmt.Errorf("LLM connection: %w", err)
		}
		if (resp.StatusCode == 429 || resp.StatusCode == 503) && attempt < 2 {
			resp.Body.Close()
			timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Response{}, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		break
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Response{}, fmt.Errorf("LLM HTTP %d (verify endpoint, model and credentials)", resp.StatusCode)
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 8<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	out := Response{Message: Message{Role: "assistant"}}
	calls := map[int]*Call{}
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		if data == "" {
			continue
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int      `json:"index"`
						ID       string   `json:"id"`
						Type     string   `json:"type"`
						Function Function `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				Finish *string `json:"finish_reason"`
			} `json:"choices"`
			Usage Usage `json:"usage"`
		}
		if err = json.Unmarshal([]byte(data), &chunk); err != nil {
			return out, fmt.Errorf("invalid LLM stream: %w", err)
		}
		if len(chunk.Error) > 0 {
			return out, errors.New("LLM returned a streaming error")
		}
		if chunk.Usage.Total > 0 {
			out.Usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			out.Message.Content += d.Content
			delta(d.Content)
		}
		for _, part := range d.ToolCalls {
			if part.Index < 0 || part.Index > 31 {
				return out, errors.New("too many tool calls")
			}
			call := calls[part.Index]
			if call == nil {
				call = &Call{Type: "function"}
				calls[part.Index] = call
			}
			call.ID += part.ID
			call.Function.Name += part.Function.Name
			call.Function.Arguments += part.Function.Arguments
		}
		if chunk.Choices[0].Finish != nil && (*chunk.Choices[0].Finish == "length" || *chunk.Choices[0].Finish == "content_filter") {
			return out, errors.New("LLM response truncated or filtered")
		}
	}
	if err = scanner.Err(); err != nil {
		return out, err
	}
	if !done {
		return out, errors.New("LLM stream ended before [DONE]; not retrying a partial response")
	}
	indexes := []int{}
	for i := range calls {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	seen := map[string]bool{}
	for _, i := range indexes {
		call := calls[i]
		if call.ID == "" || call.Function.Name == "" || seen[call.ID] {
			return out, errors.New("invalid streamed tool call")
		}
		seen[call.ID] = true
		out.Message.ToolCalls = append(out.Message.ToolCalls, *call)
	}
	return out, nil
}
