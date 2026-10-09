package voices

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"enterprise-ai-demo/internal/speech"
)

// tone is a 220 Hz sine of the given peak amplitude, padded with digital silence.
func tone(rate int, lead, body, trail float64, amp float64) []byte {
	n := func(sec float64) int { return int(sec * float64(rate)) }
	samples := make([]int16, n(lead)+n(body)+n(trail))
	for i := 0; i < n(body); i++ {
		v := math.Round(amp * math.Sin(2*math.Pi*220*float64(i)/float64(rate)))
		samples[n(lead)+i] = int16(math.Max(-32768, math.Min(32767, v)))
	}
	return speech.WAVAt(speech.PCMBytes(samples), rate)
}

func status(t *testing.T, a *Analysis, id string) string {
	t.Helper()
	for _, c := range a.Report.Checks {
		if c.ID == id {
			return c.Status
		}
	}
	t.Fatalf("no check %s", id)
	return ""
}

func TestAnalyzeCloneQualityMetrics(t *testing.T) {
	for name, tc := range map[string]struct {
		wav                                []byte
		duration, level, clipping, silence string
	}{
		"good":           {tone(24000, 0.5, 6, 0.5, 8000), "pass", "pass", "pass", "pass"},
		"too short":      {tone(24000, 0.5, 2, 0.5, 8000), "fail", "pass", "pass", "pass"},
		"a bit short":    {tone(24000, 0.5, 3.5, 0.5, 8000), "warn", "pass", "pass", "pass"},
		"too long":       {tone(16000, 0, 26.5, 0, 8000), "fail", "pass", "pass", "pass"},
		"quiet":          {tone(24000, 0.5, 6, 0.5, 300), "pass", "warn", "pass", "pass"},
		"very quiet":     {tone(24000, 0.5, 6, 0.5, 100), "pass", "fail", "pass", "pass"},
		"clipped":        {tone(24000, 0.5, 6, 0.5, 40000), "pass", "pass", "fail", "pass"},
		"mostly silence": {tone(24000, 8, 5, 8, 8000), "pass", "pass", "pass", "warn"},
		"48 kHz":         {tone(48000, 0.2, 5, 0.2, 8000), "pass", "pass", "pass", "pass"},
	} {
		t.Run(name, func(t *testing.T) {
			a, err := AnalyzeClone(tc.wav)
			if err != nil {
				t.Fatal(err)
			}
			got := [4]string{status(t, a, "duration"), status(t, a, "level"), status(t, a, "clipping"), status(t, a, "silence")}
			if want := [4]string{tc.duration, tc.level, tc.clipping, tc.silence}; got != want {
				t.Fatalf("got %v want %v: %+v", got, want, a.Report)
			}
		})
	}
}

func TestAnalyzeCloneTrimsSilenceAndReportsLevels(t *testing.T) {
	a, err := AnalyzeClone(tone(24000, 1, 6, 2, 8000))
	if err != nil {
		t.Fatal(err)
	}
	// 6 s of speech plus 150 ms of padding on each side.
	if d := a.Report.DurationMS; d < 6200 || d > 6400 {
		t.Fatalf("duration %d ms", d)
	}
	if a.Audio.Rate != 24000 || len(a.Audio.Samples) != a.Report.DurationMS*24 {
		t.Fatalf("trimmed audio: %d Hz, %d samples", a.Audio.Rate, len(a.Audio.Samples))
	}
	// A sine of amplitude 8000 has an RMS of 5657, about -15.3 dBFS.
	if math.Abs(a.Report.RMSdBFS-(-15.3)) > 0.6 {
		t.Fatalf("rms %.1f dBFS", a.Report.RMSdBFS)
	}
	if a.Report.ClippingRatio != 0 || !a.Speech {
		t.Fatalf("%+v", a.Report)
	}
	clipped, err := AnalyzeClone(tone(24000, 0.5, 6, 0.5, 40000))
	if err != nil {
		t.Fatal(err)
	}
	if clipped.Report.ClippingRatio < 0.2 {
		t.Fatalf("clipping ratio %.3f", clipped.Report.ClippingRatio)
	}
	silent, err := AnalyzeClone(speech.WAVAt(make([]byte, 24000*2*5), 24000))
	if err != nil {
		t.Fatal(err)
	}
	if silent.Speech || status(t, silent, "silence") != "fail" || silent.Report.RMSdBFS != -120 {
		t.Fatalf("silent recording: %+v", silent.Report)
	}
}

