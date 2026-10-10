#!/usr/bin/env bash
# Run adversarial conversations against the live KW ingress from an in-cluster browser.
set -euo pipefail
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
k="kubectl --context kw -n enterprise-ai-demo"
name="adversarial-$(date +%s)"
agents="${AGENTS:-}"
seed="${SEED:-$(date +%s)}"
run_id="$(date +%Y%m%d-%H%M%S)-$$"
dest="$here/results/$run_id"
mkdir -p "$dest"
cleanup() { $k delete pod "$name" --wait=false >/dev/null 2>&1 || true; $k delete configmap "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT
$k create configmap "$name" --from-file="$here/harness.mjs" --from-file="$here/runner.sh"
sed -e "s|__NAME__|$name|g" -e "s|__AGENTS__|$agents|g" -e "s|__SEED__|$seed|g" "$here/pod.yaml" | $k apply -f -
for _ in $(seq 1 1800); do
  if $k exec "$name" -- test -f /out/DONE >/dev/null 2>&1; then break; fi
  status="$($k get pod "$name" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  [[ "$status" == Failed ]] && { $k logs "$name"; exit 1; }
  sleep 5
done
$k exec "$name" -- tar -C /out -cf - . | tar -C "$dest" -xf -
$k logs "$name" > "$dest/pod.log" 2>&1
if [[ -z "$agents" ]]; then cp "$dest/REPORT.md" "$here/REPORT.md"; fi
printf 'Results: %s\nReport: %s/REPORT.md\n' "$dest" "$here"
