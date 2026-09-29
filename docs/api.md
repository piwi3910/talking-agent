# Phase 1 contracts

The frontend is one consumer of the API. All commands are JSON; responses use standard HTTP errors with `{ "error": "..." }`. The identity picker is trusted demo input, not authentication.

| Endpoint | Purpose |
|---|---|
| `GET /api/health` | API liveness and active memory provider |
| `GET /api/agents` | Loaded configurations, skills, users, and provider labels |
| `POST /api/sessions` | `{ "agent_id": "telecom-support", "user_id": "C001" }` → `201 { "id": "..." }` |
| `POST /api/sessions/{id}/messages` | `{ "message": "My internet upstairs is terrible again." }` → `202 { "turn_id": "..." }` |
| `POST /api/sessions/{id}/messages` | `{ "confirmation": "proposal-id" }` executes the exact saved action; add `"reject": true` to cancel |
| `POST /api/sessions/{id}/cancel` | Cancels the running turn |
| `GET /api/sessions/{id}/events` | SSE stream; `Last-Event-ID` or `?after=N` replays retained events |

Maximum message size is 8,000 bytes, request body 32 KiB. Unknown fields are rejected. Only one running turn is allowed per session; conflicts return 409. Starting or switching sessions does not change the model's trusted user identity for existing sessions. Cross-origin browser writes are rejected; Vite uses a same-origin proxy.

## Event envelope

```json
{
  "version": 1,
  "id": 12,
  "type": "tool.completed",
  "session_id": "opaque-session-id",
  "turn_id": "opaque-turn-id",
  "time": "2026-09-29T09:30:00Z",
  "data": {
    "tool": "wifi.diagnostics",
    "duration_ms": 83,
    "result": { "summary": "High interference on channel 6", "records": [] }
  }
}
```

SSE uses default `message` events with JSON envelopes and numeric `id:` fields. Heartbeat comments occur every 10 seconds. The connection persists across turns so asynchronous memory events remain visible after an answer. Disconnecting SSE does not cancel a turn; use the cancellation endpoint. Clients should deduplicate by sequence ID. Replay is bounded to the newest 1,000 events, not a durable audit log; a client older than that window must start a new session for a full trace.

Implemented event types:

- `session.started`, `turn.started`, `turn.completed`
- `memory.retrieval.started`, `.completed`, `.failed`
- `memory.store.started`, `.completed`, `.failed`
- `skill.activated`
- `llm.started`, `llm.first_token`, `llm.completed`, `llm.failed`
- `tool.started`, `tool.completed`, `tool.failed`
- `action.confirmation.required`
- `safety.blocked`, `agent.error`
- `agent.response.delta`, `agent.response.completed`
- `agent.response.recovered` — an empty final generation was replaced by verified current-turn tool results
- `agent.response.grounded` — a confirmed mutation or configured read was rendered directly from its successful backend response

`llm.first_token` measures time to first emitted text for each model invocation. Tool-only responses have no text TTFT event. Usage contains provider-reported token counts; the offline provider reports none. `agent.response.delta` is append-only visible text, potentially including model preambles before tools. `agent.response.completed` marks a completed response, while `turn.completed` releases the UI send state even on errors or cancellation.

Future audio (`speech.*`, `vad.*`, `stt.*`, `tts.*`, `barge_in`) and avatar (`avatar.state`, `avatar.expression`, `avatar.viseme`) types can use the same envelope. Consumers should ignore unfamiliar types. No audio events are emitted in this phase.

## Enterprise backend adapter

`GET /users/{industry}` returns a list of typed identity records for the picker. `POST /tools/execute` receives:

```json
{
  "industry": "telecom",
  "user_id": "C001",
  "name": "wifi.optimize",
  "arguments": { "channel": "11" }
}
```

`industry` and `user_id` are attached by the runtime, not supplied by the LLM. Each tool has its own closed input schema in `skills.yaml`; the catalog is also visible through `GET /api/agents`. A `Result` contains `summary`, typed `records`, and optionally `error: { code, message, retryable }`. `Record` has explicit ID, kind, name, status, description, user/related IDs, specialty, location, start, amount/currency, and value/unit fields. Empty results use an empty array. Ownership, known IDs, account state, insurance plans, date ranges, and appointment conflicts are validated by backend operations.

The standalone mock service (`go run ./cmd/mock-backend`) listens on loopback `:8081` by default. Set `MOCK_BACKEND_URL` on the API to use it. Each agent can override this via its `backend.url` or configured `backend.url_env` variable. It is a trusted internal demo service, with no public authentication. Do not expose it as a public enterprise integration.

LLM completion and failure events include `diagnostics.generations`, `diagnostics.reasoning_characters`, and `diagnostics.finish_reason`. Only counts are exposed; reasoning text is never sent to the frontend or reused as a user-facing answer. Failed generations include their reported token usage. A recovered response still records `llm.failed`, followed by `agent.response.recovered` and normal response/turn completion, without `agent.error`.
