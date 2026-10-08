package audio

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"testing"
)

func loadSpeech(t testing.TB, name string, rmsTarget float64) []int16 {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, len(raw)/2)
	var sum float64
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
		sum += float64(pcm[i]) * float64(pcm[i])
	}
	gain := rmsTarget / math.Sqrt(sum/float64(len(pcm)))
	for i := range pcm {
		pcm[i] = int16(math.Max(-32768, math.Min(32767, float64(pcm[i])*gain)))
	}
	return pcm
}

func addNoise(x []int16, sigma float64, rng *rand.Rand) []int16 {
	out := make([]int16, len(x))
	for i, v := range x {
		out[i] = int16(math.Max(-32768, math.Min(32767, float64(v)+rng.NormFloat64()*sigma)))
	}
	return out
}

// decisions runs the detector and returns one decision per 10 ms frame.
func decisions(x []int16) []bool {
	var out []bool
	NewVAD().Process(x, func(v bool) { out = append(out, v) })
	return out
}

func fraction(d []bool, from, to int) float64 {
	n := 0
	for _, v := range d[from:to] {
		if v {
			n++
		}
	}
	return float64(n) / float64(to-from)
}

// scene is noise, speech, noise; frame indices of the speech span are returned.
func scene(speech []int16, sigma float64, lead, tail int) (x []int16, start, end int) {
	rng := rand.New(rand.NewSource(1))
	x = addNoise(make([]int16, lead), sigma, rng)
	start = len(x) / VADFrame
	x = append(x, addNoise(speech, sigma, rng)...)
	end = len(x) / VADFrame
	x = append(x, addNoise(make([]int16, tail), sigma, rng)...)
	return
}

func TestVADRecordedSpeechAcrossNoiseLevels(t *testing.T) {
	for _, tc := range []struct {
		file  string
		level float64 // speech RMS
		sigma float64 // noise RMS
	}{
		{"speech_a.raw", 2000, 0},
		{"speech_a.raw", 2000, 30},
		{"speech_a.raw", 2000, 200}, // 20 dB SNR
		{"speech_b.raw", 2000, 200},
		{"speech_b.raw", 800, 200}, // 12 dB SNR, quiet talker
		{"speech_a.raw", 300, 20},  // very quiet but clean line
		{"speech_b.raw", 4000, 600},
	} {
		sp := loadSpeech(t, tc.file, tc.level)
		x, start, end := scene(sp, tc.sigma, 8000, 8000)
		d := decisions(x)
		lead := fraction(d, 5, start-1)
		tail := fraction(d, end+20, len(d)) // allow the hangover and the synthesized voice decay
		inside := fraction(d, start, end)
		t.Logf("%s speech=%.0f noise=%.0f: lead %.0f%% inside %.0f%% tail %.0f%%", tc.file, tc.level, tc.sigma, lead*100, inside*100, tail*100)
		if lead > 0.02 || tail > 0.02 {
			t.Errorf("false positives in noise: lead %.2f tail %.2f", lead, tail)
		}
		if inside < 0.6 {
			t.Errorf("speech detected in only %.0f%% of frames", inside*100)
		}
	}
}

func TestVADRejectsNonSpeech(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	const n = 8000 * 10
	gen := map[string]func(i int) float64{
		"digital silence":   func(int) float64 { return 0 },
		"quiet white noise": func(int) float64 { return rng.NormFloat64() * 50 },
		"loud white noise":  func(int) float64 { return rng.NormFloat64() * 2000 },
		"mains hum 50 Hz":   func(i int) float64 { return 6000 * math.Sin(2*math.Pi*50*float64(i)/8000) },
		"hum with harmonics": func(i int) float64 {
			f := float64(i) / 8000
			return 3000*math.Sin(2*math.Pi*100*f) + 2000*math.Sin(2*math.Pi*200*f)
		},
		"steady 1 kHz tone": func(i int) float64 { return 8000 * math.Sin(2*math.Pi*1000*float64(i)/8000) },
		"DC offset":         func(int) float64 { return 3000 },
		"ringback 440+480": func(i int) float64 {
			f := float64(i) / 8000
			return 3000 * (math.Sin(2*math.Pi*440*f) + math.Sin(2*math.Pi*480*f))
		},
	}
	for name, g := range gen {
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(math.Max(-32768, math.Min(32767, g(i))))
		}
		d := decisions(x)
		if f := fraction(d, 10, len(d)); f > 0.02 {
			t.Errorf("%s classified as speech in %.1f%% of frames", name, f*100)
		}
	}
}

func TestVADAdaptsToNoiseStep(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	quiet := addNoise(make([]int16, 8000*2), 40, rng)
	loud := addNoise(make([]int16, 8000*8), 700, rng) // line gets noisy mid-call
	d := decisions(append(quiet, loud...))
	// After the floor has had time to rise the line must be quiet again.
	if f := fraction(d, len(d)-300, len(d)); f > 0.02 {
		t.Fatalf("still triggering on steady noise after adaptation: %.1f%%", f*100)
	}
	t.Logf("first second after the noise step: %.0f%% voiced", fraction(d, 200, 300)*100)
}

func TestVADChunkInvariantAndFrameCount(t *testing.T) {
	sp := loadSpeech(t, "speech_b.raw", 2000)
	want := decisions(sp)
	if len(want) != len(sp)/VADFrame {
		t.Fatalf("frames %d for %d samples", len(want), len(sp))
	}
	var got []bool
	v := NewVAD()
	for i := 0; i < len(sp); {
		n := 1 + (i*13)%173
		if i+n > len(sp) {
			n = len(sp) - i
		}
		v.Process(sp[i:i+n], func(b bool) { got = append(got, b) })
		i += n
	}
	if len(got) != len(want) {
		t.Fatalf("len %d vs %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d differs between chunkings", i)
		}
	}
}

func BenchmarkVADPacket20ms(b *testing.B) {
	sp := loadSpeech(b, "speech_a.raw", 2000)
	v := NewVAD()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		off := (i * 160) % (len(sp) - 160)
		v.Process(sp[off:off+160], func(bool) {})
	}
}

func TestVADFalseAlarmRateOnLongNoise(t *testing.T) {
	for _, sigma := range []float64{20, 100, 400, 1500} {
		for seed := int64(0); seed < 3; seed++ {
			x := addNoise(make([]int16, 8000*60), sigma, rand.New(rand.NewSource(seed)))
			d := decisions(x)
			if f := fraction(d, 10, len(d)); f > 0.01 {
				t.Errorf("sigma %.0f seed %d: %.2f%% false alarms", sigma, seed, f*100)
			}
		}
	}
}

// A caller who talks the instant the call connects must still be heard.
func TestVADSpeechFromTheFirstFrame(t *testing.T) {
	sp := loadSpeech(t, "speech_a.raw", 2000)
	x := addNoise(sp, 30, rand.New(rand.NewSource(4)))
	d := decisions(x)
	first := -1
	for i, v := range d {
		if v {
			first = i
			break
		}
	}
	t.Logf("first voiced frame %d (%d ms)", first, first*10)
	if first < 0 || first > 60 {
		t.Fatalf("speech from call start not detected promptly: first=%d", first)
	}
	if f := fraction(d, first, 360); f < 0.6 {
		t.Fatalf("only %.0f%% detected", f*100)
	}
}
