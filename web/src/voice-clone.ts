// Pure helpers for the guided voice-cloning flow: scripts, WAV encoding and
// resampling, and word-level transcript comparison. No DOM or React here so the
// Playwright specs can import them directly.

export const CLONE_RATE = 24000;
export const MAX_SECONDS = 25;
export const COUNTDOWN_SECONDS = 3;
export const CONSENT_TEXT =
  "This is my own voice, or I have the speaker's explicit permission to clone it.";
export const DEFAULT_TEST_SENTENCE =
  "Hello, thanks for calling. How can I help you today?";

export type Script = { id: string; label: string; text: string };

// About 12 to 20 seconds each at a normal speaking pace.
export const SCRIPTS: Script[] = [
  {
    id: "neutral",
    label: "Neutral, phonetically rich",
    text: "The quick brown fox jumps over the lazy dog near the quiet river bank. Please call Zoe at nine forty five to book a table for six, and bring the thin yellow folder with the printed maps.",
  },
  {
    id: "school",
    label: "School",
    text: "Good morning, and welcome back to school. Today we will read a short story together, then practise our spelling words. If you need help, raise your hand, and your teacher will come over to you.",
  },
  {
    id: "service",
    label: "Customer service",
    text: "Thank you for calling customer support, this is your virtual assistant. I can check your order, update your address, or book a technician visit. Could you please tell me your account number, and what I can help you with today?",
  },
];

export const TIPS = [
  "Use a quiet room, without fans, music or echo.",
  "Speak at a normal pace, as you would to a caller.",
  "Keep the microphone about 20 cm from your mouth.",
  "Use your natural tone and read the whole script.",
];

export function concatChunks(chunks: Float32Array[]): Float32Array {
  let total = 0;
  for (const c of chunks) total += c.length;
  const out = new Float32Array(total);
  let at = 0;
  for (const c of chunks) {
    out.set(c, at);
    at += c.length;
  }
  return out;
}

// Averages the source span that each output sample covers, which also
// low-passes when downsampling.
export function resample(
  input: Float32Array,
  from: number,
  to: number,
): Float32Array {
  if (from === to) return input.slice();
  const ratio = from / to;
  const out = new Float32Array(Math.floor(input.length / ratio));
  for (let j = 0; j < out.length; j++) {
    const start = j * ratio;
    const end = start + ratio;
    let sum = 0;
    let weight = 0;
    for (let i = Math.floor(start); i < input.length && i < end; i++) {
      const w = Math.min(i + 1, end) - Math.max(i, start);
      if (w <= 0) continue;
      sum += input[i] * w;
      weight += w;
    }
    out[j] = weight > 0 ? sum / weight : 0;
  }
  return out;
}

// Mono 16-bit PCM WAV.
export function encodeWav(samples: Float32Array, rate: number): ArrayBuffer {
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  const text = (at: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(at + i, s.charCodeAt(i));
  };
  text(0, "RIFF");
  view.setUint32(4, 36 + samples.length * 2, true);
  text(8, "WAVEfmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, rate, true);
  view.setUint32(28, rate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  text(36, "data");
  view.setUint32(40, samples.length * 2, true);
  for (let i = 0; i < samples.length; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(
      44 + i * 2,
      Math.round(s < 0 ? s * 0x8000 : s * 0x7fff),
      true,
    );
  }
  return buffer;
}

// Turns captured microphone chunks into the 24 kHz mono WAV the server expects,
// cut at the maximum length.
export function recordingToWav(
  chunks: Float32Array[],
  inputRate: number,
): { wav: ArrayBuffer; seconds: number } {
  let samples = concatChunks(chunks);
  const max = Math.floor(MAX_SECONDS * inputRate);
  if (samples.length > max) samples = samples.subarray(0, max);
  const out = resample(samples, inputRate, CLONE_RATE);
  return { wav: encodeWav(out, CLONE_RATE), seconds: out.length / CLONE_RATE };
}

export function toBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (let i = 0; i < bytes.length; i += 0x2000)
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x2000));
  return btoa(binary);
}

// Level in 0..1 on a -60..0 dBFS scale, for the meter.
export function meterLevel(rms: number): number {
  if (rms <= 0) return 0;
  return Math.max(0, Math.min(1, (20 * Math.log10(rms) + 60) / 60));
}

export function clock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export function words(text: string): string[] {
  return text
    .toLowerCase()
    .replace(/['’]/g, "")
    .replace(/[^\p{L}\p{N}]+/gu, " ")
    .split(" ")
    .filter(Boolean);
}

// Longest-common-subsequence table over two word lists.
function lcs(a: string[], b: string[]): number[][] {
  const t = Array.from({ length: a.length + 1 }, () =>
    new Array<number>(b.length + 1).fill(0),
  );
  for (let i = a.length - 1; i >= 0; i--)
    for (let j = b.length - 1; j >= 0; j--)
      t[i][j] =
        a[i] === b[j]
          ? t[i + 1][j + 1] + 1
          : Math.max(t[i + 1][j], t[i][j + 1]);
  return t;
}

// Word-level similarity between the script and what was heard, 0 to 1:
// matching words in order over the longer of the two texts.
export function wordSimilarity(script: string, heard: string): number {
  const a = words(script);
  const b = words(heard);
  if (a.length === 0 && b.length === 0) return 1;
  if (a.length === 0 || b.length === 0) return 0;
  return lcs(a, b)[0][0] / Math.max(a.length, b.length);
}

export type HeardWord = { word: string; match: boolean };

// The heard words, each flagged as part of the in-order match with the script or not.
export function markHeard(script: string, heard: string): HeardWord[] {
  const a = words(script);
  const b = words(heard);
  const t = lcs(a, b);
  const out: HeardWord[] = [];
  let i = 0;
  let j = 0;
  while (j < b.length) {
    if (i < a.length && a[i] === b[j]) {
      out.push({ word: b[j], match: true });
      i++;
      j++;
    } else if (i < a.length && t[i + 1][j] >= t[i][j + 1]) {
      i++;
    } else {
      out.push({ word: b[j], match: false });
      j++;
    }
  }
  return out;
}
