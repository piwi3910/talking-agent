import { VoiceCues } from "./voice-cues";
import type { MicVAD } from "@ricky0123/vad-web";
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
const MAX_SPEECH_IN_FLIGHT = 2;
const SPEECH_HEADERS_DEADLINE = 20000;
// Streaming TTS sends audio in bursts that the network splits into many reads.
// Joining reads that arrive within this window avoids a buffer seam (an audible
// click) inside a burst.
const BURST_GAP_MS = 25;
// Lead before audio starts from silence; covers the gap until the second burst.
const START_LEAD = 0.25;
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
  // primed is an AudioContext created inside the user's click, for flows that
  // start voice only after several awaits (autoplay policies need the gesture).
  constructor(
    private session: string,
    private cb: VoiceCallbacks,
    private agentID: string,
    private primed?: AudioContext,
  ) {}
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
      await this.context.resume();
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
    if (playing && this.gain)
      this.gain.gain.setTargetAtTime(
        this.speechFrames >= 64 ? 0.12 : 1,
        this.context!.currentTime,
        0.015,
      );
    if (
      !this.active &&
      !this.socket &&
      this.speechFrames >= (playing ? 128 : 224)
    ) {
      this.active = true;
      this.silence = 0;
      this.frames = 0;
      this.firstPlayback = false;
      this.stopOutput();
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
      ws.onopen = () => {
        if (this.socket !== ws) return;
        initial.forEach((b) => ws.send(b));
      };
      let partial = "";
      let final = false;
      ws.onmessage = (e) => {
        if (this.socket !== ws) return;
        const data = JSON.parse(e.data);
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
            this.stopOutput();
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
      ws.onerror = () =>
        this.cb.error(
          "Speech connection failed. You can still type your message.",
        );
      ws.onclose = () => {
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
    if (!value) this.stopOutput();
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
    this.queue.push({
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
        try {
          res = await fetch(`/api/sessions/${this.session}/speech`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ text: phrase.text, turn_id: phrase.turn }),
            signal: ctl.signal,
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
          this.aborts.delete(ctl);
          return;
        }
        if (!res) {
          this.aborts.delete(ctl);
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
          const flush = () => {
            clearTimeout(timer);
            timer = undefined;
            if (!pending.length || !live()) return;
            const size = pending.reduce((n, c) => n + c.length, 0);
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
              return;
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
          if (first) throw new Error("Speech service returned no audio.");
        } catch (e) {
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
    if (this.order[0] === phrase) {
      this.cues?.cancel();
      this.play(bytes, gen, phrase);
    } else phrase.chunks.push(bytes);
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
    const at =
      Math.ceil(Math.max(ctx.currentTime + START_LEAD, this.nextAudio) * rate) /
      rate;
    this.nextAudio = at + buffer.duration;
    this.output.add(source);
    source.onended = () => {
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
  stopOutput() {
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
    this.firstPlayback = false;
  }
  stop() {
    this.closed = true;
    this.cues?.close();
    this.resetInput();
    this.stopOutput();
    this.media?.getTracks().forEach((t) => t.stop());
    const ctx = this.context;
    if (this.detector)
      void this.detector.destroy().finally(() => {
        void ctx?.close();
      });
    else void ctx?.close();
    this.cb.status("Voice off");
  }
}
