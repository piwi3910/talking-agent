package audio

import "math"

// VADFrame is the VAD decision granularity: 10 ms of 8 kHz audio.
const VADFrame = 80

// vadBands partitions the telephone speech band (bin = 62.5 Hz at a 128 point
// DFT of the 8 kHz signal). Energy below ~250 Hz (mains hum, handling noise)
// and above 3.4 kHz (outside the codec's passband) is ignored.
var vadBands = [...][2]int{{4, 8}, {8, 12}, {12, 17}, {17, 24}, {24, 32}, {32, 42}, {42, 54}}

// vadWeights favour the 300-1500 Hz region where voiced speech and its first
// formants carry most energy.
var vadWeights = [len(vadBands)]float64{1.2, 1.4, 1.4, 1.2, 1.0, 0.7, 0.5}

const (
	vadMinBandSNR    = 5.0   // dB a band must exceed its noise floor to count as active
	vadMinBands      = 3     // speech excites several bands; a single tone or hum does not
	vadScoreOn       = 6.0   // weighted mean of band SNR (dB, clipped) that means speech
	vadScoreOff      = 3.5   // lower threshold while already in speech (hysteresis)
	vadSNRClip       = 30.0  // a very loud band cannot outvote the others
	vadMinRMS        = 40.0  // absolute floor: ignore anything quieter than about -58 dBFS
	vadHangover      = 4     // frames (40 ms) a voiced decision is held across short dips
	vadNoiseFall     = 0.15  // EMA weight when the energy is below the floor: follow drops quickly
	vadNoiseAvg      = 0.05  // EMA weight for energies near the floor: average the noise itself
	vadNearFloor     = 2.5   // energies within this factor of the floor are treated as noise
	vadNoiseCreep    = 1.004 // per-frame growth for clearly louder non-speech frames
	vadNoiseRiseSp   = 1.002 // per-frame growth while speech is detected
	vadNoiseRiseLong = 1.05  // after vadLongRun frames of unbroken "speech" it is far more likely a new noise level
	vadLongRun       = 400   // 4 s without a 40 ms gap
	vadSeedMargin    = 2.0   // lifts the seeded minimum towards the typical noise level
	vadNoiseInitial  = 8     // frames used to seed the floor when the call starts
)

// VAD is a spectral voice activity detector for 8 kHz telephone audio. It
// measures energy in speech sub-bands every 10 ms against an adaptive per-band
// noise floor, so it follows line noise instead of using a fixed threshold, and
// requires broadband (multi-band) activity, so steady tones and hum are not
// speech. It is streaming and not safe for concurrent use.
type VAD struct {
	pending [VADFrame]int16
	n       int
	window  [VADFrame]float64
	cos     [][VADFrame]float64 // per bin
	sin     [][VADFrame]float64
	noise   [len(vadBands)]float64
	frames  int
	voiced  bool
	hang    int
	run     int // consecutive voiced frames
}

// NewVAD returns a detector in its initial (silent, uncalibrated) state.
func NewVAD() *VAD {
	v := &VAD{cos: make([][VADFrame]float64, 54), sin: make([][VADFrame]float64, 54)}
	for i := range v.window {
		v.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*(float64(i)+0.5)/VADFrame)
	}
	for k := 4; k < 54; k++ {
		for i := 0; i < VADFrame; i++ {
			a := 2 * math.Pi * float64(k) * float64(i) / 128
			v.cos[k][i], v.sin[k][i] = math.Cos(a), math.Sin(a)
		}
	}
	return v
}

// Process feeds 8 kHz samples and calls emit once per completed 10 ms frame
// with that frame's decision. Chunks of any size are accepted.
func (v *VAD) Process(pcm []int16, emit func(voiced bool)) {
	for len(pcm) > 0 {
		c := copy(v.pending[v.n:], pcm)
		v.n += c
		pcm = pcm[c:]
		if v.n == VADFrame {
			v.n = 0
			emit(v.frame())
		}
	}
}

func (v *VAD) frame() bool {
	var x [VADFrame]float64
	var sumsq float64
	for i, s := range v.pending {
		x[i] = float64(s) * v.window[i]
		sumsq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumsq / VADFrame)
	var energy [len(vadBands)]float64
	for b, r := range vadBands {
		for k := r[0]; k < r[1]; k++ {
			var re, im float64
			for i, s := range x {
				re += s * v.cos[k][i]
				im += s * v.sin[k][i]
			}
			energy[b] += re*re + im*im
		}
		energy[b] /= float64(r[1] - r[0])
		energy[b] += 1 // keep the log finite for digital silence
	}
	if v.frames < vadNoiseInitial {
		// Seed with the quietest of the first frames, so a caller who speaks
		// the moment the call connects does not become the "noise". The minimum
		// of a few noise frames sits a little below their mean; the margin and
		// the near-floor averaging below correct that within a few hundred ms.
		for b := range energy {
			if v.frames == 0 || energy[b] < v.noise[b] {
				v.noise[b] = energy[b]
			}
			if v.frames == vadNoiseInitial-1 {
				v.noise[b] *= vadSeedMargin
			}
		}
		v.frames++
		return false
	}
	v.frames++

	var score, wsum float64
	active := 0
	for b := range energy {
		snr := 10 * math.Log10(energy[b]/v.noise[b])
		if snr > vadMinBandSNR {
			active++
		}
		score += vadWeights[b] * math.Min(math.Max(snr, 0), vadSNRClip)
		wsum += vadWeights[b]
	}
	score /= wsum
	threshold := vadScoreOn
	if v.voiced {
		threshold = vadScoreOff
	}
	speech := rms >= vadMinRMS && active >= vadMinBands && score >= threshold

	// Track the noise floor: down fast, up slowly, slower still during speech.
	rise := 1.0
	if speech {
		rise = vadNoiseRiseSp
		if v.run > vadLongRun {
			rise = vadNoiseRiseLong
		}
	}
	for b := range energy {
		switch {
		case speech:
			// Only ever grow during speech, and never above what was measured:
			// quiet bands inside a vowel say nothing about the noise level.
			if energy[b] > v.noise[b] {
				v.noise[b] = math.Min(v.noise[b]*rise, energy[b])
			}
		case energy[b] < v.noise[b]/vadNearFloor:
			// The noise level genuinely dropped: follow it quickly.
			v.noise[b] += vadNoiseFall * (energy[b] - v.noise[b])
		case energy[b] < vadNearFloor*v.noise[b]:
			// Ordinary fluctuation of the noise itself: average it in.
			v.noise[b] += vadNoiseAvg * (energy[b] - v.noise[b])
		default:
			// Clearly above the floor but not called speech (a consonant, an
			// onset): creep, so one loud frame cannot drag the floor up.
			v.noise[b] = math.Min(v.noise[b]*vadNoiseCreep, energy[b])
		}
	}

	if speech {
		v.hang = vadHangover
		v.voiced = true
	} else if v.hang > 0 {
		v.hang--
	} else {
		v.voiced = false
	}
	if v.voiced {
		v.run++
	} else {
		v.run = 0
	}
	return v.voiced
}
