// Self-contained: serves the built app (web/dist, run `npm run build` first) through page.route and mocks every API call.
import { test, expect, Page, Route } from "@playwright/test";
import { readFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { VoiceCues } from "../src/voice-cues";
import { hideVocalEvents } from "../src/vocal-events";

const ORIGIN = "http://voices.test";
const dist = fileURLToPath(new URL("../dist/", import.meta.url));
const types: Record<string, string> = {
  html: "text/html",
  js: "text/javascript",
  css: "text/css",
  svg: "image/svg+xml",
  wasm: "application/wasm",
  onnx: "application/octet-stream",
};
const json = (route: Route, data: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(data),
  });
const agents = [
  { id: "telecom", name: "Sara" },
  { id: "school", name: "Tom" },
].map((a) => ({
  config: {
    id: a.id,
    name: a.name,
    organization: a.id + " org",
    role: "r",
    industry: a.id,
    persona: {},
    memory: { namespace: "n" },
    skills: [],
    knowledge: [],
    branding: {},
  },
  users: [{ id: "u1", name: "U One", description: "" }],
  skills: {},
  llm: "x",
  memory: "x",
}));
const baseSnapshot = () => ({
  revision: 3,
  voices: [
    {
      id: "ref-telecom",
      name: "Sara (original)",
      description: "",
      builtin: true,
    },
    {
      id: "ref-school",
      name: "Tom (original)",
      description: "",
      builtin: true,
    },
    {
      id: "british-gent",
      name: "British gent",
      description: "A deep, slow, older British man.",
      builtin: false,
    },
  ],
  personas: {
    telecom: { voice_id: "ref-telecom", direction: "Warm" },
    school: { voice_id: "ref-school", direction: "" },
  },
  cues: {
    telecom: { state: "ready", done: 0, total: 19, error: "" },
    school: { state: "ready", done: 0, total: 19, error: "" },
  },
});
type Snapshot = ReturnType<typeof baseSnapshot>;

async function setup(
  page: Page,
  state: {
    snapshot: Snapshot;
    posts: any[];
    previews: any[];
    cuePosts: string[];
    gets: { n: number };
    onGet?: () => void;
    previewStatus?: number;
  },
) {
  await page.addInitScript(() => {
    const w = window as any;
    w.__played = [];
    w.__revoked = [];
    w.__created = [];
    HTMLMediaElement.prototype.play = function () {
      w.__played.push(this.src);
      return Promise.resolve();
    };
    HTMLMediaElement.prototype.pause = function () {};
    const revoke = URL.revokeObjectURL.bind(URL);
    URL.revokeObjectURL = (u: string) => {
      w.__revoked.push(u);
      revoke(u);
    };
    const create = URL.createObjectURL.bind(URL);
    URL.createObjectURL = (b: Blob) => {
      const u = create(b);
      w.__created.push(u);
      return u;
    };
  });
  await page.route(ORIGIN + "/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    if (path === "/api/settings/voices/preview") {
      state.previews.push(route.request().postDataJSON());
      await new Promise((r) => setTimeout(r, 150));
      if (state.previewStatus)
        return json(route, { error: "busy" }, state.previewStatus);
      return route.fulfill({
        status: 200,
        contentType: "audio/wav",
        body: Buffer.from("RIFF....WAVE"),
      });
    }
    if (path.startsWith("/api/settings/voices/cues/")) {
      state.cuePosts.push(path.split("/").pop()!);
      return json(route, state.snapshot, 202);
    }
    if (path === "/api/settings/voices") {
      if (method === "POST") {
        const b = route.request().postDataJSON();
        state.posts.push(b);
        state.snapshot = {
          ...state.snapshot,
          revision: b.revision + 1,
          personas: b.personas,
          voices: [
            ...state.snapshot.voices.filter((v) => v.builtin),
            ...b.voices.map((v: any) => ({ ...v, builtin: false })),
          ],
        };
        return json(route, state.snapshot);
      }
      state.gets.n++;
      state.onGet?.();
      return json(route, state.snapshot);
    }
    if (path === "/api/agents") return json(route, agents);
    if (path === "/api/voice") return json(route, { enabled: false });
    if (path.startsWith("/api/"))
      return json(route, { error: "not mocked" }, 404);
    const file =
      path === "/" || path === "/settings" ? "index.html" : path.slice(1);
    if (!existsSync(dist + file))
      return route.fulfill({ status: 404, body: "" });
    return route.fulfill({
      status: 200,
      contentType: types[file.split(".").pop()!] || "application/octet-stream",
      body: readFileSync(dist + file),
    });
  });
}
const fresh = () => ({
  snapshot: baseSnapshot(),
  posts: [] as any[],
  previews: [] as any[],
  cuePosts: [] as string[],
  gets: { n: 0 },
});
async function open(
  page: Page,
  state: ReturnType<typeof fresh> & Record<string, any>,
) {
  await setup(page, state);
  await page.goto(ORIGIN + "/settings");
  const panel = page.getByRole("region", {
    name: "Voice settings",
    exact: true,
  });
  await expect(
    panel.getByRole("button", { name: "Add voice", exact: true }),
  ).toBeVisible();
  return panel;
}

