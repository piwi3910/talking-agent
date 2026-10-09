import React, { useCallback, useEffect, useState } from "react";
import "./mcp-settings.css";

type Persona = { config: { id: string; name: string } };
type Status = {
  state: string;
  error?: string;
  tools: number;
  transport?: string;
};
type Server = {
  id: string;
  name: string;
  url: string;
  transport: string;
  headers?: Record<string, string>;
  enabled: boolean;
  agents: string[];
  allow_tools?: string[];
  deny_tools?: string[];
  unsupported?: string;
  status: Status;
};
type ToolInfo = { name: string; tool?: string; description: string };
type Header = { name: string; value: string };
type Draft = {
  isNew: boolean;
  id: string;
  name: string;
  url: string;
  transport: string;
  headers: Header[];
  enabled: boolean;
  all: boolean;
  agents: string[];
  allow: string;
  deny: string;
};

// The server returns this in place of stored header values; sending it back
// unchanged keeps the stored value.
const MASK = "********";

async function api<T>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const response = await fetch(path, {
    method,
    headers:
      body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok)
    throw new Error(data.error || `Request failed (${response.status})`);
  return data as T;
}

const blank = (): Draft => ({
  isNew: true,
  id: "",
  name: "",
  url: "",
  transport: "auto",
  headers: [{ name: "Authorization", value: "" }],
  enabled: true,
  all: false,
  agents: [],
  allow: "",
  deny: "",
});
const list = (text: string) =>
  text
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean);

function toDraft(s: Server): Draft {
  return {
    isNew: false,
    id: s.id,
    name: s.name,
    url: s.url,
    transport: s.transport || "auto",
    headers: Object.entries(s.headers || {}).map(([name, value]) => ({
      name,
      value,
    })),
    enabled: s.enabled,
    all: s.agents.includes("*"),
    agents: s.agents.filter((a) => a !== "*"),
    allow: (s.allow_tools || []).join(", "),
    deny: (s.deny_tools || []).join(", "),
  };
}

function stateLabel(s: Status): string {
  switch (s.state) {
    case "ok":
      return `Connected · ${s.tools} tool${s.tools === 1 ? "" : "s"}`;
    case "error":
      return "Error";
    case "disabled":
      return "Disabled";
    case "unconfigured":
      return "Credential missing";
    case "unsupported":
      return "Not supported";
    default:
      return "Not checked yet";
  }
}

