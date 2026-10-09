// Two agents talk to each other through the real web UI (web/src/voice.ts playback
// path), in headless Chromium, while the rendered audio is recorded.
//
// Env: BASE (default http://localhost:8080), OUT (default /out), TURNS (6),
//      CTX_RATE (force the AudioContext sample rate, e.g. 24000), AGENTS ("a,b").
import { chromium } from "playwright-core";
import fs from "node:fs";

const BASE = process.env.BASE || "http://localhost:8080";
const OUT = process.env.OUT || "/out";
const TURNS = Number(process.env.TURNS || 6);
const CTX_RATE = Number(process.env.CTX_RATE || 0);
const [AGENT_A, AGENT_B] = (
  process.env.AGENTS || "aquila-admissions,assistant"
).split(",");
const SEED =
  process.env.SEED ||
  "Hello, I am calling to ask how the admissions process works and what the next steps are.";
fs.mkdirSync(OUT, { recursive: true });

// Runs in every page before the app: records events, /speech bytes and the
// rendered output of the Voice gain node.
function instrument(ctxRate) {
  window.__events = [];
  window.__speech = [];
  window.__tap = { blocks: [], rate: 0, session: "", state: [] };
  const ES = window.EventSource;
  window.EventSource = class extends ES {
    constructor(url, init) {
      super(url, init);
      this.addEventListener("message", (e) => {
        try {
          window.__events.push({
            ...JSON.parse(e.data),
            perf: performance.now(),
          });
        } catch {}
      });
    }
  };
  const f = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const url = typeof input === "string" ? input : input.url;
    const res = await f(input, init);
    if (url.includes("/speech") && init?.method === "POST") {
      const rec = {
        text: JSON.parse(init.body).text,
        t0: performance.now(),
        chunks: [],
        done: false,
        status: res.status,
      };
      window.__speech.push(rec);
      const reader = res.clone().body.getReader();
      (async () => {
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          rec.chunks.push({ t: performance.now(), v: value });
        }
        rec.done = true;
      })().catch(() => {
        rec.done = true;
      });
    }
    return res;
  };
  if (ctxRate) {
    const AC = window.AudioContext;
    window.AudioContext = class extends AC {
      constructor(o) {
        super({ ...(o || {}), sampleRate: ctxRate });
      }
    };
  }
  // AudioWorklet tap: runs on the audio thread, so it cannot drop or jitter blocks
  // the way a ScriptProcessor does. Every block carries its exact context frame.
  window.__voiceTap = (ctx, gain, session) => {
    const tap = window.__tap;
    tap.rate = ctx.sampleRate;
    tap.session = session;
    tap.ready = false;
    const code = `class Tap extends AudioWorkletProcessor {
      constructor() { super(); this.buf = new Float32Array(2048); this.n = 0; this.frame = 0; }
      process(inputs) {
        const ch = inputs[0] && inputs[0][0];
        const len = ch ? ch.length : 128;
        for (let i = 0; i < len; i++) {
          if (this.n === 0) this.frame = currentFrame + i;
          this.buf[this.n++] = ch ? ch[i] : 0;
          if (this.n === this.buf.length) {
            this.port.postMessage({ frame: this.frame, d: this.buf }, [this.buf.buffer]);
            this.buf = new Float32Array(2048); this.n = 0;
          }
        }
        return true;
      }
    }
    registerProcessor("tap", Tap);`;
    const url = URL.createObjectURL(
      new Blob([code], { type: "text/javascript" }),
    );
    ctx.audioWorklet.addModule(url).then(() => {
      const node = new AudioWorkletNode(ctx, "tap", {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        channelCount: 1,
      });
      node.port.onmessage = (e) => {
        tap.blocks.push({
          frame: e.data.frame,
          pt: e.data.frame / ctx.sampleRate,
          ct: ctx.currentTime,
          perf: performance.now(),
          d: e.data.d,
        });
      };
      const mute = ctx.createGain();
      mute.gain.value = 0;
      gain.connect(node);
      node.connect(mute);
      mute.connect(ctx.destination);
      tap.ready = true;
    });
    ctx.addEventListener("statechange", () =>
      tap.state.push({
        s: ctx.state,
        ct: ctx.currentTime,
        perf: performance.now(),
      }),
    );
  };
}

