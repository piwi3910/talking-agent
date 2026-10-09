// Screen 2 · Live stage: the audience-facing conversation, with an optional X-ray.
import React, { useEffect, useRef, useState } from "react";
import type { Live } from "./main";
import { actionLabel, clock, detailOf, Event, kindOf } from "./api";
import { hideVocalEvents } from "./vocal-events";
import { BackButton, NavLink, PersonaMark, ThemeToggle } from "./ui";
import {
  Calendar,
  Copy,
  Eye,
  Keyboard,
  Mic,
  MicOff,
  StopSquare,
} from "./icons";

const latest = (events: Event[], type: string) =>
  [...events].reverse().find((e) => e.type === type);

// Server-reported stage timings plus the running turn clock.
export function metricsOf(live: Live): [string, number | undefined][] {
  const { events, busy, now } = live;
  const num = (v: unknown) => (typeof v === "number" ? v : undefined);
  const turnStart = latest(events, "turn.started");
  return [
    [
      "Turn elapsed",
      busy && turnStart
        ? Math.max(0, now - new Date(turnStart.time).getTime())
        : num(latest(events, "turn.completed")?.data.duration_ms),
    ],
    ["LLM text TTFT", num(latest(events, "llm.first_token")?.data.ttft_ms)],
    ["LLM generation", num(latest(events, "llm.completed")?.data.duration_ms)],
    [
      "Memory retrieval",
      num(latest(events, "memory.retrieval.completed")?.data.duration_ms),
    ],
    [
      "STT first partial",
      num(latest(events, "stt.first_partial")?.data.duration_ms),
    ],
    [
      "STT finalization",
      num(latest(events, "stt.final")?.data.finalization_ms),
    ],
    ["TTS first audio", num(latest(events, "tts.first_audio")?.data.ttfa_ms)],
    ["TTS generation", num(latest(events, "tts.completed")?.data.duration_ms)],
  ];
}

export function stateOf(live: Live) {
  if (!live.session) return { key: "idle", label: "READY WHEN YOU ARE" };
  if (live.openState === "calling")
    return {
      key: "thinking",
      label: live.outbound ? "CALLING…" : "PICKING UP…",
    };
  if (live.voiceOn && live.voiceStatus === "Speaking")
    return { key: "speaking", label: "SPEAKING · say “stop” to interrupt" };
  if (live.busy) return { key: "thinking", label: "THINKING" };
  if (live.voiceOn && live.micMuted)
    return { key: "muted", label: "MICROPHONE MUTED" };
  if (live.voiceOn)
    return { key: "listening", label: live.voiceStatus.toUpperCase() };
  return { key: "ready", label: "YOUR TURN" };
}

function Bars({ state }: { state: string }) {
  const amp =
    state === "speaking"
      ? 1
      : state === "listening"
        ? 0.55
        : state === "thinking"
          ? 0.3
          : 0.15;
  return (
    <div className={`bars ${state}`} aria-hidden="true">
      {Array.from({ length: 28 }, (_, i) => (
        <span
          key={i}
          style={{
            height: Math.round(
              (10 + 30 * Math.abs(Math.sin(i * 1.7))) * amp + 4,
            ),
            opacity: 0.45 + 0.55 * Math.abs(Math.cos(i * 0.9)),
            animationDelay: `${(i * 0.06).toFixed(2)}s`,
          }}
        />
      ))}
    </div>
  );
}