func TestAnalyzeCloneRejectsBadAudio(t *testing.T) {
	good := tone(24000, 0, 5, 0, 8000)
	patch := func(offset int, value ...byte) []byte {
		b := append([]byte{}, good...)
		copy(b[offset:], value)
		return b
	}
	for name, wav := range map[string][]byte{
		"empty":           nil,
		"not riff":        append([]byte("RIFX"), good[4:]...),
		"not wave":        patch(8, 'A', 'V', 'I', ' '),
		"stereo":          patch(22, 2, 0),
		"8-bit":           patch(34, 8, 0),
		"float format":    patch(20, 3, 0),
		"8 kHz":           patch(24, 0x40, 0x1f, 0, 0),
		"96 kHz":          patch(24, 0x00, 0x77, 0x01, 0),
		"no data":         good[:36],
		"too large":       append(append([]byte{}, good...), make([]byte, MaxCloneAudio)...),
		"truncated chunk": good[:30],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := AnalyzeClone(wav); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	// A data chunk that claims more than the file holds is read to the end of the file.
	if a, err := AnalyzeClone(patch(40, 0xff, 0xff, 0xff, 0x7f)); err != nil || a.Report.DurationMS == 0 {
		t.Fatalf("streamed header: %v", err)
	}
}

func cloneRequest(s *Store, id, transcript string) CloneRequest {
	return CloneRequest{Revision: s.Snapshot().Revision, ID: id, Name: "My voice", Transcript: transcript, Audio: tone(24000, 0.5, 6, 0.5, 8000)}
}

func kindOf(s Snapshot, id string) string {
	for _, v := range s.Voices {
		if v.ID == id {
			return v.Kind
		}
	}
	return ""
}

func TestCreateCloneStoresFilesResolvesAndChangesCueKey(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, nil)
	before := s.key("a")
	snap, err := s.CreateClone(cloneRequest(s, "my-voice", "  The quick   brown fox. "))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Revision != 1 || kindOf(snap, "my-voice") != KindClone || kindOf(snap, "preset-ryan") != KindPreset {
		t.Fatalf("%+v", snap)
	}
	dir := filepath.Join(f.dir, "var", "voices", "my-voice")
	wav, err := os.ReadFile(filepath.Join(dir, "reference.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := speech.ParseWAV(wav); err != nil || a.Rate != 24000 || len(a.Samples) > 24000*7 {
		t.Fatalf("stored reference: %v %+v", err, a.Rate)
	}
	if txt, _ := os.ReadFile(filepath.Join(dir, "reference.txt")); string(txt) != "The quick brown fox." {
		t.Fatalf("transcript %q", txt)
	}
	if matches, _ := filepath.Glob(filepath.Join(f.dir, "var", "voices", ".tmp-*")); len(matches) != 0 {
		t.Fatalf("temp dirs left: %v", matches)
	}
	if _, err = os.Stat(filepath.Join(f.agents["a"].Dir, "reference.wav")); err == nil {
		t.Fatal("clone written inside an agent dir")
	}

	if _, err = s.Save(request(s, func(in *SaveRequest) {
		in.Personas["a"] = Persona{VoiceID: "my-voice"}
		in.Personas["b"] = Persona{VoiceID: "my-voice", Direction: "Calm."}
	})); err != nil {
		t.Fatal(err)
	}
	v := s.Resolve("a")
	if v.Model != speech.ModelQwen3Base || v.Reference == nil || v.Reference.Text != "The quick brown fox." || v.Instruct != "" {
		t.Fatalf("clone mode: %+v", v)
	}
	// A Base clone has no instruction support, so direction does not change it.
	if v = s.Resolve("b"); v.Reference == nil || v.Instruct != "" {
		t.Fatalf("direction on a clone: %+v", v)
	}
	first := s.key("a")
	if first == before || first != s.key("b") {
		t.Fatal("cue key must follow the clone, not the ignored direction")
	}
	// Same audio, different transcript: a different reference, a different key.
	if _, err = s.CreateClone(cloneRequest(s, "other-voice", "A different sentence.")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "other-voice"} })); err != nil {
		t.Fatal(err)
	}
	if s.key("a") == first {
		t.Fatal("cue key ignores the clone's reference content")
	}
	if st := s.Snapshot().Cues["a"].State; st != "queued" {
		t.Fatalf("assigning a clone must queue its cue render, got %s", st)
	}

	// Everything survives a restart.
	reopened := f.open(t, nil)
	if v = reopened.Resolve("a"); v.Reference == nil || v.Reference.Text != "A different sentence." {
		t.Fatalf("after restart: %+v", v)
	}
	if kindOf(reopened.Snapshot(), "my-voice") != KindClone {
		t.Fatal("clone lost on restart")
	}
}

