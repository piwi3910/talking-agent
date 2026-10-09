package agent

import (
	"context"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/mock"
	"enterprise-ai-demo/internal/session"
	"errors"
	"strings"
	"testing"
)

type errorClient struct {
	respond func(llm.Request, func(string)) (llm.Response, error)
}

func (errorClient) Name() string { return "test" }
func (c errorClient) Chat(_ context.Context, r llm.Request, d func(string)) (llm.Response, error) {
	return c.respond(r, d)
}
func hasEvent(s *session.Session, kind string) bool {
	for _, e := range s.Events.Since(0) {
		if e.Type == kind {
			return true
		}
	}
	return false
}

func TestEmptyAnswerRecoversOnlyVerifiedCurrentResults(t *testing.T) {
	for _, mode := range []string{"success", "no_slots", "failed_tool", "no_tools", "partial", "provider_error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			r, agents, b := setup(t)
			if mode == "no_slots" {
				bookAllTechnicianSlots(t, b)
			}
			s := session.NewStore().Create(agents["telecom-support"], "C001")
			// A prior result must not be reused when this turn never reaches a tool.
			s.History = []llm.Message{{Role: "user", Content: "old request"}, {Role: "assistant", Content: "old verified answer"}}
			count := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.Clients[s.Agent.ID] = errorClient{respond: func(_ llm.Request, delta func(string)) (llm.Response, error) {
				count++
				if count == 1 && mode != "no_tools" {
					args := `{}`
					if mode == "failed_tool" {
						args = `{"from":"not-a-date"}`
					}
					return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.Call{{ID: "lookup", Type: "function", Function: llm.Function{Name: "technician__availability", Arguments: args}}}}}, nil
				}
				if mode == "partial" {
					delta("Partial answer")
					return llm.Response{Message: llm.Message{Content: "Partial answer"}}, llm.ErrEmptyResponse
				}
				if mode == "provider_error" {
					return llm.Response{}, errors.New("gateway unavailable")
				}
				if mode == "cancelled" {
					cancel()
				}
				return llm.Response{}, llm.ErrEmptyResponse
			}}
			r.Run(ctx, s, "turn", Turn{Text: "Show technician visits"})
			recover := mode == "success" || mode == "no_slots"
			if hasEvent(s, "agent.response.recovered") != recover || hasEvent(s, "agent.error") == recover {
				t.Fatal(mode, s.Events.Since(0))
			}
			if recover {
				text := conversation(s)
				if mode == "success" && (!strings.Contains(text, "UTC") || !strings.Contains(text, "ID: TECH-")) {
					t.Fatal(text)
				}
				if mode == "no_slots" && !strings.Contains(text, "Available technician visits.") {
					t.Fatal(text)
				}
			}
		})
	}
}

func TestConfirmedMutationAcknowledgedWithoutModel(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "conflict"}[fail], func(t *testing.T) {
			r, agents, b := setup(t)
			s := session.NewStore().Create(agents["telecom-support"], "C001")
			slots := b.Execute(request("telecom", "C001", "technician.availability"))
			if len(slots.Records) == 0 {
				t.Fatal("missing fixtures")
			}
			slot := slots.Records[0]
			args := map[string]string{"slot_id": slot.ID}
			if fail {
				args["slot_id"] = "missing"
			}
			// Use the real pending-action path, not a direct mutation call.
			r.Clients[s.Agent.ID] = testClient{respond: func(_ llm.Request, _ func(string)) llm.Response {
				return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.Call{{ID: "book", Type: "function", Function: llm.Function{Name: "technician__book", Arguments: `{"slot_id":"` + args["slot_id"] + `"}`}}}}}
			}}
			r.Run(context.Background(), s, "propose", Turn{Text: "Book a technician visit"})
			if len(s.Pending) != 1 {
				t.Fatal("missing confirmation")
			}
			var id string
			for k := range s.Pending {
				id = k
			}
			r.Clients[s.Agent.ID] = panicClient{}
			r.Run(context.Background(), s, "confirm", Turn{Confirmation: id})
			if hasEvent(s, "agent.response.grounded") == fail {
				t.Fatal("incorrect acknowledgement")
			}
			if !fail {
				text := conversation(s)
				if !strings.Contains(text, "Technician visit booked.") || !strings.Contains(text, "UTC") || !strings.Contains(text, slot.ID) {
					t.Fatal(text)
				}
				remaining := b.Execute(request("telecom", "C001", "technician.availability"))
				if len(remaining.Records) != len(slots.Records)-1 {
					t.Fatal("mutation duplicated or lost", len(remaining.Records), len(slots.Records))
				}
				r.Run(context.Background(), s, "replay", Turn{Confirmation: id})
				if !hasEvent(s, "agent.error") {
					t.Fatal("replayed confirmation accepted")
				}
			} else if !hasEvent(s, "agent.error") || strings.Contains(conversation(s), "Technician visit booked.") {
				t.Fatal("failed write reported success")
			}
		})
	}
}

