# Enterprise AI Agent Demo

A working Go + React/TypeScript text demonstration of **one AI platform, many industries, any frontend**. Nova Telecom support and Crescent Hospital patient services use the same runtime, streaming API, skill activation, HTTP tool execution, session management, and memory interface.

The default provider is an explicitly labeled **offline scripted demo**. It exercises real tools and stateful mock services without an inference server. Connect an OpenAI-compatible endpoint for open-ended AI conversation. No model is hard-coded.

## Deploy to the KW cluster

Deploy through **Kuvryn Sync** at [agent.kw.watteel.lab](https://agent.kw.watteel.lab). The [KW deployment guide](deploy/kw/README.md) documents the configured `kw` context, existing BuildKit/Nexus services, reused cluster secrets, and Git-driven deployment flow. Desired resources live in `deploy/kw/manifests`; Sync reconciles the `main` branch. Cluster credentials are not stored in this repository.

## Run locally

Requires Go 1.23+ and Node.js 22+ (Node 24 recommended for the supplied lockfile).

```sh
npm --prefix web ci
npm --prefix web run build
go run ./cmd/server
```

Open **http://127.0.0.1:8080**. The server automatically starts a private mock backend over HTTP. Select an agent and identity, then click **Start session**. Start with Telecom / C001 and “My internet upstairs is terrible again.”

For frontend development, run the Go server and `npm --prefix web run dev` in separate terminals, then open http://127.0.0.1:5173. Vite proxies `/api` to Go.

Alternatively:

```sh
docker compose up --build
```

Compose runs the API and mock backend in separate containers. Only the console/API port is published, on loopback. Inside a container, `localhost` is the container; use a reachable inference service URL when configuring an external LLM.

## Connect an LLM

```sh
export LLM_PROVIDER=openai-compatible
export LLM_BASE_URL=http://localhost:8000/v1
export LLM_MODEL=your-served-model-name
# Optional for local endpoints, required for authenticated endpoints:
export LLM_API_KEY=your-key
go run ./cmd/server
```

The adapter uses streaming `/chat/completions`, function calling, usage reporting, cancellation, a 60-second request timeout, and limited pre-stream retries for HTTP 429/503. Endpoints must support streaming tool calls and `stream_options.include_usage`. The model must support function calling. Domain tool names such as `wifi.diagnostics` map to wire-safe names such as `wifi__diagnostics`.

The implementation follows the [official OpenAI function-calling and streaming format](https://developers.openai.com/api/docs/guides/function-calling); no OpenAI SDK or fixed model is required. vLLM, SGLang, a relay, or a hosted compatible endpoint can supply the model. Actual compatibility still depends on that endpoint's implementation.

`.env.example` documents all settings. The Go binary reads environment variables, **not `.env` automatically**. Docker Compose reads `.env` for interpolation.

## Included

- 2 configuration-driven agents, 18 business skills, and 49 tools (28 telecom, 21 hospital).
- 20 fictional telecom customers, 20 fictional patients, and 20 doctors across five specialties.
- All T1–T8 and H1–H6 scenarios, relative-date appointment calendars, insurance, facilities, referrals, and prescription fulfillment status.
- Stateful mutations with exact-action operator confirmation, ownership checks, atomic slot allocation, and no automatic mutation retries.
- Relevant memory retrieval, asynchronous allowlisted fact extraction, prior-resolution memory, and isolation across tenant / organization / agent / namespace / user.
- Separate session history, bounded tool loops, timeout/cancellation, structured errors/logs, SSE event replay, and live tool/memory/LLM telemetry.
- Deterministic hospital emergency handling before model/tool execution; patient services only.

## NovaMem status

`MemoryProvider` and `NovaMemProvider` are first-class integration boundaries. **A live NovaMem API contract was not supplied. No NovaMem HTTP API has been invented.** `MEMORY_PROVIDER=novamem` fails explicitly until a real adapter is wired in.

Development uses `InMemoryProvider`, clearly labeled in the console. Memories survive **new sessions within the running process**, but reset when the server restarts. Telecom C001 starts with three fixture memories demonstrating recurring upstairs interference. Hospital preferences are created through conversation. This is not durable NovaMem persistence yet. See [NovaMem integration](docs/novamem.md).

## Verify

```sh
go test ./... -timeout 60s
go vet ./...
# With a C compiler available:
go test -race ./... -timeout 120s
npm --prefix web run build
# Start a fresh server on :8080 first; browser tests modify mock records.
cd web
npx playwright install --with-deps chromium
npm run test:e2e
```

Tests cover every configured tool, all telecom scenarios, memory isolation in all five scope dimensions, cross-session preferences, emergency bypass, confirmation/replay, streaming call assembly, partial-stream failures, cancellation/timeouts, ownership, conflicting bookings, insurance plans, HTTP/SSE, and browser flows.

## Guides

- [Sales demo walkthrough](docs/demo-guide.md)
- [Architecture and extension points](docs/architecture.md)
- [HTTP, SSE, and backend contracts](docs/api.md)
- [NovaMem adapter boundary](docs/novamem.md)
- [Validation record and untested integrations](docs/validation.md)

## Phase 1 boundaries

Only fictional data and trusted operator-selected identities are supported. Authentication is a demo assertion, not identity verification. Session IDs act as capabilities; this local console has no production login, authorization gateway, durable session storage, or rate limiter. Mock changes reset when their process restarts. Hospital keyword safety is intentionally conservative and not a clinical triage system. The offline provider recognizes documented phrases and IDs, rather than general natural language.

Agent files are discovered at startup; switching loaded agents requires no restart or code changes. Editing configuration files or adding an agent requires restarting the server. No voice, STT/TTS, WebRTC, avatars, engines, or production UI is implemented.
