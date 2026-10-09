import { VoiceCues } from "./voice-cues";
import type { MicVAD } from "@ricky0123/vad-web";
import { Trace, type TraceData } from "./trace";
// Browser transport only: finalized speech uses the same message API as typing.
export type VoiceCallbacks = {
  status: (status: string) => void;
  transcript: (text: string) => void;
  final: (text: string) => void;
  interrupt: () => void;
  error: (message: string) => void;
  metric: (name: string, ms: number) => void;
};
// One spoken phrase on its way to the speakers.
type Phrase = {
  id: string;
  index: number; // position within its turn; the first two keep their raw PCM
  buffers: number; // audio buffers scheduled so far
  bursts: number; // bursts delivered so far
  raw: Uint8Array[];
  burstOffsets: number[];
  rawBytes: number;
  text: string;
  turn: string;
  queuedAt: number;
  first: boolean;
  textAt: number;
  reported: boolean; // first phrase of its turn, and when that turn's text began
  chunks: Uint8Array[];
  done: boolean;
};
const PHRASE_RETRY_DELAYS = [150, 300, 600];
// One /speech stream at a time: two concurrent streams share the GPU and delay
// the head phrase's second burst past the end of its first, which is an audible
// gap after the first word. A phrase downloads in about half its playback time,
// so the next one is still ready before it is needed.
const MAX_SPEECH_IN_FLIGHT = 1;
const SPEECH_HEADERS_DEADLINE = 20000;
// Streaming TTS sends audio in bursts that the network splits into many reads.
// Joining reads that arrive within this window avoids a buffer seam (an audible
// click) inside a burst.
const BURST_GAP_MS = 25;
// Lead before audio starts from silence. The first burst is 0.64 s of audio and
// the second arrives about 0.6-0.75 s later, so 0.4 s keeps ~0.3 s of margin.
const START_LEAD = 0.4;
// Speech needed before ducking playback. Echo of the agent's own voice can
// briefly reach 64 ms; a real interruption keeps going to the 128 ms barge-in.
const DUCK_SPEECH_MS = 96;
const MIN_PHRASE = 25;
const FIRST_CLAUSE_MIN = 40;
// Abbreviations whose full stop does not end a sentence. "No" only counts before a number.
const ABBREVIATION = /(?:\b(?:Dr|Mr|Ms|Mrs|St)|\be\.g|\bi\.e)$/;
export class Voice {
  private context?: AudioContext;
  private gain?: GainNode;
  private cues?: VoiceCues;
  private cuesEnabled = true;
  private cuePhase = "waiting";
  private turnRunning = false;
  private firstSpeechAt = 0;
  private media?: MediaStream;
  private detector?: MicVAD;
  private socket?: WebSocket;
  private active = false;
  private muted = false;
  private closed = false;
  private speechFrames = 0;
  private silence = 0;
  private frames = 0;
  private preRoll: ArrayBuffer[] = [];
  private output = new Set<AudioBufferSourceNode>();
  // Every in-flight /speech request, so barge-in can cancel all of them.
  private aborts = new Set<AbortController>();
  private generation = 0;
  private queue: Phrase[] = [];
  // Phrases whose audio is being fetched, in play order. The head plays as it
  // streams; later ones buffer until the head is done, so order is preserved.
  private order: Phrase[] = [];
  private inflight = 0;
  private pumpTimer?: ReturnType<typeof setTimeout>;
  private text = "";
  private carry = "";
  private turnPhrases = 0;
  private firstDeltaTurn = "";
  private firstDeltaAt = 0;
  private turn = "";
  private blockedTurn = "";
  private nextAudio = 0;
  private lastSpeech = 0;
  private firstPlayback = false;
  private outputEnabled = true;
  private trace: Trace;
  private phraseSeq = 0;
  private lastGainTarget = 1;
  private lastVadLog = 0;
  // Last sample and end time of the most recently scheduled buffer.
  private lastSample = 0;
  private lastPhrase = "";
  // primed is an AudioContext created inside the user's click, for flows that
  // start voice only after several awaits (autoplay policies need the gesture).
  constructor(
    private session: string,
    private cb: VoiceCallbacks,
    private agentID: string,
    private primed?: AudioContext,
  ) {
    this.trace = new Trace(session);
    const raw = cb;
    this.cb = {
      ...raw,
      status: (text) => {
        this.tr("status", { text });
        raw.status(text);
      },
      error: (message) => {
        this.tr("error", { message });
        raw.error(message);
      },
      final: (text) => {
        this.tr("stt.final.text", { chars: text.length });
        raw.final(text);
      },
      interrupt: () => {
        this.tr("interrupt");
        raw.interrupt();
      },
      metric: (name, ms) => {
        this.tr("metric", { name, ms: Math.round(ms) });
        raw.metric(name, ms);
      },
    };
  }
  get traceID(): string {
    return this.session;
  }
  private tr(type: string, data?: TraceData) {
    this.trace.event(type, data);
  }
  private audioInfo(): TraceData {
    const ctx = this.context;
    if (!ctx) return {};
    return {
      state: ctx.state,
      sample_rate: ctx.sampleRate,
      base_latency: ctx.baseLatency,
      output_latency: ctx.outputLatency,
      ct: ctx.currentTime,
    };
  }
  static prime(): AudioContext | undefined {
    try {
      const ctx = new AudioContext();
      void ctx.resume().catch(() => undefined);
      return ctx;
    } catch {
      return undefined;
    }
  }
  async start() {
    this.cb.status("Requesting microphone…");
    try {
      this.context = this.primed ?? new AudioContext();
      this.tr("audio.context", {
        ...this.audioInfo(),
        primed: !!this.primed,
        start_lead: START_LEAD,
        burst_gap_ms: BURST_GAP_MS,
      });
      const watched = this.context;
      watched.onstatechange = () => this.tr("audio.state", this.audioInfo());
      await this.context.resume();
      this.tr("audio.resumed", this.audioInfo());
      this.gain = this.context.createGain();
      this.gain.connect(this.context.destination);
      this.cues = new VoiceCues(
        this.context,
        this.gain,
        () =>
          !this.closed &&
          this.cuesEnabled &&
          this.outputEnabled &&
          !this.active &&
          !this.socket &&
          !this.output.size,
        (text) => this.cb.status(text),
        (type, data) => this.tr(type, data),
      );
      void this.cues.load(this.agentID);
      this.media = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
          channelCount: 1,
        },
      });
      if (this.closed) {
        this.media.getTracks().forEach((t) => t.stop());
        return;
      }
      // The microphone prompt can outlive the click gesture; make sure output is running.
      const live = this.context!;
      if (live.state !== "running") void live.resume().catch(() => undefined);
      this.cb.status("Loading speech detection…");
      const { MicVAD } = await import("@ricky0123/vad-web");
      if (this.closed) return;
      this.detector = await MicVAD.new({
        model: "v6",
        audioContext: this.context,
        startOnLoad: false,
        baseAssetPath: "/voice-assets/",
        onnxWASMBasePath: "/voice-assets/",
        ortConfig: (ort) => {
          ort.env.wasm.numThreads = 1;
        },
        getStream: async () => this.media!,
        pauseStream: async () => {},
        resumeStream: async () => this.media!,
        positiveSpeechThreshold: 0.65,
        negativeSpeechThreshold: 0.4,
        redemptionMs: 700,
        minSpeechMs: 224,
        onFrameProcessed: (probabilities, frame) => {
          const pcm = new ArrayBuffer(frame.length * 2);
          const view = new DataView(pcm);
          for (let i = 0; i < frame.length; i++) {
            const v = Math.max(-1, Math.min(1, frame[i]));
            view.setInt16(i * 2, v < 0 ? v * 32768 : v * 32767, true);
          }
          this.frame(pcm, probabilities.isSpeech, frame.length / 16);
        },
      });
      if (this.closed) {
        await this.detector.destroy();
        return;
      }
      await this.detector.start();
      this.cb.status("Listening");
      this.cues?.schedule("listening", 800);
    } catch (e) {
      this.tr("start.failed", { message: (e as Error).message });
      this.stop();
      throw e;
    }
  }
  private frame(pcm: ArrayBuffer, probability: number, ms: number) {
    if (this.closed || this.muted) return;
    this.preRoll.push(pcm);
    if (this.preRoll.length > 13) this.preRoll.shift();
    const playing = !!this.output.size || !!this.cues?.playing;
    const speaking = probability > (this.active ? 0.4 : 0.6);
    if (speaking) {
      if (this.speechFrames === 0) this.firstSpeechAt = performance.now();
      this.lastSpeech = performance.now();
      this.speechFrames = Math.min(256, this.speechFrames + ms);
    } else {
      this.speechFrames = Math.max(0, this.speechFrames - ms * 2);
    }
    // Duck at the first credible onset; commit without waiting for an STT result.
    if (playing && this.gain) {
      const target = this.speechFrames >= DUCK_SPEECH_MS ? 0.12 : 1;
      this.gain.gain.setTargetAtTime(target, this.context!.currentTime, 0.015);
      if (target !== this.lastGainTarget) {
        this.lastGainTarget = target;
        this.tr("gain.target", {
          target,
          ct: this.context!.currentTime,
          value: this.gain.gain.value,
          speech_frames: this.speechFrames,
          vad: probability,
        });
      }
    }
    // Sampled VAD while audio plays or the user speaks: shows echo-driven ducking.
    const nowMs = performance.now();
    if ((playing || speaking) && nowMs - this.lastVadLog >= 250) {
      this.lastVadLog = nowMs;
      this.tr("vad", {
        p: Math.round(probability * 1000) / 1000,
        speech_frames: this.speechFrames,
        playing,
        gain: this.gain?.gain.value,
      });
    }
    if (
      !this.active &&
      !this.socket &&
      this.speechFrames >= (playing ? 128 : 224)
    ) {
      this.active = true;
      this.silence = 0;
      this.frames = 0;
      this.firstPlayback = false;
      this.tr("barge-in.detected", {
        playing,
        speech_frames: this.speechFrames,
        vad: probability,
        since_onset_ms: performance.now() - this.firstSpeechAt,
      });
      this.stopOutput(playing ? "barge-in" : "speech-start");
      if (playing)
        this.cb.metric(
          "Interruption detection",
          performance.now() - this.firstSpeechAt,
        );
      this.cb.interrupt();
      this.cb.transcript("");
      this.cb.status("Listening to you…");
      const ws = new WebSocket(
        `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/sessions/${this.session}/transcribe?utterance=${crypto.randomUUID()}`,
      );
      this.socket = ws;
      const initial = [...this.preRoll];
      this.tr("stt.ws.connecting", { preroll_chunks: initial.length });
      ws.onopen = () => {
        this.tr("stt.ws.open");
        if (this.socket !== ws) return;
        initial.forEach((b) => ws.send(b));
      };
      let partial = "";
      let final = false;
      ws.onmessage = (e) => {
        if (this.socket !== ws) return;
        const data = JSON.parse(e.data);
        this.tr("stt.ws.message", {
          type: data.type,
          chars: (data.text || data.message || "").length,
        });
        if (data.type === "stt.partial") {
          partial += data.text;
          this.cb.transcript(partial);
        }
        if (data.type === "stt.final") {
          final = true;
          this.active = false;
          this.socket = undefined;
          ws.close();
          const text = (data.text || partial).trim();
          this.cb.transcript(text);
          this.cb.metric(
            "STT after speech",
            performance.now() - this.lastSpeech,
          );
          if (
            /^(?:please\s+)?(?:stop|stop talking|stop speaking|be quiet|pause)(?:\s+please)?[.!?]*$/i.test(
              text,
            )
          ) {
            this.stopOutput("stop-command");
            this.cb.interrupt();
            this.cb.status("Listening");
            this.cues?.schedule("interrupted", 250);
          } else if (text) {
            this.cb.status("Waiting for reply");
            this.cb.final(text);
          } else {
            this.cb.status("Listening");
          }
        }
        if (data.type === "error") {
          this.cb.error(data.message);
          ws.close();
        }
      };
      // Once the final transcript arrived, the server hanging up is not a failure.
      ws.onerror = () => {
        if (!final)
          this.cb.error(
            "Speech connection failed. You can still type your message.",
          );
      };
      ws.onclose = (e) => {
        this.tr("stt.ws.close", { code: e.code, final });
        if (this.socket === ws) {
          this.socket = undefined;
          this.active = false;
          if (!final) {
            this.cb.status("Listening");
            this.cb.error("Transcription interrupted. Please try again.");
          }
        }
      };
      return;
    }
    if (this.active) {
      this.frames += ms;
      if (this.socket?.readyState === WebSocket.OPEN) {
        if (this.socket.bufferedAmount > 128000) {
          this.cb.error("Speech connection is too slow.");
          this.resetInput();
          return;
        }
        this.socket.send(pcm);
      }
      this.silence = speaking ? 0 : this.silence + ms;
      if (this.silence >= 700 || this.frames >= 55000) this.finishInput();
    }
  }
  finishInput() {
    if (this.active && this.socket?.readyState === WebSocket.OPEN) {
      this.active = false;
      this.speechFrames = 0;
      this.tr("stt.finish.sent", {
        frames_ms: this.frames,
        silence_ms: this.silence,
      });
      this.socket.send(JSON.stringify({ type: "finish" }));
      this.cb.metric("Endpointing", performance.now() - this.lastSpeech);
      this.cb.status("Transcribing…");
    }
  }
  private resetInput() {
    this.active = false;
    const ws = this.socket;
    this.socket = undefined;
    ws?.close();
    this.preRoll = [];
    this.speechFrames = 0;
  }
  mute(value: boolean) {
    this.tr("mute", { value });
    this.muted = value;
    this.media?.getAudioTracks().forEach((t) => (t.enabled = !value));
    if (value) this.resetInput();
    this.cb.status(value ? "Microphone muted" : "Listening");
  }
  enableCues(value: boolean) {
    this.cuesEnabled = value;
    if (!value) this.cues?.cancel();
  }
  event(type: string, turn: string, tool?: string) {
    if (type !== "agent.response.delta")
      this.tr("sse", { type, turn, tool, blocked: turn === this.blockedTurn });
    if (type === "turn.started") {
      this.turnRunning = true;
      this.turn = turn;
      this.turnPhrases = 0;
      this.carry = "";
      this.firstDeltaTurn = "";
      this.cuePhase = "waiting";
      this.cues?.begin();
    }
    if (type === "turn.completed") this.turnRunning = false;
    if (turn === this.blockedTurn) return;
    if (type === "tool.started") {
      this.cuePhase =
        tool?.startsWith("network.") || tool?.startsWith("wifi.")
          ? "network"
          : tool === "appointment.availability" ||
              tool === "technician.availability"
            ? "availability"
            : "lookup";
      this.cues?.schedule(this.cuePhase, 1000);
    }
    if (type === "tool.completed" || type === "tool.failed") {
      this.cues?.cancel();
      this.cuePhase = "waiting";
    }
    if (type === "llm.started") this.cues?.schedule("waiting", 1400);
    if (
      type === "safety.blocked" ||
      type === "agent.error" ||
      type === "action.confirmation.required"
    )
      this.cues?.cancel();
  }
  enableOutput(value: boolean) {
    this.outputEnabled = value;
    if (!value) this.stopOutput("output-disabled");
  }
  delta(turn: string, text: string) {
    if (this.closed || !this.outputEnabled || turn === this.blockedTurn) return;
    if (turn !== this.turn) {
      this.text = "";
      this.carry = "";
      this.turnPhrases = 0;
      this.turn = turn;
    }
    if (turn !== this.firstDeltaTurn) {
      this.firstDeltaTurn = turn;
      this.firstDeltaAt = performance.now();
      this.tr("sse", { type: "agent.response.delta.first", turn });
    }
    // Record lists arrive atomically from verified backend results. Keep the full
    // list in chat and speak a bounded preview, without reading database IDs.
    if (text.includes("\n- ")) {
      const lines = text.split("\n");
      const rows = lines.filter((line) => line.startsWith("- "));
      text =
        lines.filter((line) => !line.startsWith("- ")).join(" ") +
        " " +
        rows
          .slice(0, 3)
          .map(
            (line) =>
              line
                .replace(/(?:Related )?ID:\s*[^\s·]+/g, "")
                .replace(/\s*·\s*$/, "") + ".",
          )
          .join(" ");
      if (rows.length > 3) text += " More options are listed in the chat.";
    }
    this.text += text;
    this.flush(false);
  }
  complete() {
    this.cues?.cancel();
    this.flush(true);
  }
  // End of the first sentence in this.text, or 0. Full stops after abbreviations
  // (Dr, Mr, Mrs, St, e.g., i.e., No before a number), inside numbers and after a
  // list marker do not count; closing quotes and brackets belong to the sentence.
  private boundary(final: boolean): number {
    const re = /[.!?]+["'”’)\]]*(?:\s|$)/g;
    for (let m = re.exec(this.text); m; m = re.exec(this.text)) {
      const before = this.text.slice(0, m.index);
      const end = m.index + m[0].length;
      const atEnd = end === this.text.length && !/\s$/.test(m[0]);
      if (m[0][0] === ".") {
        if (ABBREVIATION.test(before)) continue;
        const after = this.text.slice(end);
        if (/\bNo$/.test(before) && (!after.trim() || /^\s*\d/.test(after)))
          continue;
        // "1." at the start of a line is a list marker: speak it with its text.
        if (/(?:^|\n)\s*\d{1,3}$/.test(before)) continue;
        // A buffer ending in "51." may continue as "51.5" with the next delta.
        if (atEnd && !final && /\d$/.test(before)) continue;
      }
      return end;
    }
    return 0;
  }
  // First phrase of a turn only: a clause break once enough text is buffered, so
  // speech starts before the whole first sentence has been written.
  private clause(): number {
    const re = /[,;:](?=\s)|—/g;
    for (let m = re.exec(this.text); m; m = re.exec(this.text)) {
      const end = m.index + m[0].length;
      if (end >= FIRST_CLAUSE_MIN) return end;
    }
    return 0;
  }
  private clean(raw: string) {
    return raw
      .replace(/\b(?:Related )?ID:\s*[^\s·,]+/g, "")
      .replace(/[*#`]/g, "")
      .replace(/\s·\s/g, ", ")
      .replace(/^\s*-\s*/gm, "")
      .trim();
  }
  private enqueue(text: string): boolean {
    if (this.queue.length >= 40) {
      this.cb.error("Spoken reply is too long; the full answer is in chat.");
      this.text = "";
      this.carry = "";
      return false;
    }
    const id = `p${++this.phraseSeq}`;
    this.tr("phrase.queued", {
      id,
      turn: this.turn,
      index: this.turnPhrases,
      chars: text.length,
      text,
    });
    this.queue.push({
      id,
      index: this.turnPhrases,
      buffers: 0,
      bursts: 0,
      raw: [],
      burstOffsets: [],
      rawBytes: 0,
      text,
      turn: this.turn,
      queuedAt: performance.now(),
      first: this.turnPhrases === 0,
      textAt: this.firstDeltaAt,
      reported: false,
      chunks: [],
      done: false,
    });
    this.turnPhrases++;
    return true;
  }
  private flush(final: boolean) {
    // Tool records retain their complete visual representation. Strip technical IDs
    // from their spoken version, without asking a second LLM to reinterpret facts.
    while (this.text.trim()) {
      let n = this.boundary(final);
      if (this.turnPhrases === 0) {
        const c = this.clause();
        if (c && (!n || c < n)) n = c;
      }
      if ((!n || n > 240) && this.text.length > 240)
        n = this.text.lastIndexOf(" ", 220) + 1 || 220;
      if (!n && final) n = this.text.length;
      if (!n) break;
      const phrase = this.clean(this.carry + " " + this.text.slice(0, n));
      this.text = this.text.slice(n);
      if (!phrase) {
        this.carry = "";
        continue;
      }
      // Tiny phrases ride along with the next one: one request, one natural clause.
      if (!final && phrase.length < MIN_PHRASE) {
        this.carry = phrase;
        continue;
      }
      this.carry = "";
      if (!this.enqueue(phrase)) break;
    }
    if (final && this.carry) {
      const rest = this.carry;
      this.carry = "";
      this.enqueue(rest);
    }
    this.pump();
  }
  // Starts /speech requests, at most MAX_SPEECH_IN_FLIGHT at once, in queue order.
  private pump() {
    if (this.closed) return;
    const gen = this.generation;
    while (this.inflight < MAX_SPEECH_IN_FLIGHT && this.queue.length) {
      // Bound prepared audio; generation overlaps playback of the previous phrase.
      if (this.context && this.nextAudio - this.context.currentTime > 8) {
        if (!this.pumpTimer)
          this.pumpTimer = setTimeout(() => {
            this.pumpTimer = undefined;
            this.pump();
          }, 80);
        return;
      }
      const phrase = this.queue.shift()!;
      this.order.push(phrase);
      this.inflight++;
      void this.run(phrase, gen);
    }
  }
  // Fetches one phrase and hands its audio over in order. A failed request is
  // retried in place with backoff (keeping the phrase's turn in the playback order)
  // and only dropped, with one non-blocking error, after the last retry.
  private async run(phrase: Phrase, gen: number) {
    const live = () => gen === this.generation && !this.closed;
    let dropped = false;
    try {
      for (let attempt = 0; ; attempt++) {
        const ctl = new AbortController();
        this.aborts.add(ctl);
        const started = performance.now();
        // The deadline covers the headers phase only, so a hung request cannot block the queue.
        const timer = setTimeout(() => ctl.abort(), SPEECH_HEADERS_DEADLINE);
        let res: Response | undefined;
        let failure = "";
        this.tr("speech.request", {
          id: phrase.id,
          attempt,
          queued_ms: started - phrase.queuedAt,
          inflight: this.inflight,
          ct: this.context?.currentTime,
        });
        try {
          res = await fetch(`/api/sessions/${this.session}/speech`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ text: phrase.text, turn_id: phrase.turn }),
            signal: ctl.signal,
          });
          this.tr("speech.headers", {
            id: phrase.id,
            attempt,
            status: res.status,
            ms: performance.now() - started,
            content_type: res.headers.get("content-type"),
            sample_rate: res.headers.get("x-audio-sample-rate"),
          });
          if (!res.ok) {
            failure = `Speech service unavailable (${res.status})`;
            void res.body?.cancel().catch(() => undefined);
            res = undefined;
          }
        } catch (e) {
          failure =
            (e as Error).name === "AbortError"
              ? "Speech service did not respond in time"
              : (e as Error).message;
        } finally {
          clearTimeout(timer);
        }
        if (!live()) {
          this.tr("speech.abandoned", { id: phrase.id, stage: "headers" });
          this.aborts.delete(ctl);
          return;
        }
        if (!res) {
          this.aborts.delete(ctl);
          this.tr("speech.failed", {
            id: phrase.id,
            attempt,
            failure,
            will_retry: attempt < PHRASE_RETRY_DELAYS.length,
          });
          if (attempt < PHRASE_RETRY_DELAYS.length) {
            await new Promise((r) =>
              setTimeout(r, PHRASE_RETRY_DELAYS[attempt]),
            );
            if (!live()) return;
            continue;
          }
          dropped = true;
          this.cb.error(
            `${failure}. One spoken phrase was skipped; the full reply is in chat.`,
          );
          return;
        }
        try {
          const reader = res.body!.getReader();
          let first = true;
          let leftover = new Uint8Array(0);
          let pending: Uint8Array[] = [];
          let timer: ReturnType<typeof setTimeout> | undefined;
          let reads = 0;
          let readBytes = 0;
          let burstReads = 0;
          let delivered = 0;
          const keepRaw = phrase.index < 2;
          const flush = () => {
            clearTimeout(timer);
            timer = undefined;
            if (!pending.length || !live()) return;
            const size = pending.reduce((n, c) => n + c.length, 0);
            this.tr("speech.burst", {
              id: phrase.id,
              n: phrase.bursts + 1,
              bytes: size,
              reads: burstReads,
              audio_ms: size / 48,
              since_request_ms: performance.now() - started,
            });
            phrase.burstOffsets.push(delivered);
            delivered += size;
            burstReads = 0;
            const burst = new Uint8Array(size);
            let at = 0;
            for (const c of pending) {
              burst.set(c, at);
              at += c.length;
            }
            pending = [];
            this.deliver(phrase, burst, gen);
          };
          for (;;) {
            const { value, done } = await reader.read();
            if (done) break;
            if (!live()) {
              clearTimeout(timer);
              this.tr("speech.abandoned", {
                id: phrase.id,
                stage: "body",
                reads,
                bytes: readBytes,
              });
              return;
            }
            reads++;
            burstReads++;
            readBytes += value.length;
            this.tr("speech.read", {
              id: phrase.id,
              n: reads,
              bytes: value.length,
              total: readBytes,
              since_request_ms: performance.now() - started,
              odd: value.length % 2 !== 0,
            });
            if (keepRaw && phrase.rawBytes < 512 * 1024) {
              phrase.raw.push(value.slice());
              phrase.rawBytes += value.length;
            }
            if (first) {
              first = false;
              const now = performance.now();
              this.cb.metric("TTS first bytes", now - started);
              this.cb.metric(
                "Phrase queued → first byte",
                now - phrase.queuedAt,
              );
            }
            const joined = new Uint8Array(leftover.length + value.length);
            joined.set(leftover);
            joined.set(value, leftover.length);
            const n = joined.length - (joined.length % 2);
            leftover = joined.slice(n);
            if (n) {
              pending.push(joined.slice(0, n));
              clearTimeout(timer);
              timer = setTimeout(flush, BURST_GAP_MS);
            }
          }
          flush();
          this.tr("speech.done", {
            id: phrase.id,
            reads,
            bytes: readBytes,
            leftover_bytes: leftover.length,
            audio_ms: readBytes / 48,
            duration_ms: performance.now() - started,
          });
          if (phrase.raw.length)
            this.trace.pcm(phrase.id, phrase.raw, phrase.burstOffsets, {
              turn: phrase.turn,
              index: phrase.index,
              text: phrase.text,
            });
          if (first) throw new Error("Speech service returned no audio.");
        } catch (e) {
          this.tr("speech.error", {
            id: phrase.id,
            message: (e as Error).message,
            live: live(),
          });
          if (live()) {
            dropped = true;
            this.cb.error((e as Error).message);
          }
        } finally {
          this.aborts.delete(ctl);
        }
        return;
      }
    } finally {
      if (live()) {
        phrase.done = true;
        this.inflight--;
        this.advance();
        if (
          dropped &&
          !this.output.size &&
          !this.order.length &&
          !this.queue.length
        )
          this.cb.status("Listening");
      }
    }
  }
  // Audio of the head phrase plays as it arrives; any other phrase buffers.
  private deliver(phrase: Phrase, bytes: Uint8Array, gen: number) {
    phrase.bursts++;
    if (this.order[0] === phrase) {
      this.cues?.cancel();
      this.play(bytes, gen, phrase);
    } else {
      this.tr("speech.held", {
        id: phrase.id,
        bytes: bytes.length,
        head: this.order[0]?.id,
      });
      phrase.chunks.push(bytes);
    }
  }
  // Retires finished head phrases and plays what the next one buffered meanwhile.
  private advance() {
    while (this.order.length && this.order[0].done) {
      this.order.shift();
      const head = this.order[0];
      if (head) {
        const chunks = head.chunks;
        head.chunks = [];
        for (const chunk of chunks) {
          this.cues?.cancel();
          this.play(chunk, this.generation, head);
        }
      }
    }
    this.pump();
  }
  private play(bytes: Uint8Array, gen: number, phrase?: Phrase) {
    const ctx = this.context;
    if (!ctx || this.closed || gen !== this.generation) return;
    // A context that is not running (autoplay policy, interruption) produces no sound.
    if (ctx.state !== "running")
      void ctx.resume().then(
        () => {
          if (!this.closed && this.output.size && ctx.state === "running")
            this.cb.status("Speaking");
        },
        () => undefined,
      );
    const buffer = ctx.createBuffer(1, bytes.length / 2, 24000);
    const samples = buffer.getChannelData(0);
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    for (let i = 0; i < samples.length; i++)
      samples[i] = view.getInt16(i * 2, true) / 32768;
    const source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(this.gain!);
    // Start on a sample of the output clock so consecutive buffers join exactly.
    const rate = ctx.sampleRate;
    const now = ctx.currentTime;
    const previousEnd = this.nextAudio;
    const at = Math.ceil(Math.max(now + START_LEAD, previousEnd) * rate) / rate;
    this.nextAudio = at + buffer.duration;
    // Waveform edges: a click shows as a jump at the buffer start or at a seam.
    let jump = 0;
    for (let i = 1; i < Math.min(samples.length, 256); i++)
      jump = Math.max(jump, Math.abs(samples[i] - samples[i - 1]));
    const first = samples.length ? samples[0] : 0;
    const last = samples.length ? samples[samples.length - 1] : 0;
    const sameDirect = previousEnd > 0 && this.lastPhrase === phrase?.id;
    const flags: string[] = [];
    // The previous buffer had already finished: silence, then this buffer.
    const gap = previousEnd > 0 ? at - previousEnd : null;
    if (previousEnd === 0 || previousEnd < now) flags.push("FROM_SILENCE");
    if (sameDirect && previousEnd < now) flags.push("UNDERRUN");
    if (previousEnd > 0 && previousEnd < now && !sameDirect)
      flags.push("PHRASE_GAP");
    if (gap !== null && gap >= 0 && gap < 1.5 / rate && previousEnd >= now)
      flags.push("SEAM");
    let peak = 0;
    for (let i = 0; i < samples.length; i += 1)
      peak = Math.max(peak, Math.abs(samples[i]));
    this.tr("audio.schedule", {
      phrase: phrase?.id,
      buffer: phrase ? phrase.buffers + 1 : undefined,
      ct: now,
      at,
      lead_ms: (at - now) * 1000,
      duration_ms: buffer.duration * 1000,
      samples: samples.length,
      previous_end: previousEnd || null,
      gap_ms: gap === null ? null : gap * 1000,
      flags,
      first_sample: first,
      first8: Array.from(samples.slice(0, 8), (v) => Math.round(v * 32768)),
      last_sample: last,
      prev_last_sample: sameDirect || previousEnd > 0 ? this.lastSample : null,
      seam_jump: previousEnd > 0 ? Math.abs(first - this.lastSample) : null,
      max_jump_first256: jump,
      peak,
      gain: this.gain?.gain.value,
      ctx_state: ctx.state,
      output_latency: ctx.outputLatency,
      active_sources: this.output.size,
    });
    if (flags.includes("UNDERRUN") || flags.includes("SEAM"))
      this.tr("audio.flag", { phrase: phrase?.id, flags });
    this.lastSample = last;
    this.lastPhrase = phrase?.id ?? "";
    if (phrase) phrase.buffers++;
    this.output.add(source);
    source.onended = () => {
      const ended = ctx.currentTime;
      this.tr("audio.ended", {
        phrase: phrase?.id,
        ct: ended,
        expected_end: at + buffer.duration,
        late_ms: (ended - (at + buffer.duration)) * 1000,
        remaining_sources: this.output.size - 1,
      });
      this.output.delete(source);
      source.disconnect();
      if (!this.output.size && !this.closed) {
        if (!this.order.length && !this.queue.length && !this.turnRunning)
          this.cb.status(this.muted ? "Microphone muted" : "Listening");
        else this.cues?.schedule("waiting", 1600);
      }
    };
    source.start(at);
    if (ctx.state === "running") this.cb.status("Speaking");
    if (phrase?.first && !phrase.reported) {
      phrase.reported = true;
      this.cb.metric(
        "First phrase: text → audible",
        performance.now() - phrase.textAt + (at - ctx.currentTime) * 1000,
      );
    }
    if (!this.firstPlayback) {
      this.firstPlayback = true;
      this.cb.metric("Browser playback buffer", (at - ctx.currentTime) * 1000);
      if (this.lastSpeech)
        this.cb.metric(
          "Speech → reply audio (estimated)",
          performance.now() - this.lastSpeech + (at - ctx.currentTime) * 1000,
        );
    }
  }
  stopOutput(reason = "stop") {
    this.tr("output.stop", {
      reason,
      ct: this.context?.currentTime,
      sources: this.output.size,
      queued: this.queue.length,
      inflight: this.inflight,
      cue_playing: !!this.cues?.playing,
      gain: this.gain?.gain.value,
    });
    this.turnRunning = false;
    this.cues?.cancel();
    if (this.gain) this.gain.gain.setValueAtTime(1, this.context!.currentTime);
    this.blockedTurn = this.turn;
    this.generation++;
    for (const ctl of this.aborts) ctl.abort();
    this.aborts.clear();
    this.queue = [];
    this.order = [];
    this.inflight = 0;
    this.text = "";
    this.carry = "";
    if (this.pumpTimer) {
      clearTimeout(this.pumpTimer);
      this.pumpTimer = undefined;
    }
    for (const source of this.output) {
      source.onended = null;
      try {
        source.stop();
        source.disconnect();
      } catch {
        /* Already ended. */
      }
    }
    this.output.clear();
    this.nextAudio = 0;
    this.lastSample = 0;
    this.lastPhrase = "";
    this.firstPlayback = false;
  }
  stop() {
    this.tr("voice.stop", this.audioInfo());
    this.closed = true;
    this.cues?.close();
    this.resetInput();
    this.stopOutput("stop");
    this.media?.getTracks().forEach((t) => t.stop());
    const ctx = this.context;
    if (this.detector)
      void this.detector.destroy().finally(() => {
        void ctx?.close();
      });
    else void ctx?.close();
    this.cb.status("Voice off");
    this.trace.stop();
  }
}