// bookAllTechnicianSlots leaves the telecom backend with no open technician visits.
func bookAllTechnicianSlots(t *testing.T, b *mock.Backend) {
	t.Helper()
	for _, slot := range b.Execute(request("telecom", "C002", "technician.availability")).Records {
		req := request("telecom", "C002", "technician.book")
		req.Arguments = map[string]string{"slot_id": slot.ID}
		if res := b.Execute(req); res.Error != nil {
			t.Fatal(res.Error)
		}
	}
}

// recordsMode marks technician.availability as an authoritative-records tool.
func recordsMode(r *Runtime, agent string) {
	skill := r.Catalogs[agent]["technician"]
	for i := range skill.Tools {
		if skill.Tools[i].Name == "technician.availability" {
			skill.Tools[i].ResponseMode = "records"
		}
	}
	r.Catalogs[agent]["technician"] = skill
}

func TestConfiguredRecordsEndTurnWithoutSynthesis(t *testing.T) {
	r, agents, _ := setup(t)
	recordsMode(r, "telecom-support")
	s := session.NewStore().Create(agents["telecom-support"], "C001")
	count := 0
	r.Clients[s.Agent.ID] = testClient{respond: func(_ llm.Request, _ func(string)) llm.Response {
		count++
		if count > 1 {
			t.Fatal("configured record results sent back for model synthesis")
		}
		return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.Call{{ID: "slots", Type: "function", Function: llm.Function{Name: "technician__availability", Arguments: `{}`}}}}}
	}}
	r.Run(context.Background(), s, "search", Turn{Text: "Show technician visits"})
	if count != 1 || !hasEvent(s, "agent.response.grounded") || hasEvent(s, "agent.error") || !strings.Contains(conversation(s), "ID: TECH-") {
		t.Fatal(conversation(s))
	}
}

func TestEmptyRecordsContinueToAlternativeSearch(t *testing.T) {
	r, agents, b := setup(t)
	recordsMode(r, "telecom-support")
	bookAllTechnicianSlots(t, b)
	s := session.NewStore().Create(agents["telecom-support"], "C001")
	count := 0
	r.Clients[s.Agent.ID] = testClient{respond: func(_ llm.Request, _ func(string)) llm.Response {
		count++
		if count == 1 {
			return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: []llm.Call{{ID: "slots", Type: "function", Function: llm.Function{Name: "technician__availability", Arguments: `{}`}}}}}
		}
		return llm.Response{Message: llm.Message{Role: "assistant", Content: "Would a later week work?"}}
	}}
	r.Run(context.Background(), s, "search", Turn{Text: "Show technician visits"})
	if count != 2 || hasEvent(s, "agent.response.grounded") || !strings.Contains(conversation(s), "later week") {
		t.Fatal(conversation(s))
	}
}
