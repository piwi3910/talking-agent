# Adversarial E2E suite

`run.sh` starts a short-lived Playwright pod in `enterprise-ai-demo` and drives
the live `https://agent.kw.watteel.lab` UI. The pod records SSE transcripts and
per-session trace JSONL; no local app server or local Go tests are used.

Run all available agents:

```sh
tests/adversarial/run.sh
```

Run a focused follow-up:

```sh
AGENTS=school-services tests/adversarial/run.sh
```

Full runs write raw transcripts and traces under `results/<timestamp>/` and
copy the generated report to `REPORT.md`. A focused run leaves the main report
alone. `merge-report.mjs <full-results-dir> <school-follow-up-dir>` merges a
targeted school follow-up into the full report.

The suite covers typed multi-turn conversation and voice playback, tool
availability/booking confirmation, refusals, repeated inputs, and concurrent
sessions. It does not yet inject microphone audio for barge-in or degraded-STT
checks, reopen a session mid-turn, or switch agents in a live session. The
outbound outreach persona is not started because its UI action places a call.
Successful booking scenarios create demo bookings; the report records the demo
identity and confirmation evidence.
