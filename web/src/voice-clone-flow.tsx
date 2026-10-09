import { useCallback, useEffect, useRef, useState } from "react";
import {
  CONSENT_TEXT,
  COUNTDOWN_SECONDS,
  DEFAULT_TEST_SENTENCE,
  MAX_SECONDS,
  SCRIPTS,
  TIPS,
  clock,
  markHeard,
  meterLevel,
  recordingToWav,
  toBase64,
  wordSimilarity,
} from "./voice-clone";
import type { Snapshot } from "./voice-settings";

type Step = "start" | "record" | "check" | "preview" | "save";
type Phase = "idle" | "starting" | "countdown" | "recording" | "recorded";
type CheckItem = {
  id: string;
  label: string;
  status: "pass" | "warn" | "fail";
  detail: string;
};
type Report = {
  duration_ms: number;
  rms_dbfs: number;
  clipping_ratio: number;
  checks: CheckItem[];
  transcript: string;
};
type Take = { wav: ArrayBuffer; url: string; seconds: number };
type Mic = {
  stream: MediaStream;
  ctx: AudioContext;
  source: MediaStreamAudioSourceNode;
  processor: ScriptProcessorNode;
  mute: GainNode;
};

const STEPS: { id: Step; label: string }[] = [
  { id: "start", label: "Start" },
  { id: "record", label: "Read and record" },
  { id: "check", label: "Check" },
  { id: "preview", label: "Preview" },
  { id: "save", label: "Save" },
];
const STATUS_LABEL = { pass: "Pass", warn: "Warning", fail: "Failed" } as const;

async function parse(response: Response) {
  try {
    return await response.json();
  } catch {
    return {};
  }
}
function micMessage(e: unknown): string {
  const name = (e as { name?: string })?.name;
  if (name === "NotAllowedError" || name === "SecurityError")
    return "Microphone access was blocked. Allow the microphone for this site in your browser settings, then try again.";
  if (name === "NotFoundError" || name === "OverconstrainedError")
    return "No microphone was found. Connect one and try again.";
  if (name === "NotReadableError")
    return "The microphone is in use by another application. Close it and try again.";
  return "Could not start the microphone. Check your browser settings and try again.";
}

