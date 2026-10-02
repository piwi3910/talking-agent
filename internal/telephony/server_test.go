package telephony

import (
	"bytes"
	"context"
	"encoding/binary"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"github.com/zaf/g711"
	"io"
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
		for _, b := range out.Bytes() {
			if b != expected {
				t.Fatal("bad resampling or codec")
			}
		}
	}
}
func TestCaptureStreamsDuringReply(t *testing.T) {
	speech := bytes.Repeat([]byte{g711.EncodeUlawFrame(3000)}, 1600)
	silence := bytes.Repeat([]byte{g711.EncodeUlawFrame(0)}, 6000)
	var streams []*pcmStream
	interrupted := false
	captureLive(context.Background(), io.MultiReader(bytes.NewReader(speech), bytes.NewReader(silence)), 0, func() *pcmStream {
		interrupted = true
		stream := newPCMStream(context.Background())
		streams = append(streams, stream)
		return stream
	})
	if !interrupted || len(streams) != 1 {
		t.Fatal("speech did not interrupt and start ASR")
	}
	raw, err := io.ReadAll(streams[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 1600*4 || len(raw)%4 != 0 {
		t.Fatalf("bad 16kHz stream length: %d", len(raw))
	}
}