export function Stage({ live }: { live: Live }) {
  const { agent, chat, pending, busy, session } = live;
  const brand = agent?.config.branding || {};
  const user = agent?.users.find((u) => u.id === live.userID);
  const [typing, setTyping] = useState(false);
  const bottom = useRef<HTMLDivElement>(null);
  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "auto", block: "nearest" });
  }, [chat, pending, busy]);
  const state = stateOf(live);
  const voiceMode = live.voiceOn && live.channel === "voice";
  const panelOpen = !voiceMode || typing;
  const lastUser = [...chat].reverse().find((m) => m.role === "user");
  const lastAgent = [...chat].reverse().find((m) => m.role === "assistant");
  const calling = !!session && live.opens && live.openState !== "failed";
  const statusText = session
    ? live.outbound && live.openState === "calling"
      ? "Calling…"
      : live.connected
        ? "Connected"
        : "Reconnecting…"
    : "Ready to help";
  const channelLabel =
    live.channel === "voice"
      ? "Web voice"
      : live.channel === "phone"
        ? "Phone"
        : "Web chat";

  return (
    <div className={`screen stage ${live.captions ? "big-captions" : ""}`}>
      <header className="topbar">
        <div className="topbar-group">
          <BackButton
            live={live}
            to={{ screen: "launcher" }}
            label="Back to launcher"
          />
          <span className="persona-chip">
            <PersonaMark agent={agent} size="sm" />
            <strong>{agent?.config.name || "Agent"}</strong>
            <span>{agent?.config.organization}</span>
          </span>
          <span className="runtime-pill">
            {session && <span className="live-dot rec" aria-hidden="true" />}
            {session ? "LIVE" : "IDLE"} · {channelLabel}
            {session && live.startedAt
              ? ` · ${clock(live.now - live.startedAt)}`
              : ""}
          </span>
          <span className={live.connected ? "status connected" : "status"}>
            {statusText}
          </span>
        </div>
        <div className="topbar-actions">
          <button
            type="button"
            className={`button ghost ${live.xray ? "on" : ""}`}
            aria-pressed={live.xray}
            onClick={() => live.setXray(!live.xray)}
          >
            <Eye /> X-ray
          </button>
          <NavLink
            live={live}
            to={{ screen: "cockpit" }}
            className="button ghost"
          >
            Cockpit
          </NavLink>
          <ThemeToggle live={live} />
          <button
            type="button"
            className="button danger"
            disabled={!session}
            onClick={live.endSession}
          >
            End session
          </button>
        </div>
      </header>

      <main className="stage-main">
        <section className="stage-center" aria-label="Customer experience">
          <div
            className={`orb-wrap ${state.key} ${panelOpen ? "compact" : ""}`}
            aria-hidden="true"
          >
            <span className="ripple" />
            <span className="ripple second" />
            <span className="orbit" />
            <span className="orb" />
          </div>
          <div className="stage-state">
            <Bars state={state.key} />
            <span className="state-label" role="status">
              {state.label}
            </span>
          </div>

          {!panelOpen && (
            <div className="captions" aria-live="polite">
              <p className="caption-user">
                {live.transcript || lastUser?.text
                  ? `“${live.transcript || lastUser?.text}”`
                  : `Say hello to ${agent?.config.name || "the agent"}.`}
              </p>
              {lastAgent && (
                <p className="caption-agent">
                  {hideVocalEvents(lastAgent.text)}
                </p>
              )}
            </div>
          )}

          {panelOpen && (
            <section className="conversation" aria-label="Support chat">
              <div
                className="messages"
                role="log"
                aria-label="Conversation messages"
                aria-live="polite"
              >
                {!chat.length && calling && (
                  <div className="welcome call-state" role="status">
                    <PersonaMark agent={agent} size="lg" />
                    <h3>
                      {live.outbound
                        ? live.openState === "connected"
                          ? `Connected to ${user?.name || "contact"}`
                          : `Calling ${user?.name || "contact"}…`
                        : `${agent?.config.name} is picking up…`}
                    </h3>
                  </div>
                )}
                {!chat.length && !calling && (
                  <div className="welcome">
                    <PersonaMark agent={agent} size="lg" />
                    <h3>
                      {session
                        ? `Hi ${user?.name.split(" ")[0] || "there"}, I’m ${agent?.config.name}.`
                        : `Hello, I’m ${agent?.config.name || "your assistant"}.`}
                    </h3>
                    <p>
                      {brand.welcome || "Tell me what you need a hand with."}
                    </p>
                    {!session && (
                      <button
                        className="primary start-chat"
                        disabled={!agent || !live.userID || live.starting}
                        onClick={() => void live.start()}
                      >
                        {live.starting
                          ? live.outbound
                            ? "Calling…"
                            : "Connecting…"
                          : live.outbound
                            ? `Place call to ${user?.name || "contact"}`
                            : "Start chat"}
                      </button>
                    )}
                  </div>
                )}
                {chat.map((m) => (
                  <article
                    key={m.id}
                    className={`bubble ${m.role} ${busy && m.role === "assistant" && m === chat[chat.length - 1] ? "streaming" : ""}`}
                  >
                    <div className="speaker">
                      {m.role === "user" ? "You" : agent?.config.name}
                    </div>
                    <div>
                      {m.role === "assistant"
                        ? hideVocalEvents(m.text)
                        : m.text}
                    </div>
                  </article>
                ))}
                {busy && (
                  <div className="working" role="status">
                    <span className="typing" aria-hidden="true">
                      ● ● ●
                    </span>{" "}
                    {agent?.config.name} is helping you…
                  </div>
                )}
                <div ref={bottom} />
              </div>
              <form
                className="composer"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (live.message.trim()) void live.send(live.message.trim());
                }}
              >
                <label className="sr-only" htmlFor="message">
                  Message
                </label>
                <textarea
                  id="message"
                  placeholder={
                    session
                      ? `Message ${agent?.config.name}…`
                      : "Go live to start the conversation"
                  }
                  disabled={!session || busy}
                  value={live.message}
                  maxLength={8000}
                  rows={1}
                  onChange={(e) => live.setMessage(e.target.value)}
                  onKeyDown={(e) => {
                    if (
                      e.key === "Enter" &&
                      !e.shiftKey &&
                      !e.nativeEvent.isComposing
                    ) {
                      e.preventDefault();
                      if (live.message.trim() && !busy)
                        void live.send(live.message.trim());
                    }
                  }}
                />
                {busy ? (
                  <button
                    type="button"
                    className="button"
                    onClick={() => void live.cancel()}
                  >
                    Stop
                  </button>
                ) : (
                  <button
                    className="primary send"
                    aria-label="Send ↗"
                    disabled={!session || !live.message.trim()}
                    type="submit"
                  >
                    Send <span aria-hidden="true">↗</span>
                  </button>
                )}
              </form>
            </section>
          )}

          {pending.map((p) => (
            <div className="confirmation" key={p.id}>
              <span className="confirmation-icon" aria-hidden="true">
                <Calendar />
              </span>
              <div className="confirmation-body">
                <span className="eyebrow warn">
                  NEEDS YOUR APPROVAL · until{" "}
                  {new Date(p.expires).toLocaleTimeString()}
                </span>
                <h3>{actionLabel(p.tool)}</h3>
                <dl>
                  {Object.entries(p.arguments).map(([key, value]) => (
                    <React.Fragment key={key}>
                      <dt>{key.replaceAll("_", " ")}</dt>
                      <dd>{value}</dd>
                    </React.Fragment>
                  ))}
                </dl>
              </div>
              <div className="confirmation-actions">
                <button
                  disabled={busy}
                  onClick={() => void live.send("", p.id, true)}
                >
                  Cancel
                </button>
                <button
                  className="primary"
                  disabled={busy}
                  onClick={() => void live.send("", p.id)}
                >
                  Confirm
                </button>
              </div>
            </div>
          ))}

          {live.error && (
            <div className="error" role="alert">
              {live.error}
              <button
                onClick={() => live.setError("")}
                aria-label="Dismiss error"
              >
                ×
              </button>
            </div>
          )}

          {live.voiceOn && live.transcript && (
            <p className="live-transcript" aria-live="polite">
              {live.transcript}
            </p>
          )}

          <div
            className="voice-controls"
            aria-label="Voice controls"
            role="group"
          >
            {live.voiceOn && (
              <button
                type="button"
                className="round"
                aria-label={live.micMuted ? "Unmute mic" : "Mute mic"}
                aria-pressed={live.micMuted}
                onClick={live.toggleMute}
              >
                <MicOff />
              </button>
            )}
            <button
              type="button"
              className={`round talk ${live.voiceOn ? "on" : ""}`}
              aria-label={live.voiceOn ? "End voice" : "Start voice"}
              title={
                !live.voiceEnabled
                  ? "Speech is not configured"
                  : live.voiceOn
                    ? "End voice"
                    : "Start voice"
              }
              disabled={!session || !live.voiceEnabled}
              onClick={() => void live.toggleVoice()}
            >
              <Mic size={28} />
            </button>
            {live.voiceOn && (
              <>
                <button
                  type="button"
                  className="round"
                  aria-label="Stop audio"
                  onClick={live.stopAudio}
                >
                  <StopSquare />
                </button>
                <button
                  type="button"
                  className="button ghost"
                  onClick={live.finishInput}
                >
                  Finish speaking
                </button>
              </>
            )}
            {voiceMode && (
              <button
                type="button"
                className="round"
                aria-label={typing ? "Hide keyboard" : "Type instead"}
                aria-pressed={typing}
                onClick={() => setTyping(!typing)}
              >
                <Keyboard />
              </button>
            )}
            {live.voiceOn && <span role="status">{live.voiceStatus}</span>}
          </div>
          <p className="chat-footnote">
            {agent?.config.name} is an AI assistant.{" "}
            {brand.disclaimer || "Please check important details."}
          </p>
        </section>

        {live.xray && <XRay live={live} />}
      </main>
    </div>
  );
}