test.describe("voice settings UI (mocked API)", () => {
  test.skip(
    !existsSync(dist + "index.html"),
    "build the app first: npm run build",
  );

  test("add voice saves immediately with custom voices only, plus revision and personas", async ({
    page,
  }) => {
    const state = fresh();
    const panel = await open(page, state);
    await panel.getByLabel("New voice name").fill("Calm Narrator!");
    await panel
      .getByLabel("New voice description")
      .fill("A calm, soft-spoken narrator.");
    await panel.getByRole("button", { name: "Add voice", exact: true }).click();
    await expect(
      panel.getByRole("status").filter({ hasText: "added" }),
    ).toBeVisible();
    expect(state.posts).toHaveLength(1);
    expect(state.posts[0]).toEqual({
      revision: 3,
      voices: [
        {
          id: "british-gent",
          name: "British gent",
          description: "A deep, slow, older British man.",
        },
        {
          id: "calm-narrator",
          name: "Calm Narrator!",
          description: "A calm, soft-spoken narrator.",
        },
      ],
      personas: state.snapshot.personas,
    });
    await expect(panel.getByLabel("Name of voice calm-narrator")).toHaveValue(
      "Calm Narrator!",
    );
    await expect(panel.getByLabel("New voice name")).toHaveValue("");
  });

  test("changing a persona voice and saving posts the whole object with the revision", async ({
    page,
  }) => {
    const state = fresh();
    const panel = await open(page, state);
    const save = panel.getByRole("button", { name: "Save voice settings" });
    await expect(save).toBeDisabled();
    await panel
      .getByRole("combobox", { name: "Voice for Sara" })
      .selectOption("british-gent");
    await panel.getByLabel("Delivery direction for Sara").fill("Slow");
    await save.click();
    await expect(panel.getByText("Voice settings saved")).toBeVisible();
    expect(state.posts[0].revision).toBe(3);
    expect(state.posts[0].personas.telecom).toEqual({
      voice_id: "british-gent",
      direction: "Slow",
    });
    expect(state.posts[0].personas.school).toEqual({
      voice_id: "ref-school",
      direction: "",
    });
    expect(
      state.posts[0].voices.every(
        (v: any) => !("builtin" in v) && !v.id.startsWith("ref-"),
      ),
    ).toBe(true);
  });

  test("server errors are shown and stale state can be reloaded", async ({
    page,
  }) => {
    const state = fresh();
    const panel = await open(page, state);
    await page.route(ORIGIN + "/api/settings/voices", (r) =>
      r.request().method() === "POST"
        ? json(r, { error: "voice british-gent is still used by Sara" }, 400)
        : r.fallback(),
    );
    await panel.getByRole("button", { name: "Delete British gent" }).click();
    await panel.getByRole("button", { name: "Save voice settings" }).click();
    await expect(panel.getByRole("alert")).toContainText("still used by Sara");
  });

  test("Generate cues posts for the persona and polling updates the progress every 3s", async ({
    page,
  }) => {
    const state = fresh();
    state.snapshot.personas.telecom.voice_id = "british-gent";
    state.snapshot.cues.telecom = {
      state: "queued",
      done: 0,
      total: 19,
      error: "",
    };
    const panel = await open(page, state);
    const status = panel.getByRole("status", { name: "Cue status for Sara" });
    await expect(status).toHaveText("Queued");
    await expect(
      panel.getByRole("button", { name: "Generate cues for Sara" }),
    ).toBeDisabled();
    await expect(
      panel.getByRole("button", { name: "Generate cues for Tom" }),
    ).toBeEnabled();
    state.snapshot.cues.telecom = {
      state: "rendering",
      done: 7,
      total: 19,
      error: "",
    };
    await expect(status).toHaveText("Rendering 7/19", { timeout: 6000 });
    const polled = state.gets.n;
    state.snapshot.cues.telecom = {
      state: "ready",
      done: 19,
      total: 19,
      error: "",
    };
    await expect(status).toHaveText("Ready", { timeout: 6000 });
    await page.waitForTimeout(3500);
    expect(state.gets.n).toBeLessThanOrEqual(polled + 2); // polling stops once nothing is active
    state.snapshot.cues.telecom = {
      state: "failed",
      done: 3,
      total: 19,
      error: "tts offline",
    };
    await panel.getByRole("button", { name: "Reload saved voices" }).click();
    await expect(status).toHaveText("Failed: tts offline");
    state.snapshot.cues.telecom = {
      state: "queued",
      done: 0,
      total: 19,
      error: "",
    };
    await panel.getByRole("button", { name: "Generate cues for Sara" }).click();
    expect(state.cuePosts).toEqual(["telecom"]);
    await expect(status).toHaveText("Queued");
  });

  test("Preview buttons send the right body, play a blob URL, revoke old URLs and disable while loading", async ({
    page,
  }) => {
    const state = fresh();
    const panel = await open(page, state);
    const personaPreview = panel.getByRole("button", {
      name: "Preview voice for Sara",
    });
    await personaPreview.click();
    await expect(personaPreview).toHaveText("Loading…");
    await expect(
      panel.getByRole("button", { name: "Preview Sara (original)" }),
    ).toBeDisabled();
    await expect(personaPreview).toHaveText("Preview");
    expect(state.previews[0]).toEqual({
      voice_id: "ref-telecom",
      direction: "Warm",
    });
    await panel.getByRole("button", { name: "Preview British gent" }).click();
    await expect(
      panel.getByRole("button", { name: "Preview British gent" }),
    ).toHaveText("Preview");
    expect(state.previews[1]).toEqual({ voice_id: "british-gent" });
    await panel
      .getByLabel("Description of voice british-gent")
      .fill("Edited description");
    await panel.getByRole("button", { name: "Preview British gent" }).click();
    await expect(
      panel.getByRole("button", { name: "Preview British gent" }),
    ).toHaveText("Preview");
    expect(state.previews[2]).toEqual({ description: "Edited description" });
    const addPreview = panel.getByRole("button", { name: "Preview" }).first();
    await expect(
      panel.getByRole("button", { name: /^Preview new voice$/ }),
    ).toBeDisabled(); // needs a description
    await panel.getByLabel("New voice description").fill("Bright and quick");
    await panel.getByRole("button", { name: /^Preview new voice$/ }).click();
    await expect(
      panel.getByRole("button", { name: /^Preview new voice$/ }),
    ).toBeEnabled();
    expect(state.previews[3]).toEqual({ description: "Bright and quick" });
    void addPreview;
    const info = await page.evaluate(() => {
      const w = window as any;
      return { played: w.__played, revoked: w.__revoked, created: w.__created };
    });
    expect(info.played).toHaveLength(4);
    expect(info.played.every((u: string) => u.startsWith("blob:"))).toBe(true);
    expect(info.revoked).toEqual(info.created.slice(0, 3)); // every replaced URL revoked; the latest stays playable
  });

  test("preview failure is reported", async ({ page }) => {
    const state: any = fresh();
    state.previewStatus = 409;
    const panel = await open(page, state);
    await panel.getByRole("button", { name: "Preview voice for Sara" }).click();
    await expect(panel.getByRole("alert")).toContainText("busy");
  });

  test("usable at phone width without horizontal overflow", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    const state = fresh();
    const panel = await open(page, state);
    await panel.scrollIntoViewIfNeeded();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    const box = await panel
      .getByRole("combobox", { name: "Voice for Sara" })
      .boundingBox();
    expect(box!.width).toBeGreaterThan(200);
    expect(box!.x + box!.width).toBeLessThanOrEqual(375);
  });
});

