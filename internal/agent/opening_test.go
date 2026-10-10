package agent

import (
	"context"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/memory"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/skills"
	"enterprise-ai-demo/internal/tools"
	"strings"
	"testing"
)

func TestOpeningTurnSelectsInstructionByDirection(t *testing.T) {
	inbound := &config.Agent{Persona: map[string]string{"opening": "inbound"}}
	turn, ok := OpeningTurn(inbound, "Sarah Ahmed")
	if !ok || !strings.HasPrefix(turn.Opening, "[Call answered]") || strings.Contains(turn.Opening, "Sarah") {
		t.Fatalf("inbound turn: %+v ok=%v", turn, ok)
	}
	if !strings.Contains(turn.MemoryQuery, "Sarah Ahmed") || !strings.Contains(turn.MemoryQuery, "previous enquiry tour application") {
		t.Fatalf("inbound memory query: %q", turn.MemoryQuery)
	}
	if turn.Text != "" {
		t.Fatal("an opening turn must not carry user text")
	}

	outbound := &config.Agent{Persona: map[string]string{"opening": "outbound"}}
	turn, ok = OpeningTurn(outbound, "Sarah Ahmed")
	if !ok || !strings.HasPrefix(turn.Opening, "[Outbound call connected]") || !strings.Contains(turn.Opening, "You placed this call to Sarah Ahmed.") {
		t.Fatalf("outbound turn: %+v ok=%v", turn, ok)
	}
	if turn, _ = OpeningTurn(outbound, ""); !strings.Contains(turn.Opening, "the selected contact") || turn.MemoryQuery != "previous enquiry tour application" {
		t.Fatalf("outbound turn without a name: %+v", turn)
	}

	for _, persona := range []map[string]string{nil, {}, {"opening": "sideways"}} {
		if _, ok := OpeningTurn(&config.Agent{Persona: persona}, "x"); ok {
			t.Fatalf("persona %v must not speak first", persona)
		}
	}
}

func TestMemoryScopeUsesSharedDomain(t *testing.T) {
	a := &config.Agent{ID: "aquila-reception", Tenant: "t", Organization: "o"}
	a.Memory.Namespace = "n"
	s := &session.Session{Agent: a, UserID: "L001"}
	if got := Scope(s).Domain; got != "aquila-reception" {
		t.Fatalf("default domain %q", got)
	}
	a.Memory.Domain = "aquila-school"
	scope := Scope(s)
	if scope.Domain != "aquila-school" || scope.User != "L001" || scope.Namespace != "n" || scope.Tenant != "t" || scope.Organization != "o" {
		t.Fatalf("shared scope %+v", scope)
	}
	other := &config.Agent{ID: "aquila-admissions", Tenant: "t", Organization: "o"}
	other.Memory.Namespace = "n"
	other.Memory.Domain = "aquila-school"
	if Scope(&session.Session{Agent: other, UserID: "L001"}) != scope {
		t.Fatal("two personas with the same domain must share one memory scope")
	}
}

func TestAquilaPersonasShareFamilyMemoryAndOpeningIsNeverStoredAsUserText(t *testing.T) {
	r, agents, _ := setup(t)
	store := session.NewStore()
	reception := store.Create(agents["aquila-reception"], "L001")
	admissions := store.Create(agents["aquila-admissions"], "L001")
	outreach := store.Create(agents["aquila-outreach"], "L002")
	ctx := context.Background()
	if err := r.Memory.Store(ctx, memory.Memory{Scope: Scope(reception), Text: "2026-10-09: Sarah asked reception about the Mudon bus", Tags: []string{"bus"}}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Memory.Retrieve(ctx, memory.RetrieveRequest{Scope: Scope(admissions), Query: "Mudon bus"})
	if err != nil || len(got) != 1 {
		t.Fatalf("admissions should recall what reception stored: %v %v", got, err)
	}
	if other, _ := r.Memory.Retrieve(ctx, memory.RetrieveRequest{Scope: Scope(outreach), Query: "Mudon bus"}); len(other) != 0 {
		t.Fatal("memory leaked to a different family")
	}

	turn, ok := OpeningTurn(agents["aquila-outreach"], "Raj and Priya Menon")
	if !ok {
		t.Fatal("outreach persona must speak first")
	}
	r.Run(ctx, outreach, "open-1", turn)
	if len(outreach.History) == 0 {
		t.Fatal("opening produced no reply")
	}
	for _, m := range outreach.History {
		if m.Role == "user" || strings.Contains(m.Content, "Outbound call connected") {
			t.Fatalf("opening instruction stored in history: %+v", m)
		}
	}
	retrieved := false
	for _, e := range outreach.Events.Since(0) {
		if e.Type == "memory.retrieval.completed" {
			retrieved = true
		}
	}
	if !retrieved {
		t.Fatal("opening turn skipped memory retrieval")
	}
}

func TestOpeningCueOnlyAsksForLookupToolsTheAgentHas(t *testing.T) {
	withCRM := skills.Catalog{"family": {Tools: []tools.Definition{{Name: "contact.profile"}, {Name: "crm.history"}}}}
	if cue := openingCue("[Call answered] Greet.", withCRM); !strings.Contains(cue, "First call contact.profile and crm.history silently") || !strings.Contains(cue, "never your reasoning") {
		t.Fatal(cue)
	}
	if cue := openingCue("[Call answered] Greet.", skills.Catalog{}); strings.Contains(cue, "contact.profile") || strings.Contains(cue, "crm.history") || !strings.Contains(cue, "say only the greeting itself") {
		t.Fatal(cue)
	}
}
