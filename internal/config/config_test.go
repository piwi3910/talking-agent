package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgent(t *testing.T, root, id, memory string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"id":"` + id + `","name":"Test","organization":"Org","tenant":"tenant","memory":{"namespace":"ns"` + memory + `},"skills":["family"]}`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryDomainDefaultsToAgentID(t *testing.T) {
	root := t.TempDir()
	writeAgent(t, root, "solo", "")
	writeAgent(t, root, "shared-a", `,"domain":"shared-domain"`)
	writeAgent(t, root, "shared-b", `,"domain":"shared-domain"`)
	agents, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := agents["solo"].MemoryDomain(); got != "solo" {
		t.Fatalf("default domain %q", got)
	}
	if agents["shared-a"].MemoryDomain() != "shared-domain" || agents["shared-b"].MemoryDomain() != "shared-domain" {
		t.Fatal("configured domain not used")
	}
}

func TestMemoryDomainValidation(t *testing.T) {
	for _, bad := range []string{"has space", "-leading", "semi;colon", strings.Repeat("a", 65)} {
		root := t.TempDir()
		writeAgent(t, root, "bad", `,"domain":"`+bad+`"`)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "memory domain") {
			t.Fatalf("domain %q accepted: %v", bad, err)
		}
	}
}

func TestShippedAquilaPersonasShareOneMemoryScope(t *testing.T) {
	agents, err := Load("../../agents")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"aquila-admissions", "aquila-reception", "aquila-outreach"} {
		a := agents[id]
		if a == nil {
			t.Fatalf("missing persona %s", id)
		}
		if a.MemoryDomain() != "aquila-school" || a.Memory.Namespace != "aquila-demo" || a.Tenant != "aquila-demo" || a.Organization != "The Aquila School" || a.Industry != "aquila" {
			t.Fatalf("%s: unexpected shared memory identity %+v", id, a.Memory)
		}
		if want := map[string]string{"aquila-outreach": "outbound"}[id]; want != "" && a.Persona["opening"] != want {
			t.Fatalf("%s opening %q", id, a.Persona["opening"])
		} else if want == "" && a.Persona["opening"] != "inbound" {
			t.Fatalf("%s opening %q", id, a.Persona["opening"])
		}
	}
}
