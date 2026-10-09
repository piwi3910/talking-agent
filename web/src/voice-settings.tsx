import React, { useCallback, useEffect, useRef, useState } from "react";
import { VoiceCloneFlow } from "./voice-clone-flow";

type Persona = { config: { id: string; name: string; organization: string } };
type Voice = {
  id: string;
  name: string;
  description: string;
  builtin: boolean;
  // Older servers omit the kind: anything not builtin is a designed voice.
  // "preset" is a built-in speaker of the Qwen3 TTS model.
  kind?: "builtin" | "design" | "clone" | "preset";
  // A designed voice whose sample could not be rendered yet (Qwen3 only).
  unavailable?: boolean;
};
type Assignment = { voice_id: string; direction: string; events: boolean };
type CueState = {
  state: "original" | "ready" | "rendering" | "queued" | "failed";
  done: number;
  total: number;
  error?: string;
};
export type Snapshot = {
  revision: number;
  // Older servers omit these: they mean Breeze, which supports vocal events.
  provider?: string;
  capabilities?: { vocal_events?: boolean };
  voices: Voice[];
  personas: Record<string, Assignment>;
  cues: Record<string, CueState>;
};
// A cloned voice has no description: its reference audio lives on the server.
type CustomVoice = {
  id: string;
  name: string;
  description: string;
  kind?: "clone";
};

async function parse(response: Response) {
  try {
    return await response.json();
  } catch {
    return {};
  }
}
async function request(
  path = "",
  init?: { body?: unknown; method?: string },
): Promise<Snapshot> {
  const response = await fetch(
    "/api/settings/voices" + path,
    init
      ? {
          method: init.method || "POST",
          ...(init.body !== undefined
            ? {
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(init.body),
              }
            : {}),
        }
      : undefined,
  );
  const data = await parse(response);
  if (!response.ok)
    throw new Error(data.error || "Could not load voice settings");
  return data;
}
export function slugify(name: string, taken: Set<string>): string {
  let base = name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40)
    .replace(/-+$/, "");
  if (!base || base.startsWith("ref-")) base = "voice-" + base;
  base = base.replace(/-+$/, "").slice(0, 40);
  let id = base;
  for (let n = 2; taken.has(id); n++) {
    const suffix = "-" + n;
    id = base.slice(0, 40 - suffix.length).replace(/-+$/, "") + suffix;
  }
  return id;
}
export function cueLabel(c?: CueState): string {
  if (!c) return "Unknown";
  switch (c.state) {
    case "original":
      return "Original cues";
    case "ready":
      return "Ready";
    case "queued":
      return "Queued";
    case "rendering":
      return `Rendering ${c.done}/${c.total}`;
    case "failed":
      return c.error ? `Failed: ${c.error}` : "Failed";
  }
}
const customOf = (voices: Voice[]): CustomVoice[] =>
  voices
    .filter((v) => !v.builtin && v.kind !== "builtin")
    .map((v): CustomVoice =>
      v.kind === "clone"
        ? { id: v.id, name: v.name, description: "", kind: "clone" }
        : { id: v.id, name: v.name, description: v.description },
    );
// The save body: designed voices carry a description, clones only id, name and kind.
const payload = (v: CustomVoice) =>
  v.kind === "clone"
    ? { id: v.id, name: v.name, kind: "clone" }
    : { id: v.id, name: v.name, description: v.description };

// One preview at a time. Each result is a blob URL that is revoked when replaced or on unmount.
function usePreview(setError: (m: string) => void) {
  const [busy, setBusy] = useState("");
  const url = useRef("");
  const audio = useRef<HTMLAudioElement | null>(null);
  const abort = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  const release = useCallback(() => {
    audio.current?.pause();
    audio.current = null;
    if (url.current) URL.revokeObjectURL(url.current);
    url.current = "";
  }, []);
  // On unmount: cancel an in-flight preview request and stop any playback.
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      abort.current?.abort();
      release();
    };
  }, [release]);
  async function preview(key: string, body: Record<string, string>) {
    if (busy) return;
    const controller = new AbortController();
    abort.current = controller;
    setBusy(key);
    setError("");
    try {
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
      release();
      url.current = URL.createObjectURL(blob);
      audio.current = new Audio(url.current);
      await audio.current.play();
    } catch (e) {
      if (mounted.current) setError((e as Error).message);
    } finally {
      if (abort.current === controller) abort.current = null;
      if (mounted.current) setBusy("");
    }
  }
  return { busy, preview };
}

