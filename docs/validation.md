# Validation record

Verified in the workspace on 2026-09-29 with Go 1.27.1 (Linux arm64) and Node.js 24.21.0:

- Go unit/integration tests, including the race detector.
- `go vet ./...`.
- TypeScript checking and the Vite production build.
- Three Chromium browser tests against the production build: Telecom recurring Wi-Fi and confirmed optimization; hospital preference across new sessions and confirmed booking; deterministic emergency response without tools, including a mobile viewport.
- All 49 configured tools have backend execution coverage. Other tests cover each memory scope dimension, multi-call handling, bounded loops, rejected cross-domain tools and identities, confirmation replay, conflicting bookings, cancellation/timeouts, streaming fragments, and HTTP/SSE events.

The browser suite modifies mock state; restart the server (or separate mock process) for the original sales scenarios.

The initial scripted validation used controlled HTTP/SSE fixtures. Later live inference and NovaMem checks are recorded below. The Dockerfile has also been built successfully with KW’s ARM64 BuildKit service and the image published to Nexus. Docker Compose itself has not been exercised.

KW deployment verification on 2026-09-29:

- ARM64 image built on the existing KW BuildKit service and pushed to Nexus using the existing `novaforge/nexus-pull` credential.
- Kuvryn Sync Application `enterprise-ai-demo/talking-agent` reconciles the repository's `main` branch and reports `Healthy / Synced`.
- `https://agent.kw.watteel.lab` passes CA-verified TLS and the API health check.
- All three Chromium end-to-end tests passed against that cluster URL, including streamed tools, confirmed mutations, cross-session memory, and emergency handling.
- Persistent `kw` kubectl context and documented Sync/build workflows are configured for future deployments; credentials remain outside Git.

## KW SDK integration (2026-09-29)

- LLM transport now uses `github.com/azrtydxb/go-ai-sdk` v0.6.0; Go 1.26 build.
- FastLLM model is `qwen3.5-9b`, with the existing Secret-backed API key.
- Race tests and vet pass, including SDK tool-result conversion, streamed tool arguments, cancellation, pre-stream retries, safe gateway errors, and bounded empty-generation recovery.
- Qwen's template rejects consecutive system messages. The adapter combines leading system instructions and memory into a single system message.
- Live TLS-verified SSE checks on KW exercised telecom outage/network/Wi-Fi diagnostics and hospital doctor/appointment availability tools, with streamed final answers. No appointment or router mutations were requested during these checks.
- One hospital generation returned no visible answer. Empty completed generations now receive one retry within the same deadline; a second empty generation produces an explicit error. Partial streams and tool execution are not retried by this recovery path.

## Live NovaMem integration (2026-09-29)

- Official NovaMem Go client pinned to revision `523af7193caf`; actual KW API contract verified.
- Forty separate NovaMem accounts provisioned for the twenty customers and twenty patients. No admin credential is mounted in the app.
- Opt-in live suite passed against six separate validation accounts: durable write/read, exact-fact dedup, provider reconstruction, all five scope dimensions and cleanup of the test fact.
- Unit tests reject unprovisioned identities, shared tokens, foreign metadata, degraded responses and rejected writes. Runtime tests verify that a failed lookup is explained to the model as unavailable rather than no history.
- A hospital preference was stored through the public SSE API, the app pod was replaced through Sync (new UID confirmed), and P006 recalled it in a fresh conversation. P007 and Telecom C001 did not receive that preference.
- Live score inspection identified unrelated telecom hits at vector similarity 0.36–0.42. A configurable cutoff now rejects those weak matches while preserving keyword hits and stronger semantic matches.
- Final-release API checks returned the persisted morning preference for P006, zero memories for P007, and zero unrelated telecom memories for the Dr. Ahmed query. A direct preference question produced a streamed answer acknowledging mornings.
- Observed before the grounded-response changes below: Qwen3.5-9B occasionally completes an appointment-answer generation without visible text, including after the bounded retry. This is surfaced as an error. A direct gateway probe with thinking disabled produced incorrect calendar dates, so that setting was not adopted. These model-quality cases are not memory retrieval failures; do not describe every live appointment conversation as verified reliable.

## Empty-answer recovery and confirmed actions

- Added deterministic tests for current-turn tool-result recovery, no matching slots, failed tools, stale history, interrupted text, cancellation and upstream errors.
- Reasoning-only/whitespace-only SDK streams are tested without exposing their reasoning content; generation counts, finish reason and total usage remain observable.
- Confirmed booking tests prove that the backend acknowledgement does not invoke the model, failed writes cannot report success, and replay cannot duplicate a booking.
- `web/tests/live.spec.ts` is an opt-in browser test covering availability, booking, rescheduling and cancellation for fictional P018, with explicit confirmation and exact backend timestamps. Enable with `LIVE_MODEL_TESTS=true`; the default scripted suite skips it.
- The first live browser run passed all four hospital operations in 29.4 seconds, including three explicit confirmations and matching backend timestamps.
- Manual review caught an incorrect model-generated weekday in an availability reply despite correct ISO dates. Hospital availability now opts into `response_mode: records`; Go renders weekday/date/time and slot IDs directly. Tests prove this bypasses model synthesis while an empty calendar continues to the model for alternatives.
- Final calendar-policy release passed the live Chromium test in 25.4 seconds. Assertions cover exact weekday/date/UTC rendering and slot IDs in the conversation, followed by confirmed booking, rescheduling and cancellation with matching backend records. All three temporary mutations were completed; the test appointment ended cancelled.

## Phase 2 customer experience (2026-09-29)

