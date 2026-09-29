#!/usr/bin/env bash
# Run opt-in isolation tests with disposable credentials outside the working tree.
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
python3 "$script_dir/provision-memory.py" --validation
credentials="$(mktemp)"
trap 'rm -f -- "$credentials"' EXIT
kubectl --context kw -n enterprise-ai-demo get secret novamem-validation-identities -o jsonpath='{.data.credentials\.json}' | base64 -d > "$credentials"
cd "$script_dir/../.."
NOVAMEM_LIVE_CREDENTIALS="$credentials" NOVAMEM_LIVE_URL=http://novamem.novamem.svc.cluster.local:7778 \
  "${GO_BIN:-go}" test ./internal/memory -run TestNovaMemLiveIsolation -count=1 -timeout 120s -v