test("vocal event helper hides only the four allowlisted events", () => {
  expect(hideVocalEvents("Sure (laugh) I can help.")).toBe("Sure I can help.");
  expect(hideVocalEvents("(Sigh) Okay. (CLEARS THROAT) Next (cough)")).toBe(
    "Okay. Next ",
  );
  expect(hideVocalEvents("Done (smile) and (maybe) ok")).toBe(
    "Done (smile) and (maybe) ok",
  );
  expect(hideVocalEvents("plain  text  untouched")).toBe(
    "plain  text  untouched",
  );
  expect(hideVocalEvents("(laugh)")).toBe("");
});

test("VoiceCues treats a 404 manifest as no cues and reloads when reference_sha256 changes", async () => {
  const original = globalThis.fetch;
  const urls: string[] = [];
  const errors: unknown[][] = [];
  const origError = console.error;
  console.error = (...a) => {
    errors.push(a);
  };
  let mode: "404" | "a" | "b" = "404";
  let decoded = 0;
  const started: string[] = [];
  const ctx = {
    decodeAudioData: async () => {
      decoded++;
      return { duration: 0.2 };
    },
    createBufferSource: () => ({
      buffer: null,
      connect() {},
      disconnect() {},
      start() {
        started.push("x");
      },
      stop() {},
      onended: null,
    }),
  } as unknown as AudioContext;
  globalThis.fetch = async (u: any) => {
    const url = String(u);
    urls.push(url);
    if (url.endsWith("voice-cues"))
      return mode === "404"
        ? new Response(JSON.stringify({ error: "not ready" }), { status: 404 })
        : new Response(
            JSON.stringify({
              reference_sha256: mode,
              cues: [
                {
                  id: "w1",
                  category: "waiting",
                  text: "One moment.",
                  duration_ms: 200,
                },
              ],
            }),
          );
    return new Response(new Uint8Array([1, 0]));
  };
  const cues = new VoiceCues(
    ctx,
    {} as AudioNode,
    () => true,
    () => {},
  );
  try {
    await cues.load("telecom");
    expect(urls).toEqual(["/api/agents/telecom/voice-cues"]);
    cues.play("waiting");
    expect(started).toHaveLength(0);
    mode = "a";
    await cues.load("telecom");
    expect(decoded).toBe(1);
    expect(urls.at(-1)).toBe("/api/agents/telecom/voice-cues/w1?v=a");
    await cues.load("telecom");
    expect(decoded).toBe(1); // unchanged hash: no re-download
    mode = "b";
    await cues.load("telecom");
    expect(decoded).toBe(2);
    expect(urls.at(-1)).toBe("/api/agents/telecom/voice-cues/w1?v=b");
    mode = "404";
    await cues.load("telecom");
    cues.play("waiting");
    expect(started).toHaveLength(0); // muted while the new voice renders
    expect(errors).toEqual([]);
  } finally {
    console.error = origError;
    globalThis.fetch = original;
    cues.close();
  }
});

test("VoiceCues keys clips by agent and the full reference_sha256 string", async () => {
  const original = globalThis.fetch;
  let sha = "key-1";
  let decoded = 0;
  const ctx = {
    decodeAudioData: async () => {
      decoded++;
      return { duration: 0.2 };
    },
  } as unknown as AudioContext;
  globalThis.fetch = async (u: any) => {
    const url = String(u);
    if (url.endsWith("voice-cues"))
      return new Response(
        JSON.stringify({
          reference_sha256: sha,
          cues: [
            {
              id: "w1",
              category: "waiting",
              text: "One moment.",
              duration_ms: 200,
            },
          ],
        }),
      );
    return new Response(new Uint8Array([1, 0]));
  };
  const cues = new VoiceCues(
    ctx,
    {} as AudioNode,
    () => true,
    () => {},
  );
  try {
    await cues.load("telecom");
    expect(decoded).toBe(1);
    await cues.load("school");
    expect(decoded).toBe(2); // same sha, other agent: not reused
    await cues.load("school");
    expect(decoded).toBe(2);
    sha = "key-2";
    await cues.load("school");
    expect(decoded).toBe(3); // forced re-render changes the generation suffix
  } finally {
    globalThis.fetch = original;
    cues.close();
  }
});
