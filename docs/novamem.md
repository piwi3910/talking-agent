# NovaMem integration boundary

The live NovaMem contract is unknown in this workspace. `internal/memory` therefore exposes a provider contract and an explicit `NovaMemProvider{Adapter: ...}` wrapper, plus a development `InMemoryProvider`. Calling an unconfigured NovaMem provider returns `ErrNotConfigured`; selecting it at startup fails rather than silently substituting another store.

```go
type Provider interface {
    Retrieve(context.Context, RetrieveRequest) ([]Memory, error)
    Store(context.Context, Memory) error
    Name() string
}
```

`RetrieveRequest` includes a full `Scope`, query, and limit. `Memory` includes that scope, an extracted service fact, tags, and a creation timestamp. Scope has separate tenant, organization, domain/agent, namespace, and user fields; do not flatten them using ambiguous delimiter concatenation.

Once the actual NovaMem SDK/API and credentials are available, implement a concrete adapter, map all scope dimensions into NovaMem's supported isolation/filter mechanism, preserve contexts/timeouts, and wire it in `cmd/server`. Do not introduce a guessed `/retrieve` or `/store` endpoint. The runtime and frontend do not need to change.

Before claiming a live integration, run the same isolation suite against a disposable NovaMem namespace, verify relevance limits, write/read behavior across process restarts, and exact error semantics. Define retention/deletion and credential provisioning for the deployment separately. The current UI explicitly identifies the development provider and the lack of durable persistence.

Seed memories belong to fictional Telecom C001 only. Hospital morning preferences are learned from explicit statements. Memory writes happen on a bounded asynchronous worker; the console reports stored facts or failures. Starting a new session clears conversation history while preserving process-local facts. Restarting the process restores only seed memories.
