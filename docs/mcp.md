# MCP tools

Any Model Context Protocol server can be attached to agents. The server's tools appear to the model next to the agent's own skills, named `mcp.<server>.<tool>` (on the wire `mcp__<server>__<tool>`). Calls run directly, emit the usual `tool.started` and `tool.completed` or `tool.failed` events, show in the trace and trigger the same voice cues. Tool failures (tool errors, JSON-RPC errors, timeouts, unreachable servers) come back to the model as tool results so it can recover; they never abort the turn.

## Implementation

`internal/mcp` builds on the `mcp` package of go-ai-sdk (client, JSON-RPC, Streamable HTTP transport, `tools/list` pagination). It adds:

- a legacy HTTP+SSE transport (`transport: sse`), used when `transport` is `auto` and the Streamable HTTP POST is answered with 400, 404 or 405;
- a registry, a per-server connection with cached tool lists, and per-agent access control;
- result shaping: text parts are joined, binary parts are omitted, and output beyond 8000 characters is truncated with a note.

stdio servers are not supported (the container has no Node or npx).

Tool lists are cached for 5 minutes. A failing server is not retried for 30 seconds. Connect, initialize and list share a 10 second budget; each `tools/call` has 30 seconds. A transport failure or expired session reconnects once.

Access is checked again at call time: the agent must be allowed, the server enabled, and the tool permitted by the allow and deny lists, whatever the model asked for.

## Registry

Servers come from two places. Defaults are built in (the Z.ai presets below) or provided as JSON in `MCP_DEFAULT_SERVERS`, for example from a Secret. A JSON file edited through the API overrides them by `id`: `MCP_SERVERS_FILE` (default `var/mcp-servers.json`, `/app/var/mcp-servers.json` on KW, on the PVC). Deleting a default writes a tombstone to the file.

| Field         | Meaning                                                      |
| ------------- | ------------------------------------------------------------ |
| `id`          | 1-40 characters: lowercase letters, digits, single hyphens   |
| `name`        | Display name                                                 |
| `url`         | http or https endpoint                                       |
| `transport`   | `auto` (default), `http` (Streamable HTTP) or `sse` (legacy) |
| `headers`     | Sent on every request. Values may contain `${VAR}`           |
| `enabled`     | Disabled servers are never contacted                         |
| `agents`      | Agent ids allowed to use the server, or `["*"]`              |
| `allow_tools` | Optional. Only these server tool names are offered           |
| `deny_tools`  | Optional. These server tool names are never offered          |

`${VAR}` references are expanded from the process environment when connecting, so secrets live in a Kubernetes Secret and not in the file. To stop a registry editor from sending arbitrary process secrets to a server of their choosing, only `MCP_*`, `ZAI_API_KEY` and the comma-separated names in `MCP_ENV_ALLOW` may be referenced. A server with an unset variable shows the status `unconfigured` and is not contacted.

URLs are not filtered: MCP servers legitimately live on private addresses. Anyone who can reach the API can point the app at any http(s) host, and the app has no login, so keep the API on a trusted network.

## API

All mutating methods apply the same-origin rule as other writes (`Origin` must match the host).

- `GET /api/mcp/servers`: servers with `status` (`state` ok, error, disabled, unconfigured, unsupported or unknown; `tools`; `transport`; `error`).
- `POST /api/mcp/servers`: create (201). `PUT /api/mcp/servers/{id}`: replace. `DELETE /api/mcp/servers/{id}`.
- `POST /api/mcp/servers/{id}/test`: connect fresh and list tools.
- `GET /api/mcp/servers/{id}/tools`: tools with their JSON Schema (cached).

Header values are never returned. References such as `Bearer ${ZAI_API_KEY}` are shown as written; literal values come back as `********`. Send `********` unchanged on `PUT` to keep the stored value.

## Settings UI

Settings has a **Tools (MCP)** section (`web/src/mcp-settings.tsx`): servers with state and tool count, add and edit (name, URL, transport, headers with masked values, agents, enabled), delete, test connection and a tool list.

## Z.ai presets

The same servers Claude Code uses, enabled for the `assistant` agent and authenticated with `Authorization: Bearer ${ZAI_API_KEY}`:

| id                 | URL                                             | Transport                    |
| ------------------ | ----------------------------------------------- | ---------------------------- |
| `web-search-prime` | `https://api.z.ai/api/mcp/web_search_prime/mcp` | Streamable HTTP              |
| `web-reader`       | `https://api.z.ai/api/mcp/web_reader/mcp`       | Streamable HTTP              |
| `zread`            | `https://api.z.ai/api/mcp/zread/mcp`            | Streamable HTTP              |
| `zai-mcp-server`   | `npx -y @z_ai/mcp-server` (vision)              | stdio: listed as unsupported |

KW reads the key from Secret `enterprise-ai-demo/zai-mcp`, key `ZAI_API_KEY` (optional reference in `deploy/kw/manifests/resources.yaml`). Create or rotate it without writing the value anywhere:

```sh
read -rs ZAI_KEY   # paste the key; nothing is echoed
kubectl --context kw -n enterprise-ai-demo create secret generic zai-mcp \
  --from-literal=ZAI_API_KEY="$ZAI_KEY" --dry-run=client -o yaml | kubectl --context kw apply -f -
unset ZAI_KEY
```

Restart the Deployment afterwards so the new value is picked up.

## The assistant agent

`agents/assistant` is Nova, an open general assistant. `scope_policy: false` in its `agent.yaml` opts it out of the shared role-scope policy; the shared emergency safety rules still apply. It has its own persistent NovaMem scope (`assistant-demo` / `assistant`, users U001-U003) with `auto_capture`. Provision the identities once before the first deploy:

```sh
python3 deploy/kw/provision-memory.py --scopes-file deploy/kw/assistant-scopes.json
```
