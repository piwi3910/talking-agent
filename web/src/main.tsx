import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";
import { Voice } from "./voice";
import { api, accentOf, Agent, Chat, Event, Pending, SpeechInfo } from "./api";
import { Launcher } from "./launcher";
import { Stage } from "./stage";
import { Cockpit } from "./cockpit";
import { SettingsPage } from "./settings-page";
import { Summary, SummaryData } from "./summary";

export type Channel = "chat" | "voice" | "phone";
export type Theme = "dark" | "light";
export type Route =
  | { screen: "launcher" }
  | { screen: "stage" }
  | { screen: "cockpit" }
  | { screen: "summary" }
  | { screen: "settings"; section: string };

function parseRoute(path: string): Route {
  const parts = path.replace(/\/+$/, "").split("/").filter(Boolean);
  if (parts[0] === "stage") return { screen: "stage" };
  if (parts[0] === "cockpit") return { screen: "cockpit" };
  if (parts[0] === "summary") return { screen: "summary" };
  if (parts[0] === "settings")
    return { screen: "settings", section: parts[1] || "channels" };
  return { screen: "launcher" };
}
function pathOf(route: Route) {
  if (route.screen === "settings")
    return route.section === "channels"
      ? "/settings"
      : `/settings/${route.section}`;
  return route.screen === "launcher" ? "/" : `/${route.screen}`;
}
function storedTheme(): Theme {
  try {
    const value = localStorage.getItem("stage-theme");
    if (value === "light" || value === "dark") return value;
  } catch {
    // Storage can be unavailable (private mode); fall back to the stage default.
  }
  return "dark";
}

// Everything the screens need from the live session engine.
export type Live = {
  agents: Agent[];
  agent?: Agent;
  agentID: string;
  userID: string;
  channel: Channel;
  session: string;
  startedAt: number;
  now: number;
  chat: Chat[];
  events: Event[];
  pending: Pending[];
  message: string;
  busy: boolean;
  starting: boolean;
  connected: boolean;
  openState: "none" | "calling" | "connected" | "failed";
  error: string;
  opens: boolean;
  outbound: boolean;
  voiceEnabled: boolean;
  speechInfo: SpeechInfo;
  voiceOn: boolean;
  voiceStatus: string;
  transcript: string;
  micMuted: boolean;
  spoken: boolean;
  cuesOn: boolean;
  xray: boolean;
  captions: boolean;
  latencyRibbon: boolean;
  browserMetrics: Record<string, number>;
  traceCopied: boolean;
  theme: Theme;
  setTheme: (t: Theme) => void;
  navigate: (r: Route) => void;
  changeAgent: (id: string) => void;
  changeUser: (id: string) => void;
  setChannel: (c: Channel) => void;
  setMessage: (m: string) => void;
  setError: (m: string) => void;
  start: () => Promise<void>;
  goLive: () => Promise<void>;
  endSession: () => void;
  send: (
    text: string,
    confirmation?: string,
    reject?: boolean,
  ) => Promise<void>;
  cancel: () => Promise<void>;
  toggleVoice: () => Promise<void>;
  toggleMute: () => void;
  finishInput: () => void;
  stopAudio: () => void;
  setSpoken: (v: boolean) => void;
  setCues: (v: boolean) => void;
  setXray: (v: boolean) => void;
  setCaptions: (v: boolean) => void;
  setLatencyRibbon: (v: boolean) => void;
  copyTrace: () => void;
};