const b64 = (bytes) => Buffer.from(bytes).toString("base64");

const browser = await chromium.launch({
  args: [
    "--use-fake-ui-for-media-stream",
    "--use-fake-device-for-media-stream",
    "--autoplay-policy=no-user-gesture-required",
    ...(process.env.FAKE_MIC
      ? [`--use-file-for-fake-audio-capture=${process.env.FAKE_MIC}`]
      : []),
  ],
});

async function openAgent(id, names) {
  const ctx = await browser.newContext({
    permissions: ["microphone"],
    baseURL: BASE,
  });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => console.log(`[${id}] pageerror`, e.message));
  await page.addInitScript(instrument, CTX_RATE);
  await page.goto("/");
  await page.locator(".persona-card", { hasText: names[id] }).first().click();
  await page.getByRole("button", { name: /^Go live with/ }).click();
  await page.getByRole("button", { name: "Start voice", exact: true }).click();
  await page
    .locator(".voice-controls")
    .getByText("Listening")
    .first()
    .waitFor({ timeout: 60000 });
  await page.waitForFunction(() => window.__tap.ready, null, {
    timeout: 10000,
  });
  // In voice channel mode the chat panel is hidden until "Type instead".
  const typeBtn = page.getByRole("button", { name: "Type instead" });
  if (await typeBtn.isVisible().catch(() => false)) await typeBtn.click();
  await page.locator("#message").waitFor({ timeout: 10000 });
  await settle(page);
  return page;
}

async function say(page, text) {
  const n = await page.evaluate(() => window.__events.length);
  await page.locator("#message").fill(text);
  await page.locator("#message").press("Enter");
  return n;
}

// Waits until nothing is playing (e.g. the opening greeting has finished).
async function settle(page) {
  for (let i = 0; i < 100; i++) {
    const st = await page.evaluate(() => {
      const blocks = window.__tap.blocks;
      let quiet = 0;
      for (let i = blocks.length - 1; i >= 0 && quiet < 80; i--) {
        const d = blocks[i].d;
        let m = 0;
        for (let k = 0; k < d.length; k++) m = Math.max(m, Math.abs(d[k]));
        if (m > 1e-4) break;
        quiet += d.length / window.__tap.rate;
      }
      return { quiet, open: window.__speech.some((s) => !s.done) };
    });
    if (!st.open && st.quiet >= 2) return;
    await new Promise((r) => setTimeout(r, 300));
  }
}

// Waits for the reply to the message sent when n events existed, and for it to
// finish playing. Returns the reply text.
async function reply(page, n) {
  const deadline = Date.now() + 150000;
  let turn = "";
  for (;;) {
    if (Date.now() > deadline) throw new Error("reply timeout");
    const st = await page.evaluate((n) => {
      const ev = window.__events.slice(n);
      const started = ev.find((e) => e.type === "turn.started");
      const turn = started?.turn_id || "";
      const done = ev.some(
        (e) => e.type === "turn.completed" && e.turn_id === turn,
      );
      const text = ev
        .filter((e) => e.type === "agent.response.delta" && e.turn_id === turn)
        .map((e) => e.data.text || "")
        .join("");
      const speechOpen = window.__speech.some((s) => !s.done);
      const blocks = window.__tap.blocks;
      let quiet = 0;
      for (let i = blocks.length - 1; i >= 0 && quiet < 80; i--) {
        const d = blocks[i].d;
        let m = 0;
        for (let k = 0; k < d.length; k++) m = Math.max(m, Math.abs(d[k]));
        if (m > 1e-4) break;
        quiet += d.length / window.__tap.rate;
      }
      const status =
        document.querySelector('.voice-controls [role="status"]')
          ?.textContent || "";
      return {
        turn,
        done,
        text,
        speechOpen,
        quiet,
        status,
        spoke: window.__speech.length,
      };
    }, n);
    turn = st.turn;
    // Idle = turn finished, nothing being fetched, and the voice reports Listening.
    if (
      st.done &&
      !st.speechOpen &&
      st.quiet >= 1.5 &&
      /Listening/.test(st.status)
    )
      return { turn, text: st.text, spoke: st.spoke };
    await new Promise((r) => setTimeout(r, 300));
  }
}

