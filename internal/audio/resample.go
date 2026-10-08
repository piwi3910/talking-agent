// Package audio holds the signal processing used on the telephone path: a
// streaming polyphase resampler and a voice activity detector.
package audio

import (
	"fmt"
	"math"
	"time"
)

// Resampler converts 16-bit mono PCM between two sample rates with a Kaiser
// windowed-sinc low-pass applied in polyphase form, so only the taps that
// contribute to an output sample are evaluated. It is streaming: Process may be
// called with arbitrary chunk sizes and keeps filter state between calls.
// A Resampler is not safe for concurrent use.
type Resampler struct {
	l, m   int
	k      int         // taps per phase
	phases [][]float32 // phases[p][j] = h[p+j*l] * l
	buf    []float32   // past input, buf[0] is input sample number base
	base   int
	t      int // next output time in the up-sampled (rate*l) domain
	delay  float64
}

// ResamplerConfig describes the low-pass: frequencies up to PassHz are kept
// flat and everything above StopHz is attenuated by about 60 dB. A wider
// transition band means fewer taps and lower delay.
type ResamplerConfig struct {
	InRate, OutRate int
	PassHz, StopHz  float64
}

// NewResampler builds a resampler for the given configuration.
func NewResampler(c ResamplerConfig) (*Resampler, error) {
	if c.InRate <= 0 || c.OutRate <= 0 || c.PassHz <= 0 || c.StopHz <= c.PassHz {
		return nil, fmt.Errorf("invalid resampler config %+v", c)
	}
	g := gcd(c.InRate, c.OutRate)
	l, m := c.OutRate/g, c.InRate/g
	// The filter runs at InRate*L. Its cutoff sits mid-transition.
	fs := float64(c.InRate * l)
	const atten = 60.0
	dw := 2 * math.Pi * (c.StopHz - c.PassHz) / fs
	n := int(math.Ceil((atten-8)/(2.285*dw))) + 1
	if n%2 == 0 {
		n++
	}
	k := (n + l - 1) / l
	n = k * l
	if n%2 == 0 { // keep an odd, symmetric length
		n--
	}
	beta := 0.1102 * (atten - 8.7)
	fc := (c.PassHz + c.StopHz) / 2 / fs // cycles per sample
	h := make([]float64, k*l)
	mid := float64(n-1) / 2
	for i := 0; i < n; i++ {
		x := float64(i) - mid
		v := 2 * fc
		if x != 0 {
			v = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		r := x / mid
		h[i] = v * bessel0(beta*math.Sqrt(math.Max(0, 1-r*r))) / bessel0(beta)
	}
	var sum float64
	for _, v := range h {
		sum += v
	}
	phases := make([][]float32, l)
	for p := range phases {
		phases[p] = make([]float32, k)
		for j := 0; j < k; j++ {
			phases[p][j] = float32(h[p+j*l] / sum * float64(l))
		}
	}
	return &Resampler{l: l, m: m, k: k, phases: phases, base: -(k - 1), buf: make([]float32, k-1), delay: mid / fs}, nil
}

// Delay is the group delay the filter adds, in seconds.
func (r *Resampler) Delay() time.Duration { return time.Duration(r.delay * float64(time.Second)) }

// Process consumes input samples and appends the resulting output to dst.
func (r *Resampler) Process(dst, in []int16) []int16 {
	for _, s := range in {
		r.buf = append(r.buf, float32(s))
	}
	end := r.base + len(r.buf) // one past the newest input index
	for {
		i, p := r.t/r.l, r.t%r.l
		if i >= end {
			break
		}
		taps := r.phases[p]
		var acc float32
		// x[i-j] is buf[i-j-base]; j runs over the newest k samples up to i.
		off := i - r.base
		for j := 0; j < r.k; j++ {
			acc += taps[j] * r.buf[off-j]
		}
		dst = append(dst, clip16(acc))
		r.t += r.m
	}
	// Drop input that no future output can reach.
	keepFrom := r.t/r.l - (r.k - 1)
	if drop := keepFrom - r.base; drop > 0 {
		r.buf = append(r.buf[:0], r.buf[drop:]...)
		r.base = keepFrom
	}
	return dst
}

func clip16(v float32) int16 {
	switch {
	case v >= 32767:
		return 32767
	case v <= -32768:
		return -32768
	}
	return int16(math.Round(float64(v)))
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// bessel0 is the modified Bessel function of the first kind, order zero.
func bessel0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 50; k++ {
		term *= (x / 2) / float64(k)
		sum += term * term
		if term*term < 1e-12*sum {
			break
		}
	}
	return sum
}

// Down24kTo8k returns the resampler for TTS output (24 kHz) to the telephone
// band: flat to 3.4 kHz, 60 dB down by 4.6 kHz, so nothing aliases into speech.
func Down24kTo8k() *Resampler {
	r, _ := NewResampler(ResamplerConfig{InRate: 24000, OutRate: 8000, PassHz: 3400, StopHz: 4600})
	return r
}

// Up8kTo16k returns the resampler from G.711 audio to the 16 kHz ASR input,
// removing the spectral images that sample repetition would create.
func Up8kTo16k() *Resampler {
	r, _ := NewResampler(ResamplerConfig{InRate: 8000, OutRate: 16000, PassHz: 3400, StopHz: 4600})
	return r
}
