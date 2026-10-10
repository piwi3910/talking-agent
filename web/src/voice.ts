import { VoiceCues } from "./voice-cues";
import type { MicVAD } from "@ricky0123/vad-web";
import { Trace, type TraceData } from "./trace";
import { Resampler } from "./resample";
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
  chunks: Uint8Array[]; // an empty chunk marks the end of the phrase
  resampler?: Resampler; // continuous across the phrase's bursts
  done: boolean;
};
const PHRASE_RETRY_DELAYS = [150, 300, 600];
// One /speech stream at a time: two concurrent streams share the GPU and delay
// the head phrase's second burst past the end of its first, which is an audible
// gap after the first word. A phrase downloads in about half its playback time,
// so the next one is still ready before it is needed.
const MAX_SPEECH_IN_FLIGHT = 1;
const SPEECH_HEADERS_DEADLINE = 20000;
// The /speech stream is 16-bit mono PCM at this rate.
const SPEECH_RATE = 24000;
// Streaming TTS sends audio in bursts that the network splits into many reads.
// Joining reads that arrive within this window avoids a buffer seam (an audible
// click) inside a burst.
const BURST_GAP_MS = 25;
// Lead before audio starts from silence. The first burst is 0.64 s of audio and
// the second arrives about 0.5-0.85 s after it, so with 0.4 s of lead the second
// is still in time up to 1.04 s after the first.
const START_LEAD = 0.4;
// Pause between a filler cue finishing and the reply starting.
const CUE_GAP = 0.15;
// Silence during a running tool before a filler cue fills it.
const TOOL_CUE_SILENCE_MS = 700;
// Tool cues may follow an earlier cue sooner than general fillers do.
const TOOL_CUE_MIN_GAP_MS = 4000;
// Highest plausible echo coupling after the browser's echo canceller; a higher
// calibration means someone spoke during it and would block interruptions.
const MAX_COUPLING = 0.8;
// Audio still playing at least this far ahead is continued without a gap; one
// render quantum is 128 frames (about 3 ms).
const CONTINUE_MARGIN = 0.01;
// Speech needed before ducking playback. Echo of the agent's own voice can
// briefly reach 64 ms; a real interruption keeps going to the 128 ms barge-in.
const DUCK_SPEECH_MS = 96;
// Speech needed to treat sound during playback as an interruption. Echo of the
// agent's own voice passes the browser's echo canceller in short bursts.
const BARGE_SPEECH_MS = 250;
// Playback used to learn how loudly the agent's voice leaks into the microphone.
const ECHO_CALIBRATION_MS = 2000;
// How much louder than the expected echo the microphone must be to count as the caller.
const ECHO_MARGIN = 2;
// Continuous speech that confirms an interruption without waiting for a
// transcript (the recognizer only returns text once the person stops).
const BARGE_CONFIRM_MS = 800;
// How long spoken phrases stay eligible for echo matching.
const ECHO_WINDOW_MS = 20000;
const MIN_PHRASE = 25;
const FIRST_CLAUSE_MIN = 40;
// Abbreviations whose full stop does not end a sentence. "No" only counts before a number.
const ABBREVIATION = /(?:\b(?:Dr|Mr|Ms|Mrs|St)|\be\.g|\bi\.e)$/;
export class Voice {
  // The voice session currently driving the stage, for level-reactive visuals.
  static active: Voice | null = null;
  private analyser?: AnalyserNode;
  private levelBuffer?: Float32Array<ArrayBuffer>;
  private micLevel = 0;
  // Echo gate: how loud the agent's own voice comes back through the microphone,
  // relative to what is played, learned from the first seconds it speaks.
  private outEnvelope = 0;
  private coupling = 0;
  private calibration: number[] = [];
  private calibratedMs = 0;
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
  // First output frame after the audio scheduled so far, in whole frames of the
  // context clock (0 = nothing scheduled). Kept as an integer: adding durations
  // in seconds and rounding up occasionally lands one frame late, which is a
  // one-sample hole (a click) between two buffers.
  private nextFrame = 0;
  // Context time a filler cue still playing ends; the reply waits for it.
  private cueEnd = 0;
  // A tool call is in flight; silence after the agent's line gets a filler.
  private toolRunning = false;
  private toolCueDone = false;
  private cueGeneration = 0;
  private toolGeneration = 0;
  private silentSince = 0;
  // A barge-in during playback waits for its transcript: if it only repeats what
  // the agent just said, it was the agent hearing itself and playback continues.
  private bargePending = false;
  private recentSpoken: { text: string; at: number }[] = [];
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
      // A side branch that only measures what is being played (speech and cues).
      this.analyser = this.context.createAnalyser();
      this.analyser.fftSize = 512;
      this.analyser.smoothingTimeConstant = 0;
      this.gain.connect(this.analyser);
      // Test-only hook (tests/e2e-audio): lets a harness record the rendered signal.
      (
        window as unknown as {
          __voiceTap?: (c: AudioContext, g: GainNode, s: string) => void;
        }
      ).__voiceTap?.(this.context, this.gain, this.session);
      Voice.active = this;
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
  // Loudness of the agent's voice and of the microphone, each 0..1.
  levels(): { out: number; mic: number } {
    let out = 0;
    if (this.analyser && this.context?.state === "running")
      out = Math.min(1, this.outputRms() * 4);
    // While the agent talks, the microphone mostly hears the agent: only show
    // the caller once an interruption is confirmed (playback stopped).
    const playing = !!this.output.size || !!this.cues?.playing;
    const mic = this.closed || this.muted || playing ? 0 : this.micLevel;
    return { out, mic };
  }
  // Raw loudness of what is being played, as RMS of the last analyser window.
  private outputRms(): number {
    if (!this.analyser) return 0;
    const buf = (this.levelBuffer ??= new Float32Array(this.analyser.fftSize));
    this.analyser.getFloatTimeDomainData(buf);
    let sum = 0;
    for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i];
    return Math.sqrt(sum / buf.length);
  }
  // True when the microphone is clearly louder than the agent's own echo would
  // be. Learns the echo level during the first seconds of playback in a call,
  // when nothing counts as an interruption.
  private echoGate(micRms: number, ms: number): boolean {
    const out = this.outputRms();
    // Fast attack, ~300 ms release: echo arrives after the sound that caused it.
    this.outEnvelope = Math.max(out, this.outEnvelope * Math.exp(-ms / 300));
    if (this.calibratedMs < ECHO_CALIBRATION_MS) {
      if (this.outEnvelope > 0.005) {
        this.calibration.push(micRms / this.outEnvelope);
        this.calibratedMs += ms;
        if (this.calibratedMs >= ECHO_CALIBRATION_MS) {
          const sorted = [...this.calibration].sort((a, b) => a - b);
          this.coupling = Math.min(
            MAX_COUPLING,
            Math.max(0.02, sorted[Math.floor(sorted.length * 0.95)] || 0),
          );
          this.tr("echo.calibrated", {
            coupling: this.coupling,
            samples: sorted.length,
          });
        }
      }
      // During calibration, reject ordinary speaker leakage but allow a caller
      // whose level exceeds even the capped worst-case coupling estimate.
      return micRms > MAX_COUPLING * this.outEnvelope + 0.004;
    }
    return micRms > this.coupling * this.outEnvelope * ECHO_MARGIN + 0.004;
  }
  private frame(pcm: ArrayBuffer, probability: number, ms: number) {
    if (this.closed || this.muted) return;
    this.fillToolSilence();
    let micRms = 0;
    {
      const v = new Int16Array(pcm);
      let sum = 0;
      for (let i = 0; i < v.length; i++) sum += v[i] * v[i];
      const rms = v.length ? Math.sqrt(sum / v.length) / 32768 : 0;
      this.micLevel = probability > 0.5 ? Math.min(1, rms * 6) : 0;
      micRms = rms;
    }
    this.preRoll.push(pcm);
    if (this.preRoll.length > 13) this.preRoll.shift();
    const playing = !!this.output.size || !!this.cues?.playing;
    let speaking = probability > (this.active ? 0.4 : 0.6);
    if (playing) speaking = this.echoGate(micRms, ms) && speaking;
    else if (!playing) this.outEnvelope = 0;
    if (speaking) {
      if (this.speechFrames === 0) this.firstSpeechAt = performance.now();
      this.lastSpeech = performance.now();
      this.speechFrames = Math.min(256, this.speechFrames + ms);
    } else {
      this.speechFrames = Math.max(0, this.speechFrames - ms * 2);
    }
    // Duck at the first credible onset; commit without waiting for an STT result.
    if (playing && this.gain) {
      const target =
        this.bargePending || this.speechFrames >= DUCK_SPEECH_MS ? 0.12 : 1;
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
    if (
      this.bargePending &&
      speaking &&
      this.speechFrames >= BARGE_SPEECH_MS &&
      performance.now() - this.firstSpeechAt >= BARGE_CONFIRM_MS
    ) {
      this.tr("barge-in.confirmed", { by: "sustained-speech" });
      this.stopOutput("barge-in");
      this.cb.interrupt();
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
      this.speechFrames >= (playing ? BARGE_SPEECH_MS : 224)
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
      if (playing) {
        this.bargePending = true;
        this.cb.metric(
          "Interruption detection",
          performance.now() - this.firstSpeechAt,
        );
      } else {
        this.stopOutput("speech-start");
        this.cb.interrupt();
      }
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
          // The first words that are not the agent's own confirm a real interruption:
          // stop now instead of waiting for the end of the utterance.
          if (this.bargePending && partial.trim() && !this.isEcho(partial)) {
            this.tr("barge-in.confirmed", { chars: partial.length });
            this.stopOutput("barge-in");
            this.cb.interrupt();
          }
        }
        if (data.type === "stt.final") {
          final = true;
          this.active = false;
          this.socket = undefined;
          ws.close();
          const text = (data.text || partial).trim();
          if (this.bargePending) {
            this.bargePending = false;
            if (this.isEcho(text)) {
              this.tr("barge-in.echo", { text });
              this.restoreGain();
              this.cb.transcript("");
              this.cb.status(this.output.size ? "Speaking" : "Listening");
              return;
            }
            this.stopOutput("barge-in");
            this.cb.interrupt();
          }
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
          if (this.bargePending) {
            this.bargePending = false;
            this.restoreGain();
          }
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
      this.cueGeneration++;
      this.toolGeneration = this.cueGeneration;
      this.toolRunning = false;
      this.toolCueDone = false;
      this.turnRunning = true;
      this.turn = turn;
      this.turnPhrases = 0;
      this.carry = "";
      this.firstDeltaTurn = "";
      this.cuePhase = "waiting";
      this.cues?.begin();
    }
    if (type !== "turn.started" && turn !== this.turn) return;
    if (type === "turn.completed") this.turnRunning = false;
    if (turn === this.blockedTurn) return;
    if (type === "tool.started") {
      this.toolGeneration = this.cueGeneration;
      this.toolRunning = true;
      this.toolCueDone = false;
    }
    if (
      type === "tool.completed" ||
      type === "tool.failed" ||
      type === "turn.completed"
    )
      this.toolRunning = false;
    if (type === "tool.started") {
      this.cuePhase =
        tool?.startsWith("network.") || tool?.startsWith("wifi.")
          ? "network"
          : tool?.endsWith(".availability")
            ? "availability"
            : "lookup";
      // The cue itself is played by fillToolSilence once the agent is quiet.
    }
    if (type === "tool.completed" || type === "tool.failed") {
      this.settleCue();
      this.cuePhase = "waiting";
    }
    if (type === "llm.started") this.cues?.schedule("waiting", 2400);
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
    this.settleCue();
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
    return (
      raw
        .replace(/\b(?:Related )?ID:\s*[^\s·,]+/g, "")
        // Stage directions such as "[Phone call answered]" are never spoken.
        .replace(/\[[^\]\n]{1,60}\]/g, "")
        .replace(/[*#`]/g, "")
        .replace(/\s·\s/g, ", ")
        .replace(/^\s*-\s*/gm, "")
        .trim()
    );
  }
  // True when a transcript heard during playback only repeats the agent's own
  // recent words: the microphone picked up the speakers.
  private isEcho(text: string): boolean {
    const words = (t: string) => t.toLowerCase().match(/[a-z0-9']+/g) || [];
    const heard = words(text);
    if (!heard.length) return true;
    const since = performance.now() - ECHO_WINDOW_MS;
    this.recentSpoken = this.recentSpoken.filter((p) => p.at >= since);
    if (heard.length <= 4) {
      // Short answers often reuse one offered word. Only treat a short
      // transcript as echo when its whole multiword phrase occurs contiguously.
      if (heard.length < 2) return false;
      const phrase = heard.join(" ");
      return this.recentSpoken.some((p) =>
        words(p.text).join(" ").includes(phrase),
      );
    }
    const spoken = new Set(this.recentSpoken.flatMap((p) => words(p.text)));
    const known = heard.filter(
      (w) =>
        spoken.has(w) ||
        (w.length >= 3 && [...spoken].some((s) => s.startsWith(w))),
    );
    return heard.length <= 12 && known.length / heard.length >= 0.6;
  }
  // While a tool runs and the agent has said its line, one fitting cue fills the
  // silence once it lasts TOOL_CUE_SILENCE_MS. Checked every audio frame, so it
  // does not depend on timers racing the agent's own speech.
  private fillToolSilence() {
    const now = performance.now();
    // Speech still queued or downloading counts as talking: the agent's own
    // "one moment" line must come before any filler.
    if (
      this.output.size ||
      this.cues?.playing ||
      this.queue.length ||
      this.order.length ||
      this.inflight
    ) {
      this.silentSince = now;
      return;
    }
    if (
      this.toolRunning &&
      this.toolGeneration === this.cueGeneration &&
      !this.toolCueDone &&
      now - this.silentSince >= TOOL_CUE_SILENCE_MS
    ) {
      this.toolCueDone = true;
      this.cues?.play(this.cuePhase, TOOL_CUE_MIN_GAP_MS);
    }
  }
  // Stops pending filler cues without cutting off one that is mid-word.
  private settleCue() {
    this.cueEnd = Math.max(this.cueEnd, this.cues?.settle() ?? 0);
  }
  private restoreGain() {
    if (this.gain && this.context)
      this.gain.gain.setTargetAtTime(1, this.context.currentTime, 0.015);
    this.lastGainTarget = 1;
  }
  private enqueue(text: string): boolean {
    this.recentSpoken.push({ text, at: performance.now() });
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
      if (
        this.context &&
        this.nextFrame / this.context.sampleRate - this.context.currentTime > 8
      ) {
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
          // End of phrase: lets the resampler release its last few samples.
          if (live()) this.deliver(phrase, new Uint8Array(0), gen);
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
    if (bytes.length) phrase.bursts++;
    if (this.order[0] === phrase) {
      this.settleCue();
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
          this.settleCue();
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
    // The browser must never convert a sample rate itself: its per-buffer
    // resampler can render a frame too many at a seam. Convert here, continuously
    // across the phrase's bursts, so buffers are already at the context rate.
    const rate = ctx.sampleRate;
    const input = new Float32Array(bytes.length >> 1);
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    for (let i = 0; i < input.length; i++)
      input[i] = view.getInt16(i * 2, true) / 32768;
    let samples: Float32Array<ArrayBuffer> = input;
    if (rate !== SPEECH_RATE) {
      const r = phrase
        ? (phrase.resampler ??= new Resampler(SPEECH_RATE, rate))
        : new Resampler(SPEECH_RATE, rate);
      samples = r.push(input, !bytes.length);
    }
    if (!samples.length) return;
    const buffer = ctx.createBuffer(1, samples.length, rate);
    buffer.copyToChannel(samples, 0);
    const source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(this.gain!);
    // Start on a frame of the output clock. Audio that is still playing is
    // continued on the frame right after it, with no floating-point rounding. The
    // start lead applies only when starting from silence (or after an underrun):
    // applying it to a buffer that arrives while the previous one still plays
    // opens a silent gap of (arrival - previous end + lead), audible as a hiccup
    // right after the first 0.64 s burst whenever the second one is a little late.
    const now = ctx.currentTime;
    const previousEnd = this.nextFrame / rate;
    const startFrame =
      this.nextFrame >= Math.ceil((now + CONTINUE_MARGIN) * rate)
        ? this.nextFrame
        : Math.max(
            Math.ceil((now + START_LEAD) * rate),
            Math.ceil((this.cueEnd + CUE_GAP) * rate),
          );
    const at = startFrame / rate;
    this.nextFrame = startFrame + samples.length;
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
    // Silence scheduled although the previous buffer was still playing.
    if (gap !== null && previousEnd >= now && gap >= 1.5 / rate)
      flags.push("SCHEDULE_GAP");
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
    if (
      flags.includes("UNDERRUN") ||
      flags.includes("SEAM") ||
      flags.includes("SCHEDULE_GAP")
    )
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
        else if (!this.toolRunning) this.cues?.schedule("waiting", 1600);
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
    this.cueGeneration++;
    this.toolRunning = false;
    this.toolCueDone = false;
    this.bargePending = false;
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
    this.nextFrame = 0;
    this.cueEnd = 0;
    this.lastSample = 0;
    this.lastPhrase = "";
    this.firstPlayback = false;
  }
  stop() {
    if (Voice.active === this) Voice.active = null;
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
