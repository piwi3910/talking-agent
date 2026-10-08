package audio

import (
	"math"
	"testing"
)

func sine(rate int, hz, amp float64, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amp * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
	}
	return out
}

// rms ignores the filter's start-up and tail transients.
func rms(x []int16) float64 {
	skip := len(x) / 8
	var s float64
	for _, v := range x[skip : len(x)-skip] {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(len(x)-2*skip))
}

func db(ratio float64) float64 { return 20 * math.Log10(ratio+1e-12) }

func TestDownsampleFrequencyResponse(t *testing.T) {
	const amp = 10000.0
	for _, tc := range []struct {
		hz       float64
		min, max float64 // allowed gain in dB
	}{
		{100, -0.5, 0.5},
		{1000, -0.5, 0.5},
		{3000, -0.5, 0.5},
		{3400, -1.0, 0.5},
		// Out of band tones fold back into the telephone band if not removed.
		{4600, -400, -55},
		{5000, -400, -55},
		{7000, -400, -55},
		{9500, -400, -55},
		{11000, -400, -55},
	} {
		out := Down24kTo8k().Process(nil, sine(24000, tc.hz, amp, 24000))
		g := db(rms(out) / (amp / math.Sqrt2))
		t.Logf("%5.0f Hz: %7.1f dB", tc.hz, g)
		if g < tc.min || g > tc.max {
			t.Errorf("%.0f Hz gain %.1f dB outside [%.1f, %.1f]", tc.hz, g, tc.min, tc.max)
		}
	}
}

// The previous 3-sample average alias rejection, kept as a regression baseline.
func TestBeatsThreeSampleAverage(t *testing.T) {
	in := sine(24000, 5000, 10000, 24000)
	old := make([]int16, 0, len(in)/3)
	for i := 0; i+3 <= len(in); i += 3 {
		old = append(old, int16((int32(in[i])+int32(in[i+1])+int32(in[i+2]))/3))
	}
	neu := Down24kTo8k().Process(nil, in)
	t.Logf("5 kHz alias: old %.1f dB, new %.1f dB", db(rms(old)/7071), db(rms(neu)/7071))
	if rms(neu)*100 > rms(old) {
		t.Fatal("polyphase filter must reject the alias at least 40 dB better than averaging")
	}
}

func TestUpsampleRemovesImages(t *testing.T) {
	const amp = 10000.0
	in := sine(8000, 1000, amp, 8000)
	out := Up8kTo16k().Process(nil, in)
	if len(out) < 15990 || len(out) > 16000 {
		t.Fatalf("expected ~16000 samples, got %d", len(out))
	}
	if g := db(rms(out) / (amp / math.Sqrt2)); math.Abs(g) > 0.5 {
		t.Fatalf("1 kHz passband gain %.2f dB", g)
	}
	// Sample repetition leaves an image at 7 kHz (16k-1k); it must be gone.
	var im, sig float64
	for _, f := range []struct {
		hz  float64
		dst *float64
	}{{7000, &im}, {1000, &sig}} {
		*f.dst = tone(out, 16000, f.hz)
	}
	t.Logf("image %.1f dB below carrier", db(sig/im))
	if db(im/sig) > -55 {
		t.Fatalf("image only %.1f dB down", db(im/sig))
	}
}

// tone is the magnitude of the DFT bin at hz.
func tone(x []int16, rate int, hz float64) float64 {
	var re, im float64
	for i, v := range x {
		a := 2 * math.Pi * hz * float64(i) / float64(rate)
		re += float64(v) * math.Cos(a)
		im += float64(v) * math.Sin(a)
	}
	return math.Hypot(re, im) / float64(len(x))
}

func TestStreamingIsChunkInvariant(t *testing.T) {
	in := sine(24000, 1500, 8000, 24000)
	for i := range in { // add a second tone so the signal is not trivially periodic
		in[i] += int16(3000 * math.Sin(float64(i)*0.37))
	}
	want := Down24kTo8k().Process(nil, in)
	r := Down24kTo8k()
	var got []int16
	for i := 0; i < len(in); {
		n := 1 + (i*7)%481 // irregular chunk sizes including 1 and odd lengths
		if i+n > len(in) {
			n = len(in) - i
		}
		got = r.Process(got, in[i:i+n])
		i += n
	}
	if len(got) != len(want) {
		t.Fatalf("length %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d differs: %d vs %d", i, got[i], want[i])
		}
	}
}

func TestRatioAndDelay(t *testing.T) {
	r := Down24kTo8k()
	out := r.Process(nil, make([]int16, 2400))
	if len(out) != 800 {
		t.Fatalf("24k->8k of 2400 samples gave %d", len(out))
	}
	// An impulse peaks at the group delay.
	imp := make([]int16, 480)
	imp[100] = 30000
	out = Down24kTo8k().Process(nil, imp)
	peak := 0
	for i, v := range out {
		if v > out[peak] {
			peak = i
		}
	}
	got := float64(peak)/8000 - 100.0/24000
	want := Down24kTo8k().Delay().Seconds()
	t.Logf("filter delay %.2f ms (impulse peak %.2f ms)", want*1000, got*1000)
	if math.Abs(got-want) > 1.0/8000 {
		t.Fatalf("impulse delay %.4f s, expected %.4f s", got, want)
	}
	if want > 0.003 {
		t.Fatalf("delay %.1f ms too large for a voice path", want*1000)
	}
}

func TestConfigValidationAndClipping(t *testing.T) {
	if _, err := NewResampler(ResamplerConfig{InRate: 8000, OutRate: 16000, PassHz: 4000, StopHz: 3000}); err == nil {
		t.Fatal("stop below pass must be rejected")
	}
	loud := sine(24000, 500, 32767, 4800)
	for _, v := range Down24kTo8k().Process(nil, loud) {
		_ = v // int16 conversion saturates instead of wrapping; reaching here without panic is the check
	}
	if clip16(1e9) != 32767 || clip16(-1e9) != -32768 {
		t.Fatal("clip16 must saturate")
	}
}

func BenchmarkDown24kTo8kFrame20ms(b *testing.B) {
	r := Down24kTo8k()
	in := sine(24000, 1000, 8000, 480)
	dst := make([]int16, 0, 160)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dst = r.Process(dst[:0], in)
	}
}

func BenchmarkUp8kTo16kFrame20ms(b *testing.B) {
	r := Up8kTo16k()
	in := sine(8000, 1000, 8000, 160)
	dst := make([]int16, 0, 320)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dst = r.Process(dst[:0], in)
	}
}
