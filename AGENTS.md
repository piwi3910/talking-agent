# KW deployment conventions

- Deploy this app through Kuvryn Sync on the KW cluster. Do not deploy to localhost as the delivery target or directly apply workload resources to bypass Sync.
- Use the existing `kw` kubectl context and the `enterprise-ai-demo` namespace. The configured API endpoint is `https://192.168.10.102:6443`.
- The application URL is `https://agent.kw.watteel.lab`; the Sync console is `https://sync.kw.watteel.lab`.
- `deploy/kw/manifests/` is the desired workload state. Push committed changes to `main`; Sync's `talking-agent` Application reconciles that path. `deploy/kw/deploy.sh` registers the Repository/Application and namespace-scoped deployer permissions.
- Use the cluster's existing BuildKit and Nexus services. Build ARM64 images via `deploy/kw/build.sh`, publish through `192.168.10.131:5000`, and deploy immutable digests via the pull endpoint `192.168.10.131`.
- Reuse existing cluster credentials: `buildkit/buildkit-client-tls`, `novaforge/nexus-pull`, and `sera/sera-sync-git`. The provided scripts load these without printing values. Never put credentials, kubeconfig contents, private keys, or passwords in this repository.
- `kubectl` is installed at `/usr/local/bin/kubectl`; persistent credentials are in `~/.kube/config`. Build client credentials are under `~/.config/buildkit/kw/`, outside the repository.
- Verify Sync is `Healthy` and `Synced` to the intended Git revision, then verify TLS, health, and conversation/SSE at the cluster hostname.
- Keep one replica while sessions and mock state are process-local. The KW deployment uses FastLLM at `http://fastllm-proxy.fastllm.svc.cluster.local/v1` with model `qwen3-6-35b-a3b` (Qwen3.6-35B-A3B), with `LLM_DISABLE_THINKING=true`. Its API key comes from `enterprise-ai-demo/fastllm-api`, key `LLM_API_KEY`. KW memory uses live NovaMem with per-scope credentials in `enterprise-ai-demo/novamem-identities`. Provision missing identities with `deploy/kw/provision-memory.py`; never mount NovaMem admin credentials in the app. Local runs default to offline/scripted mode.

- The LLM adapter uses `github.com/azrtydxb/go-ai-sdk` (version pinned in go.mod; bumped by the go-ai-sdk-bump workflow). Build with Go 1.26+. Preserve the SDK integration when changing providers.

- Sync pruning is disabled for this Application because generated Service EndpointSlices inherit Sync labels and are otherwise repeatedly pruned. Use explicit reviewed removal of obsolete resources until Sync excludes controller-owned children. Speech endpoints are `speech-stt` (port 8093) and `speech-tts` (port 8092) in this namespace: ExternalName aliases for Kuvryn-managed audio.cpp workers in `kuvryn-ai-workloads` on the DGX nodes (see `deploy/speech/README.md`). The browser reaches them only through the app.