export function McpSettings({ personas }: { personas: Persona[] }) {
  const [servers, setServers] = useState<Server[]>();
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState("");
  const [draft, setDraft] = useState<Draft>();
  const [tools, setTools] = useState<Record<string, ToolInfo[]>>({});
  const [problems, setProblems] = useState<Record<string, string>>({});

  const load = useCallback(async () => {
    setError("");
    try {
      setServers(await api<Server[]>("/api/mcp/servers"));
    } catch (e) {
      setError((e as Error).message);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!draft) return;
    setBusy("save");
    setError("");
    setNotice("");
    const headers: Record<string, string> = {};
    for (const h of draft.headers)
      if (h.name.trim()) headers[h.name.trim()] = h.value;
    const body = {
      ...(draft.isNew ? { id: draft.id.trim() } : {}),
      name: draft.name.trim(),
      url: draft.url.trim(),
      transport: draft.transport,
      headers,
      enabled: draft.enabled,
      agents: draft.all ? ["*"] : draft.agents,
      allow_tools: list(draft.allow),
      deny_tools: list(draft.deny),
    };
    try {
      if (draft.isNew) await api("/api/mcp/servers", "POST", body);
      else
        await api(
          `/api/mcp/servers/${encodeURIComponent(draft.id)}`,
          "PUT",
          body,
        );
      setNotice(
        `Saved ${draft.name.trim()}. Tools are loaded the next time an agent needs them.`,
      );
      setDraft(undefined);
      await load();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy("");
    }
  }

  async function remove(s: Server) {
    if (!window.confirm(`Remove ${s.name}? Agents lose its tools.`)) return;
    setBusy(s.id);
    setError("");
    setNotice("");
    try {
      await api(`/api/mcp/servers/${encodeURIComponent(s.id)}`, "DELETE");
      await load();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy("");
    }
  }

  async function test(s: Server) {
    setBusy(s.id);
    setError("");
    setNotice("");
    try {
      const out = await api<{ status: Status; tools: ToolInfo[] }>(
        `/api/mcp/servers/${encodeURIComponent(s.id)}/test`,
        "POST",
        {},
      );
      setTools((old) => ({ ...old, [s.id]: out.tools }));
      setProblems((old) => ({
        ...old,
        [s.id]:
          out.status.state === "ok"
            ? ""
            : out.status.error || "Connection failed",
      }));
      await load();
    } catch (err) {
      setProblems((old) => ({ ...old, [s.id]: (err as Error).message }));
    } finally {
      setBusy("");
    }
  }

  async function showTools(s: Server) {
    if (tools[s.id]) {
      setTools((old) => {
        const next = { ...old };
        delete next[s.id];
        return next;
      });
      return;
    }
    setBusy(s.id);
    try {
      const out = await api<ToolInfo[]>(
        `/api/mcp/servers/${encodeURIComponent(s.id)}/tools`,
      );
      setTools((old) => ({ ...old, [s.id]: out }));
      setProblems((old) => ({ ...old, [s.id]: "" }));
      await load();
    } catch (err) {
      setProblems((old) => ({ ...old, [s.id]: (err as Error).message }));
    } finally {
      setBusy("");
    }
  }

  const set = (patch: Partial<Draft>) =>
    setDraft((d) => (d ? { ...d, ...patch } : d));
  const setHeader = (i: number, patch: Partial<Header>) =>
    setDraft((d) =>
      d
        ? {
            ...d,
            headers: d.headers.map((h, j) =>
              j === i ? { ...h, ...patch } : h,
            ),
          }
        : d,
    );
  const agentName = (id: string) =>
    personas.find((p) => p.config.id === id)?.config.name || id;

  return (
    <section className="phone-settings mcp-settings" aria-label="Tools (MCP)">
      <div className="section-heading">
        <h3>Tools (MCP)</h3>
        <span className="pill">
          {servers ? `${servers.length} servers` : "Loading…"}
        </span>
      </div>
      <p className="muted">
        Attach Model Context Protocol servers so agents can search the web, read
        pages and more. Header values can reference a secret as{" "}
        <code>{"${NAME}"}</code>; stored values are never shown again.
      </p>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {notice && (
        <p className="settings-saved" role="status">
          {notice}
        </p>
      )}
      {servers && servers.length === 0 && (
        <p className="quiet">No tool servers yet.</p>
      )}
      <ul className="mcp-list" aria-label="MCP servers">
        {(servers || []).map((s) => (
          <li className="phone-persona mcp-server" key={s.id}>
            <h4>
              {s.name} <small>{s.id}</small>
            </h4>
            <p>
              <span className={`pill mcp-state mcp-${s.status.state}`}>
                {stateLabel(s.status)}
              </span>
              {s.status.transport && (
                <span className="quiet">
                  {" "}
                  {s.status.transport === "sse"
                    ? "Legacy SSE"
                    : "Streamable HTTP"}
                </span>
              )}
            </p>
            {!s.unsupported && <p className="quiet mcp-url">{s.url}</p>}
            <p className="quiet">
              {s.agents.includes("*")
                ? "Available to all agents"
                : s.agents.length
                  ? `Available to ${s.agents.map(agentName).join(", ")}`
                  : "Not assigned to any agent"}
            </p>
            {(s.status.error || problems[s.id]) && (
              <p className="error" role="alert">
                {problems[s.id] || s.status.error}
              </p>
            )}
            <div className="voice-actions">
              <button
                type="button"
                disabled={!!busy || !!s.unsupported}
                onClick={() => void test(s)}
              >
                {busy === s.id ? "Working…" : "Test connection"}
                <span className="sr-only"> {s.name}</span>
              </button>
              <button
                type="button"
                disabled={!!busy || !!s.unsupported || !s.enabled}
                onClick={() => void showTools(s)}
              >
                {tools[s.id] ? "Hide tools" : "Show tools"}
                <span className="sr-only"> {s.name}</span>
              </button>
              <button
                type="button"
                disabled={!!busy || !!s.unsupported}
                onClick={() => setDraft(toDraft(s))}
              >
                Edit<span className="sr-only"> {s.name}</span>
              </button>
              <button
                type="button"
                disabled={!!busy}
                onClick={() => void remove(s)}
              >
                Remove<span className="sr-only"> {s.name}</span>
              </button>
            </div>
            {tools[s.id] && (
              <ul className="mcp-tools" aria-label={`Tools of ${s.name}`}>
                {tools[s.id].length === 0 && (
                  <li className="quiet">This server offers no tools.</li>
                )}
                {tools[s.id].map((t) => (
                  <li key={t.name}>
                    <code>{t.name}</code>
                    <span className="quiet"> {t.description}</span>
                  </li>
                ))}
              </ul>
            )}
            {s.unsupported && <p className="quiet">{s.unsupported}</p>}
          </li>
        ))}
      </ul>
      {!draft && (
        <button
          type="button"
          className="primary"
          onClick={() => setDraft(blank())}
        >
          Add server
        </button>
      )}
      {draft && (
        <form
          className="phone-settings-form mcp-form"
          aria-label={draft.isNew ? "Add MCP server" : "Edit MCP server"}
          onSubmit={save}
        >
          <fieldset disabled={busy === "save"}>
            <h4>{draft.isNew ? "Add server" : `Edit ${draft.name}`}</h4>
            {draft.isNew && (
              <label>
                Server id
                <input
                  type="text"
                  value={draft.id}
                  maxLength={40}
                  placeholder="web-search"
                  pattern="[a-z0-9]+(-[a-z0-9]+)*"
                  onChange={(e) => set({ id: e.target.value })}
                  required
                />
              </label>
            )}
            <label>
              Name
              <input
                type="text"
                value={draft.name}
                maxLength={80}
                onChange={(e) => set({ name: e.target.value })}
                required
              />
            </label>
            <label>
              URL
              <input
                type="url"
                value={draft.url}
                placeholder="https://example.com/mcp"
                onChange={(e) => set({ url: e.target.value })}
                required
              />
            </label>
            <label>
              Transport
              <select
                value={draft.transport}
                onChange={(e) => set({ transport: e.target.value })}
              >
                <option value="auto">Automatic</option>
                <option value="http">Streamable HTTP</option>
                <option value="sse">Legacy SSE</option>
              </select>
            </label>
            <div className="mcp-headers" role="group" aria-label="Headers">
              <span className="mcp-label">Headers</span>
              {draft.headers.map((h, i) => (
                <div className="mcp-header-row" key={i}>
                  <input
                    type="text"
                    aria-label={`Header ${i + 1} name`}
                    value={h.name}
                    placeholder="Authorization"
                    onChange={(e) => setHeader(i, { name: e.target.value })}
                  />
                  <input
                    type="password"
                    autoComplete="off"
                    aria-label={`Header ${i + 1} value`}
                    value={h.value}
                    placeholder={"Bearer ${MCP_API_KEY}"}
                    onChange={(e) => setHeader(i, { value: e.target.value })}
                  />
                  <button
                    type="button"
                    onClick={() =>
                      set({ headers: draft.headers.filter((_, j) => j !== i) })
                    }
                    aria-label={`Remove header ${i + 1}`}
                  >
                    Remove
                  </button>
                </div>
              ))}
              <button
                type="button"
                onClick={() =>
                  set({ headers: [...draft.headers, { name: "", value: "" }] })
                }
              >
                Add header
              </button>
              <p className="quiet">
                {draft.headers.some((h) => h.value === MASK)
                  ? "A value shown as dots is stored; leave it to keep it. "
                  : ""}
                Use <code>{"${NAME}"}</code> to read a secret from the server
                environment.
              </p>
            </div>
            <div
              className="mcp-agents"
              role="group"
              aria-label="Agents allowed"
            >
              <span className="mcp-label">Agents allowed</span>
              <label className="speech-toggle">
                <input
                  type="checkbox"
                  checked={draft.all}
                  onChange={(e) => set({ all: e.target.checked })}
                />{" "}
                All agents
              </label>
              {!draft.all &&
                personas.map((p) => (
                  <label className="speech-toggle" key={p.config.id}>
                    <input
                      type="checkbox"
                      checked={draft.agents.includes(p.config.id)}
                      onChange={(e) =>
                        set({
                          agents: e.target.checked
                            ? [...draft.agents, p.config.id]
                            : draft.agents.filter((a) => a !== p.config.id),
                        })
                      }
                    />{" "}
                    {p.config.name}
                  </label>
                ))}
            </div>
            <label>
              Only these tools (optional, comma separated)
              <input
                type="text"
                value={draft.allow}
                onChange={(e) => set({ allow: e.target.value })}
              />
            </label>
            <label>
              Never these tools (optional, comma separated)
              <input
                type="text"
                value={draft.deny}
                onChange={(e) => set({ deny: e.target.value })}
              />
            </label>
            <label className="speech-toggle">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(e) => set({ enabled: e.target.checked })}
              />{" "}
              Enabled
            </label>
            <div className="voice-actions">
              <button className="primary" type="submit">
                {busy === "save" ? "Saving…" : "Save server"}
              </button>
              <button type="button" onClick={() => setDraft(undefined)}>
                Cancel
              </button>
            </div>
          </fieldset>
        </form>
      )}
      <button type="button" disabled={!!busy} onClick={() => void load()}>
        Reload servers
      </button>
    </section>
  );
}
