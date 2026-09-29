package llm

// Offline is an explicit scripted provider for demos and integration tests, not an LLM.
import (
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/tools"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

type DemoStep struct {
	UnlessStatus []string          `json:"unless_status"`
	WhenStatus   string            `json:"when_status"`
	WhenEmpty    bool              `json:"when_empty"`
	Tool         string            `json:"tool"`
	Arguments    map[string]string `json:"arguments"`
}
type DemoRule struct {
	Keywords []string   `json:"keywords"`
	Steps    []DemoStep `json:"steps"`
	Reply    string     `json:"reply"`
}
type Demo struct{ Rules []DemoRule }

func LoadDemo(path string) (*Demo, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	d := &Demo{}
	e = json.Unmarshal(b, &d.Rules)
	return d, e
}
func (*Demo) Name() string { return "offline scripted demo (no LLM)" }
func (d *Demo) Chat(ctx context.Context, in Request, delta func(string)) (Response, error) {
	start := 0
	query := ""
	for i, m := range in.Messages {
		if m.Role == "user" {
			start = i
			query = m.Content
		}
	}
	lower := strings.ToLower(query)
	var rule *DemoRule
	for i, r := range d.Rules {
		for _, k := range r.Keywords {
			if strings.Contains(lower, k) {
				rule = &d.Rules[i]
				break
			}
		}
		if rule != nil {
			break
		}
	}
	results := []tools.Result{}
	completed := map[string]bool{}
	for i := start + 1; i < len(in.Messages); i++ {
		m := in.Messages[i]
		if m.Role == "tool" {
			var r tools.Result
			if json.Unmarshal([]byte(m.Content), &r) == nil && (r.Summary != "" || r.Error != nil) {
				results = append(results, r)
			}
			for j := i - 1; j >= start; j-- {
				for _, call := range in.Messages[j].ToolCalls {
					if call.ID == m.ToolCallID {
						completed[DomainName(call.Function.Name)+"|"+call.Function.Arguments] = true
					}
				}
			}
		}
	}
	reply := "I can help with the service capabilities shown above. Try a scenario prompt, or include a specific record ID for an action. Offline mode supports the documented scenario phrases; connect an LLM for open-ended conversation."
	if rule != nil {
		reply = rule.Reply
		for _, step := range rule.Steps {
			skip := false
			matched := step.WhenStatus == ""
			for _, r := range results {
				for _, rec := range r.Records {
					if rec.Status == step.WhenStatus {
						matched = true
					}
					for _, status := range step.UnlessStatus {
						if rec.Status == status {
							skip = true
						}
					}
				}
			}
			if skip || !matched {
				continue
			}
			if step.WhenEmpty && (len(results) == 0 || len(results[len(results)-1].Records) != 0) {
				continue
			}
			available := false
			for _, t := range in.Tools {
				if t.Function.Name == WireName(step.Tool) {
					available = true
				}
			}
			if !available {
				return demoCall("skills.activate", map[string]string{"skill_id": strings.Split(step.Tool, ".")[0]}), nil
			}
			args := map[string]string{}
			for k, v := range step.Arguments {
				if strings.HasPrefix(v, "$") {
					v = demoValue(v, query, in.Messages, results)
				}
				if v != "" {
					args[k] = v
				}
			}
			encoded, _ := json.Marshal(args)
			if completed[step.Tool+"|"+string(encoded)] {
				continue
			}
			for _, t := range in.Tools {
				if t.Function.Name == WireName(step.Tool) {
					for _, required := range t.Function.Parameters.Required {
						if args[required] == "" {
							return demoText(ctx, "Please include the "+required+" from the available records, for example by using an action example shown in the guide.", delta)
						}
					}
				}
			}
			return demoCall(step.Tool, args), nil
		}
	}
	if len(results) > 0 {
		parts := []string{}
		memoryPrefix := ""
		for _, m := range in.Messages {
			if m.Role == "system" && strings.HasPrefix(m.Content, "Relevant memories") {
				if strings.Contains(m.Content, "upstairs") && strings.Contains(strings.ToLower(m.Content), "previous") && strings.Contains(m.Content, "interference") && (strings.Contains(lower, "upstairs") || strings.Contains(lower, "wi-fi")) {
					memoryPrefix = "It looks like the upstairs Wi-Fi problem has returned. Last time we found channel interference. I’ve checked the current service state.\n\n"
				}
				if strings.Contains(m.Content, "morning") && (strings.Contains(lower, "appointment") || strings.Contains(lower, "ahmed") || strings.Contains(lower, "dermatolog")) {
					memoryPrefix = "You usually prefer mornings. Here are the service results using that preference.\n\n"
				}
			}
		}
		for _, r := range results {
			if strings.HasPrefix(r.Summary, "Skill activated:") {
				continue
			}
			if r.Error != nil {
				parts = append(parts, "Unable to complete action: "+r.Error.Message)
				continue
			}
			p := r.Summary
			for i, rec := range r.Records {
				if i >= 8 {
					break
				}
				line := rec.ID
				for _, v := range []string{rec.Name, rec.Status, rec.Start, rec.Location, rec.Description} {
					if v != "" {
						line += " · " + v
					}
				}
				if rec.Amount > 0 {
					line += fmt.Sprintf(" · %.2f %s", rec.Amount, rec.Currency)
				}
				p += "\n• " + line
			}
			parts = append(parts, p)
		}
		reply = memoryPrefix + strings.Join(parts, "\n\n")
	}
	return demoText(ctx, reply, delta)
}
func demoCall(name string, args map[string]string) Response {
	b, _ := json.Marshal(args)
	return Response{Message: Message{Role: "assistant", ToolCalls: []Call{{ID: fmt.Sprintf("call-%d", time.Now().UnixNano()), Type: "function", Function: Function{WireName(name), string(b)}}}}}
}
func demoText(ctx context.Context, s string, delta func(string)) (Response, error) {
	for _, word := range strings.SplitAfter(s, " ") {
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(4 * time.Millisecond):
			delta(word)
		}
	}
	return Response{Message: Message{Role: "assistant", Content: s}}, nil
}
func demoValue(key, query string, messages []Message, results []tools.Result) string {
	patterns := map[string]string{"$slot": `(?i)\bS-D\d{3}-\d{8}-\d{2}\b`, "$appointment": `(?i)\bA-(?:P\d{3}|\d{4})\b`, "$doctor": `(?i)\bD\d{3}\b`, "$technician": `(?i)\bTECH-\d{2}\b`, "$ticket": `(?i)\b(?:T-C\d{3}|TICKET-\d+)\b`, "$plan": `(?i)\b(?:fiber-100|fiber-500|mobile-50)\b`}
	if p, ok := patterns[key]; ok {
		returnValue := regexp.MustCompile(p).FindString(query)
		if key == "$plan" {
			return strings.ToLower(returnValue)
		}
		if returnValue != "" {
			return strings.ToUpper(returnValue)
		}
		if key == "$doctor" && strings.Contains(strings.ToLower(query), "ahmed") {
			return "D001"
		}
		return ""
	}
	switch key {
	case "$query":
		return query
	case "$morning":
		for _, m := range messages {
			if m.Role == "system" && strings.Contains(m.Content, "prefers morning") {
				return "morning"
			}
		}
		if strings.Contains(strings.ToLower(query), "morning") {
			return "morning"
		}
	case "$specialty":
		for _, s := range []string{"Dermatology", "Cardiology", "Orthopedics", "Pediatrics", "General Medicine"} {
			if strings.Contains(strings.ToLower(query), strings.ToLower(s[:len(s)-1])) {
				return s
			}
		}
	case "$from":
		return time.Now().UTC().Format("2006-01-02")
	case "$to":
		if strings.Contains(strings.ToLower(query), "this week") {
			now := time.Now().UTC()
			offset := (7 - int(now.Weekday())) % 7
			return now.AddDate(0, 0, offset).Format("2006-01-02")
		}
	case "$provider":
		for _, s := range []string{"DemoCare", "HealthFirst"} {
			if strings.Contains(strings.ToLower(query), strings.ToLower(s)) {
				return s
			}
		}
	case "$insurance_plan":
		for _, s := range []string{"Standard", "Plus", "Gold", "Silver", "Platinum"} {
			if strings.Contains(strings.ToLower(query), strings.ToLower(s)) {
				return s
			}
		}
	}
	return ""
}