func TestCreateCloneValidation(t *testing.T) {
	s := newFixture(t).open(t, nil)
	good := cloneRequest(s, "my-voice", "Hello there.")
	for name, mutate := range map[string]func(*CloneRequest){
		"bad id":            func(r *CloneRequest) { r.ID = "My Voice" },
		"ref prefix":        func(r *CloneRequest) { r.ID = "ref-mine" },
		"builtin id":        func(r *CloneRequest) { r.ID = "preset-ryan" },
		"empty name":        func(r *CloneRequest) { r.Name = "  " },
		"long name":         func(r *CloneRequest) { r.Name = strings.Repeat("n", 61) },
		"empty transcript":  func(r *CloneRequest) { r.Transcript = " \n " },
		"long transcript":   func(r *CloneRequest) { r.Transcript = strings.Repeat("a", 501) },
		"short recording":   func(r *CloneRequest) { r.Audio = tone(24000, 0.5, 1, 0.5, 8000) },
		"very quiet":        func(r *CloneRequest) { r.Audio = tone(24000, 0.5, 6, 0.5, 100) },
		"stereo":            func(r *CloneRequest) { r.Audio = append([]byte{}, r.Audio...); r.Audio[22] = 2 },
		"not a recording":   func(r *CloneRequest) { r.Audio = []byte("nope") },
		"nothing but quiet": func(r *CloneRequest) { r.Audio = speech.WAVAt(make([]byte, 24000*10), 24000) },
	} {
		t.Run(name, func(t *testing.T) {
			in := good
			mutate(&in)
			if _, err := s.CreateClone(in); err == nil || errors.Is(err, ErrConflict) || errors.Is(err, ErrStorage) {
				t.Fatalf("%v", err)
			}
			if s.Snapshot().Revision != 0 {
				t.Fatal("revision changed")
			}
		})
	}
	if _, err := s.CreateClone(good); err != nil {
		t.Fatal(err)
	}
	dup := cloneRequest(s, "my-voice", "Hello again.")
	if _, err := s.CreateClone(dup); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate id: %v", err)
	}
	stale := good
	stale.ID = "second"
	if _, err := s.CreateClone(stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
}

func TestCreateCloneStorageFailureLeavesNothingBehind(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, nil)
	// A file where the settings directory should be makes every write fail.
	if err := os.WriteFile(filepath.Join(f.dir, "var"), []byte("file in the way"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateClone(cloneRequest(s, "my-voice", "Hello there.")); !errors.Is(err, ErrStorage) {
		t.Fatalf("%v", err)
	}
	if s.Snapshot().Revision != 0 || kindOf(s.Snapshot(), "my-voice") != "" {
		t.Fatal("memory changed after a failed write")
	}
	// The settings write fails after the files were stored: they must be rolled back.
	f2 := newFixture(t)
	s2 := f2.open(t, nil)
	if err := os.MkdirAll(filepath.Join(f2.dir, "var", "voices"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f2.dir, "var", "voices.json"), 0700); err != nil { // a directory cannot be replaced by a file
		t.Fatal(err)
	}
	if _, err := s2.CreateClone(cloneRequest(s2, "my-voice", "Hello there.")); !errors.Is(err, ErrStorage) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(f2.dir, "var", "voices", "my-voice")); err == nil {
		t.Fatal("clone files kept after the settings write failed")
	}
}

