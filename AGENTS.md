# KW deployment conventions

- Deploy this app through Kuvryn Sync on the KW cluster. Do not deploy to localhost as the delivery target or directly apply workload resources to bypass Sync.
- Use the existing `kw` kubectl context and the `enterprise-ai-demo` namespace. The configured API endpoint is `https://192.168.10.102:6443`.
- The application URL is `https://agent.kw.watteel.lab`; the Sync console is `https://sync.kw.watteel.lab`.
- `deploy/kw/manifests/` is the desired workload state. Push committed changes to `main`; Sync's `talking-agent` Application reconciles that path. `deploy/kw/deploy.sh` registers the Repository/Application and namespace-scoped deployer permissions.
- Use the cluster's existing BuildKit and Nexus services. Build ARM64 images via `deploy/kw/build.sh`, publish through `192.168.10.131:5000`, and deploy immutable digests via the pull endpoint `192.168.10.131`.
- Reuse existing cluster credentials: `buildkit/buildkit-client-tls`, `novaforge/nexus-pull`, and `sera/sera-sync-git`. The provided scripts load these without printing values. Never put credentials, kubeconfig contents, private keys, or passwords in this repository.
- `kubectl` is installed at `/usr/local/bin/kubectl`; persistent credentials are in `~/.kube/config`. Build client credentials are under `~/.config/buildkit/kw/`, outside the repository.
- Verify Sync is `Healthy` and `Synced` to the intended Git revision, then verify TLS, health, and conversation/SSE at the cluster hostname.
- Keep one replica while memory, sessions, and mock state are process-local. The KW deployment uses FastLLM at `http://fastllm-proxy.fastllm.svc.cluster.local/v1` with model `qwen3.5-9b` (Qwen3.5-9B). Its API key comes from `enterprise-ai-demo/fastllm-api`, key `LLM_API_KEY`. Memory is still the development provider. Local runs default to offline/scripted mode.
