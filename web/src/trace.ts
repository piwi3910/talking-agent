// Per-call diagnostic recorder. Events carry t (ms on the monotonic clock since
// the call started) and wall (epoch ms), and are batched to the server, which
// appends them to the session's trace file. Nothing here may throw or block audio.
export type TraceData = Record<string, unknown>;
type TraceEvent = { type: string; t: number; wall: number; data?: TraceData };

const FLUSH_MS = 2000;
const MAX_EVENTS = 6000; // bounded buffer while the server is unreachable
const BATCH_BYTES = 900_000; // well below the 4 MB ingress limit
const BEACON_BYTES = 60_000; // sendBeacon and keepalive cap at 64 KB
const PCM_SESSION_CAP = 2 * 1024 * 1024; // raw PCM kept per session
const PCM_EVENT_CAP = 512 * 1024; // raw PCM kept per phrase

function base64(bytes: Uint8Array): string {
  let out = "";
  for (let i = 0; i < bytes.length; i += 0x8000)
    out += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(out);
}

export class Trace {
  private t0 = performance.now();
  private wall0 = Date.now();
  private events: TraceEvent[] = [];
  private dropped = 0;
  private timer?: ReturnType<typeof setInterval>;
  private sending = false;
  private pcmUsed = 0;
  private stopped = false;
  private serverFull = false;
  private hide = () => this.flush(true);

  constructor(readonly session: string) {
    try {
      this.timer = setInterval(() => void this.flush(false), FLUSH_MS);
      window.addEventListener("pagehide", this.hide);
      this.event("trace.start", {
        user_agent: navigator.userAgent,
        language: navigator.language,
        hardware_concurrency: navigator.hardwareConcurrency,
        url: location.pathname,
        wall_iso: new Date(this.wall0).toISOString(),
      });
    } catch {
      /* Tracing is best effort. */
    }
  }

  // Milliseconds on the call clock, for callers that compute their own spans.
  now(): number {
    return performance.now() - this.t0;
  }

  event(type: string, data?: TraceData) {
    try {
      if (this.stopped) return;
      const t = this.now();
      this.events.push({
        type,
        t: Math.round(t * 1000) / 1000,
        wall: Math.round((this.wall0 + t) * 1000) / 1000,
        data,
      });
      if (this.events.length > MAX_EVENTS) {
        const cut = this.events.length - MAX_EVENTS;
        this.events.splice(0, cut);
        this.dropped += cut;
      }
    } catch {
      /* ignore */
    }
  }

  // Keeps the raw PCM of a phrase (24 kHz s16le) so its waveform can be inspected.
  pcm(phrase: string, chunks: Uint8Array[], bursts: number[], meta: TraceData) {
    try {
      const size = chunks.reduce((n, c) => n + c.length, 0);
      const keep = Math.min(
        size,
        PCM_EVENT_CAP,
        PCM_SESSION_CAP - this.pcmUsed,
      );
      if (keep <= 0) return;
      const all = new Uint8Array(keep);
      let at = 0;
      for (const c of chunks) {
        if (at >= keep) break;
        const part = c.subarray(0, keep - at);
        all.set(part, at);
        at += part.length;
      }
      this.pcmUsed += keep;
      this.event("pcm", {
        ...meta,
        phrase,
        format: "s16le",
        sample_rate: 24000,
        bytes: keep,
        total_bytes: size,
        truncated: keep < size,
        burst_offsets: bursts,
        base64: base64(all),
      });
    } catch {
      /* ignore */
    }
  }

  private send(batch: TraceEvent[], beacon: boolean): Promise<boolean> {
    const url = `/api/sessions/${this.session}/trace`;
    const body = JSON.stringify(batch);
    if (beacon) {
      try {
        return Promise.resolve(
          navigator.sendBeacon(
            url,
            new Blob([body], { type: "application/json" }),
          ),
        );
      } catch {
        return Promise.resolve(false);
      }
    }
    return fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    }).then(
      (r) => {
        if (r.status === 413) this.serverFull = true;
        return r.ok || r.status === 413 || r.status === 404;
      },
      () => false,
    );
  }

  // Takes events off the buffer in size-bounded batches. In beacon mode (page
  // hide) only small events fit and raw PCM is left behind.
  private take(beacon: boolean): TraceEvent[] {
    const limit = beacon ? BEACON_BYTES : BATCH_BYTES;
    const batch: TraceEvent[] = [];
    let size = 2;
    let i = 0;
    for (; i < this.events.length; i++) {
      const ev = this.events[i];
      const n = ev.data?.base64 ? (ev.data.base64 as string).length + 200 : 150;
      if (beacon && ev.type === "pcm") continue;
      if (batch.length && size + n > limit) break;
      if (beacon && size + n > limit) break;
      batch.push(ev);
      size += n;
    }
    this.events.splice(0, i);
    return batch;
  }

  async flush(beacon: boolean) {
    try {
      if (this.serverFull) {
        this.events = [];
        return;
      }
      if (beacon) {
        while (this.events.length) {
          const batch = this.take(true);
          if (!batch.length) break;
          this.send(batch, true);
        }
        return;
      }
      if (this.sending) return;
      this.sending = true;
      try {
        while (this.events.length && !this.serverFull) {
          const batch = this.take(false);
          if (!batch.length) break;
          if (!(await this.send(batch, false))) {
            this.events.unshift(...batch);
            break;
          }
        }
      } finally {
        this.sending = false;
      }
    } catch {
      this.sending = false;
    }
  }

  stop() {
    if (this.stopped) return;
    if (this.dropped) this.event("trace.dropped", { events: this.dropped });
    this.event("trace.stop");
    this.stopped = true;
    clearInterval(this.timer);
    window.removeEventListener("pagehide", this.hide);
    void this.flush(false).then(() => {
      if (this.events.length) void this.flush(true);
    });
  }
}