func TestSaveRenamesAndDeletesClones(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, nil)
	for _, id := range []string{"keep", "gone"} {
		if _, err := s.CreateClone(cloneRequest(s, id, "Hello there.")); err != nil {
			t.Fatal(err)
		}
	}
	goneDir := filepath.Join(f.dir, "var", "voices", "gone")
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "gone"} })); err != nil {
		t.Fatal(err)
	}
	omit := func(in *SaveRequest) {
		var kept []Custom
		for _, v := range in.Voices {
			if v.ID != "gone" {
				kept = append(kept, v)
			}
		}
		in.Voices = kept
	}
	// Still assigned: blocked, and the files stay.
	if _, err := s.Save(request(s, omit)); err == nil || !strings.Contains(err.Error(), "still assigned") {
		t.Fatalf("assigned clone deleted: %v", err)
	}
	if _, err := os.Stat(goneDir); err != nil {
		t.Fatal("files removed by a rejected save")
	}
	// Unassigned but the save fails for another reason: the files stay.
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		omit(in)
		in.Personas["a"] = Persona{VoiceID: "nope"}
	})); err == nil {
		t.Fatal("invalid save accepted")
	}
	if _, err := os.Stat(goneDir); err != nil {
		t.Fatal("files removed by a failed save")
	}
	// Rename keeps the clone; omitting it after unassigning deletes it, after the save.
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Personas["a"] = Persona{VoiceID: "preset-ryan"}
		for i := range in.Voices {
			if in.Voices[i].ID == "keep" {
				in.Voices[i].Name = "Renamed"
			}
		}
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(goneDir); err != nil {
		t.Fatal("rename removed an unrelated clone")
	}
	saved, err := s.Save(request(s, omit))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(goneDir); !os.IsNotExist(err) {
		t.Fatalf("omitted clone not removed: %v", err)
	}
	if kindOf(saved, "gone") != "" || kindOf(saved, "keep") != KindClone {
		t.Fatalf("%+v", saved.Voices)
	}
	for _, v := range saved.Voices {
		if v.ID == "keep" && v.Name != "Renamed" {
			t.Fatalf("rename lost: %+v", v)
		}
	}
	if _, err = os.Stat(filepath.Join(f.dir, "var", "voices", "keep", "reference.wav")); err != nil {
		t.Fatal(err)
	}
}

func TestSaveCannotCreateCloneOrChangeKind(t *testing.T) {
	s := newFixture(t).open(t, nil)
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Voices = []Custom{{ID: "ghost", Name: "Ghost", Kind: KindClone}} })); err == nil {
		t.Fatal("a clone without reference files was accepted")
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) {
		in.Voices = []Custom{{ID: "x", Name: "X", Description: "d", Kind: "bogus"}}
	})); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := s.CreateClone(cloneRequest(s, "mine", "Hello there.")); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Custom){
		"clone with a description": func(c *Custom) { c.Description = "Deep." },
		"clone turned into design": func(c *Custom) { c.Kind, c.Description = KindDesign, "Deep." },
		"design kind implied":      func(c *Custom) { c.Kind, c.Description = "", "Deep." },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Save(request(s, func(in *SaveRequest) { mutate(&in.Voices[0]) })); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestOpenDropsCloneWithMissingFilesWithoutFailing(t *testing.T) {
	f := newFixture(t)
	s := f.open(t, nil)
	for _, id := range []string{"mine", "broken"} {
		if _, err := s.CreateClone(cloneRequest(s, id, "Hello there.")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Save(request(s, func(in *SaveRequest) { in.Personas["a"] = Persona{VoiceID: "broken"} })); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.dir, "var", "voices", "broken", "reference.txt")); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t, nil)
	snap := reopened.Snapshot()
	if kindOf(snap, "broken") != "" || kindOf(snap, "mine") != KindClone {
		t.Fatalf("%+v", snap.Voices)
	}
	if snap.Personas["a"].VoiceID != "preset-ryan" {
		t.Fatalf("persona kept a dropped clone: %+v", snap.Personas["a"])
	}
	// The whole directory missing is the same.
	if err := os.RemoveAll(filepath.Join(f.dir, "var", "voices")); err != nil {
		t.Fatal(err)
	}
	if kindOf(f.open(t, nil).Snapshot(), "mine") != "" {
		t.Fatal("clone with no files survived")
	}
}

func TestOpenIgnoresPathTraversalInCloneIDs(t *testing.T) {
	f := newFixture(t)
	raw := `{"revision":1,"voices":[{"id":"../../etc","name":"Evil","description":"","kind":"clone"}],"personas":{}}`
	if err := os.MkdirAll(filepath.Join(f.dir, "var"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "var", "voices.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(f.dir, "var", "voices.json"), filepath.Join(f.dir, "var", "cues"), f.agents, nil); err == nil {
		t.Fatal("invalid saved clone id accepted")
	}
}
