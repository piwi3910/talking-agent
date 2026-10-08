package telephony

import (
	"bytes"
	"context"
	"encoding/binary"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"github.com/zaf/g711"
	"io"
	"math"
	"os"
	"testing"
)

func TestConfigRoutesAndPeers(t *testing.T) {
	agents := map[string]*config.Agent{"one": {ID: "one"}, "two": {ID: "two"}}
	c := Config{BindHost: "0.0.0.0", Port: 5060, AdvertiseIP: "192.0.2.10", RTPStart: 10000, RTPEnd: 10100, MaxCalls: 10, AllowedPeers: []string{"192.0.2.0/24"}, Numbers: map[string]string{"500": "one", "501": "two"}}
	if err := c.validate(agents); err != nil {
		t.Fatal(err)
	}
	if !c.allowed("192.0.2.20:5060") || c.allowed("198.51.100.1:5060") || c.allowed("invalid") {
		t.Fatal("peer allowlist failed")
	}
	c.Numbers["502"] = "missing"
	if c.validate(agents) == nil {
		t.Fatal("unknown agent accepted")
	}
	delete(c.Numbers, "502")
	c.AdvertiseIP = "0.0.0.0"
	if c.validate(agents) == nil {
		t.Fatal("unspecified advertised IP accepted")
	}
}
func TestCallSessionIsolation(t *testing.T) {
	store := session.NewStore()
	a := &config.Agent{ID: "one"}
	b := &config.Agent{ID: "two"}
	first := store.Create(a, "sip-anonymous:"+session.ID())
	second := store.Create(a, "sip-anonymous:"+session.ID())
	third := store.Create(b, "sip-anonymous:"+session.ID())
	if first.ID == second.ID || first.UserID == second.UserID || third.Agent != b {
		t.Fatal("call identity or routing shared")
	}
	first.Active["test"] = true
	if second.Active["test"] {
		t.Fatal("shared conversation state")
	}
	store.Delete(first.ID)
	if store.Get(first.ID) != nil || store.Get(second.ID) != second {
		t.Fatal("call cleanup affected other session")
	}
}
func TestPlaybackChunkBoundariesAndCodecs(t *testing.T) {
	for _, pt := range []uint8{0, 8} {
		var raw []byte
		for i := 0; i < 480; i++ {
			raw = binary.LittleEndian.AppendUint16(raw, uint16(1200))
		}
		var out bytes.Buffer
		err := playback(context.Background(), &out, pt, func(emit func([]byte) error) error {
			for _, p := range raw {
				if err := emit([]byte{p}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if out.Len() != 160 {
			t.Fatalf("expected 20ms packet, got %d", out.Len())
		}
		expected := g711.EncodeUlawFrame(1200)
		if pt == 8 {
			expected = g711.EncodeAlawFrame(1200)
		}
		// A constant input passes through the low-pass unchanged once the
		// filter has filled (about 5 ms); the settled part must be exact.
		for _, b := range out.Bytes()[48 : 160-48] {
			if b != expected {
				t.Fatal("bad resampling or codec")
			}
		}
	}
}

// recordedSpeech returns a real speech recording (8 kHz, 16-bit) as G.711 plus
// lead-in and trailing line noise-free silence, in 20 ms RTP-sized reads.
func recordedSpeech(t *testing.T, lead, tail int) []byte {
	t.Helper()
	pcm, err := os.ReadFile("../audio/testdata/speech_a.raw")
	if err != nil {
		t.Fatal(err)
	}
	out := bytes.Repeat([]byte{g711.EncodeUlawFrame(0)}, lead)
	out = append(out, g711.EncodeUlaw(pcm)...)
	return append(out, bytes.Repeat([]byte{g711.EncodeUlawFrame(0)}, tail)...)
}

// packets splits a G.711 stream into 160-byte RTP payloads like a real call.
type packets struct{ data []byte }

func (p *packets) Read(b []byte) (int, error) {
	if len(p.data) == 0 {
		return 0, io.EOF
	}
	n := min(160, len(p.data), len(b))
	copy(b, p.data[:n])
	p.data = p.data[n:]
	return n, nil
}
func TestCaptureStreamsDuringReply(t *testing.T) {
	var streams []*pcmStream
	interrupted := false
	captureLive(context.Background(), &packets{recordedSpeech(t, 4000, 12000)}, 0, func() *pcmStream {
		interrupted = true
		stream := newPCMStream(context.Background())
		streams = append(streams, stream)
		return stream
	})
	if !interrupted || len(streams) != 1 {
		t.Fatalf("speech did not interrupt and start ASR exactly once: %d streams", len(streams))
	}
	raw, err := io.ReadAll(streams[0])
	if err != nil {
		t.Fatal(err)
	}
	// The utterance (3.6 s of 8 kHz speech) arrives as 16 kHz PCM, plus the
	// pre-roll before the onset and up to 700 ms of trailing non-speech.
	if len(raw) < 3*16000*2 || len(raw)%4 != 0 || len(raw) > 6*16000*2 {
		t.Fatalf("bad 16kHz stream length: %d", len(raw))
	}
}

// Barge-in must ignore line noise and short blips but start on real speech.
func TestCaptureIgnoresNoiseAndStartsOnSpeech(t *testing.T) {
	noise := make([]byte, 8000*5)
	for i := range noise { // loud hiss: mean |sample| far above the old fixed 450 threshold
		noise[i] = g711.EncodeUlawFrame(int16(((i*7919)%4001 - 2000)))
	}
	started := 0
	captureLive(context.Background(), &packets{noise}, 0, func() *pcmStream { started++; return newPCMStream(context.Background()) })
	if started != 0 {
		t.Fatalf("steady noise started %d turns", started)
	}
	// A 60 ms click is not 128 ms of sustained speech.
	click := bytes.Repeat([]byte{g711.EncodeUlawFrame(0)}, 4000)
	for i := 0; i < 480; i++ {
		click = append(click, g711.EncodeUlawFrame(int16(((i*7919)%20001 - 10000))))
	}
	click = append(click, bytes.Repeat([]byte{g711.EncodeUlawFrame(0)}, 4000)...)
	captureLive(context.Background(), &packets{click}, 0, func() *pcmStream { started++; return newPCMStream(context.Background()) })
	if started != 0 {
		t.Fatalf("a short click started %d turns", started)
	}
	captureLive(context.Background(), &packets{recordedSpeech(t, 4000, 8000)}, 0, func() *pcmStream { started++; return newPCMStream(context.Background()) })
	if started != 1 {
		t.Fatalf("speech started %d turns", started)
	}
}

// Out-of-band TTS energy must not alias into the telephone band.
func TestPlaybackRejectsAliasing(t *testing.T) {
	level := func(hz float64) float64 {
		var raw []byte
		for i := 0; i < 24000; i++ {
			raw = binary.LittleEndian.AppendUint16(raw, uint16(int16(10000*math.Sin(2*math.Pi*hz*float64(i)/24000))))
		}
		var out bytes.Buffer
		if err := playback(context.Background(), &out, 0, func(emit func([]byte) error) error { return emit(raw) }); err != nil {
			t.Fatal(err)
		}
		var sum float64
		pcm := g711.DecodeUlaw(out.Bytes())
		for i := 0; i+1 < len(pcm); i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(pcm[i:])))
			sum += v * v
		}
		return math.Sqrt(sum / float64(len(pcm)/2))
	}
	inBand, alias := level(1000), level(5000)
	if alias > inBand/100 {
		t.Fatalf("5 kHz leaks through at %.0f vs %.0f in band", alias, inBand)
	}
}
