# NovaMem integration

KW uses the existing NovaMem service at `http://novamem.novamem.svc.cluster.local:7778` through its official Go client, `github.com/azrtydxb/novamem/clients/go`, pinned in `go.mod`. The adapter calls the documented `POST /v1/remember` and `POST /v1/search` operations. No invented protocol or conversation-history dump is used.

`internal/memory.Provider` remains the runtime boundary. `NewNovaMem` creates the SDK adapter; the process-local provider remains available for offline development. An unconfigured NovaMem provider fails explicitly rather than falling back silently.

## Isolation

Every complete `(tenant, organization, agent/domain, namespace, user)` tuple has a separate NovaMem user account and bearer token. NovaMem namespaces are organizational shelves, not authorization boundaries, so namespace filtering alone is insufficient.

The trusted operator-selected identity determines the credential. The model cannot choose a token or memory scope. The adapter rejects missing identities, incomplete scopes, duplicate scopes and reused tokens. It also sends an explicit versioned namespace and validates returned scope metadata, namespace, source and project before any memory reaches the model.

`ScopeKey` hashes length-prefixed UTF-8 scope fields using SHA-256. Length prefixes prevent ambiguous delimiter concatenation; the five readable scope fields are preserved in entry metadata. Neither credentials nor admin sessions reach the frontend or LLM.

## Provisioning on KW

Run `python3 deploy/kw/provision-memory.py` from the configured operator workstation. It discovers the app's agents and fictional users, reuses existing scoped tokens, and provisions only missing identities through NovaMem's admin API. It uses the cluster's existing bootstrap credential for a temporary cookie session, signs out afterwards, and never gives admin access to the app.

Tokens are saved after each account creation to `enterprise-ai-demo/novamem-identities`, key `credentials.json`. They are mounted read-only in the app. They are not Git manifests or environment values printed in logs. Do not delete this Secret to reset a demo: lost tokens require deliberate credential recovery or rotation. A duplicate account with a missing token fails explicitly instead of creating another identity.

The credential file is a JSON array of `{ "scope": { "tenant", "organization", "domain", "namespace", "user" }, "token": "..." }` records. Configure:

```
MEMORY_PROVIDER=novamem
NOVAMEM_BASE_URL=http://novamem.novamem.svc.cluster.local:7778
NOVAMEM_CREDENTIALS_FILE=/run/secrets/novamem/credentials.json
```

Adding an agent or demo user requires provisioning its complete scope before using NovaMem. Changing a scope deliberately creates an isolated new memory identity; old data is not automatically migrated or deleted.

## Storage and retrieval

The bounded asynchronous worker stores allowlisted preferences, recurring issues and successful resolutions. `Remember` is used for these already-extracted facts, not NovaMem's transcript extraction. Its content-hash dedup makes fixture replay and repeated facts idempotent. Seed memories belong only to fictional Telecom C001; other preferences are learned from explicit statements.

Retrieval requests at most five facts, with keyword/vector relevance and no recency-only weighting. A result must have a keyword match or vector similarity of at least `NOVAMEM_MIN_VECTOR_SCORE` (default `0.5`, calibrated against the current KW embeddings). Weak semantic matches are excluded even when the search returns them; recalibrate this threshold if the embedding model changes. Session history remains separate. Errors and degraded retrieval are reported as unavailable, never as proof that no prior history exists. Failed writes do not emit a successful storage event.

Once acknowledged by NovaMem, facts survive app restarts. The pending write queue, conversations and mock backend state remain process-local. There is no automatic retention or bulk deletion policy in this demo; keep only fictional service facts. NovaMem's administrative deletion tools remain the operator's responsibility.

## Validation

Normal Go tests cover request scope, credential selection, every scope dimension, malicious cross-scope results, cancellation, degraded reads, rejected writes and reconstruction without local memory state.

`deploy/kw/verify-memory.sh` provisions six dedicated validation accounts, runs the opt-in live contract suite and deletes only the fact created by that run. It checks NovaMem's real deduplication, retrieval after constructing a fresh provider, and isolation across all five dimensions. Validation tokens live in a separate Secret and are not mounted in the app.

For deployment persistence, run `python3 deploy/kw/verify-memory-conversation.py store` (stores a telecom preference for C006 and waits for `memory.store.completed`), deploy a fresh app pod through Sync, then run it with `recall`. `memory.retrieval.completed` should contain the preference for C006; telecom C007 and the school agent must not receive it.

The repeatable API check is `python3 deploy/kw/verify-memory-conversation.py store`, followed by a fresh pod deployed through Sync, then `python3 deploy/kw/verify-memory-conversation.py recall`. It uses telecom C006, checks telecom C007 and a school family for leakage, and validates CA-signed HTTPS plus the NovaMem event labels. Use other identities if C007 has deliberately learned the same preference independently.
