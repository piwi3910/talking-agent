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

For a focused booking rerun, set `TOURS_ONLY=1` with the tour agents in
`AGENTS`; this runs the tour identity preflight and booking flow without the
other adversarial cases. The API has no identity-creation route, so the
preflight rotates the existing demo identities and skips tour cases when none
has clean CRM history and memory.

Full runs write raw transcripts and traces under `results/<timestamp>/` and
copy the generated report to `REPORT.md`. A focused run leaves the main report
alone. `merge-report.mjs <full-results-dir> <school-follow-up-dir>` merges a
targeted school follow-up into the full report.

The suite covers typed multi-turn conversation and voice playback, tool
availability/booking confirmation, refusals, repeated inputs, and concurrent
sessions. On Nova it also sends noisy PCM over the transcription WebSocket,
cancels a streaming turn while that WebSocket is active, then sends an immediate
follow-up. The reopen probe checks whether an existing session can be resumed;
the report records the API result. It also attempts the outreach persona's
outbound UI flow, which can place a call. Successful booking scenarios create
demo bookings; the report records the demo identity and confirmation evidence.