const agents = await (await fetch(`${BASE}/api/agents`)).json();
const names = Object.fromEntries(
  agents.map((a) => [a.config.id, a.config.name]),
);
const pages = {
  [AGENT_A]: await openAgent(AGENT_A, names),
  [AGENT_B]: await openAgent(AGENT_B, names),
};

const log = [];
let message = SEED;
let speaker = AGENT_A;
for (let turn = 0; turn < TURNS; turn++) {
  const page = pages[speaker];
  const n = await say(page, message);
  const r = await reply(page, n);
  console.log(
    `turn ${turn} ${speaker}: ${r.text.slice(0, 100).replace(/\n/g, " ")}...`,
  );
  log.push({
    turn,
    agent: speaker,
    input: message,
    reply: r.text,
    turn_id: r.turn,
  });
  message = r.text.replace(/\s+/g, " ").trim().slice(0, 500);
  speaker = speaker === AGENT_A ? AGENT_B : AGENT_A;
}
fs.writeFileSync(`${OUT}/conversation.json`, JSON.stringify(log, null, 1));

// Collect recordings.
for (const [id, page] of Object.entries(pages)) {
  await new Promise((r) => setTimeout(r, 2500));
  const dump = await page.evaluate(() => {
    const tap = window.__tap;
    const total = tap.blocks.reduce((n, b) => n + b.d.length, 0);
    const all = new Float32Array(total);
    let at = 0;
    for (const b of tap.blocks) {
      all.set(b.d, at);
      at += b.d.length;
    }
    const u8 = new Uint8Array(all.buffer);
    let s = "";
    for (let i = 0; i < u8.length; i += 0x8000)
      s += String.fromCharCode(...u8.subarray(i, i + 0x8000));
    return {
      session: tap.session,
      rate: tap.rate,
      state: tap.state,
      f32: btoa(s),
      blocks: tap.blocks.map((b) => ({
        frame: b.frame,
        pt: b.pt,
        ct: b.ct,
        perf: b.perf,
        n: b.d.length,
      })),
      speech: window.__speech.map((s) => {
        const bytes = new Uint8Array(
          s.chunks.reduce((n, c) => n + c.v.length, 0),
        );
        let o = 0;
        for (const c of s.chunks) {
          bytes.set(c.v, o);
          o += c.v.length;
        }
        let t = "";
        for (let i = 0; i < bytes.length; i += 0x8000)
          t += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
        return {
          text: s.text,
          t0: s.t0,
          status: s.status,
          pcm: btoa(t),
          reads: s.chunks.map((c) => [c.t - s.t0, c.v.length]),
        };
      }),
      events: window.__events
        .filter((e) => e.type !== "agent.response.delta")
        .map((e) => ({ type: e.type, turn: e.turn_id, perf: e.perf })),
    };
  });
  const dir = `${OUT}/${id}`;
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(`${dir}/rendered.f32`, Buffer.from(dump.f32, "base64"));
  dump.speech.forEach((s, i) =>
    fs.writeFileSync(
      `${dir}/server-${String(i).padStart(2, "0")}.pcm`,
      Buffer.from(s.pcm, "base64"),
    ),
  );
  fs.writeFileSync(
    `${dir}/meta.json`,
    JSON.stringify({
      agent: id,
      session: dump.session,
      rate: dump.rate,
      state: dump.state,
      blocks: dump.blocks,
      speech: dump.speech.map((s) => ({
        text: s.text,
        t0: s.t0,
        status: s.status,
        reads: s.reads,
      })),
      events: dump.events,
    }),
  );
  // End the call so the client trace is flushed, then fetch it from the server.
  await page
    .getByRole("button", { name: "End voice", exact: true })
    .click()
    .catch(() => {});
  await new Promise((r) => setTimeout(r, 3000));
  const tr = await fetch(`${BASE}/api/traces/${dump.session}`);
  fs.writeFileSync(`${dir}/trace.jsonl`, await tr.text());
}
await browser.close();
fs.writeFileSync(`${OUT}/HARNESS_DONE`, "ok");
console.log("recorded", OUT);
