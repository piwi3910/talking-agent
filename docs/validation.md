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
