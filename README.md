# Enterprise AI Agent Demo

A working Go + React/TypeScript chat and voice demonstration of **one AI platform, many industries, any frontend**. Nova Telecom support, Crescent Hospital patient services, and Willowbrook School admissions/reception use the same runtime, streaming API, skill activation, HTTP tool execution, session management, and memory interface.

The default provider for local runs is an explicitly labeled **offline scripted demo**. It exercises real tools and stateful mock services without an inference server. Connect an OpenAI-compatible endpoint for open-ended AI conversation. No model is hard-coded.

## Deploy to the KW cluster

Deploy through **Kuvryn Sync** at [agent.kw.watteel.lab](https://agent.kw.watteel.lab). The [KW deployment guide](deploy/kw/README.md) documents the configured `kw` context, existing BuildKit/Nexus services, reused cluster secrets, and Git-driven deployment flow. Desired resources live in `deploy/kw/manifests`; Sync reconciles the `main` branch. KW uses the FastLLM gateway with **Qwen3.6-35B-A3B** (`qwen3-6-35b-a3b`) and a Kubernetes Secret for its API key. Cluster credentials are not stored in this repository.

## Run locally

Requires Go 1.26+ and Node.js 22+ (Node 24 recommended for the supplied lockfile).

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

The implementation follows the [official OpenAI function-calling and streaming format](https://developers.openai.com/api/docs/guides/function-calling); the provider adapter uses go-ai-sdk without a fixed model. vLLM, SGLang, a relay, or a hosted compatible endpoint can supply the model. Actual compatibility still depends on that endpoint's implementation.

`.env.example` documents all settings. The Go binary reads environment variables, **not `.env` automatically**. Docker Compose reads `.env` for interpolation.

## Included

- 3 configuration-driven agents, 23 business skills, and 60 tools (28 telecom, 21 hospital, 11 school).
- 20 fictional telecom customers, 20 school families, 20 fictional patients, and 20 doctors across five specialties.
- All T1–T8 and H1–H6 scenarios, relative-date appointment calendars, insurance, facilities, referrals, and prescription fulfillment status.
- Stateful mutations with exact-action operator confirmation, ownership checks, atomic slot allocation, and no automatic mutation retries. Successful changes are acknowledged directly from backend records.
- Calendar results rendered with exact UTC dates and slot IDs; empty model explanations after successful tools can recover from verified service results.
- Relevant memory retrieval, asynchronous allowlisted fact extraction, prior-resolution memory, and isolation across tenant / organization / agent / namespace / user.
- Separate session history, bounded tool loops, timeout/cancellation, structured errors/logs, SSE event replay, and live tool/memory/LLM telemetry.
- Deterministic hospital emergency handling before model/tool execution; patient services only.

## NovaMem status

KW now uses **live NovaMem** through its official Go client. Each complete tenant / organization / agent / namespace / user scope has a separate NovaMem account and token. Relevant facts are retrieved before LLM turns; allowlisted preferences and resolutions are stored asynchronously. Acknowledged memories persist across sessions and app restarts.

Local development still defaults to `InMemoryProvider`, which resets on restart. Session history and mock backend state remain process-local in both modes. See [NovaMem integration and provisioning](docs/novamem.md).

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

Agent files are discovered at startup; switching loaded agents requires no restart or code changes. Editing configuration files or adding an agent requires restarting the server. Phase 2 now adds browser voice and STT/TTS; WebRTC, avatars, engines, and production UI remain out of scope.

The live LLM adapter uses `github.com/azrtydxb/go-ai-sdk` v0.6.0 with its OpenAI-compatible provider. Go 1.26+ is required. Endpoint, model and credential environment variable remain configurable; the platform runtime owns tool execution and telemetry.

For the live KW hospital flow (uses fictional P018 and ends by cancelling the test appointment):

```sh
DEMO_BASE_URL=https://agent.kw.watteel.lab DEMO_IGNORE_HTTPS_ERRORS=true LIVE_MODEL_TESTS=true npm --prefix web run test:e2e -- tests/live.spec.ts
```

The live test checks exact calendar dates and slot IDs, booking, rescheduling, cancellation, and operator confirmation. The original `demo.spec.ts` suite targets the offline scripted provider.

## Browser voice (Phase 2)

The KW demo now supports live microphone transcription (Nemotron 3.5 ASR), streaming spoken replies (Qwen3-TTS), interruption, and live latency metrics. Start a chat, then select **Start voice**. The LLM uses `qwen3-6-35b-a3b` through the existing gateway and Go AI SDK, with thinking disabled. See [voice controls, architecture and limitations](docs/voice.md).

## School admissions and reception

Select **Willowbrook School** in the Personas tab to talk with Emma. The school uses the shared streaming chat, Qwen3-TTS voice, interruption, activity metrics and confirmation workflow. Its independent configuration, prompt, knowledge, skills, default voice and NovaMem namespace live in `agents/school/`. Emma speaks with her own designed voice, and her acknowledgement cues are rendered in it.

School tools cover family profiles, programs and annual tuition, application progress, admissions enquiries, campus tour availability/booking/cancellation, school information and reception messages. Twenty fictional families include a new applicant (F001) and an application awaiting documents (F002). Weekday tour slots are generated relative to startup; booking is atomic and cancellation checks family ownership. Demo records reset on rollout; NovaMem preferences persist. No real admission decisions, messages to school staff, document uploads or collection authorizations occur.

Before deploying new identities, provision memory using `python3 deploy/kw/provision-memory.py --scopes-file <scopes.json>` with an array of tenant/organization/domain/namespace/user objects. This reuses existing cluster credentials without embedding secrets. School live UI verification: `SCHOOL_LIVE_TESTS=true DEMO_BASE_URL=https://agent.kw.watteel.lab npx playwright test tests/school.spec.ts` from `web/`.

## The Aquila School demo personas

Three sales-demo personas for The Aquila School, Dubai live in `agents/aquila-*`: Amelia (admissions, inbound), Noor (reception, inbound) and Sophie (admissions outreach, outbound). They set `memory.domain` to `aquila-school`, so all three share one NovaMem scope per family and remember each other's conversations. Seven fictional contacts (`mock/aquila/contacts.json`) come with seeded multi-session history (`agents/aquila-admissions/memory-seeds.json`), and the mock backend simulates tours, CAT4 and meet-and-greet bookings, a fee quote engine on the real 2026-27 fees and discounts, absences, buses, clubs and outreach outcomes. Personas with `persona.opening` speak first through `POST /api/sessions/{id}/open` (and on SIP calls instead of a static greeting). Provision memory before deploying with `python3 deploy/kw/provision-memory.py --scopes-file deploy/kw/aquila-scopes.json`. See [the Aquila demo script](docs/aquila-demo.md).

## SIP phones and Hello PBX

The optional SIP/RTP endpoint routes configurable phone numbers to agents and creates an independent conversation per call. See [Hello integration and deployment configuration](deploy/sip/README.md).
