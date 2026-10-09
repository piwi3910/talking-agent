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
			if spoken, ok := demoSlotReply(r); ok {
				parts = append(parts, spoken)
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

// demoSlotReply answers an availability result the way a person would: which
// day suits first, then a couple of times once the day is known. No ids, no
// zones, no list. Slot ids stay in the records for the booking step.
func demoSlotReply(r tools.Result) (string, bool) {
	if len(r.Records) == 0 {
		return "", false
	}
	var days []string
	times := map[string][]string{}
	for _, rec := range r.Records {
		if !strings.HasSuffix(rec.Kind, "_slot") {
			return "", false
		}
		at, err := time.Parse(time.RFC3339, rec.Start)
		if err != nil {
			return "", false
		}
		day := fmt.Sprintf("%s the %s", at.Weekday(), ordinal(at.Day()))
		if _, seen := times[day]; !seen {
			days = append(days, day)
		}
		clock := at.Format("3 pm")
		if at.Minute() != 0 {
			clock = at.Format("3:04 pm")
		}
		times[day] = append(times[day], clock)
	}
	if len(days) == 1 {
		options := times[days[0]]
		if len(options) > 3 {
			options = options[:3]
		}
		return fmt.Sprintf("On %s I could do %s. Which would suit you best?", days[0], joinWords(options)), true
	}
	if len(days) > 3 {
		days = days[:3]
	}
	return fmt.Sprintf("I have openings on %s. Which day suits you best, and would you rather come in the morning or the afternoon?", joinWords(days)), true
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

func joinWords(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
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
	patterns := map[string]string{"$technician": `(?i)\bTECH-\d{2}\b`, "$ticket": `(?i)\b(?:T-C\d{3}|TICKET-\d+)\b`, "$plan": `(?i)\b(?:fiber-100|fiber-500|mobile-50)\b`}
	if p, ok := patterns[key]; ok {
		returnValue := regexp.MustCompile(p).FindString(query)
		if key == "$plan" {
			return strings.ToLower(returnValue)
		}
		if returnValue != "" {
			return strings.ToUpper(returnValue)
		}
		return ""
	}
	switch key {
	case "$query":
		return query
	}
	return ""
}
