// Screens 5 + 6 · Settings: channels (SIP + phone numbers), voice studio, personas, models, memory.
import React from "react";
import type { Live } from "./main";
import { PhoneSettings } from "./phone-settings";
import { VoiceSettings } from "./voice-settings";
import { Brand, NavLink, PersonaMark, ThemeToggle } from "./ui";

const sections: { id: string; label: string }[] = [
  { id: "channels", label: "Channels" },
  { id: "voices", label: "Voices" },
  { id: "personas", label: "Personas" },
  { id: "models", label: "Models" },
  { id: "memory", label: "Memory" },
  { id: "identities", label: "Demo identities" },
];

export function SettingsPage({
  live,
  section,
}: {
  live: Live;
  section: string;
}) {
  const current = sections.find((s) => s.id === section) || sections[0];
  return (
    <div className="screen settings">
      <nav className="settings-nav" aria-label="Settings sections">
        <NavLink
          live={live}
          to={{ screen: "launcher" }}
          className="settings-home"
        >
          <Brand subtitle="Settings" />
        </NavLink>
        {sections.map((s) => (
          <NavLink
            key={s.id}
            live={live}
            to={{ screen: "settings", section: s.id }}
            className="settings-nav-link"
            aria-current={s.id === current.id ? "page" : undefined}
          >
            {s.label}
          </NavLink>
        ))}
        <div className="settings-nav-foot">
          <NavLink
            live={live}
            to={{ screen: "launcher" }}
            className="button ghost"
          >
            Back to launcher
          </NavLink>
          <ThemeToggle live={live} />
        </div>
      </nav>
      <main className="settings-main">
        {current.id === "channels" && <Channels live={live} />}
        {current.id === "voices" && <Voices live={live} />}
        {current.id === "personas" && <Personas live={live} />}
        {current.id === "models" && <Models live={live} />}
        {current.id === "memory" && <Memory live={live} />}
        {current.id === "identities" && <Identities live={live} />}
      </main>
    </div>
  );
}

function Heading({ title, text }: { title: string; text: string }) {
  return (
    <div className="page-heading">
      <h1>{title}</h1>
      <p>{text}</p>
    </div>
  );
}

function Health({ live }: { live: Live }) {
  const tiles: [string, string, boolean][] = [
    ["LLM", live.agent?.llm || "—", !!live.agent],
    [
      "Speech-to-text",
      live.voiceEnabled
        ? live.speechInfo.stt || "Configured"
        : "Not configured",
      live.voiceEnabled,
    ],
    [
      "Text-to-speech",
      live.voiceEnabled
        ? live.speechInfo.tts || "Configured"
        : "Not configured",
      live.voiceEnabled,
    ],
    ["Memory", live.agent?.memory || "—", !!live.agent],
  ];
  return (
    <div className="health" aria-label="Runtime health">
      {tiles.map(([name, detail, ok]) => (
        <div className="health-tile" key={name}>
          <span>
            <span
              className={`live-dot ${ok ? "ok" : "off"}`}
              aria-hidden="true"
            />
            {name}
          </span>
          <small className="mono">{detail}</small>
        </div>
      ))}
    </div>
  );
}

function Channels({ live }: { live: Live }) {
  return (
    <>
      <Heading
        title="Channels"
        text="Every frontend that can reach your agents. One runtime behind all of them."
      />
      <Health live={live} />
      <div className="settings-cards">
        <PhoneSettings personas={live.agents} />
      </div>
    </>
  );
}

function Voices({ live }: { live: Live }) {
  return (
    <>
      <Heading
        title="Voice studio"
        text={`${live.speechInfo.tts || "Text-to-speech"} · preset, designed and cloned voices`}
      />
      <VoiceSettings personas={live.agents} />
    </>
  );
}

