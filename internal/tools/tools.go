package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/telemetry"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Property struct {
	Type        string   `yaml:"type" json:"type"`
	Description string   `yaml:"description" json:"description"`
	Enum        []string `yaml:"enum,omitempty" json:"enum,omitempty"`
}
type Schema struct {
	Type                 string              `yaml:"type" json:"type"`
	Properties           map[string]Property `yaml:"properties" json:"properties"`
	Required             []string            `yaml:"required" json:"required"`
	AdditionalProperties bool                `yaml:"additionalProperties" json:"additionalProperties"`
}
type MemoryEffect struct {
	Contains string   `yaml:"contains" json:"contains"`
	Text     string   `yaml:"text" json:"text"`
	Tags     []string `yaml:"tags" json:"tags"`
}
type Definition struct {
	// "records" finalizes a successful read with non-empty records using Go formatting.
	ResponseMode string        `yaml:"response_mode,omitempty" json:"response_mode,omitempty"`
	MemoryEffect *MemoryEffect `yaml:"memory_effect,omitempty" json:"memory_effect,omitempty"`
	Name         string        `yaml:"name" json:"name"`
	Description  string        `yaml:"description" json:"description"`
	Input        Schema        `yaml:"input" json:"input"`
	Mutation     bool          `yaml:"mutation" json:"mutation"`
	TimeoutMS    int           `yaml:"timeout_ms" json:"timeout_ms"`
}
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Record is a typed projection shared by mock services and enterprise HTTP adapters.
type Record struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name,omitempty"`
	Status      string  `json:"status,omitempty"`
	Description string  `json:"description,omitempty"`
	UserID      string  `json:"user_id,omitempty"`
	RelatedID   string  `json:"related_id,omitempty"`
	Specialty   string  `json:"specialty,omitempty"`
	Location    string  `json:"location,omitempty"`
	Start       string  `json:"start,omitempty"`
	Amount      float64 `json:"amount,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	Value       float64 `json:"value,omitempty"`
	Unit        string  `json:"unit,omitempty"`
}
type Result struct {
	Summary string   `json:"summary"`
	Records []Record `json:"records"`
	Error   *Error   `json:"error,omitempty"`
}
type Request struct {
	Industry  string            `json:"industry"`
	UserID    string            `json:"user_id"`
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments"`
}
type Executor interface {
	Execute(context.Context, Definition, Request, telemetry.Sink) Result
}
type HTTPExecutor struct {
	BaseURL string
	Client  *http.Client
}

func (d Definition) Validate(raw string) (map[string]string, error) {
	var rawArgs map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&rawArgs); err != nil {
		return nil, errors.New("arguments must be an object of strings")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	if rawArgs == nil {
		return nil, errors.New("arguments must be an object")
	}
	args := make(map[string]string, len(rawArgs))
	for key, value := range rawArgs {
		var text string
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
			return nil, fmt.Errorf("argument %s must be a string", key)
		}
		args[key] = text
	}
	for k, v := range args {
		p, ok := d.Input.Properties[k]
		if !ok {
			return nil, fmt.Errorf("unknown argument %s", k)
		}
		if len(v) > 2000 {
			return nil, fmt.Errorf("argument %s too long", k)
		}
		if len(p.Enum) > 0 {
			ok = false
			for _, e := range p.Enum {
				if e == v {
					ok = true
				}
			}
			if !ok {
				return nil, fmt.Errorf("invalid value for %s", k)
			}
		}
	}
	for _, k := range d.Input.Required {
		if strings.TrimSpace(args[k]) == "" {
			return nil, fmt.Errorf("missing %s", k)
		}
	}
	return args, nil
}
func (h HTTPExecutor) Execute(ctx context.Context, d Definition, req Request, emit telemetry.Sink) (out Result) {
	start := time.Now()
	emit("tool.started", map[string]any{"tool": d.Name})
	defer func() {
		kind := "tool.completed"
		if out.Error != nil {
			kind = "tool.failed"
		}
		emit(kind, map[string]any{"tool": d.Name, "duration_ms": time.Since(start).Milliseconds(), "result": out})
	}()
	ms := d.TimeoutMS
	if ms <= 0 {
		ms = 5000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	defer cancel()
	b, err := json.Marshal(req)
	if err != nil {
		return Failure("invalid_input", err.Error())
	}
	r, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(h.BaseURL, "/")+"/tools/execute", bytes.NewReader(b))
	if err != nil {
		return Failure("backend_error", err.Error())
	}
	r.Header.Set("Content-Type", "application/json")
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(r)
	if err != nil {
		return Failure("backend_unavailable", err.Error())
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return Failure("backend_protocol", "invalid backend result")
	}
	if out.Error == nil && out.Summary == "" {
		return Failure("backend_protocol", "backend returned an empty result")
	}
	if resp.StatusCode >= 400 && out.Error == nil {
		return Failure("backend_error", resp.Status)
	}
	return out
}
func Failure(code, message string) Result {
	return Result{Records: []Record{}, Error: &Error{Code: code, Message: message}}
}
