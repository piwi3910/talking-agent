// Screen 1 · Persona launcher: choose a role, a channel and a caller, then go live.
import React, { useEffect } from "react";
import type { Channel, Live } from "./main";
import { accentOf, industryLabel, markOf, tagsOf } from "./api";
import { Brand, NavLink, NewBadge, PersonaMark, ThemeToggle } from "./ui";
import { Gear, Mic, Monitor, Phone } from "./icons";

const channels: {
  id: Channel | "mobile" | "kiosk" | "avatar";
  label: string;
  glyph: string;
  soon?: boolean;
}[] = [
  { id: "chat", label: "Web chat", glyph: "▤" },
  { id: "voice", label: "Web voice", glyph: "◉" },
  { id: "phone", label: "Phone · SIP", glyph: "◐" },
  { id: "mobile", label: "Mobile app", glyph: "▯", soon: true },
  { id: "kiosk", label: "Kiosk", glyph: "▣", soon: true },
  { id: "avatar", label: "Avatar", glyph: "◍", soon: true },
];

export function Launcher({ live }: { live: Live }) {
  const { agent, agents } = live;
  const user = agent?.users.find((u) => u.id === live.userID);
  const brand = agent?.config.branding || {};
  const locked = live.busy || live.starting;
  const ready = !!agent && !!live.userID && !live.starting;
  const phone = live.channel === "phone";

  // Space starts, 1–9 switch persona, unless the presenter is typing or on a control.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement | null;
      if (
        e.metaKey ||
        e.ctrlKey ||
        e.altKey ||
        target?.closest("input, textarea, select, button, a, [contenteditable]")
      )
        return;
      if (e.key === " " && ready && !phone) {
        e.preventDefault();
        void live.goLive();
      }
      const n = Number(e.key);
      if (n >= 1 && n <= agents.length && !locked)
        live.changeAgent(agents[n - 1].config.id);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const cta = live.starting
    ? live.outbound
      ? "Calling…"
      : "Connecting…"
    : live.outbound
      ? `Place call to ${user?.name || "contact"}`
      : `Go live with ${agent?.config.name || "your agent"}`;

  return (
    <div className="screen launcher">
      <header className="topbar">
        <Brand />
        <div className="topbar-actions">
          <span className="runtime-pill">
            <span className="live-dot ok" aria-hidden="true" />
            {/* Model names stay in the cockpit and settings, not the audience view. */}
            Runtime online
          </span>
          <NavLink
            live={live}
            to={{ screen: "cockpit" }}
            className="button ghost"
          >
            <Monitor /> Presenter cockpit
          </NavLink>
          <NavLink
            live={live}
            to={{ screen: "settings", section: "channels" }}
            className="button ghost"
          >
            <Gear /> Settings
          </NavLink>
          <ThemeToggle live={live} />
        </div>
      </header>

      <main className="launcher-main">
        <section className="launcher-roles" aria-label="Choose a role">
          <div className="hero">
            <span className="eyebrow">STEP 1 · CHOOSE A ROLE</span>
            <h1>
              One platform.
              <br />
              Every role. Every voice.
            </h1>
            <p>
              {agents.length || "Several"} agents running on one runtime. Pick
              who the customer meets, where they meet them, and the story you
              want to tell.
            </p>
          </div>
          <div className="persona-grid">
            {agents.map((a) => {
              const on = a.config.id === live.agentID;
              const accent = accentOf(a);
              return (
                <button
                  key={a.config.id}
                  className={`persona-card ${on ? "selected" : ""}`}
                  aria-pressed={on}
                  disabled={locked}
                  onClick={() => live.changeAgent(a.config.id)}
                  style={
                    {
                      "--accent-dark": accent.dark,
                      "--accent-light": accent.light,
                    } as React.CSSProperties
                  }
                >
                  <span className="persona-card-head">
                    <span className="persona-mark md" aria-hidden="true">
                      {markOf(a)}
                    </span>
                    <span className="persona-card-title">
                      <strong>{a.config.name}</strong>
                      <small>{a.config.organization}</small>
                    </span>
                  </span>
                  <span className="persona-card-role">{a.config.role}</span>
                  <span className="tags">
                    {tagsOf(a).map((t) => (
                      <span className="tag" key={t}>
                        {t}
                      </span>
                    ))}
                  </span>
                </button>
              );
            })}
          </div>
        </section>

        <aside className="launcher-setup" aria-label="Session setup">
          <div className="selected-persona">
            <PersonaMark agent={agent} size="lg" />
            <div>
              <h2>{agent?.config.name || "Loading agents…"}</h2>
              <span>
                {agent?.config.organization}
                {brand.tagline ? ` · “${brand.tagline}”` : ""}
              </span>
            </div>
          </div>
          {brand.welcome && (
            <blockquote className="opening-line">{brand.welcome}</blockquote>
          )}

          <fieldset className="step">
            <legend className="eyebrow">STEP 2 · CHANNEL</legend>
            <div className="channel-grid">
              {channels.map((c) => {
                const on = c.id === live.channel;
                const unavailable =
                  c.soon || (c.id === "voice" && !live.voiceEnabled);
                return (
                  <button
                    key={c.id}
                    type="button"
                    className={`channel ${on ? "selected" : ""}`}
                    aria-pressed={on}
                    disabled={unavailable || locked}
                    title={
                      c.soon
                        ? "Coming soon"
                        : c.id === "voice" && !live.voiceEnabled
                          ? "Speech is not configured"
                          : undefined
                    }
                    onClick={() => live.setChannel(c.id as Channel)}
                  >
                    <span className="channel-glyph" aria-hidden="true">
                      {c.glyph}
                    </span>
                    {c.label}
                    {c.soon && <NewBadge />}
                  </button>
                );
              })}
            </div>
          </fieldset>

          <div className="step">
            <h3 className="eyebrow">
              STEP 3 · {industryLabel(agent?.config.industry)}
            </h3>
            <label className="field">
              <span>Demo identity</span>
              <select
                aria-label="Demo identity"
                value={live.userID}
                disabled={locked}
                onChange={(e) => live.changeUser(e.target.value)}
              >
                {agent?.users.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.id} · {u.name}
                  </option>
                ))}
              </select>
            </label>
            {user?.description && (
              <p className="story">
                <span className="story-title">{user.name}</span>
                <span>{user.description}</span>
              </p>
            )}
          </div>

          {phone ? (
            <NavLink
              live={live}
              to={{ screen: "settings", section: "channels" }}
              className="go-live"
            >
              <Phone size={20} /> Set up phone numbers
            </NavLink>
          ) : (
            <button
              type="button"
              className="go-live"
              disabled={!ready}
              onClick={() => void live.goLive()}
            >
              <Mic /> {cta}
            </button>
          )}
          <p className="hint">
            {phone ? (
              "Callers reach each persona on its own number through the SIP gateway."
            ) : (
              <>
                Press <kbd>Space</kbd> to start ·{" "}
                <kbd>1–{agents.length || 6}</kbd> to switch persona
              </>
            )}
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
          <p className="quiet">
            All records are fictional. A new session clears the conversation;
            long-term memory is preserved.
          </p>
        </aside>
      </main>
    </div>
  );
}
