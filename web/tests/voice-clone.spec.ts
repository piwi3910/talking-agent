// Self-contained: serves the built app (web/dist, run `npm run build` first) through page.route and mocks every API call.
// The microphone is stubbed with an oscillator stream, so no real audio device is needed.
import { test, expect, Page, Route } from "@playwright/test";
import { readFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  CONSENT_TEXT,
  MAX_SECONDS,
  SCRIPTS,
  encodeWav,
  markHeard,
  recordingToWav,
  resample,
  wordSimilarity,
} from "../src/voice-clone";

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
const clone = {
  id: "front-desk",
  name: "Front desk",
  description: "",
  builtin: false,
  kind: "clone",
};
const baseSnapshot = () => ({
  revision: 3,
  voices: [
    {
      id: "ref-telecom",
      name: "Sara (original)",
      description: "",
      builtin: true,
      kind: "builtin",
    },
    {
      id: "ref-school",
      name: "Tom (original)",
      description: "",
      builtin: true,
      kind: "builtin",
    },
    {
      id: "british-gent",
      name: "British gent",
      description: "A deep, slow, older British man.",
      builtin: false,
      kind: "design",
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
const heardText =
  "the quick brown fox jumps over the lazy dog near the quiet river bank please call zoe at nine forty five to book a table for six and bring the tin yellow folder with the printed maps";
const baseReport = () => ({
  duration_ms: 6300,
  rms_dbfs: -18.2,
  clipping_ratio: 0,
  checks: [
    {
      id: "duration",
      label: "Length",
      status: "pass",
      detail: "6.3 s of speech (4 to 25 s works best)",
    },
    {
      id: "level",
      label: "Volume",
      status: "warn",
      detail: "Rather quiet (-42.0 dBFS); move closer to the microphone",
    },
    {
      id: "clipping",
      label: "Clipping",
      status: "pass",
      detail: "0.00% of samples at full scale",
    },
    {
      id: "silence",
      label: "Silence",
      status: "pass",
      detail: "Trimmed 0.5 s of leading and 0.6 s of trailing silence",
    },
  ],
  transcript: heardText,
});
type Snapshot = ReturnType<typeof baseSnapshot>;
interface CloneBody {
  id: string;
  name: string;
  revision: number;
  [key: string]: unknown;
}
interface VoiceBody {
  id: string;
  name: string;
  description?: string;
  kind?: string;
}
interface SaveBody {
  revision: number;
  personas: Snapshot["personas"];
  voices: VoiceBody[];
}
interface TestWindow {
  __played: string[];
  __revoked: string[];
  __created: string[];
  __mics: number;
  __stopped: number;
  __constraints: unknown;
  __micDenied: boolean;
}
type State = {
  snapshot: Snapshot;
  report: ReturnType<typeof baseReport>;
  checks: { contentType: string; body: Buffer }[];
  clones: CloneBody[];
  previews: Record<string, string>[];
  posts: SaveBody[];
  cloneStatus?: number;
};
const fresh = (): State => ({
  snapshot: baseSnapshot(),
  report: baseReport(),
  checks: [],
  clones: [],
  previews: [],
  posts: [],
});

async function setup(page: Page, state: State) {
  await page.addInitScript(() => {
    const w = window as unknown as TestWindow;
    w.__played = [];
    w.__revoked = [];
    w.__created = [];
    w.__mics = 0;
    w.__stopped = 0;
    w.__constraints = null;
    w.__micDenied = false;
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
    // The app is served from an insecure test origin, where mediaDevices does not exist.
    const getUserMedia = async (constraints: unknown) => {
      w.__constraints = constraints;
      if (w.__micDenied)
        throw new DOMException("Permission denied", "NotAllowedError");
      w.__mics++;
      const ctx = new AudioContext();
      await ctx.resume();
      const osc = ctx.createOscillator();
      osc.frequency.value = 220;
      const gain = ctx.createGain();
      gain.gain.value = 0.4;
      const dest = ctx.createMediaStreamDestination();
      osc.connect(gain);
      gain.connect(dest);
      osc.start();
      dest.stream.getTracks().forEach((track) => {
        const stop = track.stop.bind(track);
        track.stop = () => {
          w.__stopped++;
          stop();
        };
      });
      return dest.stream;
    };
    Object.defineProperty(navigator, "mediaDevices", {
      value: { getUserMedia },
      configurable: true,
    });
  });
  await page.route(ORIGIN + "/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    if (path === "/api/settings/voices/clone/check") {
      state.checks.push({
        contentType: route.request().headers()["content-type"] || "",
        body: route.request().postDataBuffer() || Buffer.alloc(0),
      });
      return json(route, state.report);
    }
    if (path === "/api/settings/voices/clone") {
      const b = route.request().postDataJSON() as CloneBody;
      state.clones.push(b);
      if (state.cloneStatus)
        return json(
          route,
          { error: "voice settings changed" },
          state.cloneStatus,
        );
      state.snapshot = {
        ...state.snapshot,
        revision: b.revision + 1,
        voices: [
          ...state.snapshot.voices,
          { ...clone, id: b.id, name: b.name },
        ],
      };
      return json(route, state.snapshot);
    }
    if (path === "/api/settings/voices/preview") {
      state.previews.push(route.request().postDataJSON());
      return route.fulfill({
        status: 200,
        contentType: "audio/wav",
        body: Buffer.from("RIFF....WAVE"),
      });
    }
    if (path === "/api/settings/voices") {
      if (method === "POST") {
        const b = route.request().postDataJSON() as SaveBody;
        state.posts.push(b);
        state.snapshot = {
          ...state.snapshot,
          revision: b.revision + 1,
          personas: b.personas,
          voices: [
            ...state.snapshot.voices.filter((v) => v.builtin),
            ...b.voices.map((v) => ({
              description: "",
              kind: "design",
              ...v,
              builtin: false,
            })),
          ],
        };
      }
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
async function open(page: Page, state: State) {
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
const flowOf = (panel: ReturnType<Page["getByRole"]>) =>
  panel.getByRole("region", { name: "Clone a voice", exact: true });

async function start(
  panel: ReturnType<Page["getByRole"]>,
  name = "Front desk",
) {
  await panel
    .getByRole("button", { name: "Clone a voice", exact: true })
    .click();
  const flow = flowOf(panel);
  await expect(flow).toBeVisible();
  await flow.getByLabel("Name of the new voice").fill(name);
  await flow.getByRole("checkbox", { name: /my own voice/ }).check();
  await flow.getByRole("button", { name: "Continue", exact: true }).click();
  return flow;
}
// Records for about a second and a half through the stubbed microphone.
async function record(page: Page, flow: ReturnType<Page["getByRole"]>) {
  await flow.getByRole("button", { name: "Start recording" }).click();
  await expect(flow.locator(".clone-countdown")).toHaveText("3");
  const stop = flow.getByRole("button", { name: "Stop recording" });
  await expect(stop).toBeVisible({ timeout: 10000 });
  await expect(flow.locator(".clone-meter")).toBeVisible();
  await page.waitForTimeout(1500);
  await expect(flow.locator(".clone-timer")).toContainText("/ 0:25");
  await stop.click();
  await expect(flow.getByLabel("Your recording")).toBeVisible();
}

test.describe("voice cloning helpers", () => {
  test("encodes mono 16-bit PCM WAV and resamples", () => {
    const wav = new DataView(
      encodeWav(new Float32Array([0, 1, -1, 2, -2, 0.5]), 24000),
    );
    const ascii = (at: number, n: number) =>
      String.fromCharCode(
        ...Array.from({ length: n }, (_, i) => wav.getUint8(at + i)),
      );
    expect(ascii(0, 4)).toBe("RIFF");
    expect(ascii(8, 4)).toBe("WAVE");
    expect(wav.getUint16(20, true)).toBe(1); // PCM
    expect(wav.getUint16(22, true)).toBe(1); // mono
    expect(wav.getUint32(24, true)).toBe(24000);
    expect(wav.getUint16(34, true)).toBe(16);
    expect(wav.getUint32(40, true)).toBe(12);
    expect(wav.byteLength).toBe(44 + 12);
    const samples = [0, 1, 2, 3, 4, 5].map((i) =>
      wav.getInt16(44 + i * 2, true),
    );
    expect(samples).toEqual([0, 32767, -32768, 32767, -32768, 16384]);

    const flat = resample(new Float32Array(4800).fill(0.25), 48000, 16000);
    expect(flat.length).toBe(1600);
    expect(Array.from(flat).every((v) => Math.abs(v - 0.25) < 1e-6)).toBe(true);
  });

  test("recordings become 24 kHz WAV and are cut at the maximum length", () => {
    const two = recordingToWav([new Float32Array(48000 * 2).fill(0.1)], 48000);
    expect(two.seconds).toBeCloseTo(2, 3);
    expect(new DataView(two.wav).getUint32(24, true)).toBe(24000);
    expect(two.wav.byteLength).toBe(44 + 24000 * 2 * 2);
    const long = recordingToWav(
      [new Float32Array(16000 * 20), new Float32Array(16000 * 20)],
      16000,
    );
    expect(long.seconds).toBeCloseTo(MAX_SECONDS, 3);
  });

  test("word similarity and highlighting ignore case and punctuation", () => {
    const script = SCRIPTS[0].text;
    expect(wordSimilarity("The quick brown fox.", "the QUICK brown, fox")).toBe(
      1,
    );
    expect(wordSimilarity("one two three four", "one two three five")).toBe(
      0.75,
    );
    expect(wordSimilarity("one two", "")).toBe(0);
    expect(wordSimilarity(script, heardText)).toBeGreaterThan(0.9);
    expect(wordSimilarity(script, heardText)).toBeLessThan(1);
    const marks = markHeard("one two three", "one too three");
    expect(marks.map((m) => m.match)).toEqual([true, false, true]);
    expect(SCRIPTS).toHaveLength(3);
  });
});

test.describe("voice cloning UI (mocked API)", () => {
  test.skip(
    !existsSync(dist + "index.html"),
    "build the app first: npm run build",
  );
  test.use({
    launchOptions: { args: ["--autoplay-policy=no-user-gesture-required"] },
  });

  test("start step needs a name and the consent checkbox", async ({ page }) => {
    const panel = await open(page, fresh());
    await panel
      .getByRole("button", { name: "Clone a voice", exact: true })
      .click();
    const flow = flowOf(panel);
    const next = flow.getByRole("button", { name: "Continue", exact: true });
    await expect(
      flow.getByRole("heading", { name: "Clone a voice: Start" }),
    ).toBeVisible();
    await expect(flow.getByText(CONSENT_TEXT)).toBeVisible();
    await expect(next).toBeDisabled();
    await flow.getByLabel("Name of the new voice").fill("Front desk");
    await expect(next).toBeDisabled(); // no consent yet
    const consent = flow.getByRole("checkbox", { name: /my own voice/ });
    await consent.check();
    await expect(next).toBeEnabled();
    await consent.uncheck();
    await expect(next).toBeDisabled();
    await consent.check();
    await flow.getByLabel("Name of the new voice").fill("  ");
    await expect(next).toBeDisabled();
    await flow.getByLabel("Name of the new voice").fill("Front desk");
    await next.click();
    await expect(
      flow.getByRole("heading", { name: "Clone a voice: Read and record" }),
    ).toBeVisible();
    // Five steps are listed and the second is current.
    const steps = flow.getByRole("list", { name: "Cloning steps" });
    await expect(steps.getByRole("listitem")).toHaveCount(5);
    await expect(steps.locator("[aria-current=step]")).toContainText(
      "Read and record",
    );
  });

  test("full flow: record, check, preview and save posts the clone", async ({
    page,
  }) => {
    const state = fresh();
    const panel = await open(page, state);
    const createdBefore = await page.evaluate(
      () => (window as unknown as TestWindow).__created.length as number,
    );
    const flow = await start(panel);

    // Read step: script options and tips.
    await expect(flow.getByLabel("Script to read aloud")).toContainText(
      "quick brown fox",
    );
    await flow.getByRole("radio", { name: "School" }).check();
    await expect(flow.getByLabel("Script to read aloud")).toContainText(
      "welcome back to school",
    );
    await flow.getByRole("radio", { name: "Customer service" }).check();
    await expect(flow.getByLabel("Script to read aloud")).toContainText(
      "customer support",
    );
    await flow
      .getByRole("radio", { name: "Neutral, phonetically rich" })
      .check();
    await expect(
      flow.getByRole("list", { name: "Recording tips" }),
    ).toContainText("20 cm");

    await record(page, flow);
    await expect(flow.getByRole("status")).toContainText("Recorded");
    expect(
      await page.evaluate(() => (window as unknown as TestWindow).__mics),
    ).toBe(1);
    // The microphone is released as soon as the take is made.
    expect(
      await page.evaluate(() => (window as unknown as TestWindow).__stopped),
    ).toBeGreaterThan(0);

    // Re-recording replaces the take.
    await flow.getByRole("button", { name: "Record again" }).click();
    await expect(flow.getByLabel("Your recording")).toHaveCount(0);
    await record(page, flow);
    expect(
      await page.evaluate(() => (window as unknown as TestWindow).__mics),
    ).toBe(2);

    await flow.getByRole("button", { name: "Use this recording" }).click();
    await expect(
      flow.getByRole("heading", { name: "Clone a voice: Check" }),
    ).toBeVisible();
    await expect(
      flow.getByRole("list", { name: "Quality checks" }),
    ).toBeVisible();

    // The upload is a mono 16-bit 24 kHz WAV with real audio in it.
    expect(state.checks).toHaveLength(1);
    const upload = state.checks[0];
    expect(upload.contentType).toBe("audio/wav");
    expect(upload.body.toString("ascii", 0, 4)).toBe("RIFF");
    expect(upload.body.toString("ascii", 8, 12)).toBe("WAVE");
    expect(upload.body.readUInt16LE(22)).toBe(1);
    expect(upload.body.readUInt32LE(24)).toBe(24000);
    expect(upload.body.readUInt16LE(34)).toBe(16);
    expect(upload.body.readUInt32LE(40)).toBeGreaterThan(24000 * 2 * 0.8);
    expect(upload.body.length).toBe(44 + upload.body.readUInt32LE(40));
    let peak = 0;
    for (let i = 44; i + 1 < upload.body.length; i += 2)
      peak = Math.max(peak, Math.abs(upload.body.readInt16LE(i)));
    expect(peak).toBeGreaterThan(1000);

    // Quality checks and transcript match.
    const checks = flow
      .getByRole("list", { name: "Quality checks" })
      .getByRole("listitem");
    await expect(checks).toHaveCount(4);
    await expect(checks.nth(0)).toContainText("Pass");
    await expect(checks.nth(0)).toContainText("Length");
    await expect(checks.nth(1)).toContainText("Warning");
    await expect(checks.nth(1)).toContainText("Rather quiet");
    await expect(flow.getByLabel("Heard transcript")).toContainText(
      "quick brown fox",
    );
    const pct = Math.round(wordSimilarity(SCRIPTS[0].text, heardText) * 100);
    await expect(flow.locator(".clone-similarity")).toContainText(`${pct}%`);
    const transcript = flow.getByLabel("Transcript of the recording");
    await expect(transcript).toHaveValue(heardText);
    await transcript.fill("The quick brown fox, exactly as said.");
    await flow.getByRole("button", { name: "Continue", exact: true }).click();

    // Preview with the unsaved clone.
    await expect(
      flow.getByRole("heading", { name: "Clone a voice: Preview" }),
    ).toBeVisible();
    await flow.getByLabel("Delivery direction (optional)").fill("Calm");
    await flow.getByRole("button", { name: "Play preview" }).click();
    await expect(flow.getByRole("status")).toContainText("Playing the preview");
    expect(state.previews).toHaveLength(1);
    const preview = state.previews[0];
    expect(Object.keys(preview).sort()).toEqual([
      "clone_audio_base64",
      "clone_transcript",
      "direction",
      "text",
    ]);
    expect(preview.clone_transcript).toBe(
      "The quick brown fox, exactly as said.",
    );
    expect(preview.direction).toBe("Calm");
    expect(preview.text).toBe(
      "Hello, thanks for calling. How can I help you today?",
    );
    expect(
      Buffer.from(preview.clone_audio_base64, "base64").toString("ascii", 0, 4),
    ).toBe("RIFF");
    expect(
      await page.evaluate(
        () => (window as unknown as TestWindow).__played.length,
      ),
    ).toBe(1);

    // Save.
    await flow.getByRole("button", { name: "Continue to save" }).click();
    await expect(
      flow.getByRole("heading", { name: "Clone a voice: Save" }),
    ).toBeVisible();
    await expect(flow.getByText("Front desk", { exact: true })).toBeVisible();
    await flow.getByRole("button", { name: "Save voice" }).click();
    await expect(flow).toHaveCount(0);
    await expect(
      panel.getByRole("status").filter({ hasText: "cloned" }),
    ).toBeVisible();

    expect(state.clones).toHaveLength(1);
    const posted = state.clones[0];
    expect(Object.keys(posted).sort()).toEqual([
      "audio_base64",
      "consent",
      "id",
      "name",
      "revision",
      "transcript",
    ]);
    expect(posted).toMatchObject({
      revision: 3,
      id: "front-desk",
      name: "Front desk",
      transcript: "The quick brown fox, exactly as said.",
      consent: true,
    });
    const wav = Buffer.from(posted.audio_base64, "base64");
    expect(wav.toString("ascii", 0, 4)).toBe("RIFF");
    expect(wav.readUInt32LE(24)).toBe(24000);

    // The new voice is listed as cloned and can be assigned to a persona.
    await expect(panel.locator(".voice-list .pill")).toHaveText([
      "Built-in",
      "Built-in",
      "Designed",
      "Cloned",
    ]);
    await expect(
      panel.getByRole("combobox", { name: "Voice for Sara" }).locator("option"),
    ).toContainText([
      "Sara (original)",
      "Tom (original)",
      "British gent",
      "Front desk",
    ]);

    // Every blob URL created during the flow is revoked and the microphone is released.
    const urls = await page.evaluate(() => ({
      created: (window as unknown as TestWindow).__created as string[],
      revoked: (window as unknown as TestWindow).__revoked as string[],
      stopped: (window as unknown as TestWindow).__stopped as number,
    }));
    const mine = urls.created.slice(createdBefore);
    expect(mine.length).toBeGreaterThanOrEqual(3); // two takes and the preview
    for (const u of mine) expect(urls.revoked).toContain(u);
    expect(urls.stopped).toBeGreaterThanOrEqual(2);
  });

  test("check results: failures block continuing, warnings do not, and the script text can be used", async ({
    page,
  }) => {
    const state = fresh();
    state.report.checks[2] = {
      id: "clipping",
      label: "Clipping",
      status: "fail",
      detail: "4.0% of samples are clipped; speak softer or move back",
    };
    state.report.transcript = "";
    const panel = await open(page, state);
    const flow = await start(panel);
    await record(page, flow);
    await flow.getByRole("button", { name: "Use this recording" }).click();
    const checks = flow
      .getByRole("list", { name: "Quality checks" })
      .getByRole("listitem");
    await expect(checks.nth(2)).toContainText("Failed");
    await expect(checks.nth(2)).toContainText("clipped");
    await expect(flow.getByLabel("Heard transcript")).toContainText(
      "Nothing was recognised",
    );
    await expect(flow.locator(".clone-similarity")).toContainText("0%");
    const next = flow.getByRole("button", { name: "Continue", exact: true });
    await expect(flow.getByLabel("Transcript of the recording")).toHaveValue(
      "",
    );
    await expect(next).toBeDisabled(); // failed check and no transcript
    await flow.getByRole("button", { name: "Use the script text" }).click();
    await expect(flow.getByLabel("Transcript of the recording")).toHaveValue(
      SCRIPTS[0].text,
    );
    await expect(next).toBeDisabled(); // still a failed check
    await expect(flow.getByText("A check failed")).toBeVisible();
    await flow.getByRole("button", { name: "Record again" }).click();
    await expect(
      flow.getByRole("heading", { name: "Clone a voice: Read and record" }),
    ).toBeVisible();
    await expect(flow.getByLabel("Your recording")).toHaveCount(0);
  });

  test("a failing check request is shown and can be retried", async ({
    page,
  }) => {
    const state = fresh();
    const panel = await open(page, state);
    let fail = true;
    await page.route(ORIGIN + "/api/settings/voices/clone/check", (route) =>
      fail
        ? json(route, { error: "Speech recognition failed" }, 502)
        : route.fallback(),
    );
    const flow = await start(panel);
    await record(page, flow);
    await flow.getByRole("button", { name: "Use this recording" }).click();
    await expect(flow.getByRole("alert")).toContainText(
      "Speech recognition failed",
    );
    fail = false;
    await flow.getByRole("button", { name: "Try the check again" }).click();
    await expect(flow.getByLabel("Transcript of the recording")).toHaveValue(
      heardText,
    );
    await expect(flow.getByRole("alert")).toHaveCount(0);
  });

  test("denied microphone permission shows a clear message and can be retried", async ({
    page,
  }) => {
    const panel = await open(page, fresh());
    const flow = await start(panel);
    await page.evaluate(
      () => ((window as unknown as TestWindow).__micDenied = true),
    );
    await flow.getByRole("button", { name: "Start recording" }).click();
    await expect(flow.getByRole("alert")).toContainText(
      "Microphone access was blocked",
    );
    await expect(
      flow.getByRole("button", { name: "Start recording" }),
    ).toBeEnabled();
    await page.evaluate(
      () => ((window as unknown as TestWindow).__micDenied = false),
    );
    await record(page, flow);
    await expect(flow.getByRole("alert")).toHaveCount(0);
  });

  test("closing the panel mid-recording releases the microphone", async ({
    page,
  }) => {
    const panel = await open(page, fresh());
    const flow = await start(panel);
    await flow.getByRole("button", { name: "Start recording" }).click();
    await expect(
      flow.getByRole("button", { name: "Stop recording" }),
    ).toBeVisible({
      timeout: 10000,
    });
    expect(
      await page.evaluate(() => (window as unknown as TestWindow).__stopped),
    ).toBe(0);
    await flow.getByRole("button", { name: "Cancel cloning" }).click();
    await expect(flow).toHaveCount(0);
    await expect
      .poll(() =>
        page.evaluate(() => (window as unknown as TestWindow).__stopped),
      )
      .toBeGreaterThan(0);
  });

  test("a save conflict is shown and keeps the recording", async ({ page }) => {
    const state = fresh();
    state.cloneStatus = 409;
    const panel = await open(page, state);
    const flow = await start(panel);
    await record(page, flow);
    await flow.getByRole("button", { name: "Use this recording" }).click();
    await flow.getByRole("button", { name: "Continue", exact: true }).click();
    await flow.getByRole("button", { name: "Continue to save" }).click();
    await flow.getByRole("button", { name: "Save voice" }).click();
    await expect(flow.getByRole("alert")).toContainText(
      "voice settings changed",
    );
    await expect(
      flow.getByRole("button", { name: "Save voice" }),
    ).toBeEnabled();
    expect(state.clones).toHaveLength(1);
  });

  test("clone voices show a Cloned badge and are renamed and deleted through the normal save", async ({
    page,
  }) => {
    const state = fresh();
    state.snapshot.voices.push({ ...clone });
    const panel = await open(page, state);
    await expect(panel.locator(".voice-list .pill")).toHaveText([
      "Built-in",
      "Built-in",
      "Designed",
      "Cloned",
    ]);
    // A clone has a name but no description field.
    await expect(panel.getByLabel("Name of voice front-desk")).toHaveValue(
      "Front desk",
    );
    await expect(
      panel.getByLabel("Description of voice front-desk"),
    ).toHaveCount(0);
    await expect(
      panel.getByLabel("Description of voice british-gent"),
    ).toHaveCount(1);
    await panel.getByLabel("Name of voice front-desk").fill("Reception");
    await panel.getByRole("button", { name: "Save voice settings" }).click();
    await expect(panel.getByText("Voice settings saved")).toBeVisible();
    expect(state.posts[0].voices).toEqual([
      {
        id: "british-gent",
        name: "British gent",
        description: "A deep, slow, older British man.",
      },
      { id: "front-desk", name: "Reception", kind: "clone" },
    ]);
    await expect(panel.getByLabel("Name of voice front-desk")).toHaveValue(
      "Reception",
    );

    // Previews of a saved clone go by id, and deleting omits it from the save.
    await panel.getByRole("button", { name: "Preview Reception" }).click();
    await expect.poll(() => state.previews.length).toBe(1);
    expect(state.previews[0]).toEqual({ voice_id: "front-desk" });
    await panel.getByRole("button", { name: "Delete Reception" }).click();
    await panel.getByRole("button", { name: "Save voice settings" }).click();
    await expect(panel.getByText("Voice settings saved")).toBeVisible();
    expect(state.posts[1].voices.map((v) => v.id)).toEqual(["british-gent"]);
    await expect(panel.getByLabel("Name of voice front-desk")).toHaveCount(0);
  });

  test("a clone assigned to a persona cannot be deleted and the server error is shown", async ({
    page,
  }) => {
    const state = fresh();
    state.snapshot.voices.push({ ...clone });
    state.snapshot.personas.telecom.voice_id = "front-desk";
    const panel = await open(page, state);
    await page.route(ORIGIN + "/api/settings/voices", (route) =>
      route.request().method() === "POST"
        ? json(
            route,
            {
              error:
                "voice front-desk is still assigned to telecom; choose another voice first",
            },
            400,
          )
        : route.fallback(),
    );
    await panel.getByRole("button", { name: "Delete Front desk" }).click();
    await panel.getByRole("button", { name: "Save voice settings" }).click();
    await expect(panel.getByRole("alert")).toContainText("still assigned");
  });

  test("cloning starts only from saved settings", async ({ page }) => {
    const panel = await open(page, fresh());
    const button = panel.getByRole("button", {
      name: "Clone a voice",
      exact: true,
    });
    await expect(button).toBeEnabled();
    await panel.getByLabel("Delivery direction for Sara").fill("Slow");
    await expect(button).toBeDisabled();
    await expect(
      panel.getByText("Save or reload your voice changes"),
    ).toBeVisible();
    await panel.getByRole("button", { name: "Save voice settings" }).click();
    await expect(panel.getByText("Voice settings saved")).toBeVisible();
    await expect(button).toBeEnabled();
  });

  test("the flow fits a phone screen", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    const state = fresh();
    const panel = await open(page, state);
    const flow = await start(panel);
    await record(page, flow);
    await flow.getByRole("button", { name: "Use this recording" }).click();
    await expect(
      flow.getByRole("list", { name: "Quality checks" }),
    ).toBeVisible();
    const overflow = await flow.evaluate(
      (el) => el.scrollWidth - el.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(1);
    const box = await flow.boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(376);
  });
});
