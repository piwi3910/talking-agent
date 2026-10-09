// Streaming windowed-sinc (Lanczos) resampler with state kept across calls.
//
// Speech arrives as 24 kHz PCM in bursts. Handing each burst to the browser as its
// own 24 kHz AudioBuffer makes the browser resample every buffer on its own, and
// at a context rate that is not a whole multiple of 24 kHz (44.1 kHz) Chrome
// renders one frame too many at the end of some buffers. That frame overlaps the
// first frame of the next buffer, the two add, and the result is a one-sample
// spike (a click) at the seam. Resampling here, continuously across bursts, to the
// context rate means the browser never converts a rate: buffers hold exactly the
// frames that will be played and consecutive buffers join sample for sample.
const LOBES = 4; // Lanczos "a": taps per side for upsampling
const PHASES = 1024; // fractional-position table resolution, linearly interpolated

export class Resampler {
  private readonly step: number; // input samples per output sample
  private readonly half: number; // taps on each side of the read position
  private readonly table: Float32Array; // (PHASES + 1) rows of 2 * half weights
  private buf: Float32Array;
  private base: number; // absolute input index of buf[0]
  private out = 0; // output samples produced so far

  constructor(
    readonly from: number,
    readonly to: number,
  ) {
    this.step = from / to;
    const scale = Math.min(1, to / from); // low-pass below the target Nyquist
    this.half = Math.ceil(LOBES / scale);
    this.table = kernel(this.half, scale);
    // History before the first sample is silence.
    this.buf = new Float32Array(this.half);
    this.base = -this.half;
  }

  // Resamples input and returns every output sample that no longer depends on
  // future input. With last set, the stream ends: the remainder is flushed.
  push(input: Float32Array, last = false): Float32Array<ArrayBuffer> {
    const tail = last ? this.half : 0;
    const joined = new Float32Array(this.buf.length + input.length + tail);
    joined.set(this.buf);
    joined.set(input, this.buf.length);
    this.buf = joined;
    const { half, step, table } = this;
    const taps = 2 * half;
    const available = this.base + this.buf.length; // absolute end of known input
    // Output n reads input around n * step and needs `half` samples beyond it.
    const count = Math.max(
      0,
      Math.ceil((available - half - 1e-9) / step) - this.out,
    );
    const result = new Float32Array(count);
    for (let k = 0; k < count; k++) {
      const pos = (this.out + k) * step - this.base;
      const i0 = Math.floor(pos);
      const p = (pos - i0) * PHASES;
      const row = Math.floor(p);
      const w = p - row;
      const a = row * taps;
      const b = a + taps;
      const start = i0 - half + 1;
      let sum = 0;
      for (let t = 0; t < taps; t++) {
        const idx = start + t;
        const x = idx >= 0 && idx < this.buf.length ? this.buf[idx] : 0;
        sum += x * (table[a + t] * (1 - w) + table[b + t] * w);
      }
      result[k] = sum;
    }
    this.out += count;
    // Drop input no later output can read.
    const keepFrom = Math.floor(this.out * step) - this.base - half + 1;
    if (keepFrom > 0) {
      this.buf = this.buf.slice(keepFrom);
      this.base += keepFrom;
    }
    return result;
  }
}

// Weights for read positions i0 + row / PHASES; tap t reads input i0 - half + 1 + t.
function kernel(half: number, scale: number): Float32Array {
  const taps = 2 * half;
  const table = new Float32Array((PHASES + 1) * taps);
  for (let row = 0; row <= PHASES; row++) {
    const frac = row / PHASES;
    let sum = 0;
    for (let t = 0; t < taps; t++) {
      const x = (t - half + 1 - frac) * scale;
      const v = lanczos(x) * scale;
      table[row * taps + t] = v;
      sum += v;
    }
    for (let t = 0; t < taps; t++) table[row * taps + t] /= sum; // unity gain at DC
  }
  return table;
}

function lanczos(x: number): number {
  if (x === 0) return 1;
  if (Math.abs(x) >= LOBES) return 0;
  const px = Math.PI * x;
  return (Math.sin(px) / px) * (Math.sin(px / LOBES) / (px / LOBES));
}
