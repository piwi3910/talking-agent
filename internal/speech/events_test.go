package speech

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVocalEventsAllowStripAndLeaveOtherParentheses(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		allow    bool
		want     string
	}{
		{"allow normalises case", "Well (LAUGH) okay (Clears Throat) and (Sigh) (cough)", true, "Well (laugh) okay (clears throat) and (sigh) (cough)"},
		{"strip removes events and double spaces", "Well (laugh) okay (Sigh) then", false, "Well okay then"},
		{"strip trims a leading event", "(cough) Hello", false, "Hello"},
		{"other parentheticals survive when allowed", "Call (555) or (smile) now", true, "Call (555) or (smile) now"},
		{"other parentheticals survive when stripped", "Call (555) or (smile) (laugh) now", false, "Call (555) or (smile) now"},
		{"events with extra words are not events", "(laughs loudly) (laugh out loud)", false, "(laughs loudly) (laugh out loud)"},
		{"text without events is untouched, spacing included", "a  b", false, "a  b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := VocalEvents(tc.in, tc.allow); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSynthesizeBodyCarriesGuidanceAndOmitsEmptyInstruction(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, b)
		w.Write([]byte{1, 0})
	}))
	defer server.Close()
	c := Client{TTSURL: server.URL}
	for _, v := range []Voice{{Instruction: "Deep and slow", Guidance: "4"}, {Guidance: "1"}, {}} {
		if err := c.Synthesize(context.Background(), "Hi", v, func([]byte) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	first := bodies[0]["options"].(map[string]any)
	if first["guidance_scale"] != "4" || first["instruction"] != "Deep and slow" || first["seed"] != "42" {
		t.Fatalf("options %v", first)
	}
	second := bodies[1]["options"].(map[string]any)
	if _, ok := second["instruction"]; ok || second["guidance_scale"] != "1" {
		t.Fatalf("empty instruction sent: %v", second)
	}
	if third := bodies[2]["options"].(map[string]any); third["guidance_scale"] != nil {
		t.Fatalf("empty guidance sent: %v", third)
	}
	if _, ok := bodies[0]["voice_ref"]; ok {
		t.Fatal("design voice sent a reference")
	}
}

func TestWAVHeaderDescribes24kMonoPCM(t *testing.T) {
	w := WAV([]byte{1, 0, 2, 0})
	if string(w[:4]) != "RIFF" || string(w[8:12]) != "WAVE" || len(w) != 48 || binary.LittleEndian.Uint32(w[24:]) != 24000 || binary.LittleEndian.Uint16(w[22:]) != 1 || binary.LittleEndian.Uint32(w[40:]) != 4 {
		t.Fatalf("bad header % x", w[:44])
	}
}
