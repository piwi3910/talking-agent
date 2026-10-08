# MCP tools and phone audio

## MCP client

An agent can call tools on Model Context Protocol servers (Streamable HTTP; the
SDK negotiates 2026-07-28 and falls back to 2025-11-25). Configure per agent in
`agent.yaml`; secrets are read from environment variables only:

```yaml
mcp:
  - name: crm                      # no "." "_" ":" or spaces
    url: https://crm.example/mcp   # or url_env: CRM_MCP_URL
    allow_tools: [find_customer, create_ticket]   # deny by default
    auth:
      type: client_credentials     # none | bearer | client_credentials
      token_url: https://idp.example/oauth/token
      client_id: talking-agent
      client_secret_env: CRM_CLIENT_SECRET
      # bearer: token_env: CRM_TOKEN
```

Behaviour:

- Tools are offered to the model as `mcp.<server>.<tool>` next to the skills
  tools; only tools in `allow_tools` are listed or executable.
- Input schemas become the platform's closed, string-valued schema. Integers,
  numbers, booleans, arrays and objects are described in the property text and
  converted back to their real type when the call is made.
- A tool is treated as a mutation (operator approval first) unless the server
  marks it `readOnlyHint`.
- Results map to `tools.Result`: text content becomes the summary, structured
  content is used when there is no text, `isError` becomes `tool_error`.
- Discovery is cached for 5 minutes and refreshed in the background; a failing
  server is retried every 30 s and never blocks a turn after the first lookup
  (first lookup waits at most 3 s).
- `skills.yaml` tools keep working: the MCP executor wraps the agent's existing
  HTTP executor and delegates every non-MCP tool to it.

## Phone audio (`internal/audio`)

- `Resampler`: Kaiser windowed-sinc polyphase low-pass. 24 kHz TTS to 8 kHz is
  flat to 3.4 kHz and more than 60 dB down from 4.6 kHz; 8 kHz to 16 kHz for ASR
  uses the same filter instead of sample repetition. Group delay 1.5 ms.
- `VAD`: spectral voice activity detector on 10 ms frames with an adaptive
  per-band noise floor and multi-band requirement (hum, tones and steady noise
  are not speech). `captureLive` keeps the barge-in rules: 128 ms of speech
  starts a turn, 700 ms of non-speech ends it.
