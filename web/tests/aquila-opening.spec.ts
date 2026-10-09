// Self-contained: serves the built app (web/dist, run `npm run build` first) through page.route and mocks every API call.
import { test, expect, Page, Route } from "@playwright/test";
import { readFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";

const ORIGIN = "http://aquila.test";
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
const agent = (id: string, name: string, persona: Record<string, string>) => ({
  config: {
    id,
    name,
    organization: "The Aquila School",
    role: "Advisor",
    industry: "aquila",
    persona,
    memory: { namespace: "aquila-demo" },
    skills: [],
    knowledge: [],
    branding: { welcome: "STATIC WELCOME TEXT" },
  },
  users: [
    {
      id: "L001",
      name: "Sarah Ahmed",
      description: "Lost lead, toured in May",
    },
  ],
  skills: {},
  llm: "x",
  memory: "x",
});

type State = {
  calls: string[];
  openStatus: number;
  openText: string;
  voiceEnabled: boolean;
};

async function setup(page: Page, personaID: string, state: State) {
  const agents = [
    agent("aquila-outreach", "Sophie", { opening: "outbound" }),
    agent("aquila-admissions", "Amelia", { opening: "inbound" }),
    agent("plain", "Tom", {}),
  ];
  // Replace EventSource with a controllable fake: the test emits events after the open POST.
  await page.addInitScript(() => {
    const w = window as unknown as {
      __es: FakeES[];
      __emit: (ev: unknown) => void;
      __gum: number;
      EventSource: unknown;
    };
    w.__es = [];
    w.__gum = 0;
    // Microphone access is denied: starting voice must fall back to text silently.
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: {
        getUserMedia: async () => {
          w.__gum++;
          throw new DOMException("denied", "NotAllowedError");
        },
      },
    });
    class FakeES {
      readyState = 0;
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      onmessage: ((e: { data: string }) => void) | null = null;
      listeners: Record<string, (() => void)[]> = {};
      constructor(public url: string) {
        w.__es.push(this);
        setTimeout(() => {
          this.readyState = 1;
          this.onopen?.();
          (this.listeners.open || []).forEach((f) => f());
        }, 10);
      }
      addEventListener(type: string, fn: () => void) {
        (this.listeners[type] ||= []).push(fn);
      }
      close() {
        this.readyState = 2;
      }
    }
    w.EventSource = FakeES;
    w.__emit = (ev: unknown) =>
      w.__es.forEach((s) => s.onmessage?.({ data: JSON.stringify(ev) }));
  });
  await page.route(ORIGIN + "/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    if (path === "/api/agents")
      return json(
        route,
        agents.filter((a) => a.config.id === personaID),
      );
    if (path === "/api/voice")
      return json(route, { enabled: state.voiceEnabled });
    if (path === "/api/sessions" && method === "POST") {
      state.calls.push("create");
      return json(route, { id: "s1" }, 201);
    }
    if (path === "/api/sessions/s1/open" && method === "POST") {
      state.calls.push("open");
      if (state.openStatus !== 202)
        return json(route, { error: "nothing to open" }, state.openStatus);
      await route.fulfill({ status: 202, body: "" });
      const now = new Date().toISOString();
      const events = [
        { id: 1, type: "turn.started", turn_id: "t1", time: now, data: {} },
        {
          id: 2,
          type: "agent.response.delta",
          turn_id: "t1",
          time: now,
          data: { text: state.openText },
        },
        { id: 3, type: "turn.completed", turn_id: "t1", time: now, data: {} },
      ];
      for (const ev of events)
        await page.evaluate(
          (e) =>
            (window as unknown as { __emit: (ev: unknown) => void }).__emit(e),
          ev,
        );
      return;
    }
    if (path.startsWith("/api/"))
      return json(route, { error: "not mocked" }, 404);
    const file = path === "/" ? "index.html" : path.slice(1);
    if (!existsSync(dist + file))
      return route.fulfill({ status: 404, body: "" });
    return route.fulfill({
      status: 200,
      contentType: types[file.split(".").pop()!] || "application/octet-stream",
      body: readFileSync(dist + file),
    });
  });
}
const fresh = (): State => ({
  calls: [],
  openStatus: 202,
  openText: "Hello Sarah, it is Sophie from The Aquila School.",
  voiceEnabled: false,
});
const voiceStarts = (page: Page) =>
  page.evaluate(() => (window as unknown as { __gum: number }).__gum);

