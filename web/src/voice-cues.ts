type Cue = { id: string; category: string; text: string; duration_ms: number };
// Cues are UI feedback, never conversation history or evidence of tool success.
export class VoiceCues {
  private clips = new Map<string, AudioBuffer>();
  private cues: Cue[] = [];
  private source?: AudioBufferSourceNode;
  private timer?: ReturnType<typeof setTimeout>;
  private category = "waiting";
  private previous = "";
  private last = -Infinity;
  private count = 0;
  private closed = false;
  private loadAbort = new AbortController();
  constructor(
    private ctx: AudioContext,
    private destination: AudioNode,
    private allowed: () => boolean,
    private report: (text: string) => void,
    private trace: (
      type: string,
      data?: Record<string, unknown>,
    ) => void = () => undefined,
  ) {}
  get playing() {
    return !!this.source;
  }
  // Called at the start of each session. A missing manifest (404, cues muted or still rendering) means no cues.
  // The clips are only re-downloaded when the agent or the manifest's full reference_sha256 string changed.
  private version?: string;
  private generation = 0;
  async load(agent: string) {
    const generation = ++this.generation;
    try {
      const base = `/api/agents/${agent}/voice-cues`;
      const res = await fetch(base, { signal: this.loadAbort.signal });
      if (generation !== this.generation || this.closed) return;
      if (!res.ok) {
        this.cues = [];
        this.clips.clear();
        this.version = undefined;
        return;
      }
      const manifest = await res.json();
      if (generation !== this.generation || this.closed) return;
      const cues: Cue[] = Array.isArray(manifest?.cues) ? manifest.cues : [];
      const sha = String(manifest?.reference_sha256 ?? "");
      const version = `${agent}\u0000${sha}`;
      if (version === this.version && cues.every((c) => this.clips.has(c.id))) {
        this.cues = cues;
        return;
      }
      this.clips.clear();
      this.cues = cues;
      this.version = version;
      const cacheBuster = encodeURIComponent(sha);
      // Bound concurrent downloads; all assets are local, prerecorded WAVs.
      const pending = [...cues];
      await Promise.all(
        Array.from({ length: 3 }, async () => {
          while (
            pending.length &&
            !this.closed &&
            generation === this.generation
          ) {
            const cue = pending.shift()!;
            const r = await fetch(`${base}/${cue.id}?v=${cacheBuster}`, {
              signal: this.loadAbort.signal,
            });
            if (r.ok) {
              const buffer = await this.ctx.decodeAudioData(
                await r.arrayBuffer(),
              );
              if (!this.closed && generation === this.generation)
                this.clips.set(cue.id, buffer);
            }
          }
        }),
      );
    } catch {
      /* Voice replies work even if optional cues are unavailable. */
    }
  }
  begin() {
    this.cancel();
    this.count = 0;
    this.schedule("waiting", 1200);
  }
  schedule(category: string, delay = 1200) {
    this.category = category;
    if (this.timer || this.closed || this.count >= 2) {
      this.trace("cue.schedule.skipped", {
        category,
        delay,
        timer: !!this.timer,
        closed: this.closed,
        count: this.count,
      });
      return;
    }
    this.trace("cue.schedule", { category, delay });
    this.timer = setTimeout(() => {
      this.timer = undefined;
      this.play(this.category);
    }, delay);
  }
  play(category: string) {
    if (
      this.closed ||
      !this.allowed() ||
      this.source ||
      this.count >= 2 ||
      performance.now() - this.last < 8000
    ) {
      this.trace("cue.skip", {
        category,
        closed: this.closed,
        allowed: !this.closed && this.allowed(),
        playing: !!this.source,
        count: this.count,
      });
      return;
    }
    let options = this.cues.filter(
      (c) =>
        (c.category === category ||
          (category === "waiting" && c.category === "acknowledge")) &&
        this.clips.has(c.id) &&
        c.id !== this.previous,
    );
    if (!options.length)
      options = this.cues.filter(
        (c) =>
          c.category === "lookup" &&
          this.clips.has(c.id) &&
          c.id !== this.previous,
      );
    if (!options.length) return;
    const cue = options[Math.floor(Math.random() * options.length)];
    const source = this.ctx.createBufferSource();
    source.buffer = this.clips.get(cue.id)!;
    source.connect(this.destination);
    this.source = source;
    this.previous = cue.id;
    this.last = performance.now();
    this.count++;
    source.onended = () => {
      if (this.source === source) this.source = undefined;
      source.disconnect();
      this.trace("cue.ended", { id: cue.id, ct: this.ctx.currentTime });
    };
    source.start();
    this.trace("cue.play", {
      id: cue.id,
      category: cue.category,
      text: cue.text,
      ct: this.ctx.currentTime,
      duration_ms: cue.duration_ms,
    });
    this.report(cue.text);
  }
  cancel() {
    if (this.timer || this.source)
      this.trace("cue.cancel", {
        timer: !!this.timer,
        playing: !!this.source,
        ct: this.ctx.currentTime,
      });
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
    if (this.source) {
      this.source.onended = null;
      try {
        this.source.stop();
        this.source.disconnect();
      } catch {}
      this.source = undefined;
    }
  }
  close() {
    this.closed = true;
    this.cancel();
    this.loadAbort.abort();
    this.clips.clear();
  }
}
