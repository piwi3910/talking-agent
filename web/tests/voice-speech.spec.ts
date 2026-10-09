import { test, expect, type Page } from "@playwright/test";
// Self-contained: the session, event stream, microphone and /speech endpoint are all
// mocked. Only the page's own voice playback logic is under test.
const PCM = Buffer.alloc(24000); // 0.5 s of silence at 24 kHz, 16-bit mono

async function setup(
  page: Page,
  speech: (n: number, text: string) => { status: number; body?: Buffer },
) {
  const bodies: string[] = [];
  await page.addInitScript(() => {
    class Events {
      static instance: Events;
      onopen: ((e: unknown) => void) | null = null;
      onmessage: ((e: unknown) => void) | null = null;
      onerror = null;
      constructor() {
        Events.instance = this;
        setTimeout(() => this.onopen?.({}), 0);
      }
      close() {}
    }
    (window as any).EventSource = Events;
    (window as any).deliver = (e: unknown) =>
      Events.instance.onmessage?.({ data: JSON.stringify(e) });
    navigator.mediaDevices.getUserMedia = async () => {
      const c = new AudioContext();
      return c.createMediaStreamDestination().stream;
    };
  });
  await page.route("**/api/voice", (route) =>
    route.fulfill({
      json: {
        enabled: true,
        stt: "x",
        tts: "y",
        input_sample_rate: 16000,
        output_sample_rate: 24000,
      },
    }),
  );
  await page.route("**/api/sessions", (route) =>
    route.fulfill({ json: { id: "sp" } }),
  );
  await page.route("**/api/agents/*/voice-cues", (route) =>
    route.fulfill({ status: 404, json: { error: "none" } }),
  );
  await page.route("**/api/sessions/sp/events", (route) =>
    route.fulfill({
      contentType: "text/event-stream",
      body: ": connected\n\n",
    }),
  );
  await page.route("**/api/sessions/sp/speech", (route) => {
    const text = JSON.parse(route.request().postData() || "{}").text as string;
    bodies.push(text);
    const r = speech(bodies.length, text);
    return route.fulfill({
      status: r.status,
      contentType: r.body ? "audio/pcm" : "application/json",
      body: r.body || JSON.stringify({ error: "busy" }),
    });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Start chat", exact: true }).click();
  await page.getByRole("button", { name: "Start voice", exact: true }).click();
  await expect(page.locator(".voice-controls")).toContainText("Listening", {
    timeout: 30000,
  });
  return bodies;
}
const emit = (page: Page, id: number, type: string, data: unknown) =>
  page.evaluate(
    ({ id, type, data }) =>
      (window as any).deliver({
        id,
        type,
        data,
        turn_id: "t",
        time: new Date().toISOString(),
      }),
    { id, type, data },
  );

test("a phrase that gets 409 is retried instead of dropped", async ({
  page,
}) => {
  const bodies = await setup(page, (n) =>
    n === 1 ? { status: 409 } : { status: 200, body: PCM },
  );
  await emit(page, 1, "turn.started", {});
  await emit(page, 2, "agent.response.delta", {
    text: "Your appointment is on Tuesday at nine.",
  });
  await emit(page, 3, "turn.completed", { duration_ms: 10 });
  await expect.poll(() => bodies.length, { timeout: 5000 }).toBe(2);
  expect(bodies[1]).toBe(bodies[0]);
  await expect(page.locator(".voice-controls")).toContainText("Speaking", {
    timeout: 5000,
  });
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("the first phrase of a turn starts at a clause break once 40 characters are buffered", async ({
  page,
}) => {
  const bodies = await setup(page, () => ({ status: 200, body: PCM }));
  await emit(page, 1, "turn.started", {});
  await emit(page, 2, "agent.response.delta", {
    text: "Thank you for calling Aquila Support today, I am looking at your account",
  });
  await expect.poll(() => bodies.length, { timeout: 5000 }).toBe(1);
  expect(bodies[0]).toBe("Thank you for calling Aquila Support today,");
  // A later phrase of the same turn waits for a full sentence.
  await emit(page, 3, "agent.response.delta", {
    text: " right now, and it looks fine for the moment",
  });
  await page.waitForTimeout(400);
  expect(bodies).toHaveLength(1);
  await emit(page, 4, "turn.completed", { duration_ms: 10 });
  await expect.poll(() => bodies.length, { timeout: 5000 }).toBe(2);
});

test("short sentences are coalesced and numbers or list markers are not split", async ({
  page,
}) => {
  const bodies = await setup(page, () => ({ status: 200, body: PCM }));
  await emit(page, 1, "turn.started", {});
  await emit(page, 2, "agent.response.delta", {
    text: "Sure. Let me check that for you right now. ",
  });
  await expect.poll(() => bodies.length, { timeout: 5000 }).toBe(1);
  expect(bodies[0]).toBe("Sure. Let me check that for you right now.");
  await emit(page, 3, "agent.response.delta", {
    text: "1. Your balance is 51.5 dollars, Dr. Smith. Anything else?",
  });
  await emit(page, 4, "turn.completed", { duration_ms: 10 });
  await expect.poll(() => bodies.length, { timeout: 5000 }).toBe(3);
  expect(bodies[1]).toBe("1. Your balance is 51.5 dollars, Dr. Smith.");
  expect(bodies[2]).toBe("Anything else?");
});
