// Screen 3 · Presenter cockpit: session controls, show-off moments and stage toggles.
import React, { useEffect, useState } from "react";
import type { Live } from "./main";
import { api, clock, type SystemInfo } from "./api";
import { metricsOf, stateOf } from "./stage";
import { BackButton, NavLink, PersonaMark, Switch, ThemeToggle } from "./ui";

type Move = {
  key: string;
  title: string;
  desc: string;
  ready: boolean;
  run: () => void;
};

export function Cockpit({ live }: { live: Live }) {
  const { agent, session } = live;
  const user = agent?.users.find((u) => u.id === live.userID);
  const [fired, setFired] = useState("");
  const [systemInfo, setSystemInfo] = useState<SystemInfo>({});
  useEffect(() => {
    api<SystemInfo>("/system/info")
      .then(setSystemInfo)
      .catch(() => {});
  }, []);
  const tokens = live.events.reduce(
    (n, e) => n + (e.data.usage?.total_tokens || 0),
    0,
  );
  const state = stateOf(live);
  const moves: Move[] = [
    {
      key: "M",
      title: "Memory recall",
      desc: "Ask “what did we talk about last time?”",
      ready: !!session && !live.busy,
      run: () => void live.send("What did we talk about last time?"),
    },
    {
      key: "B",
      title: "Interrupt",
      desc: "Stop the spoken reply and cancel the turn.",
      ready: !!session && (live.busy || live.voiceOn),
      run: () => {
        live.stopAudio();
        if (live.busy) void live.cancel();
      },
    },
    {
      key: "U",
      title: live.micMuted ? "Unmute microphone" : "Mute microphone",
      desc: "Keep the room out of the conversation.",
      ready: live.voiceOn,
      run: live.toggleMute,
    },
    {
      key: "O",
      title: live.outbound ? "Place the call again" : "Agent speaks first",
      desc: live.opens
        ? "Start a fresh session; the agent opens the call."
        : "Only for personas with an opening line.",
      ready: live.opens && !live.starting,
      run: () => void live.goLive(),
    },
    {
      key: "V",
      title: "Swap voice",
      desc: "Pick or clone a voice in the voice studio.",
      ready: true,
      run: () => live.navigate({ screen: "settings", section: "voices" }),
    },
    {
      key: "P",
      title: "Call my phone",
      desc: "Assign numbers and connect the SIP gateway.",
      ready: true,
      run: () => live.navigate({ screen: "settings", section: "channels" }),
    },
  ];

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement | null;
      if (
        e.metaKey ||
        e.ctrlKey ||
        e.altKey ||
        target?.closest("input, textarea, select, [contenteditable]")
      )
        return;
      const move = moves.find((m) => m.key === e.key.toUpperCase());
      if (move?.ready) {
        e.preventDefault();
        setFired(move.title);
        move.run();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  return (
    <div className="screen cockpit">
      <header className="topbar">
        <div className="topbar-group">
          <BackButton
            live={live}
            to={{ screen: "launcher" }}
            label="Back to launcher"
          />
          <div className="title-block">
            <h1>Presenter cockpit</h1>
            <span>Only you see this screen · the audience sees the Stage</span>
          </div>
        </div>
        <div className="topbar-actions">
          <span className="runtime-pill">
            {session && <span className="live-dot rec" aria-hidden="true" />}
            {agent?.config.name || "Agent"} ·{" "}
            {live.channel === "voice" ? "Web voice" : "Web chat"}
            {session && live.startedAt
              ? ` · ${clock(live.now - live.startedAt)}`
              : " · idle"}
          </span>
          <label className="inline-field">
            <span className="sr-only">Swap identity</span>
            <select
              aria-label="Swap identity"
              value={live.userID}
              disabled={live.busy || live.starting}
              onChange={(e) => live.changeUser(e.target.value)}
            >
              {agent?.users.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.id} · {u.name}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            className="button ghost"
            disabled={!agent || !live.userID || live.busy || live.starting}
            onClick={() => void live.start()}
          >
            {session ? "Reset session" : "Start session"}
          </button>
          <ThemeToggle live={live} />
        </div>
      </header>

      <main className="cockpit-main">
        <section className="panel" aria-label="Session">
          <h2 className="eyebrow">SESSION</h2>
          <div className="selected-persona">
            <PersonaMark agent={agent} size="lg" />
            <div>
              <h3>{agent?.config.name}</h3>
              <span>
                {agent?.config.organization} · {agent?.config.role}
              </span>
            </div>
          </div>
          {user && (
            <p className="story">
              <span className="story-title">
                {user.id} · {user.name}
              </span>
              <span>{user.description}</span>
            </p>
          )}
          <dl className="properties">
            <dt>State</dt>
            <dd>{state.label}</dd>
            <dt>LLM</dt>
            <dd>{agent ? systemInfo[agent.config.id]?.llm || "—" : "—"}</dd>
            <dt>Speech</dt>
            <dd>
              {live.voiceEnabled
                ? `${live.speechInfo.stt ?? "STT"} · ${live.speechInfo.tts ?? "TTS"}`
                : "Speech not configured"}
            </dd>
            <dt>Tokens</dt>
            <dd>{tokens || "—"}</dd>
            {Object.entries(agent?.config.persona || {}).map(([key, value]) => (
              <React.Fragment key={key}>
                <dt>{key}</dt>
                <dd>{value}</dd>
              </React.Fragment>
            ))}
          </dl>
          <div className="timing-grid" aria-label="Latest timings">
            {metricsOf(live).map(([name, value]) => (
              <div key={name}>
                <span>{name}</span>
                <b>
                  {typeof value === "number" ? `${Math.round(value)} ms` : "—"}
                </b>
              </div>
            ))}
          </div>
        </section>

        <section className="panel" aria-label="Show-off moments">
          <h2 className="eyebrow">SHOW-OFF MOMENTS</h2>
          <div className="moves">
            {moves.map((m) => (
              <button
                key={m.key}
                type="button"
                className={`move ${fired === m.title ? "fired" : ""}`}
                disabled={!m.ready}
                onClick={() => {
                  setFired(m.title);
                  m.run();
                }}
              >
                <kbd>{m.key}</kbd>
                <strong>{m.title}</strong>
                <small>{m.desc}</small>
              </button>
            ))}
          </div>
          <p className="quiet">
            {fired
              ? `Sent to stage: ${fired}`
              : "Tip: every moment has a keyboard key, so you never leave the conversation."}
          </p>
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
        </section>

        <section className="panel" aria-label="Audience screen">
          <h2 className="eyebrow">AUDIENCE SCREEN</h2>
          <NavLink
            live={live}
            to={{ screen: "stage" }}
            className={`stage-preview ${state.key}`}
            aria-label="Open the audience stage"
          >
            <span className="orb" aria-hidden="true" />
            <span className="mono">{state.label}</span>
          </NavLink>
          <div className="switches">
            <Switch
              label="Spoken replies"
              hint="Text-to-speech on the audience screen"
              checked={live.spoken}
              onChange={live.setSpoken}
            />
            <Switch
              label="Filler cues"
              hint="“Let me check that…” while tools run"
              checked={live.cuesOn}
              onChange={live.setCues}
            />
            <Switch
              label="X-ray on stage"
              hint="Show events and latency to the room"
              checked={live.xray}
              onChange={live.setXray}
            />
            <Switch
              label="Big captions"
              hint="Readable from the back row"
              checked={live.captions}
              onChange={live.setCaptions}
            />
            <Switch
              label="Latency ribbon"
              hint="Voice-to-voice time per turn"
              checked={live.latencyRibbon}
              onChange={live.setLatencyRibbon}
            />
          </div>
          <p className="quiet">
            Headphones help avoid speaker echo triggering an interruption.
            Account changes still require the confirmation button.
          </p>
        </section>
      </main>
    </div>
  );
}