function App() {
  const [route, setRoute] = useState<Route>(() =>
    parseRoute(location.pathname),
  );
  function navigate(next: Route) {
    setRoute(next);
    const path = pathOf(next);
    if (location.pathname !== path) history.pushState(null, "", path);
    window.scrollTo(0, 0);
  }
  useEffect(() => {
    const back = () => setRoute(parseRoute(location.pathname));
    window.addEventListener("popstate", back);
    return () => window.removeEventListener("popstate", back);
  }, []);
  const [theme, setThemeState] = useState<Theme>(storedTheme);
  function setTheme(t: Theme) {
    setThemeState(t);
    try {
      localStorage.setItem("stage-theme", t);
    } catch {
      // Not persisted; the choice still applies for this visit.
    }
  }
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  const [voiceEnabled, setVoiceEnabled] = useState(false);
  // Model names reported by the server, so the UI never hardcodes them.
  const [speechInfo, setSpeechInfo] = useState<SpeechInfo>({});
  const [voiceOn, setVoiceOn] = useState(false);
  const [voiceStatus, setVoiceStatus] = useState("Voice off");
  const [transcript, setTranscript] = useState("");
  const [micMuted, setMicMuted] = useState(false);
  const [spoken, setSpokenState] = useState(true);
  const [cuesOn, setCuesOn] = useState(true);
  const [xray, setXray] = useState(true);
  const [captions, setCaptions] = useState(true);
  const [latencyRibbon, setLatencyRibbon] = useState(true);
  const [browserMetrics, setBrowserMetrics] = useState<Record<string, number>>(
    {},
  );
  const [now, setNow] = useState(Date.now());
  const voice = useRef<Voice | null>(null);
  const busyRef = useRef(false);
  const sendRef = useRef<(text: string) => Promise<void>>(async () => {});
  useEffect(() => {
    api<{ enabled: boolean; stt?: string; tts?: string }>("/voice")
      .then((x) => {
        setVoiceEnabled(x.enabled);
        setSpeechInfo({ stt: x.stt, tts: x.tts });
      })
      .catch(() => {});
    const timer = setInterval(() => setNow(Date.now()), 100);
    return () => {
      clearInterval(timer);
      voice.current?.stop();
    };
  }, []);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentID, setAgentID] = useState("");
  const [userID, setUserID] = useState("");
  const [channel, setChannel] = useState<Channel>("chat");
  const [session, setSession] = useState("");
  const [startedAt, setStartedAt] = useState(0);
  const [traceCopied, setTraceCopied] = useState(false);
  const [chat, setChat] = useState<Chat[]>([]);
  const [events, setEvents] = useState<Event[]>([]);
  const [pending, setPending] = useState<Pending[]>([]);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [starting, setStarting] = useState(false);
  const [connected, setConnected] = useState(false);
  const [openState, setOpenState] = useState<
    "none" | "calling" | "connected" | "failed"
  >("none");
  const [error, setError] = useState("");
  const [summary, setSummary] = useState<SummaryData | null>(null);
  const source = useRef<EventSource | null>(null);
  const lastID = useRef(0);
  const agent = agents.find((a) => a.config.id === agentID);
  const openingMode = agent?.config.persona?.opening;
  const opens = openingMode === "inbound" || openingMode === "outbound";
  const outbound = openingMode === "outbound";
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
    if (agent) document.title = `${agent.config.organization} · Stage`;
  }, [agent]);
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
    setStartedAt(0);
    setConnected(false);
    setChat([]);
    setEvents([]);
    setPending([]);
    setMessage("");
    setBusy(false);
    setError("");
    setOpenState("none");
    lastID.current = 0;
  }
  function changeAgent(id: string) {
    reset();
    setAgentID(id);
    setUserID(agents.find((a) => a.config.id === id)?.users[0]?.id || "");
  }
  function changeUser(id: string) {
    reset();
    setUserID(id);
  }
  async function start() {
    reset();
    setStarting(true);
    // Voice may start only after several awaits, long after the click.
    // Create the AudioContext now, inside the gesture, so the browser lets it run.
    const wantVoice = voiceEnabled && (outbound || channel === "voice");
    const primed = wantVoice ? Voice.prime() : undefined;
    let handedOver = false;
    try {
      const data = await api<{ id: string }>("/sessions", {
        agent_id: agentID,
        user_id: userID,
      });
      setSession(data.id);
      setStartedAt(Date.now());
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
          setOpenState((s) => (s === "calling" ? "connected" : s));
        }
        if (event.type === "agent.response.delta") {
          setOpenState((s) => (s === "calling" ? "connected" : s));
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
      if (opens) {
        // The agent speaks first: wait for the event stream so the opening reply is not missed.
        setOpenState("calling");
        await new Promise<void>((resolve) => {
          if (stream.readyState === 1) return resolve();
          const timer = setTimeout(resolve, 1500);
          stream.addEventListener(
            "open",
            () => {
              clearTimeout(timer);
              resolve();
            },
            { once: true },
          );
        });
        if (source.current !== stream) return;
        // Placing a call (or a voice channel) starts voice as well so the opening is
        // spoken and the contact can answer by voice. Without voice it silently stays text.
        if (wantVoice) {
          handedOver = true;
          await startVoice(data.id, true, primed);
          if (source.current !== stream) return;
        }
        try {
          await api(`/sessions/${data.id}/open`, {});
        } catch {
          // 400/409: nothing to open. The session stays usable with the normal welcome.
          if (source.current === stream) setOpenState("failed");
        }
      } else if (wantVoice) {
        handedOver = true;
        await startVoice(data.id, false, primed);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      if (primed && !handedOver) void primed.close().catch(() => undefined);
      setStarting(false);
    }
  }
  async function goLive() {
    setSummary(null);
    navigate({ screen: "stage" });
    await start();
  }
  function endSession() {
    if (session && agent)
      setSummary({
        agent,
        user: agent.users.find((u) => u.id === userID),
        channel,
        session,
        durationMs: startedAt ? Date.now() - startedAt : 0,
        chat,
        events,
      });
    reset();
    navigate(session ? { screen: "summary" } : { screen: "launcher" });
  }
  async function send(text: string, confirmation?: string, reject = false) {
    if (!session || busyRef.current) return;
    voice.current?.stopOutput("send");
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
    voice.current?.stopOutput("cancel");
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
    await startVoice(session);
  }
  // Starts the browser voice session for sessionID. With silent set, a failure
  // (for example a denied microphone) leaves the chat in text mode without an error.
  async function startVoice(
    sessionID: string,
    silent = false,
    context?: AudioContext,
  ) {
    if (voice.current) {
      void context?.close().catch(() => undefined);
      return;
    }
    setError("");
    setVoiceOn(true);
    setMicMuted(false);
    const v = new Voice(
      sessionID,
      {
        status: setVoiceStatus,
        transcript: setTranscript,
        error: setError,
        metric: (name, ms) =>
          setBrowserMetrics((old) => ({ ...old, [name]: Math.round(ms) })),
        interrupt: () => {
          if (busyRef.current)
            void api(`/sessions/${sessionID}/cancel`, {}).catch((e) =>
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
      context,
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
        if (!silent)
          setError(`Microphone unavailable: ${(e as Error).message}`);
      }
    }
  }
  const live: Live = {
    agents,
    agent,
    agentID,
    userID,
    channel,
    session,
    startedAt,
    now,
    chat,
    events,
    pending,
    message,
    busy,
    starting,
    connected,
    openState,
    error,
    opens,
    outbound,
    voiceEnabled,
    speechInfo,
    voiceOn,
    voiceStatus,
    transcript,
    micMuted,
    spoken,
    cuesOn,
    xray,
    captions,
    latencyRibbon,
    browserMetrics,
    traceCopied,
    theme,
    setTheme,
    navigate,
    changeAgent,
    changeUser,
    setChannel,
    setMessage,
    setError,
    start,
    goLive,
    endSession,
    send,
    cancel,
    toggleVoice,
    toggleMute: () => {
      voice.current?.mute(!micMuted);
      setMicMuted(!micMuted);
    },
    finishInput: () => voice.current?.finishInput(),
    stopAudio: () => voice.current?.stopOutput("stop-button"),
    setSpoken: (value) => {
      setSpokenState(value);
      voice.current?.enableOutput(value);
    },
    setCues: (value) => {
      setCuesOn(value);
      voice.current?.enableCues(value);
    },
    setXray,
    setCaptions,
    setLatencyRibbon,
    copyTrace: () => {
      void navigator.clipboard
        ?.writeText(session)
        .then(() => setTraceCopied(true))
        .catch(() => setTraceCopied(false));
      setTimeout(() => setTraceCopied(false), 2000);
    },
  };
  const accent = accentOf(agent);
  return (
    <div
      className={`app screen-${route.screen}`}
      style={
        {
          "--accent-dark": accent.dark,
          "--accent-light": accent.light,
        } as React.CSSProperties
      }
    >
      {route.screen === "launcher" && <Launcher live={live} />}
      {route.screen === "stage" && <Stage live={live} />}
      {route.screen === "cockpit" && <Cockpit live={live} />}
      {route.screen === "summary" && (
        <Summary data={summary} live={live} onRestart={() => void goLive()} />
      )}
      {route.screen === "settings" && (
        <SettingsPage live={live} section={route.section} />
      )}
    </div>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
