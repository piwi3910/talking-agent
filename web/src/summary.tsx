// Screen 8 · Post-call summary, computed from the finished session's events.
import React, { useState } from "react";
import type { Channel, Live } from "./main";
import { Agent, Chat, clock, Event, RecordItem } from "./api";
import { hideVocalEvents } from "./vocal-events";
import { NavLink, PersonaMark, ThemeToggle } from "./ui";
import { Copy } from "./icons";

export type SummaryData = {
  agent: Agent;
  user?: RecordItem;
  channel: Channel;
  session: string;
  durationMs: number;
  chat: Chat[];
  events: Event[];
};

const avg = (values: number[]) =>
  values.length
    ? Math.round(values.reduce((s, v) => s + v, 0) / values.length)
    : undefined;
const numbers = (events: Event[], type: string, key: string) =>
  events
    .filter((e) => e.type === type)
    .map((e) => e.data[key])
    .filter((v): v is number => typeof v === "number");

export function Summary({
  data,
  live,
  onRestart,
}: {
  data: SummaryData | null;
  live: Live;
  onRestart: () => void;
}) {
  const [tab, setTab] = useState<"happened" | "hood">("happened");
  const [copied, setCopied] = useState(false);
  if (!data)
    return (
      <div className="screen summary">
        <main className="summary-empty">
          <h1>No finished session yet</h1>
          <p className="muted">End a live session to see its summary here.</p>
          <NavLink live={live} to={{ screen: "launcher" }} className="go-live">
            Back to the launcher
          </NavLink>
        </main>
      </div>
    );
  const { events, agent } = data;
  const turns = events.filter((e) => e.type === "turn.completed").length;
  const calls = events.filter(
    (e) => e.type === "tool.completed" || e.type === "tool.failed",
  );
  const approvals = events.filter(
    (e) => e.type === "action.confirmation.required",
  ).length;
  const memories = events
    .filter((e) => e.type === "memory.retrieval.completed")
    .reduce((n, e) => n + (e.data.count || 0), 0);
  const interrupted = events.filter(
    (e) => e.type.startsWith("turn.") && e.data.cancelled,
  ).length;
  const turnMs = avg(numbers(events, "turn.completed", "duration_ms"));
  const ttft = avg(numbers(events, "llm.first_token", "ttft_ms"));
  const ttfa = avg(numbers(events, "tts.first_audio", "ttfa_ms"));
  const failed = calls.filter((e) => e.type === "tool.failed").length;
  const stats: [string, string][] = [
    [String(turns), "turns"],
    [String(calls.length), "tool calls"],
    [String(approvals), "approvals requested"],
    [String(memories), "memories recalled"],
    [String(interrupted), "turns interrupted"],
    [turnMs !== undefined ? `${turnMs} ms` : "—", "avg turn"],
    [ttft !== undefined ? `${ttft} ms` : "—", "avg LLM first token"],
    [ttfa !== undefined ? `${ttfa} ms` : "—", "avg TTS first audio"],
  ];
  return (
    <div className="screen summary">
      <header className="summary-head">
        <div className="selected-persona">
          <PersonaMark agent={agent} size="lg" />
          <div>
            <div className="summary-title">
              <h1>Session ended after {clock(data.durationMs)}</h1>
              {failed === 0 && calls.length > 0 && (
                <span className="chip ok">ALL TOOLS SUCCEEDED</span>
              )}
            </div>
            <span>
              {agent.config.name} · {agent.config.organization} ·{" "}
              {data.channel === "voice" ? "Web voice" : "Web chat"}
              {data.user ? ` · ${data.user.id} ${data.user.name}` : ""}
            </span>
          </div>
        </div>
        <div className="topbar-actions">
          <div className="segmented" role="tablist" aria-label="Summary view">
            <button
              role="tab"
              aria-selected={tab === "happened"}
              onClick={() => setTab("happened")}
            >
              What happened
            </button>
            <button
              role="tab"
              aria-selected={tab === "hood"}
              onClick={() => setTab("hood")}
            >
              Under the hood
            </button>
          </div>
          <ThemeToggle live={live} />
        </div>
      </header>
      <main className="summary-main">
        {tab === "happened" ? (
          <section className="panel" aria-label="What happened">
            <h2 className="eyebrow">ACTIONS</h2>
            {calls.length ? (
              calls.map((e) => (
                <div className="tool-row" key={e.id}>
                  <code>{e.data.tool}</code>
                  <span className={e.type === "tool.failed" ? "failed" : ""}>
                    {e.type === "tool.failed" ? "failed" : "tool"}
                  </span>
                </div>
              ))
            ) : (
              <p className="quiet">No tools were called in this session.</p>
            )}
            <h2 className="eyebrow">TRANSCRIPT</h2>
            <div className="summary-transcript">
              {data.chat.length ? (
                data.chat.map((m) => (
                  <p key={m.id} className={m.role}>
                    <b>{m.role === "user" ? "Caller" : agent.config.name}</b>{" "}
                    {m.role === "assistant" ? hideVocalEvents(m.text) : m.text}
                  </p>
                ))
              ) : (
                <p className="quiet">Nothing was said.</p>
              )}
            </div>
          </section>
        ) : (
          <section className="panel" aria-label="Under the hood">
            <div className="stat-grid">
              {stats.map(([v, l]) => (
                <div className="stat" key={l}>
                  <b>{v}</b>
                  <span>{l}</span>
                </div>
              ))}
            </div>
          </section>
        )}
        <section className="panel summary-actions" aria-label="Next">
          <h2 className="eyebrow">TRACE</h2>
          <p className="trace-id">
            <code>{data.session}</code>
          </p>
          <button
            type="button"
            className="button"
            onClick={() => {
              void navigator.clipboard
                ?.writeText(data.session)
                .then(() => setCopied(true))
                .catch(() => setCopied(false));
              setTimeout(() => setCopied(false), 2000);
            }}
          >
            <Copy /> {copied ? "Copied" : "Copy trace id"}
          </button>
          <button type="button" className="go-live" onClick={onRestart}>
            New session
          </button>
          <NavLink
            live={live}
            to={{ screen: "launcher" }}
            className="button ghost"
          >
            Back to the launcher
          </NavLink>
        </section>
      </main>
    </div>
  );
}
