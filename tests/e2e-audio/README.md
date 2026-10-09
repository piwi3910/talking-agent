# In-cluster audio E2E test

Detects audible glitches ("hiccups") in the agent's spoken replies by recording
**exactly what the browser renders** and comparing it with **exactly what the server
streamed**. Everything runs on the KW cluster in a throwaway Pod; nothing runs on a
workstation.

## What it does

A Pod with three parts, all in `enterprise-ai-demo`:

| part                   | role                                                                                                                                        |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `seed` init container  | waits while `run.sh` copies `voices/`, `voice-settings.json` and `voice-cues/` from the live app, so the test uses the voices users hear    |
| `app` container        | the candidate image (digest passed to `run.sh`), configured like the live Deployment (real FastLLM, real NovaNAS TTS/STT, in-memory memory) |
| `playwright` container | Chromium (`mcr.microsoft.com/playwright:v1.63.0-noble`, same as `web/package.json`) running `harness.mjs`, then `analyze.py`                |

`harness.mjs` opens two pages (default `aquila-admissions` and `assistant`) through the
real stage UI, starts voice, and lets the two agents talk: each reply's text is typed
into the other agent's text box (no microphone, so no echo), for `TURNS` turns (6). Every
reply is spoken through the unmodified `web/src/voice.ts` playback path. Per page it records:

- the rendered signal: an AudioWorklet on the Voice gain node's output, installed through
  the test-only `window.__voiceTap` hook in `voice.ts` (a no-op when undefined). Blocks carry
  their exact context frame number;
- the raw PCM of every `/speech` response (`server-NN.pcm`, 24 kHz s16le);
- the client trace (`trace.jsonl`, fetched from `GET /api/traces/<session>`) with
  `audio.schedule`, `speech.burst`, `gain.target`, `cue.*` events.

`analyze.py` aligns each buffer of the render with the same samples of the server PCM and
reports (see the docstring for thresholds): `SEAM CLICK`, `SCHEDULE GAP`, `ONE-SAMPLE GAP`,
`UNDERRUN`, `LEVEL`, `GAP`. WAVs of every reply, rendered and source, are written to `wav/`.

## Run

```sh
tests/e2e-audio/run.sh <image-digest> <label> [context-sample-rate]
# e.g.
tests/e2e-audio/run.sh 192.168.10.131/enterprise-ai-demo@sha256:... after
tests/e2e-audio/run.sh 192.168.10.131/enterprise-ai-demo@sha256:... after48 48000   # force a 48 kHz context
TURNS=10 tests/e2e-audio/run.sh ...
```

Build the image first with `KW_TAG=<tag> deploy/kw/build.sh`. Results land in
`tests/e2e-audio/results/<label>/` (git-ignored): `report.md`, `summary.json`, `wav/`,
`<agent>/{rendered.f32,server-NN.pcm,trace.jsonl,meta.json}`, `conversation.json`.
`tests/e2e-audio/analyze.sh <results-dir>` re-runs the analysis on recorded results (also in a Pod).

Run one test at a time: concurrent runs share the TTS GPU and inflate the second-chunk
latency, which shows up as `UNDERRUN` (genuinely late audio, not a code defect).

## Reading the report

- The AudioContext rate matters: Chromium picks it from the output device (44.1 kHz in this
  headless Pod, usually 48 kHz on laptops). The first version of the playback code clicked at
  the first seam only when it was not 24000 x integer.
- `SEAM CLICK` / `ONE-SAMPLE GAP` / `SCHEDULE GAP` are code defects in scheduling or
  resampling. `UNDERRUN` means the next burst arrived after the previous had finished
  (slow TTS), and is bounded by `START_LEAD`.
- Pass criteria: no `SEAM CLICK`, `SCHEDULE GAP`, `ONE-SAMPLE GAP` or `LEVEL`/`GAP` findings
  in the first 1.5 s of a reply.