function Personas({ live }: { live: Live }) {
  return (
    <>
      <Heading
        title="Personas"
        text="Brand, persona and service capabilities loaded from each agent’s configuration."
      />
      <div className="persona-settings">
        {live.agents.map((a) => (
          <section
            className="panel"
            key={a.config.id}
            aria-label={`${a.config.name} capabilities`}
          >
            <div className="selected-persona">
              <PersonaMark agent={a} size="md" />
              <div>
                <h2>{a.config.name}</h2>
                <span>
                  {a.config.organization} · {a.config.role}
                </span>
              </div>
            </div>
            <dl className="properties">
              <dt>Industry</dt>
              <dd>{a.config.industry}</dd>
              <dt>Memory</dt>
              <dd>
                <code>{a.config.memory.namespace}</code>
                {a.config.memory.domain
                  ? " · shared across the organisation"
                  : ""}
              </dd>
              {Object.entries(a.config.persona || {}).map(([key, value]) => (
                <React.Fragment key={key}>
                  <dt>{key}</dt>
                  <dd>{value}</dd>
                </React.Fragment>
              ))}
            </dl>
            <h3>Skills &amp; tools</h3>
            {Object.entries(a.skills || {}).map(([id, s]) => (
              <details className="skill" key={id}>
                <summary>
                  <strong>{id}</strong>
                  <span>{s.tools.length} tools</span>
                </summary>
                <p>{s.description}</p>
                {s.tools.map((t) => (
                  <div className="capability" key={t.name}>
                    <code>{t.name}</code>
                    {t.mutation && <span className="pill">Confirmation</span>}
                    <p>{t.description}</p>
                  </div>
                ))}
              </details>
            ))}
            <h3>Knowledge</h3>
            {a.config.knowledge.length ? (
              a.config.knowledge.map((k) => (
                <p className="knowledge-file" key={k}>
                  {k}
                </p>
              ))
            ) : (
              <p className="quiet">No knowledge files.</p>
            )}
          </section>
        ))}
      </div>
    </>
  );
}

function Models({ live }: { live: Live }) {
  const llms = Array.from(
    new Set(live.agents.map((a) => a.llm).filter(Boolean)),
  );
  return (
    <>
      <Heading
        title="Models"
        text="What each stage of the conversation runs on."
      />
      <section className="panel">
        <dl className="properties">
          <dt>Language model</dt>
          <dd>{llms.join(", ") || "—"}</dd>
          <dt>Thinking</dt>
          <dd>Disabled in the KW conversational deployment</dd>
          <dt>Recognition</dt>
          <dd>
            {live.voiceEnabled
              ? `${live.speechInfo.stt ?? "Speech recognition"} · live PCM`
              : "Speech not configured"}
          </dd>
          <dt>Speech</dt>
          <dd>
            {live.voiceEnabled
              ? `${live.speechInfo.tts ?? "Speech synthesis"} · streamed PCM`
              : "Speech not configured"}
          </dd>
          <dt>Language</dt>
          <dd>English</dd>
          <dt>Noise handling</dt>
          <dd>Browser noise suppression + speech detection</dd>
          <dt>End of turn</dt>
          <dd>700 ms non-speech · Silero v6</dd>
        </dl>
      </section>
    </>
  );
}

function Memory({ live }: { live: Live }) {
  return (
    <>
      <Heading
        title="Memory"
        text="Agents keep their own customer memory unless their organisation shares it."
      />
      <section className="panel">
        <div className="table-scroll">
          <table className="data-table">
            <thead>
              <tr>
                <th scope="col">Persona</th>
                <th scope="col">Provider</th>
                <th scope="col">Namespace</th>
                <th scope="col">Isolation</th>
              </tr>
            </thead>
            <tbody>
              {live.agents.map((a) => (
                <tr key={a.config.id}>
                  <td>
                    {a.config.name} <small>{a.config.organization}</small>
                  </td>
                  <td>{a.memory}</td>
                  <td>
                    <code>{a.config.memory.namespace}</code>
                  </td>
                  <td>
                    {a.config.memory.domain
                      ? `Shared across ${a.config.organization}’s agents for this contact`
                      : "Organization → agent → namespace → selected user"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}

function Identities({ live }: { live: Live }) {
  return (
    <>
      <Heading
        title="Demo identities"
        text="Fictional callers each persona can be tested with. All records are fictional."
      />
      {live.agents.map((a) => (
        <section
          className="panel"
          key={a.config.id}
          aria-label={`${a.config.name} identities`}
        >
          <h2>
            {a.config.name}{" "}
            <small className="muted">{a.config.organization}</small>
          </h2>
          <div className="table-scroll">
            <table className="data-table">
              <tbody>
                {a.users.map((u) => (
                  <tr key={u.id}>
                    <td className="mono">{u.id}</td>
                    <td>{u.name}</td>
                    <td>{u.description}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ))}
    </>
  );
}