Replaced the debug-first layout with config-branded telecom/hospital chat and a collapsible right-side playground (Personas, Activity, Capabilities, Voice). Customer confirmations remain in the conversation even when the playground is hidden. Suggested questions were removed. Persona prompts now request short, natural service conversation without repeated stock acknowledgements or unsolicited follow-up menus. Speech is not enabled; the Voice tab explicitly labels the selected TTS and proposed Nemotron 3.5 STT as awaiting deployment.

Validation before release: `go test ./... -timeout 120s`, TypeScript/Vite production build, and four Chromium tests passed. Browser coverage includes telecom tool/confirmation flow, hospital preference/new session/booking, emergency bypass, brand/identity switching, operator-panel hide/show, starting chat with the panel hidden, no suggestion chips, and mobile overflow. Screenshots inspected at `/tmp/enterprise-phase2-hospital.png` and `/tmp/enterprise-phase2-mobile.png`. Model research and remaining voice work are documented in `docs/phase2-voice.md`.

## Deployed speech services on DGX .246 (2026-09-29)

The first TTS model (since replaced by Qwen3-TTS) and Nemotron 3.5 ASR Q8 ran in separate audio.cpp ARM64 CUDA containers on `gx10-48f4` / `192.168.10.246`. KW Sync applied a versioned ConfigMap and SSH delivery Job; the Job downloaded revision-pinned GGUFs, verified SHA-256, started only the enterprise-speech Compose project, and verified remote health. Internal Services/EndpointSlices expose both endpoints. The deployment Job completed and Sync became Healthy/Synced. Health was also verified from the actual agent pod through `http://speech-tts/health` and `http://speech-stt/health`.

`python3 deploy/speech/verify.py` passed real streaming synthesis, non-silent PCM validation, TTS-to-STT transcript correctness, and duplex STT. The paced 16 kHz input produced transcript deltas before upload ended and a correct final transcript. The first duplex attempt exposed HTTPConnection detaching its write socket on a close-delimited response; retaining the upload socket fixed the test client, with no change to the model service.

Measurements for a fixed 3.2-second test sentence: initial TTS first bytes 1084 ms; repeated warm runs 538–541 ms, about 2.19 seconds total synthesis (RTF ~0.68). Warm file transcription took 141 ms wall time; live STT first output was around 614 ms after beginning the paced input. These are service tests, not end-of-user-speech to audible-agent-response benchmarks. The final report is `docs/speech-validation-20260929.json`.

GPU process memory was approximately 4,194 MiB for the first TTS model and 1,277 MiB for Nemotron. The host reported 91 GiB available unified memory and no swap use after loading. BGE embedding/reranking, Laya, and the Kuvryn host agent remained running. The .245 LLM host was not modified. Browser microphone/playback, agent speech coordination, and stable Sara/Maya voice references remain subsequent integration work.

## Phase 2 voice / Qwen3.6 — 2026-09-29

- Go suite and race checks for API, speech adapter, and SDK adapter passed. Duplex test requires receipt of a transcript before upload finishes; synthesis cancellation must reach the upstream server. Origin/session/size boundary tests passed.
- Five browser checks passed at `https://agent.kw.watteel.lab`: branded UI, real Qwen3.6 hospital lookup/book/reschedule/cancel with explicit confirmations, real DGX ASR/TTS using Chromium's WAV microphone, controlled incremental-render regression, and live generated text visible while the turn is still running.
- Gateway alias `qwen3-6-35b-a3b`, thinking disabled: two short direct requests measured first visible text at 496 / 296 ms, with zero reasoning characters. Those are gateway-only measurements.
- Full agent trace using the earlier 150-word Wi-Fi question: first visible text at about 3.00 s, including 88 ms memory retrieval; LLM text TTFT 2838 ms, zero reasoning characters. Previous Qwen3.5 trace with default thinking had 9110 ms LLM TTFT and 592 reasoning characters. Different model/settings and cache conditions mean this is not an isolated model-speed comparison.
- Voice tests validate transcript, real PCM generation and browser playback scheduling. Human listening quality and real-room echo/barge-in still need hands-on evaluation. Browser timings are software estimates, not acoustic measurements.

## Stable voices and non-speech rejection — 2026-09-29

- Every phrase of the first TTS model included the persona's fixed WAV plus exact transcript, rather than relying on a seed and style prompt to preserve speaker identity. Tests verify identical conditioning across different sentences and reject reference paths outside an agent directory.
- Both synthetic reference transcripts were checked with Nemotron ASR. Sara's reference is 6.88 seconds and was generated with an English/female/gentle-Arabic-accent instruction at guidance 4; Maya's reference is 6.64 seconds. Accent authenticity and timbre quality require human listening, not ASR verification.
- Silero v6 (vad-web 0.0.31, locked ONNX runtime) replaces RMS-only triggering. Browser test played 10 seconds of deterministic hum/noise/clicks followed by speech: no transcription started during the checked noise interval; the subsequent utterance started one transcription and produced the expected words.
- Standard microphone→partial/final transcript→real streamed speech test passed with the new detector. Models/WASM are served from KW, without a public CDN.

## Neutral Sara, barge-in and prerecorded cues — 2026-09-29

- Sara was regenerated as a neutral English female reference. Both personas have 19 prerecorded, reference-conditioned clips (38 total), versioned with their source transcript and reference hash.
- Real browser microphone injection while real synthesized audio was playing stopped scheduled playback after 292 ms. The old agent turn was cancelled, late audio did not restart, and the recognized "Stop please" command did not create a new LLM request. This measures the browser pipeline with a synthetic microphone; real-room echo performance remains device dependent.
- Regression tests cover cancellation before the first model token, cue category selection, suppression during user speech, immediate cue cancellation, cooldowns and disabled/closed behavior.