export function VoiceSettings({ personas }: { personas: Persona[] }) {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [customs, setCustoms] = useState<CustomVoice[]>([]);
  const [assign, setAssign] = useState<Record<string, Assignment>>({});
  const [cues, setCues] = useState<Record<string, CueState>>({});
  const [newName, setNewName] = useState("");
  const [newDescription, setNewDescription] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(true);
  const [cloning, setCloning] = useState(false);
  const { busy, preview } = usePreview(setError);

  function adopt(data: Snapshot) {
    setSnapshot(data);
    setCustoms(customOf(data.voices));
    setAssign(data.personas);
    setCues(data.cues || {});
  }
  async function load() {
    setLoading(true);
    setError("");
    try {
      adopt(await request());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);

  const active = Object.values(cues).some(
    (c) => c.state === "queued" || c.state === "rendering",
  );
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => {
      request()
        .then((data) => setCues(data.cues || {}))
        .catch(() => {});
    }, 3000);
    return () => clearInterval(timer);
  }, [active]);

  const builtins = snapshot?.voices.filter((v) => v.builtin) || [];
  const eventsSupported = snapshot?.capabilities?.vocal_events !== false;
  const allVoices = [
    ...builtins,
    ...customs.map((c) => ({ ...c, builtin: false })),
  ];
  function body(nextCustoms = customs) {
    return {
      revision: snapshot!.revision,
      voices: nextCustoms.map(payload),
      personas: assign,
    };
  }
  async function persist(
    next: ReturnType<typeof body>,
    message: string,
  ): Promise<boolean> {
    setSaving(true);
    setError("");
    setNotice("");
    try {
      adopt(await request("", { body: next }));
      setNotice(message);
      return true;
    } catch (e) {
      setError((e as Error).message);
      return false;
    } finally {
      setSaving(false);
    }
  }
  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (snapshot)
      await persist(
        body(),
        "Voice settings saved. New phrases use these voices; cues re-render in the background.",
      );
  }
  async function add(e: React.FormEvent) {
    e.preventDefault();
    if (!snapshot) return;
    const name = newName.trim(),
      description = newDescription.trim();
    if (!name || !description) {
      setError("Give the new voice a name and a description.");
      return;
    }
    const id = slugify(name, new Set(allVoices.map((v) => v.id)));
    if (
      await persist(
        body([...customs, { id, name, description }]),
        `Voice “${name}” added.`,
      )
    ) {
      setNewName("");
      setNewDescription("");
    }
  }
  function editVoice(id: string, patch: Partial<CustomVoice>) {
    setNotice("");
    setCustoms((old) => old.map((v) => (v.id === id ? { ...v, ...patch } : v)));
  }
  function removeVoice(id: string) {
    setNotice("");
    setCustoms((old) => old.filter((v) => v.id !== id));
  }
  function change(id: string, patch: Partial<Assignment>) {
    setNotice("");
    setAssign((old) => ({ ...old, [id]: { ...old[id], ...patch } }));
  }
  async function generate(id: string) {
    setError("");
    try {
      const data = await request(`/cues/${encodeURIComponent(id)}`, {
        body: undefined,
      });
      setCues(data.cues || {});
    } catch (e) {
      setError((e as Error).message);
    }
  }
  function previewSaved(v: CustomVoice | Voice) {
    const saved = snapshot?.voices.find((s) => s.id === v.id);
    void preview(
      "voice:" + v.id,
      saved && saved.description === v.description
        ? { voice_id: v.id }
        : { description: v.description },
    );
  }
  const dirty =
    snapshot &&
    (JSON.stringify(customs) !== JSON.stringify(customOf(snapshot.voices)) ||
      JSON.stringify(assign) !== JSON.stringify(snapshot.personas));
  // A cloned voice is stored with the current settings and replaces the draft
  // with the saved state, so it starts only from a clean draft.
  function cloned(data: Snapshot, voiceName: string) {
    adopt(data);
    setCloning(false);
    setError("");
    setNotice(
      `Voice “${voiceName}” cloned. Assign it to a persona and save; its cues render in the background.`,
    );
  }

  return (
    <section
      className="phone-settings voice-settings"
      aria-label="Voice settings"
    >
      <div className="section-heading">
        <h3>Voices</h3>
        <span className="pill">{allVoices.length} voices</span>
      </div>
      <p className="muted">
        Pick the voice each persona speaks with, or describe a new one. Changes
        apply from the next spoken phrase; filler cues are re-rendered in the
        background and stay silent until ready.
      </p>
      {loading && <p role="status">Loading voice settings…</p>}
      {saving && snapshot?.provider === "qwen3" && (
        <p className="quiet" role="status">
          Saving. A new or changed designed voice renders its voice sample
          first, which can take up to 30 seconds.
        </p>
      )}
      {error && (
        <p className="error voice-error" role="alert">
          {error}
        </p>
      )}
      {notice && (
        <p className="settings-saved" role="status">
          {notice}
        </p>
      )}
      {snapshot && (
        <>
          <form className="voice-add" aria-label="Add voice" onSubmit={add}>
            <fieldset disabled={saving}>
              <h4>Add voice</h4>
              <label>
                New voice name
                <input
                  type="text"
                  value={newName}
                  maxLength={60}
                  placeholder="British gent"
                  onChange={(e) => setNewName(e.target.value)}
                />
              </label>
              <label>
                New voice description
                <textarea
                  rows={3}
                  value={newDescription}
                  maxLength={500}
                  placeholder="A deep, slow, older British man with a warm tone"
                  onChange={(e) => setNewDescription(e.target.value)}
                />
              </label>
              <div className="voice-actions">
                <button
                  type="button"
                  disabled={!!busy || !newDescription.trim()}
                  onClick={() =>
                    void preview("new", { description: newDescription.trim() })
                  }
                >
                  {busy === "new" ? "Loading…" : "Preview"}
                  <span className="sr-only"> new voice</span>
                </button>
                <button
                  className="primary"
                  type="submit"
                  disabled={!newName.trim() || !newDescription.trim()}
                >
                  Add voice
                </button>
              </div>
            </fieldset>
          </form>
          <div className="voice-clone-launch">
            <button
              type="button"
              disabled={saving || cloning || !!dirty}
              onClick={() => {
                setNotice("");
                setCloning(true);
              }}
            >
              Clone a voice
            </button>
            {dirty && !cloning && (
              <p className="quiet">
                Save or reload your voice changes before cloning a voice.
              </p>
            )}
          </div>
          {cloning && (
            <VoiceCloneFlow
              revision={snapshot.revision}
              takenIds={allVoices.map((v) => v.id)}
              makeId={slugify}
              onSaved={cloned}
              onClose={() => setCloning(false)}
            />
          )}
          <form className="voice-settings-form" onSubmit={save}>
            <fieldset disabled={saving}>
              <h4>Voice list</h4>
              <ul className="voice-list" aria-label="Voice list">
                {builtins.map((v) => (
                  <li className="voice-item" key={v.id}>
                    <div>
                      <strong>{v.name}</strong>{" "}
                      <span className="pill">
                        {v.kind === "preset" ? "Preset" : "Original"}
                      </span>
                    </div>
                    <div className="voice-actions">
                      <button
                        type="button"
                        disabled={!!busy}
                        aria-label={`Preview ${v.name}`}
                        onClick={() => previewSaved(v)}
                      >
                        {busy === "voice:" + v.id ? "Loading…" : "Preview"}
                      </button>
                    </div>
                  </li>
                ))}
                {customs.map((v) => (
                  <li className="voice-item" key={v.id}>
                    <div>
                      <span className="pill">
                        {v.kind === "clone" ? "Cloned" : "Designed"}
                      </span>
                    </div>
                    <label>
                      Name of voice {v.id}
                      <input
                        type="text"
                        value={v.name}
                        maxLength={60}
                        onChange={(e) =>
                          editVoice(v.id, { name: e.target.value })
                        }
                      />
                    </label>
                    {v.kind !== "clone" && (
                      <label>
                        Description of voice {v.id}
                        <textarea
                          rows={3}
                          value={v.description}
                          maxLength={500}
                          onChange={(e) =>
                            editVoice(v.id, { description: e.target.value })
                          }
                        />
                      </label>
                    )}
                    <div className="voice-actions">
                      <button
                        type="button"
                        disabled={
                          !!busy ||
                          (v.kind !== "clone" && !v.description.trim())
                        }
                        aria-label={`Preview ${v.name}`}
                        onClick={() => previewSaved(v)}
                      >
                        {busy === "voice:" + v.id ? "Loading…" : "Preview"}
                      </button>
                      <button
                        type="button"
                        aria-label={`Delete ${v.name}`}
                        onClick={() => removeVoice(v.id)}
                      >
                        Delete
                      </button>
                    </div>
                  </li>
                ))}
              </ul>
              <h4>Persona voices</h4>
              {personas.map((a) => {
                const id = a.config.id,
                  p = assign[id];
                if (!p) return null;
                const cue = cues[id];
                const name = a.config.name;
                return (
                  <div
                    className="phone-persona voice-persona"
                    key={id}
                    role="group"
                    aria-label={`${name} voice settings`}
                  >
                    <h4>
                      {name} <small>{a.config.organization}</small>
                    </h4>
                    <label>
                      Voice for {name}
                      <select
                        value={p.voice_id}
                        onChange={(e) =>
                          change(id, { voice_id: e.target.value })
                        }
                      >
                        {allVoices.map((v) => (
                          <option key={v.id} value={v.id}>
                            {v.name}
                          </option>
                        ))}
                      </select>
                    </label>
                    <label>
                      Delivery direction for {name}
                      <textarea
                        rows={2}
                        value={p.direction}
                        maxLength={500}
                        placeholder="Warm, calm, unhurried"
                        onChange={(e) =>
                          change(id, { direction: e.target.value })
                        }
                      />
                    </label>
                    <label className="speech-toggle">
                      <input
                        type="checkbox"
                        checked={p.events && eventsSupported}
                        disabled={!eventsSupported}
                        onChange={(e) =>
                          change(id, { events: e.target.checked })
                        }
                      />{" "}
                      Natural vocal events
                      <span className="sr-only"> for {name}</span>
                    </label>
                    <p className="quiet">
                      {eventsSupported
                        ? `Lets ${name} occasionally laugh, sigh, cough or clear their throat. Hidden in chat text.`
                        : "Not supported by the current TTS model."}
                    </p>
                    <div className="voice-actions">
                      <button
                        type="button"
                        disabled={!!busy}
                        aria-label={`Preview voice for ${name}`}
                        onClick={() =>
                          void preview("persona:" + id, {
                            voice_id: p.voice_id,
                            ...(p.direction.trim()
                              ? { direction: p.direction.trim() }
                              : {}),
                          })
                        }
                      >
                        {busy === "persona:" + id ? "Loading…" : "Preview"}
                      </button>
                      <button
                        type="button"
                        disabled={
                          !cue ||
                          cue.state === "original" ||
                          cue.state === "queued" ||
                          cue.state === "rendering"
                        }
                        aria-label={`Generate cues for ${name}`}
                        onClick={() => void generate(id)}
                      >
                        Generate cues
                      </button>
                    </div>
                    <p
                      className={`voice-cue-status ${cue?.state || ""}`}
                      role="status"
                      aria-label={`Cue status for ${name}`}
                    >
                      {cueLabel(cue)}
                    </p>
                    {snapshot.personas[id] &&
                      (snapshot.personas[id].voice_id !== p.voice_id ||
                        snapshot.personas[id].direction !== p.direction) && (
                        <p className="quiet">
                          Save to apply this voice. Cue status shows the saved
                          voice.
                        </p>
                      )}
                  </div>
                );
              })}
              <button className="primary" type="submit" disabled={!dirty}>
                {saving ? "Saving…" : "Save voice settings"}
              </button>
            </fieldset>
          </form>
        </>
      )}
      <button
        type="button"
        disabled={saving || loading}
        onClick={() => {
          setNotice("");
          void load();
        }}
      >
        Reload saved voices
      </button>
    </section>
  );
}
