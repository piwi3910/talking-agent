# KW deployment through Kuvryn Sync

Application: **https://agent.kw.watteel.lab**
Sync console: **https://sync.kw.watteel.lab**
Namespace: `enterprise-ai-demo` · Application/Repository: `talking-agent`

The `kw` kubectl context is configured against `https://192.168.10.102:6443`. Its credentials are in `~/.kube/config` with mode 0600, outside this repository. `kubectl` and `buildctl` are installed in `/usr/local/bin`. No SSH password is stored by these scripts.

## Delivery flow

1. Build and publish an ARM64 image on KW's BuildKit service:
   ```sh
   KW_TAG=your-unique-release-tag ./deploy/kw/build.sh
   ```
   The script prints the immutable image reference for deployment.
2. Update the Deployment image in `manifests/resources.yaml` to that digest. Update `kw.env` to match for operator reference.
3. Commit the source and manifests, then publish to the existing repository:
   ```sh
   ./deploy/kw/push-source.sh
   ```
4. Sync automatically polls `main` every minute and reconciles `deploy/kw/manifests`. No direct workload apply is needed. For initial registration or bootstrap updates:
   ```sh
   ./deploy/kw/deploy.sh
   ```
5. Verify the intended Git revision is deployed:
   ```sh
   kubectl --context kw -n enterprise-ai-demo get applications,repositories
   kubectl --context kw -n enterprise-ai-demo get application talking-agent \
     -o jsonpath='{.status.deployedRevision}{"\n"}'
   curl --cacert "$HOME/.config/buildkit/kw/ca.crt" \
     https://agent.kw.watteel.lab/api/health
   ```

`bootstrap.yaml` creates namespace-scoped deployer permissions. `sync.yaml` registers the source and Application, with adoption, automatic sync, pruning, self-healing, and rollback on failure. The controller manages Deployment, Service, Ingress and Certificate resources from Git. Deleting the Application defaults to orphaning its managed resources rather than deleting them.

## Existing infrastructure and credentials

- BuildKit: `192.168.10.130:1234`, TLS client authentication; reused secret `buildkit/buildkit-client-tls`.
- Nexus: publish on `192.168.10.131:5000`, pull through `192.168.10.131`; reused credential `novaforge/nexus-pull`.
- Git pushes: reused `sera/sera-sync-git`; the helper refuses an unexpected remote URL and does not persist the token.
- Ingress: `nginx`, address `192.168.10.120`. Existing DNS resolves `*.kw.watteel.lab` there.
- Certificates: existing `cluster-ca` ClusterIssuer, with the app's `talking-agent-tls` Certificate managed by Sync.

`configure-build-client.py` refreshes credentials into `~/.config/buildkit/kw/` with private file permissions. It never prints secret values or creates new accounts. The workload uses the node's existing registry trust and anonymous pull configuration, so no extra pull secret is necessary.

## Phase 1 deployment behavior

The API serves the built React console and starts its private HTTP mock backend inside the same pod. One replica and `Recreate` updates reflect the current process-local sessions and mock backend state; an update resets conversations and mock fixtures and briefly interrupts service. Acknowledged NovaMem facts persist. The pod runs non-root with a read-only root filesystem and without a Kubernetes API token. nginx buffering is disabled for SSE.

The deployed LLM uses the KW FastLLM gateway at `http://fastllm-proxy.fastllm.svc.cluster.local/v1` and model `qwen3-6-35b-a3b` (Qwen3.6-35B-A3B). `LLM_API_KEY` is injected from the existing namespace-local `fastllm-api` Secret, not from Git. Model availability depends on the shared gateway upstream. Memory uses live NovaMem with one account per complete identity scope. Run `python3 deploy/kw/provision-memory.py` to provision missing fictional users; scoped tokens remain in the `novamem-identities` Secret. See [NovaMem integration](../../docs/novamem.md).

The existing three scripted browser scenario tests are designed for offline mode and assert deterministic wording. For the live model, verify actual tool events and grounded answers through the same HTTP/SSE API rather than expecting those exact phrases.

## Browser verification

```sh
DEMO_BASE_URL=https://agent.kw.watteel.lab \
DEMO_IGNORE_HTTPS_ERRORS=true LIVE_MODEL_TESTS=true npm --prefix web run test:e2e -- tests/live.spec.ts
```

The browser override accommodates the lab CA in an isolated test browser. Separately verify the real certificate with the CA-pinned curl command above. These tests modify fictional data; use a fresh deployment when reproducing the original scenarios.

## Phone settings and SIP

The `/settings` page assigns numbers and optional demo accounts per persona. Settings persist on the `phone-settings` PVC at `/app/var/phone-settings.json`, even when a new image restarts the pod. The namespace-scoped Sync bootstrap Role includes PVC management. See [Hello SIP integration](../sip/README.md) for listener networking and voice approvals.
