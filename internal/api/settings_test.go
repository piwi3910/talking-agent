package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/telephony"
)

func TestPhoneSettingsAPI(t *testing.T) {
	accounts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := "account-a"
		if strings.HasSuffix(r.URL.Path, "/b") {
			id = "account-b"
		}
		json.NewEncoder(w).Encode([]map[string]string{{"id": id}})
	}))
	defer accounts.Close()
	agents := map[string]*config.Agent{"a": {ID: "a", Industry: "a"}, "b": {ID: "b", Industry: "b"}}
	settings, err := telephony.OpenSettings(filepath.Join(t.TempDir(), "phone.json"), agents, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &API{Agents: agents, PhoneSettings: settings, BackendURL: accounts.URL, Root: context.Background()}
	h := a.Handler()
	post := func(state telephony.PhoneSettings, origin string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(state)
		r := httptest.NewRequest("POST", "http://agent.test/api/settings/phone", bytes.NewReader(raw))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	state := settings.Snapshot()
	state.Personas["a"] = telephony.PersonaSettings{Numbers: []string{"500"}, UserID: "account-b"}
	if w := post(state, ""); w.Code != 400 {
		t.Fatalf("cross-persona account allowed: %d %s", w.Code, w.Body.String())
	}
	state.Personas["a"] = telephony.PersonaSettings{Numbers: []string{"500"}, UserID: "account-a", Cues: true}
	state.Personas["b"] = telephony.PersonaSettings{Numbers: []string{"501"}, UserID: "account-b", Cues: true}
	if w := post(state, "https://evil.test"); w.Code != 403 {
		t.Fatal("cross-origin settings write accepted")
	}
	w := post(state, "http://agent.test")
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if w := post(state, ""); w.Code != 409 {
		t.Fatal("stale settings were not rejected")
	}
	r := httptest.NewRequest("GET", "http://agent.test/api/settings/phone", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var saved telephony.PhoneSettings
	if err = json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || saved.Personas["b"].Numbers[0] != "501" {
		t.Fatal("saved settings unavailable")
	}
}
