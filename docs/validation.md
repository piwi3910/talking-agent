# Validation record

Verified in the workspace on 2026-09-29 with Go 1.27.1 (Linux arm64) and Node.js 24.21.0:

- Go unit/integration tests, including the race detector.
- `go vet ./...`.
- TypeScript checking and the Vite production build.
- Three Chromium browser tests against the production build: Telecom recurring Wi-Fi and confirmed optimization; hospital preference across new sessions and confirmed booking; deterministic emergency response without tools, including a mobile viewport.
- All 49 configured tools have backend execution coverage. Other tests cover each memory scope dimension, multi-call handling, bounded loops, rejected cross-domain tools and identities, confirmation replay, conflicting bookings, cancellation/timeouts, streaming fragments, and HTTP/SSE events.

The browser suite modifies mock state; restart the server (or separate mock process) for the original sales scenarios.

Not exercised against external services: a live inference endpoint and live NovaMem. The OpenAI-compatible client is tested against controlled HTTP/SSE fixtures. NovaMem intentionally has no fabricated wire implementation. The Dockerfile has also been built successfully with KW’s ARM64 BuildKit service and the image published to Nexus. Docker Compose itself has not been exercised.
