#!/usr/bin/env bash
# Usage: analyze.sh <results-dir>
# Re-runs analyze.py on already recorded results inside a throwaway pod on KW
# (nothing runs on the workstation) and copies report.md, summary.json and wav/ back.
set -euo pipefail
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
dir="$(cd -- "${1:?results dir}" && pwd)"
k="kubectl --context kw -n enterprise-ai-demo"
name="e2e-analyze-$RANDOM"
trap '$k delete pod "$name" --wait=false >/dev/null 2>&1 || true' EXIT
$k run "$name" --image=mcr.microsoft.com/playwright:v1.63.0-noble --restart=Never --command -- sleep 1500
$k wait --for=condition=Ready pod/"$name" --timeout=300s
$k exec "$name" -- bash -c 'apt-get update -qq && apt-get install -y -qq python3-numpy python3-scipy >/dev/null && mkdir -p /out /w'
tar -C "$dir" --exclude=wav -cf - . | $k exec -i "$name" -- tar -C /out -xf -
$k cp "$here/analyze.py" "$name:/w/analyze.py"
$k exec "$name" -- python3 /w/analyze.py /out
$k exec "$name" -- tar -C /out -cf - report.md summary.json wav | tar -C "$dir" -xf -
