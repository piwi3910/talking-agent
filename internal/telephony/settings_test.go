package telephony

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"enterprise-ai-demo/internal/config"
)

func TestSettingsDurableRoutesAndConflict(t *testing.T) {
	agents := map[string]*config.Agent{"a": {ID: "a"}, "b": {ID: "b"}}
	path := filepath.Join(t.TempDir(), "phone.json")
	s, err := OpenSettings(path, agents, map[string]string{"500": "a"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := s.Snapshot()
	p := snapshot.Personas["a"]
	p.Numbers = []string{"500", "+3212345"}
	p.UserID = "customer-a"
	snapshot.Personas["a"] = p
	p = snapshot.Personas["b"]
	p.Numbers = []string{"501"}
	snapshot.Personas["b"] = p
	saved, err := s.Save(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 {
		t.Fatal("revision not increased")
	}
	if _, err := s.Save(snapshot); !errors.Is(err, ErrSettingsConflict) {
		t.Fatalf("stale settings were saved: %v", err)
	}
	reopened, err := OpenSettings(path, agents, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, profile := reopened.Route("+3212345")
	if a != agents["a"] || profile.UserID != "customer-a" {
		t.Fatal("saved route/account not restored")
	}
	// Routing snapshots are immutable even if a settings update removes that number.
	edit := reopened.Snapshot()
	p = edit.Personas["a"]
	p.Numbers = []string{}
	edit.Personas["a"] = p
	if _, err := reopened.Save(edit); err != nil {
		t.Fatal(err)
	}
	if a, _ := reopened.Route("500"); a != nil {
		t.Fatal("removed number still routes")
	}
	if profile.Numbers[0] != "500" {
		t.Fatal("active call settings were modified")
	}
}
func TestSettingsRejectDuplicateNumbersAndFailedWrites(t *testing.T) {
	agents := map[string]*config.Agent{"a": {ID: "a"}, "b": {ID: "b"}}
	s, err := OpenSettings(filepath.Join(t.TempDir(), "settings.json"), agents, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := s.Snapshot()
	snapshot.Personas["a"] = PersonaSettings{Numbers: []string{"500"}}
	snapshot.Personas["b"] = PersonaSettings{Numbers: []string{"500"}}
	if _, err = s.Save(snapshot); err == nil {
		t.Fatal("duplicate number accepted")
	}
	snapshot.Personas["b"] = PersonaSettings{Numbers: []string{"501"}}
	s.path = t.TempDir() // rename to a directory must fail
	if _, err = s.Save(snapshot); err == nil {
		t.Fatal("failed persistence reported success")
	}
	if s.Snapshot().Revision != 0 {
		t.Fatal("routes changed despite save failure")
	}
}

func TestSettingsIgnoreSavedPersonaOfRemovedAgent(t *testing.T) {
	agents := map[string]*config.Agent{"a": {ID: "a"}}
	path := filepath.Join(t.TempDir(), "phone.json")
	saved := `{"revision":3,"personas":{"gone":{"numbers":["501"],"user_id":"x","cues":true},"a":{"numbers":["500"],"user_id":"u","cues":true}}}`
	if err := os.WriteFile(path, []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSettings(path, agents, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Route("500"); a != agents["a"] {
		t.Fatal("route of remaining agent lost")
	}
	if _, ok := s.Snapshot().Personas["gone"]; ok {
		t.Fatal("removed persona kept")
	}
}