const latencyParts: {
  name: string;
  type: string;
  key: string;
  tone: string;
}[] = [
  { name: "STT final", type: "stt.final", key: "finalization_ms", tone: "stt" },
  {
    name: "LLM first token",
    type: "llm.first_token",
    key: "ttft_ms",
    tone: "llm",
  },
  {
    name: "TTS first audio",
    type: "tts.first_audio",
    key: "ttfa_ms",
    tone: "tts",
  },
];

function XRay({ live }: { live: Live }) {
  const { events, agent, session } = live;
  const memories = latest(events, "memory.retrieval.completed");
  const recalled = memories?.data.memories || [];
  const calls = events.filter(
    (e) => e.type === "tool.completed" || e.type === "tool.failed",
  );
  const turns = events.filter((e) => e.type === "turn.completed").length;
  const display = events
    .filter((e) => e.type !== "agent.response.delta")
    .slice(-60)
    .reverse();
  const t0 = events[0] ? new Date(events[0].time).getTime() : 0;
  const parts = latencyParts.map((p) => {
    const v = latest(events, p.type)?.data[p.key];
    return { ...p, ms: typeof v === "number" ? Math.round(v) : undefined };
  });
  const measured = parts.filter((p) => p.ms !== undefined);
  const total = measured.reduce((s, p) => s + (p.ms || 0), 0);
  return (
    <aside className="xray" aria-label="Under the hood">
      <div className="xray-head">
        <h2>Under the hood</h2>
        <span className="mono quiet">
          turn {turns} · trace{" "}
          {session ? `${session.slice(0, 4)}…${session.slice(-3)}` : "—"}
        </span>
      </div>

      <section className="latency" aria-label="Live latency metrics">
        {live.latencyRibbon && (
          <div className="latency-card">
            <div className="latency-head">
              <span className="eyebrow">VOICE-TO-VOICE</span>
              <span className="latency-total">
                {measured.length ? total : "—"}
                <small> ms</small>
              </span>
            </div>
            <div className="latency-bar" aria-hidden="true">
              {measured.map((p) => (
                <span
                  key={p.name}
                  className={p.tone}
                  style={{ flexGrow: p.ms }}
                />
              ))}
            </div>
            <div className="latency-legend">
              {parts.map((p) => (
                <span key={p.name}>
                  <i className={p.tone} aria-hidden="true" />
                  {p.name} <span className="mono">{p.ms ?? "—"}</span>
                </span>
              ))}
            </div>
          </div>
        )}
        <details>
          <summary>All timings {live.busy ? "· running" : "· latest"}</summary>
          <div className="timing-grid">
            {metricsOf(live).map(([name, value]) => (
              <div key={name}>
                <span>{name}</span>
                <b>
                  {typeof value === "number" ? `${Math.round(value)} ms` : "—"}
                </b>
              </div>
            ))}
            {Object.entries(live.browserMetrics).map(([name, ms]) => (
              <div key={name}>
                <span>{name}</span>
                <b>{ms} ms</b>
              </div>
            ))}
          </div>
          <p className="quiet">
            Playback timing is a software estimate, not an acoustic measurement.
          </p>
        </details>
      </section>

      <section className="xray-split">
        <div>
          <h3>
            Memory <span className="count">{memories?.data.count ?? 0}</span>
          </h3>
          {recalled.length ? (
            recalled.map((m, i) => (
              <p className="memory" key={i}>
                {m.text}
              </p>
            ))
          ) : (
            <p className="quiet">
              {agent?.memory || "No memories recalled yet."}
            </p>
          )}
        </div>
        <div>
          <h3>
            Tools <span className="count">{calls.length}</span>
          </h3>
          {calls.length ? (
            calls.slice(-8).map((e) => (
              <div className="tool-row" key={e.id}>
                <code>{e.data.tool}</code>
                <span className={e.type === "tool.failed" ? "failed" : ""}>
                  {e.type === "tool.failed" ? "failed · " : ""}
                  {e.data.duration_ms} ms
                </span>
              </div>
            ))
          ) : (
            <p className="quiet">Tool calls will appear here.</p>
          )}
        </div>
      </section>

      <section>
        <h3>Event stream</h3>
        <div className="trace">
          {display.length === 0 && (
            <p className="quiet">
              Start a conversation to inspect the runtime.
            </p>
          )}
          {display.map((e) => {
            const kind = kindOf(e.type);
            const offset = t0 ? new Date(e.time).getTime() - t0 : 0;
            return (
              <details key={e.id}>
                <summary>
                  <span className="mono offset">+{Math.max(0, offset)}ms</span>
                  <span className={`kind kind-${kind.toLowerCase()}`}>
                    {kind}
                  </span>
                  <span className="event-text">
                    <span>{e.type}</span>
                    <small className="mono">{detailOf(e)}</small>
                  </span>
                </summary>
                <pre>{JSON.stringify(e.data, null, 2)}</pre>
              </details>
            );
          })}
        </div>
      </section>

      <section className="diagnostics">
        <h3>Diagnostics</h3>
        <p className="quiet">
          Call trace ID. Share it to have the call timeline inspected.
        </p>
        <p className="trace-id">
          <code>{session || "—"}</code>
        </p>
        <button
          type="button"
          className="button"
          disabled={!session}
          onClick={live.copyTrace}
        >
          <Copy /> {live.traceCopied ? "Copied" : "Copy trace id"}
        </button>
      </section>
    </aside>
  );
}