test.describe("agent speaks first (mocked API)", () => {
  test.skip(
    !existsSync(dist + "index.html"),
    "build the app first: npm run build",
  );

  test("outbound persona shows Place call, opens after create and renders the opening", async ({
    page,
  }) => {
    const state = fresh();
    await setup(page, "aquila-outreach", state);
    await page.goto(ORIGIN + "/");
    const call = page.getByRole("button", {
      name: /Place call to Sarah Ahmed/,
    });
    await expect(call).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Family / contact" }),
    ).toBeVisible();
    expect(state.calls).toEqual([]);
    await call.click();
    await expect(
      page
        .getByRole("log", { name: "Conversation messages" })
        .getByText("Hello Sarah, it is Sophie from The Aquila School."),
    ).toBeVisible();
    expect(state.calls).toEqual(["create", "open"]);
    await expect(page.getByText("STATIC WELCOME TEXT")).toHaveCount(0);
    await expect(page.locator(".status")).toHaveText("Connected");
    await expect(page.locator(".bubble.user")).toHaveCount(0);
  });

  test("Place call starts voice first and falls back to text when the microphone is denied", async ({
    page,
  }) => {
    const state = fresh();
    state.voiceEnabled = true;
    await setup(page, "aquila-outreach", state);
    await page.goto(ORIGIN + "/");
    expect(await voiceStarts(page)).toBe(0);
    await page
      .getByRole("button", { name: /Place call to Sarah Ahmed/ })
      .click();
    await expect(
      page
        .getByRole("log", { name: "Conversation messages" })
        .getByText("Hello Sarah, it is Sophie from The Aquila School."),
    ).toBeVisible();
    expect(await voiceStarts(page)).toBe(1);
    expect(state.calls).toEqual(["create", "open"]);
    await expect(page.getByRole("alert")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Start voice" }),
    ).toBeVisible();
  });

  test("Place call stays text-only when voice is disabled", async ({
    page,
  }) => {
    const state = fresh();
    await setup(page, "aquila-outreach", state);
    await page.goto(ORIGIN + "/");
    await page
      .getByRole("button", { name: /Place call to Sarah Ahmed/ })
      .click();
    await expect(page.getByText(state.openText)).toBeVisible();
    expect(await voiceStarts(page)).toBe(0);
  });

  test("inbound auto-open never starts voice", async ({ page }) => {
    const state = fresh();
    state.voiceEnabled = true;
    await setup(page, "aquila-admissions", state);
    await page.goto(ORIGIN + "/");
    await page.getByRole("button", { name: /Start chat/ }).click();
    await expect(page.getByText(state.openText)).toBeVisible();
    expect(await voiceStarts(page)).toBe(0);
  });

  test("inbound persona opens automatically with no static welcome", async ({
    page,
  }) => {
    const state = fresh();
    state.openText = "Welcome back Sarah, lovely to hear from you.";
    await setup(page, "aquila-admissions", state);
    await page.goto(ORIGIN + "/");
    await page.getByRole("button", { name: /Start chat/ }).click();
    await expect(
      page.getByText("Welcome back Sarah, lovely to hear from you."),
    ).toBeVisible();
    expect(state.calls).toEqual(["create", "open"]);
    await expect(page.getByText("STATIC WELCOME TEXT")).toHaveCount(0);
    await expect(page.locator(".bubble.user")).toHaveCount(0);
  });

  test("a failed open falls back to the normal welcome", async ({ page }) => {
    const state = fresh();
    state.openStatus = 409;
    await setup(page, "aquila-admissions", state);
    await page.goto(ORIGIN + "/");
    await page.getByRole("button", { name: /Start chat/ }).click();
    await expect(page.getByText("STATIC WELCOME TEXT")).toBeVisible();
    expect(state.calls).toEqual(["create", "open"]);
    await expect(page.getByRole("alert")).toHaveCount(0);
  });

  test("a persona without opening never calls open", async ({ page }) => {
    const state = fresh();
    await setup(page, "plain", state);
    await page.goto(ORIGIN + "/");
    await expect(page.getByText("STATIC WELCOME TEXT")).toBeVisible();
    await page.getByRole("button", { name: /Start chat/ }).click();
    await expect(page.locator(".status")).toHaveText("Connected");
    await page.waitForTimeout(500);
    expect(state.calls).toEqual(["create"]);
    await expect(page.getByText("STATIC WELCOME TEXT")).toBeVisible();
  });
});