export function VoiceCloneFlow({
  revision,
  takenIds,
  makeId,
  onSaved,
  onClose,
}: {
  revision: number;
  takenIds: string[];
  makeId: (name: string, taken: Set<string>) => string;
  onSaved: (snapshot: Snapshot, name: string) => void;
  onClose: () => void;
}) {
  const [step, setStep] = useState<Step>("start");
  const [name, setName] = useState("");
  const [consent, setConsent] = useState(false);
  const [scriptId, setScriptId] = useState(SCRIPTS[0].id);
  const [phase, setPhase] = useState<Phase>("idle");
  const [count, setCount] = useState(COUNTDOWN_SECONDS);
  const [elapsed, setElapsed] = useState(0);
  const [level, setLevel] = useState(0);
  const [take, setTake] = useState<Take>();
  const [micError, setMicError] = useState("");
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);
  const [report, setReport] = useState<Report>();
  const [transcript, setTranscript] = useState("");
  const [text, setText] = useState(DEFAULT_TEST_SENTENCE);
  const [direction, setDirection] = useState("");
  const [previewing, setPreviewing] = useState(false);
  const [saving, setSaving] = useState(false);
  const script = SCRIPTS.find((s) => s.id === scriptId) || SCRIPTS[0];

  const mounted = useRef(true);
  const mic = useRef<Mic | null>(null);
  const chunks = useRef<Float32Array[]>([]);
  const samples = useRef(0);
  const capturing = useRef(false);
  const levelRef = useRef(0);
  const countTimer = useRef(0);
  const stopRef = useRef<() => void>(() => {});
  const takeUrl = useRef("");
  const audio = useRef<HTMLAudioElement | null>(null);
  const previewUrl = useRef("");
  const abort = useRef<AbortController | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);

  // Stops the microphone, the capture graph and the countdown.
  const releaseMic = useCallback(() => {
    window.clearTimeout(countTimer.current);
    capturing.current = false;
    const m = mic.current;
    mic.current = null;
    if (!m) return;
    m.processor.onaudioprocess = null;
    try {
      m.source.disconnect();
      m.processor.disconnect();
      m.mute.disconnect();
    } catch {
      // Already disconnected.
    }
    m.stream.getTracks().forEach((t) => t.stop());
    void m.ctx.close().catch(() => {});
  }, []);
  const releasePreview = useCallback(() => {
    audio.current?.pause();
    audio.current = null;
    if (previewUrl.current) URL.revokeObjectURL(previewUrl.current);
    previewUrl.current = "";
  }, []);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      abort.current?.abort();
      releaseMic();
      releasePreview();
      if (takeUrl.current) URL.revokeObjectURL(takeUrl.current);
      takeUrl.current = "";
    };
  }, [releaseMic, releasePreview]);
  useEffect(() => {
    heading.current?.focus();
  }, [step]);

  // Meter and timer while the microphone is open.
  useEffect(() => {
    if (phase !== "countdown" && phase !== "recording") {
      setLevel(0);
      return;
    }
    let announced = 0;
    const id = window.setInterval(() => {
      setLevel(meterLevel(levelRef.current));
      const m = mic.current;
      if (phase === "recording" && m) {
        const seconds = samples.current / m.ctx.sampleRate;
        setElapsed(seconds);
        const mark = Math.floor(seconds / 5) * 5;
        if (mark > announced) {
          announced = mark;
          setStatus(`Recording, ${mark} seconds`);
        }
      }
    }, 80);
    return () => window.clearInterval(id);
  }, [phase]);

  const stop = useCallback(() => {
    const m = mic.current;
    if (!m) return;
    const rate = m.ctx.sampleRate;
    const captured = chunks.current;
    chunks.current = [];
    releaseMic();
    if (captured.length === 0) {
      setPhase("idle");
      setStatus("");
      setMicError(
        "No audio was captured. Check your microphone and try again.",
      );
      return;
    }
    const { wav, seconds } = recordingToWav(captured, rate);
    const url = URL.createObjectURL(new Blob([wav], { type: "audio/wav" }));
    takeUrl.current = url;
    setTake({ wav, url, seconds });
    setElapsed(seconds);
    setPhase("recorded");
    setStatus(
      `Recorded ${seconds.toFixed(1)} seconds. Play it back, then use it or record again.`,
    );
  }, [releaseMic]);
  stopRef.current = stop;

  async function openMic(): Promise<boolean> {
    if (!navigator.mediaDevices?.getUserMedia) {
      setMicError(
        "This browser cannot record audio. Open the app over HTTPS in a current browser.",
      );
      return false;
    }
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: false,
          noiseSuppression: true,
          autoGainControl: true,
        },
      });
    } catch (e) {
      setMicError(micMessage(e));
      return false;
    }
    if (!mounted.current) {
      stream.getTracks().forEach((t) => t.stop());
      return false;
    }
    const Context =
      window.AudioContext ||
      (window as unknown as { webkitAudioContext: typeof AudioContext })
        .webkitAudioContext;
    const ctx = new Context();
    try {
      await ctx.resume();
    } catch {
      // Capture below reports silence if the context never starts.
    }
    if (!mounted.current) {
      stream.getTracks().forEach((t) => t.stop());
      void ctx.close().catch(() => {});
      return false;
    }
    const source = ctx.createMediaStreamSource(stream);
    // ScriptProcessor is deprecated but works in every browser without a separate worklet file.
    const processor = ctx.createScriptProcessor(4096, 1, 1);
    const mute = ctx.createGain();
    mute.gain.value = 0;
    processor.onaudioprocess = (ev: AudioProcessingEvent) => {
      const data = ev.inputBuffer.getChannelData(0);
      let sum = 0;
      for (let i = 0; i < data.length; i++) sum += data[i] * data[i];
      levelRef.current = Math.sqrt(sum / data.length);
      if (!capturing.current) return;
      chunks.current.push(new Float32Array(data));
      samples.current += data.length;
      if (samples.current >= MAX_SECONDS * ctx.sampleRate) {
        capturing.current = false;
        window.setTimeout(() => stopRef.current(), 0);
      }
    };
    source.connect(processor);
    processor.connect(mute);
    mute.connect(ctx.destination);
    mic.current = { stream, ctx, source, processor, mute };
    return true;
  }
  function clearTake() {
    if (takeUrl.current) URL.revokeObjectURL(takeUrl.current);
    takeUrl.current = "";
    setTake(undefined);
    setReport(undefined);
    setTranscript("");
    setElapsed(0);
  }
  function startCapture() {
    chunks.current = [];
    samples.current = 0;
    capturing.current = true;
    setPhase("recording");
    setStatus("Recording. Read the script aloud.");
  }
  function runCount(n: number) {
    setCount(n);
    setStatus(`Recording starts in ${n}`);
    countTimer.current = window.setTimeout(() => {
      if (n > 1) runCount(n - 1);
      else startCapture();
    }, 1000);
  }
  async function begin() {
    setMicError("");
    setError("");
    clearTake();
    setPhase("starting");
    setStatus("Waiting for microphone permission");
    if (!(await openMic())) {
      if (mounted.current) {
        setPhase("idle");
        setStatus("");
      }
      return;
    }
    chunks.current = [];
    samples.current = 0;
    setPhase("countdown");
    runCount(COUNTDOWN_SECONDS);
  }
  function cancelCountdown() {
    releaseMic();
    setPhase("idle");
    setStatus("Recording cancelled");
  }
  function rerecord() {
    releasePreview();
    clearTake();
    setPhase("idle");
    setError("");
    setStatus("");
    setStep("record");
  }

  async function runCheck() {
    if (!take) return;
    setStep("check");
    setChecking(true);
    setError("");
    setReport(undefined);
    setStatus("Checking the recording");
    const controller = new AbortController();
    abort.current = controller;
    try {
      const response = await fetch("/api/settings/voices/clone/check", {
        method: "POST",
        headers: { "Content-Type": "audio/wav" },
        body: new Blob([take.wav], { type: "audio/wav" }),
        signal: controller.signal,
      });
      const data = await parse(response);
      if (!response.ok)
        throw new Error(data.error || `Check failed (${response.status})`);
      if (!mounted.current || controller.signal.aborted) return;
      setReport(data as Report);
      setTranscript((data as Report).transcript || "");
      setStatus("Check complete");
    } catch (e) {
      if (mounted.current && !controller.signal.aborted) {
        setError((e as Error).message);
        setStatus("");
      }
    } finally {
      if (abort.current === controller) abort.current = null;
      if (mounted.current) setChecking(false);
    }
  }

  async function playPreview() {
    if (!take || previewing) return;
    setPreviewing(true);
    setError("");
    setStatus("Generating the preview");
    const controller = new AbortController();
    abort.current = controller;
    try {
      const body: Record<string, string> = {
        clone_audio_base64: toBase64(take.wav),
        clone_transcript: transcript.trim(),
        text: text.trim() || DEFAULT_TEST_SENTENCE,
      };
      if (direction.trim()) body.direction = direction.trim();
      const response = await fetch("/api/settings/voices/preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
        signal: controller.signal,
      });
      if (!response.ok)
        throw new Error(
          (await parse(response)).error ||
            `Preview failed (${response.status})`,
        );
      const blob = await response.blob();
      if (!mounted.current || controller.signal.aborted) return;
      releasePreview();
      previewUrl.current = URL.createObjectURL(blob);
      audio.current = new Audio(previewUrl.current);
      await audio.current.play();
      setStatus("Playing the preview");
    } catch (e) {
      if (mounted.current && !controller.signal.aborted) {
        setError((e as Error).message);
        setStatus("");
      }
    } finally {
      if (abort.current === controller) abort.current = null;
      if (mounted.current) setPreviewing(false);
    }
  }

  async function save() {
    if (!take || saving) return;
    setSaving(true);
    setError("");
    setStatus("Saving the voice");
    try {
      const response = await fetch("/api/settings/voices/clone", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          revision,
          id: makeId(name.trim(), new Set(takenIds)),
          name: name.trim(),
          transcript: transcript.trim(),
          audio_base64: toBase64(take.wav),
          consent: true,
        }),
      });
      const data = await parse(response);
      if (!response.ok)
        throw new Error(data.error || "Could not save the cloned voice");
      onSaved(data as Snapshot, name.trim());
    } catch (e) {
      if (mounted.current) {
        setError((e as Error).message);
        setStatus("");
      }
    } finally {
      if (mounted.current) setSaving(false);
    }
  }

  const failed = report?.checks.some((c) => c.status === "fail") ?? false;
  const similarity = report
    ? Math.round(wordSimilarity(script.text, report.transcript) * 100)
    : 0;
  const heard = report ? markHeard(script.text, report.transcript) : [];
  const stepIndex = STEPS.findIndex((s) => s.id === step);
  const recorder = phase === "idle" || phase === "recorded";

  return (
    <section className="voice-clone" aria-label="Clone a voice">
      <div className="section-heading">
        <h4 tabIndex={-1} ref={heading}>
          Clone a voice: {STEPS[stepIndex].label}
        </h4>
        <button type="button" onClick={onClose}>
          Cancel cloning
        </button>
      </div>
      <ol className="clone-steps" aria-label="Cloning steps">
        {STEPS.map((s, i) => (
          <li
            key={s.id}
            className={
              i === stepIndex ? "current" : i < stepIndex ? "done" : ""
            }
            aria-current={i === stepIndex ? "step" : undefined}
          >
            <span className="clone-step-number">{i + 1}</span> {s.label}
          </li>
        ))}
      </ol>
      <p className="clone-status quiet" role="status" aria-live="polite">
        {status}
      </p>
      {error && (
        <p className="error voice-error" role="alert">
          {error}
        </p>
      )}

      {step === "start" && (
        <div className="clone-panel">
          <label>
            Name of the new voice
            <input
              type="text"
              value={name}
              maxLength={60}
              placeholder="My voice"
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label className="speech-toggle">
            <input
              type="checkbox"
              checked={consent}
              onChange={(e) => setConsent(e.target.checked)}
            />{" "}
            {CONSENT_TEXT}
          </label>
          <div className="voice-actions">
            <button
              className="primary"
              type="button"
              disabled={!name.trim() || !consent}
              onClick={() => setStep("record")}
            >
              Continue
            </button>
          </div>
        </div>
      )}

      {step === "record" && (
        <div className="clone-panel">
          <fieldset className="clone-scripts" disabled={phase !== "idle"}>
            <legend>Script to read</legend>
            {SCRIPTS.map((s) => (
              <label className="speech-toggle" key={s.id}>
                <input
                  type="radio"
                  name="clone-script"
                  checked={s.id === scriptId}
                  onChange={() => setScriptId(s.id)}
                />{" "}
                {s.label}
              </label>
            ))}
          </fieldset>
          <blockquote
            className="clone-script"
            aria-label="Script to read aloud"
          >
            {script.text}
          </blockquote>
          <ul className="clone-tips" aria-label="Recording tips">
            {TIPS.map((tip) => (
              <li key={tip}>{tip}</li>
            ))}
          </ul>
          {micError && (
            <p className="error voice-error" role="alert">
              {micError}
            </p>
          )}
          {phase === "countdown" && (
            <p className="clone-countdown" aria-hidden="true">
              {count}
            </p>
          )}
          {(phase === "countdown" || phase === "recording") && (
            <div className="clone-meter" aria-hidden="true">
              <span style={{ width: `${Math.round(level * 100)}%` }} />
            </div>
          )}
          {(phase === "recording" || phase === "recorded") && (
            <p className="clone-timer" aria-hidden="true">
              {clock(elapsed)} / {clock(MAX_SECONDS)}
            </p>
          )}
          {take && phase === "recorded" && (
            <audio
              className="clone-playback"
              controls
              src={take.url}
              aria-label="Your recording"
            />
          )}
          <div className="voice-actions">
            {recorder && (
              <button
                className={phase === "idle" ? "primary" : undefined}
                type="button"
                onClick={() => void begin()}
              >
                {phase === "recorded" ? "Record again" : "Start recording"}
              </button>
            )}
            {phase === "starting" && (
              <button type="button" disabled>
                Starting the microphone…
              </button>
            )}
            {phase === "countdown" && (
              <button type="button" onClick={cancelCountdown}>
                Cancel recording
              </button>
            )}
            {phase === "recording" && (
              <button className="primary" type="button" onClick={stop}>
                Stop recording
              </button>
            )}
            {phase === "recorded" && (
              <button
                className="primary"
                type="button"
                onClick={() => void runCheck()}
              >
                Use this recording
              </button>
            )}
            <button type="button" onClick={() => setStep("start")}>
              Back
            </button>
          </div>
        </div>
      )}

      {step === "check" && (
        <div className="clone-panel">
          {checking && <p>Checking the recording and transcribing it…</p>}
          {report && (
            <>
              <h5>Quality checks</h5>
              <ul className="clone-checks" aria-label="Quality checks">
                {report.checks.map((c) => (
                  <li className={`clone-check ${c.status}`} key={c.id}>
                    <span className="clone-badge">
                      {STATUS_LABEL[c.status]}
                    </span>{" "}
                    <strong>{c.label}</strong>
                    <span className="clone-detail"> {c.detail}</span>
                  </li>
                ))}
              </ul>
              <h5>Transcript match</h5>
              <div className="clone-compare">
                <div>
                  <h6>Script</h6>
                  <p>{script.text}</p>
                </div>
                <div>
                  <h6>Heard</h6>
                  <p aria-label="Heard transcript">
                    {heard.length === 0
                      ? "Nothing was recognised."
                      : heard.map((w, i) => (
                          <span
                            key={i}
                            className={w.match ? undefined : "clone-diff"}
                          >
                            {w.word}{" "}
                          </span>
                        ))}
                  </p>
                </div>
              </div>
              <p className="clone-similarity">
                Word match with the script: <strong>{similarity}%</strong>
              </p>
              <label>
                Transcript of the recording
                <textarea
                  rows={4}
                  value={transcript}
                  maxLength={500}
                  onChange={(e) => setTranscript(e.target.value)}
                />
              </label>
              <p className="quiet">
                The voice needs the exact words you spoke. Fix anything the
                recognizer got wrong; it does not have to match the script.
              </p>
              {failed && (
                <p className="error voice-error">
                  A check failed. Record again to continue.
                </p>
              )}
            </>
          )}
          <div className="voice-actions">
            {report && (
              <button type="button" onClick={() => setTranscript(script.text)}>
                Use the script text
              </button>
            )}
            <button type="button" onClick={rerecord}>
              Record again
            </button>
            {error && (
              <button type="button" onClick={() => void runCheck()}>
                Try the check again
              </button>
            )}
            {report && (
              <button
                className="primary"
                type="button"
                disabled={failed || !transcript.trim()}
                onClick={() => setStep("preview")}
              >
                Continue
              </button>
            )}
          </div>
        </div>
      )}

      {step === "preview" && (
        <div className="clone-panel">
          <label>
            Test sentence
            <textarea
              rows={2}
              value={text}
              maxLength={300}
              onChange={(e) => setText(e.target.value)}
            />
          </label>
          <label>
            Delivery direction (optional)
            <input
              type="text"
              value={direction}
              maxLength={500}
              placeholder="Warm, calm, unhurried"
              onChange={(e) => setDirection(e.target.value)}
            />
          </label>
          <div className="voice-actions">
            <button
              type="button"
              disabled={previewing}
              onClick={() => void playPreview()}
            >
              {previewing ? "Generating…" : "Play preview"}
            </button>
            <button type="button" onClick={() => setStep("check")}>
              Back
            </button>
            <button
              className="primary"
              type="button"
              onClick={() => setStep("save")}
            >
              Continue to save
            </button>
          </div>
        </div>
      )}

      {step === "save" && (
        <div className="clone-panel">
          <dl className="clone-summary">
            <dt>Name</dt>
            <dd>{name.trim()}</dd>
            <dt>Transcript</dt>
            <dd>{transcript.trim()}</dd>
            <dt>Consent</dt>
            <dd>Confirmed: {CONSENT_TEXT}</dd>
          </dl>
          <p className="quiet">
            After saving, assign the voice to a persona below and save the voice
            settings; its filler cues are then rendered in the background.
          </p>
          <div className="voice-actions">
            <button
              type="button"
              disabled={saving}
              onClick={() => setStep("preview")}
            >
              Back
            </button>
            <button
              className="primary"
              type="button"
              disabled={saving}
              onClick={() => void save()}
            >
              {saving ? "Saving…" : "Save voice"}
            </button>
          </div>
        </div>
      )}
    </section>
  );
}
