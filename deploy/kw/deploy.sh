#!/usr/bin/env bash
# Register with Kuvryn Sync. Workload resources are applied only by Sync from Git.
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source "$script_dir/kw.env"
kubectl --context "$KW_CONTEXT" create namespace "$KW_NAMESPACE" --dry-run=client -o yaml | kubectl --context "$KW_CONTEXT" apply -f -
kubectl --context "$KW_CONTEXT" apply --dry-run=server -f "$script_dir/bootstrap.yaml"
kubectl --context "$KW_CONTEXT" apply -f "$script_dir/bootstrap.yaml"
kubectl --context "$KW_CONTEXT" apply --dry-run=server -f "$script_dir/sync.yaml"
kubectl --context "$KW_CONTEXT" apply -f "$script_dir/sync.yaml"
kubectl --context "$KW_CONTEXT" -n "$KW_NAMESPACE" wait --for=condition=Ready repository/talking-agent --timeout=180s
kubectl --context "$KW_CONTEXT" -n "$KW_NAMESPACE" wait --for=condition=Ready application/talking-agent --timeout=300s
kubectl --context "$KW_CONTEXT" -n "$KW_NAMESPACE" get applications,repositories,deployments,ingresses
curl --fail --show-error --silent --retry 5 --retry-delay 2 --retry-all-errors --max-time 30 --cacert "$KW_CA_FILE" "https://$KW_HOST/api/health"
printf '\nDemo URL: https://%s\n' "$KW_HOST"
