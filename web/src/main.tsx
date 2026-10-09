import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";
import { Voice } from "./voice";
import { PhoneSettings } from "./phone-settings";
import { VoiceSettings } from "./voice-settings";
import { hideVocalEvents } from "./vocal-events";

type RecordItem = { id: string; name: string; description?: string };
type Tool = { name: string; description: string; mutation: boolean };
type Agent = {
  config: {
    id: string;
    name: string;
    organization: string;
    role: string;
    industry: string;
    persona: Record<string, string>;
    memory: { namespace: string };
    skills: string[];
    knowledge: string[];
    branding: Record<string, string>;
  };
  users: RecordItem[];
  skills: Record<
    string,
    { description: string; instructions: string; tools: Tool[] }
  >;
  llm: string;
  memory: string;
};
type Event = {
  id: number;
  type: string;
  turn_id?: string;
  time: string;
  data: {
    text?: string;
    message?: string;
    tool?: string;
    duration_ms?: number;
    ttft_ms?: number;
    count?: number;
    usage?: { total_tokens: number };
    memories?: { text: string }[];
    [key: string]: unknown;
  };
};
type Chat = { id: string; role: "user" | "assistant"; text: string };
type Pending = {
  id: string;
  tool: string;
  arguments: Record<string, string>;
  expires: string;
};
async function api<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(
    "/api" + path,
    body === undefined
      ? undefined
      : {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
  );
  const data = await response.json();
  if (!response.ok)
    throw new Error(data.error || `Request failed (${response.status})`);
  return data;
}
function actionLabel(tool: string) {
  const labels: Record<string, string> = {
    "wifi.optimize": "Update your Wi-Fi settings?",
    "wifi.restart": "Restart your router?",
    "plan.change": "Change your plan?",
    "appointment.book": "Book this appointment?",
    "appointment.reschedule": "Reschedule your appointment?",
    "appointment.cancel": "Cancel your appointment?",
    "technician.book": "Book a technician visit?",
    "ticket.create": "Create a support ticket?",
    "ticket.update": "Update your support ticket?",
    "support.create_request": "Send your service request?",
  };
  return labels[tool] || "Confirm this change?";
}
function App() {
  const [tab, setTab] = useState(
    location.pathname === "/settings" ? "settings" : "personas",
  );
  function selectTab(value: string) {
    setTab(value);
    history.replaceState(null, "", value === "settings" ? "/settings" : "/");
  }
  const [voiceEnabled, setVoiceEnabled] = useState(false);
  const [voiceOn, setVoiceOn] = useState(false);
  const [voiceStatus, setVoiceStatus] = useState("Voice off");
  const [transcript, setTranscript] = useState("");
  const [micMuted, setMicMuted] = useState(false);
  const [spoken, setSpoken] = useState(true);
  const [cuesOn, setCuesOn] = useState(true);
  const [browserMetrics, setBrowserMetrics] = useState<Record<string, number>>(
    {},
  );
  const [now, setNow] = useState(Date.now());
  const voice = useRef<Voice | null>(null);
  const busyRef = useRef(false);
  const sendRef = useRef<(text: string) => Promise<void>>(async () => {});
  useEffect(() => {
    api<{ enabled: boolean }>("/voice")
      .then((x) => setVoiceEnabled(x.enabled))
      .catch(() => {});
    const timer = setInterval(() => setNow(Date.now()), 100);
    return () => {
      clearInterval(timer);
      voice.current?.stop();
    };
  }, []);
  const [playground, setPlayground] = useState(true);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentID, setAgentID] = useState("");
  const [userID, setUserID] = useState("");
  const [session, setSession] = useState("");
  const [chat, setChat] = useState<Chat[]>([]);
  const [events, setEvents] = useState<Event[]>([]);
  const [pending, setPending] = useState<Pending[]>([]);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [starting, setStarting] = useState(false);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState("");
  const source = useRef<EventSource | null>(null);
  const lastID = useRef(0);
  const bottom = useRef<HTMLDivElement>(null);
  const agent = agents.find((a) => a.config.id === agentID);
  useEffect(() => {
    let live = true;
    api<Agent[]>("/agents")
      .then((items) => {
        if (!live) return;
        setAgents(items);
        const first =
          items.find((a) => a.config.industry === "telecom") || items[0];
        if (first) {
          setAgentID(first.config.id);
          setUserID(first.users[0]?.id || "");
        }
      })
      .catch((e) => {
        if (live) setError(e.message);
      });
    return () => {
      live = false;
      source.current?.close();
    };
  }, []);
  useEffect(() => {
    if (agent) document.title = `${agent.config.organization} · Chat`;
  }, [agent]);
  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "auto", block: "end" });
  }, [chat, pending, busy]);
  function reset() {
    voice.current?.stop();
    voice.current = null;
    setVoiceOn(false);
    setMicMuted(false);
    setTranscript("");
    setBrowserMetrics({});
    busyRef.current = false;
    source.current?.close();
    source.current = null;
    setSession("");
    setConnected(false);
    setChat([]);
    setEvents([]);
    setPending([]);
    setMessage("");
    setBusy(false);
    setError("");
    lastID.current = 0;
  }
  function changeAgent(id: string) {
    reset();
    setAgentID(id);
    setUserID(agents.find((a) => a.config.id === id)?.users[0]?.id || "");
  }
  async function start() {
    reset();
    setStarting(true);
    try {
      const data = await api<{ id: string }>("/sessions", {
        agent_id: agentID,
        user_id: userID,
      });
      setSession(data.id);
      const stream = new EventSource(`/api/sessions/${data.id}/events`);
      source.current = stream;
      stream.onopen = () => setConnected(true);
      stream.onerror = () => setConnected(false);
      stream.onmessage = (e) => {
        if (source.current !== stream) return;
        const event = JSON.parse(e.data) as Event;
        if (event.id <= lastID.current) return;
        lastID.current = event.id;
        voice.current?.event(event.type, event.turn_id || "", event.data.tool);
        setEvents((old) => [...old, event].slice(-300));
        if (event.type === "turn.started") {
          busyRef.current = true;
          setBusy(true);
        }
        if (event.type === "agent.response.delta") {
          voice.current?.delta(event.turn_id || "", event.data.text || "");
          setChat((old) => {
            const id = event.turn_id || "assistant";
            const existing = old.find((m) => m.id === id);
            if (existing)
              return old.map((m) =>
                m.id === id
                  ? { ...m, text: m.text + (event.data.text || "") }
                  : m,
              );
            return [
              ...old,
              { id, role: "assistant", text: event.data.text || "" },
            ];
          });
        }
        if (event.type === "action.confirmation.required")
          setPending((old) => [...old, event.data as unknown as Pending]);
        if (event.type === "agent.error" && !event.data.cancelled)
          setError(event.data.message || "Agent error");
        if (event.type === "turn.completed") {
          busyRef.current = false;
          setBusy(false);
          voice.current?.complete();
        }
      };
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setStarting(false);
    }
  }
  async function send(text: string, confirmation?: string, reject = false) {
    if (!session || busyRef.current) return;
    voice.current?.stopOutput();
    setError("");
    setBusy(true);
    busyRef.current = true;
    if (!confirmation) setPending([]);
    else setPending((old) => old.filter((p) => p.id !== confirmation));
    setChat((old) => [
      ...old,
      {
        id: crypto.randomUUID(),
        role: "user",
        text: confirmation
          ? reject
            ? "Cancel proposed action."
            : "Confirm proposed action."
          : text,
      },
    ]);
    setMessage("");
    try {
      await api(
        `/sessions/${session}/messages`,
        confirmation ? { confirmation, reject } : { message: text },
      );
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
      busyRef.current = false;
    }
  }
  sendRef.current = (text) => send(text);
  async function cancel() {
    voice.current?.stopOutput();
    try {
      await api(`/sessions/${session}/cancel`, {});
    } catch (e) {
      setError((e as Error).message);
    }
  }
  async function toggleVoice() {
    if (voice.current) {
      voice.current.stop();
      voice.current = null;
      setVoiceOn(false);
      setTranscript("");
      return;
    }
    if (!session) return;
    setError("");
    setVoiceOn(true);
    setMicMuted(false);
    const v = new Voice(
      session,
      {
        status: setVoiceStatus,
        transcript: setTranscript,
        error: setError,
        metric: (name, ms) =>
          setBrowserMetrics((old) => ({ ...old, [name]: Math.round(ms) })),
        interrupt: () => {
          if (busyRef.current)
            void api(`/sessions/${session}/cancel`, {}).catch((e) =>
              setError(e.message),
            );
        },
        final: async (text) => {
          // Barge-in cancels the old turn. Wait for its completion event before submitting.
          for (let i = 0; busyRef.current && i < 50; i++)
            await new Promise((r) => setTimeout(r, 100));
          if (voice.current !== v) return;
          if (busyRef.current) {
            setMessage(text);
            setError(
              "The previous reply is still stopping. Your transcript is ready to send.",
            );
            return;
          }
          await sendRef.current(text);
        },
      },
      agentID,
    );
    voice.current = v;
    v.enableOutput(spoken);
    v.enableCues(cuesOn);
    try {
      await v.start();
    } catch (e) {
      if (voice.current === v) {
        voice.current = null;
        setVoiceOn(false);
        setError(`Microphone unavailable: ${(e as Error).message}`);
      }
    }
  }
  const latest = (type: string) =>
    [...events].reverse().find((e) => e.type === type);
  const turnStart = latest("turn.started");
  const liveMetrics: [string, unknown][] = [
    [
      "Turn elapsed",
      busy && turnStart
        ? Math.max(0, now - new Date(turnStart.time).getTime())
        : latest("turn.completed")?.data.duration_ms,
    ],
    ["LLM text TTFT", latest("llm.first_token")?.data.ttft_ms],
    ["LLM generation", latest("llm.completed")?.data.duration_ms],
    [
      "Memory retrieval",
      latest("memory.retrieval.completed")?.data.duration_ms,
    ],
    ["STT first partial", latest("stt.first_partial")?.data.duration_ms],
    ["STT finalization", latest("stt.final")?.data.finalization_ms],
    ["TTS first audio", latest("tts.first_audio")?.data.ttfa_ms],
    ["TTS generation", latest("tts.completed")?.data.duration_ms],
  ];
  const memories = [...events]
    .reverse()
    .find((e) => e.type === "memory.retrieval.completed");
  const firstToken = [...events]
    .reverse()
    .find((e) => e.type === "llm.first_token");
  const tokens = events.reduce(
    (n, e) => n + (e.data.usage?.total_tokens || 0),
    0,
  );
  const calls = events.filter(
    (e) => e.type === "tool.completed" || e.type === "tool.failed",
  );
  const displayEvents = events
    .filter((e) => e.type !== "agent.response.delta")
    .slice(-60)
    .reverse();
  const brand = agent?.config.branding || {};
  const user = agent?.users.find((u) => u.id === userID);
  const tabs = ["personas", "activity", "capabilities", "voice", "settings"];
  return (
    <div
      className={`app ${playground ? "" : "customer-only"} ${tab === "settings" ? "settings-page" : ""}`}
      style={{ "--accent": brand.color || "#2764d8" } as React.CSSProperties}
    >
      <main className="workspace">
        <section
          className="customer-experience"
          aria-label="Customer experience"
        >
          <header className="brand-header">
            <div className="brand-lockup">
              <div className="brand-icon" aria-hidden="true">
                {brand.mark || "N"}
              </div>
              <div>
                <strong>{agent?.config.organization || "Welcome"}</strong>
                <span>{brand.tagline || "Here to help"}</span>
              </div>
            </div>
            <button
              className="settings-link"
              onClick={() => {
                setPlayground(true);
                selectTab("settings");
              }}
            >
              Settings
            </button>
            <button
              className="playground-toggle"
              onClick={() => setPlayground(!playground)}
              aria-expanded={playground}
              aria-controls="playground"
            >
              {playground ? "Hide playground" : "Open playground"}{" "}
              <span aria-hidden="true">☷</span>
            </button>
          </header>
          <div className="customer-stage">
            <div className="service-heading">
              <span className="eyebrow">
                {brand.service_label || "CUSTOMER SUPPORT"}
              </span>
              <h1>{brand.headline || "How can we help you today?"}</h1>
              <p>
                {brand.description || "A little help, whenever you need it."}
              </p>
            </div>
            <section className="conversation" aria-label="Support chat">
              <div className="chat-header">
                <div className="avatar">
                  {agent?.config.name.slice(0, 1) || "A"}
                  <i />
                </div>
                <div className="agent-heading">
                  <h2>{agent?.config.name || "Your assistant"}</h2>
                  <span>{agent?.config.role}</span>
                </div>
                <span className={connected ? "status connected" : "status"}>
                  {session
                    ? connected
                      ? "Connected"
                      : "Reconnecting…"
                    : "Ready to help"}
                </span>
              </div>
              <div
                className="messages"
                role="log"
                aria-label="Conversation messages"
                aria-live="polite"
              >
                {!chat.length && (
                  <div className="welcome">
                    <div className="welcome-symbol" aria-hidden="true">
                      {brand.mark || "N"}
                    </div>
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
                        disabled={!agent || !userID || starting}
                        onClick={start}
                      >
                        {starting ? "Connecting…" : "Start chat"}{" "}
                        <span aria-hidden="true">↗</span>
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
                {pending.map((p) => (
                  <div className="confirmation" key={p.id}>
                    <span className="eyebrow">YOUR APPROVAL</span>
                    <h3>{actionLabel(p.tool)}</h3>
                    <p>Please check the details before confirming.</p>
                    <dl>
                      {Object.entries(p.arguments).map(([key, value]) => (
                        <React.Fragment key={key}>
                          <dt>{key.replaceAll("_", " ")}</dt>
                          <dd>{value}</dd>
                        </React.Fragment>
                      ))}
                    </dl>
                    <small>
                      Available until {new Date(p.expires).toLocaleTimeString()}
                    </small>
                    <div>
                      <button
                        className="primary"
                        disabled={busy}
                        onClick={() => send("", p.id)}
                      >
                        Confirm
                      </button>
                      <button
                        disabled={busy}
                        onClick={() => send("", p.id, true)}
                      >
                        Cancel
                      </button>
                    </div>
                  </div>
                ))}
                {busy && (
                  <div className="working" role="status">
                    <span className="typing">● ● ●</span> {agent?.config.name}{" "}
                    is helping you…
                  </div>
                )}
                <div ref={bottom} />
              </div>
              {error && (
                <div className="error" role="alert">
                  {error}
                  <button
                    onClick={() => setError("")}
                    aria-label="Dismiss error"
                  >
                    ×
                  </button>
                </div>
              )}

              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  if (message.trim()) void send(message.trim());
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
                      : "Start a chat to get in touch"
                  }
                  disabled={!session || busy}
                  value={message}
                  maxLength={8000}
                  onChange={(e) => setMessage(e.target.value)}
                  onKeyDown={(e) => {
                    if (
                      e.key === "Enter" &&
                      !e.shiftKey &&
                      !e.nativeEvent.isComposing
                    ) {
                      e.preventDefault();
                      if (message.trim() && !busy) void send(message.trim());
                    }
                  }}
                />
                {busy ? (
                  <button type="button" onClick={cancel}>
                    Stop
                  </button>
                ) : (
                  <button
                    className="primary send"
                    aria-label="Send ↗"
                    disabled={!session || !message.trim()}
                    type="submit"
                  >
                    Send <span aria-hidden="true">↗</span>
                  </button>
                )}
              </form>
              <div className="voice-controls">
                <button
                  type="button"
                  disabled={!session || !voiceEnabled}
                  onClick={toggleVoice}
                >
                  {voiceOn ? "End voice" : "Start voice"}
                </button>
                {voiceOn && (
                  <>
                    <span role="status">{voiceStatus}</span>
                    <button
                      type="button"
                      onClick={() => {
                        voice.current?.mute(!micMuted);
                        setMicMuted(!micMuted);
                      }}
                    >
                      {micMuted ? "Unmute mic" : "Mute mic"}
                    </button>
                    <button
                      type="button"
                      onClick={() => voice.current?.finishInput()}
                    >
                      Finish speaking
                    </button>
                    <button
                      type="button"
                      onClick={() => voice.current?.stopOutput()}
                    >
                      Stop audio
                    </button>
                  </>
                )}
              </div>
              {voiceOn && transcript && (
                <p className="live-transcript" aria-live="polite">
                  {transcript}
                </p>
              )}
              <div className="chat-footnote">
                {agent?.config.name} is an AI assistant.{" "}
                {brand.disclaimer || "Please check important details."}
              </div>
            </section>
            <p className="customer-footer">
              {agent?.config.organization} <span>·</span>{" "}
              {brand.footer || "Here for you, every step of the way."}
            </p>
          </div>
        </section>
        <aside
          id="playground"
          className="playground"
          hidden={!playground}
          aria-label="Agent playground"
        >
          <div className="playground-header">
            <div>
              <span className="eyebrow">OPERATOR WORKSPACE</span>
              <h2>{tab === "settings" ? "Settings" : "Agent playground"}</h2>
            </div>
            {tab === "settings" ? (
              <button onClick={() => selectTab("personas")}>
                Back to conversation
              </button>
            ) : (
              <span className="lab-badge">LAB</span>
            )}
          </div>
          <div className="tabs" role="tablist" aria-label="Playground tabs">
            {tabs.map((t) => (
              <button
                id={`tab-${t}`}
                key={t}
                role="tab"
                aria-selected={tab === t}
                aria-controls={`panel-${t}`}
                tabIndex={tab === t ? 0 : -1}
                onClick={() => selectTab(t)}
                onKeyDown={(e) => {
                  const offset =
                    e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
                  if (offset) {
                    e.preventDefault();
                    const next =
                      tabs[
                        (tabs.indexOf(t) + offset + tabs.length) % tabs.length
                      ];
                    selectTab(next);
                    document.getElementById(`tab-${next}`)?.focus();
                  }
                }}
              >
                {t}
              </button>
            ))}
          </div>
          <section className="live-metrics" aria-label="Live latency metrics">
            <div className="section-heading">
              <h3>Live latency</h3>
              <span className="pill">{busy ? "RUNNING" : "LATEST"}</span>
            </div>
            <div className="timing-grid">
              {liveMetrics.map(([name, value]) => (
                <div key={name}>
                  <span>{name}</span>
                  <b>
                    {typeof value === "number"
                      ? `${Math.round(value)} ms`
                      : "—"}
                  </b>
                </div>
              ))}
            </div>
            <details>
              <summary>Browser audio timings</summary>
              {Object.entries(browserMetrics).map(([name, ms]) => (
                <div className="tool-row" key={name}>
                  <span>{name}</span>
                  <b>{ms} ms</b>
                </div>
              ))}
              <p className="quiet">
                Playback timing is a software estimate, not an acoustic
                measurement. Server values show the latest stage; tool timings
                are in Activity.
              </p>
            </details>
          </section>
          <div className="playground-content">
            <div
              id="panel-personas"
              role="tabpanel"
              aria-labelledby="tab-personas"
              hidden={tab !== "personas"}
            >
              <section>
                <h3>Choose your experience</h3>
                <p className="muted">
                  Switch the brand, persona and service capabilities. Each agent
                  keeps its own customer memory.
                </p>
                <div className="persona-cards">
                  {agents.map((a) => (
                    <button
                      className={`persona-card ${a.config.id === agentID ? "selected" : ""}`}
                      key={a.config.id}
                      aria-pressed={a.config.id === agentID}
                      disabled={busy || starting}
                      onClick={() => changeAgent(a.config.id)}
                    >
                      <span
                        className="persona-mark"
                        style={{ background: a.config.branding.color }}
                      >
                        {a.config.branding.mark || a.config.name[0]}
                      </span>
                      <span>
                        <strong>{a.config.organization}</strong>
                        <small>
                          {a.config.name} · {a.config.role}
                        </small>
                      </span>
                      <span className="selection-dot" />
                    </button>
                  ))}
                </div>
              </section>
              <section className="controls" aria-label="Session setup">
                <h3>
                  {agent?.config.industry === "hospital"
                    ? "Demo patient"
                    : agent?.config.industry === "school"
                      ? "Demo family"
                      : "Demo customer"}
                </h3>
                <label>
                  Demo identity
                  <select
                    aria-label="Demo identity"
                    value={userID}
                    disabled={busy || starting}
                    onChange={(e) => {
                      reset();
                      setUserID(e.target.value);
                    }}
                  >
                    {agent?.users.map((u) => (
                      <option key={u.id} value={u.id}>
                        {u.id} · {u.name} — {u.description}
                      </option>
                    ))}
                  </select>
                </label>
                <p className="scenario-note">{user?.description}</p>
                <button
                  className="primary full-width"
                  disabled={!agent || !userID || busy || starting}
                  onClick={start}
                >
                  {starting
                    ? "Starting…"
                    : session
                      ? "New session"
                      : "Start session"}
                </button>
                <p className="quiet">
                  All records are fictional. A new session clears the chat;
                  long-term memory is preserved.
                </p>
              </section>
              <section>
                <h3>Persona</h3>
                <dl className="properties">
                  <dt>Name</dt>
                  <dd>{agent?.config.name}</dd>
                  <dt>Role</dt>
                  <dd>{agent?.config.role}</dd>
                  {Object.entries(agent?.config.persona || {}).map(
                    ([key, value]) => (
                      <React.Fragment key={key}>
                        <dt>{key}</dt>
                        <dd>{value}</dd>
                      </React.Fragment>
                    ),
                  )}
                </dl>
              </section>
            </div>
            <div
              id="panel-activity"
              className="activity-content"
              role="tabpanel"
              aria-labelledby="tab-activity"
              hidden={tab !== "activity"}
            >
              <section>
                <div className="section-heading">
                  <h3>Memory</h3>
                  <span className="pill">{agent?.memory}</span>
                </div>
                <p className="metric">
                  {memories?.data.count ?? 0}
                  <small> relevant memories retrieved</small>
                </p>
                {memories?.data.memories?.map((m, i) => (
                  <p className="memory" key={i}>
                    {m.text}
                  </p>
                ))}
                <p className="quiet">
                  Namespace: {agent?.config.memory.namespace || "—"}
                </p>
              </section>
              <section>
                <h3>
                  Tools <span className="count">{calls.length}</span>
                </h3>
                {calls.length ? (
                  calls.slice(-8).map((e) => (
                    <div className="tool-row" key={e.id}>
                      <code>{e.data.tool}</code>
                      <span
                        className={e.type === "tool.failed" ? "failed" : ""}
                      >
                        {e.type === "tool.failed" ? "failed · " : ""}
                        {e.data.duration_ms} ms
                      </span>
                    </div>
                  ))
                ) : (
                  <p className="muted">Tool calls will appear here.</p>
                )}
                {pending.map((p) => (
                  <details key={p.id}>
                    <summary>Pending: {p.tool}</summary>
                    <pre>{JSON.stringify(p.arguments, null, 2)}</pre>
                  </details>
                ))}
              </section>
              <section>
                <h3>LLM</h3>
                <p className="provider-name">{agent?.llm}</p>
                <p className="quiet">
                  Thinking is disabled in the KW conversational deployment.
                </p>
                <div className="metrics">
                  <div>
                    <b>
                      {firstToken?.data.ttft_ms ?? "—"}
                      <small> ms</small>
                    </b>
                    <span>Latest text TTFT</span>
                  </div>
                  <div>
                    <b>{tokens || "—"}</b>
                    <span>Reported tokens</span>
                  </div>
                </div>
              </section>
              <section>
                <h3>Event stream</h3>
                <div className="trace">
                  {displayEvents.length === 0 && (
                    <p className="muted">
                      Start a conversation to inspect the runtime.
                    </p>
                  )}
                  {displayEvents.map((e) => (
                    <details key={e.id}>
                      <summary>
                        <span>{e.type}</span>
                        <time>{new Date(e.time).toLocaleTimeString()}</time>
                      </summary>
                      <pre>{JSON.stringify(e.data, null, 2)}</pre>
                    </details>
                  ))}
                </div>
              </section>
            </div>
            <div
              id="panel-capabilities"
              role="tabpanel"
              aria-labelledby="tab-capabilities"
              hidden={tab !== "capabilities"}
            >
              <section>
                <h3>Skills & tools</h3>
                <p className="muted">
                  Capabilities loaded from this agent’s configuration.
                </p>
                {Object.entries(agent?.skills || {}).map(([id, s]) => (
                  <details className="skill" key={id}>
                    <summary>
                      <strong>{id}</strong>
                      <span>{s.tools.length} tools</span>
                    </summary>
                    <p>{s.description}</p>
                    {s.tools.map((t) => (
                      <div className="capability" key={t.name}>
                        <code>{t.name}</code>
                        {t.mutation && (
                          <span className="pill">Confirmation</span>
                        )}
                        <p>{t.description}</p>
                      </div>
                    ))}
                  </details>
                ))}
              </section>
              <section>
                <h3>Knowledge</h3>
                {agent?.config.knowledge.map((k) => (
                  <p className="knowledge-file" key={k}>
                    {k}
                  </p>
                ))}
              </section>
              <section>
                <h3>Memory isolation</h3>
                <p className="muted">
                  Organization → agent → namespace → selected user
                </p>
                <code>{agent?.config.memory.namespace}</code>
              </section>
            </div>
            <div
              id="panel-settings"
              role="tabpanel"
              aria-labelledby="tab-settings"
              hidden={tab !== "settings"}
            >
              {tab === "settings" && (
                <>
                  <PhoneSettings personas={agents} />
                  <VoiceSettings personas={agents} />
                </>
              )}
            </div>
            <div
              id="panel-voice"
              role="tabpanel"
              aria-labelledby="tab-voice"
              hidden={tab !== "voice"}
            >
              <section>
                <span className="pill">PHASE 2</span>
                <h3 className="voice-heading">Voice workspace</h3>
                <p className="muted">
                  Turn on voice in the chat and allow microphone access. Speak
                  naturally; a short pause sends your message. You can interrupt
                  a spoken reply.
                </p>
                <dl className="properties">
                  <dt>Recognition</dt>
                  <dd>Nemotron 3.5 ASR · live PCM</dd>
                  <dt>Speech</dt>
                  <dd>Breeze TTS 2 · streamed PCM</dd>
                  <dt>Status</dt>
                  <dd>
                    {voiceEnabled ? voiceStatus : "Speech not configured"}
                  </dd>
                  <dt>Language</dt>
                  <dd>English</dd>
                  <dt>Noise handling</dt>
                  <dd>Browser noise suppression + speech detection</dd>
                  <dt>End of turn</dt>
                  <dd>700 ms non-speech · Silero v6</dd>
                </dl>
                <p className="quiet">
                  {agent?.config.name}’s fixed reference voice
                </p>
                {voiceEnabled && agentID && (
                  <audio
                    key={agentID}
                    controls
                    preload="none"
                    src={`/api/agents/${agentID}/voice-reference`}
                    style={{ width: "100%", marginTop: 8 }}
                  />
                )}
                <label className="speech-toggle">
                  <input
                    type="checkbox"
                    checked={spoken}
                    onChange={(e) => {
                      setSpoken(e.target.checked);
                      voice.current?.enableOutput(e.target.checked);
                    }}
                  />{" "}
                  Speak agent replies
                </label>
                <label className="speech-toggle">
                  <input
                    type="checkbox"
                    checked={cuesOn}
                    onChange={(e) => {
                      setCuesOn(e.target.checked);
                      voice.current?.enableCues(e.target.checked);
                    }}
                  />{" "}
                  Brief prerecorded acknowledgements
                </label>
                <p className="quiet">
                  Headphones help avoid speaker echo triggering an interruption.
                  Account changes still require the confirmation button. Each
                  persona uses one fixed synthetic reference voice across all
                  sentences.
                </p>
              </section>
            </div>
          </div>
          <div className="playground-footer">
            <span className="runtime-dot" /> One platform. Every experience.
          </div>
        </aside>
      </main>
    </div>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
