#!/usr/bin/env bash
# Usage: tests/e2e-audio/run.sh <image-digest-ref> [label] [ctx-rate]
#   e.g. tests/e2e-audio/run.sh 192.168.10.131/enterprise-ai-demo@sha256:... before
# Runs the two-agent audio test as a throwaway Pod on the KW cluster and copies the
# results (WAVs, traces, report) to tests/e2e-audio/results/<label>/ (git-ignored).
set -euo pipefail
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
image="${1:?image reference (digest) required}"
label="${2:-run}"
rate="${3:-0}"
turns="${TURNS:-6}"
name="e2e-audio-$(echo "$label" | tr -c 'a-z0-9\n' '-' | cut -c1-20)-$RANDOM"
k="kubectl --context kw -n enterprise-ai-demo"
dest="$here/results/$label"
mkdir -p "$dest"
cleanup() {
	$k delete pod "$name" --wait=false >/dev/null 2>&1 || true
	$k delete configmap "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
$k create configmap "$name" --from-file="$here/harness.mjs" --from-file="$here/analyze.py" --from-file="$here/runner.sh"
sed -e "s|__NAME__|$name|g" -e "s|__IMAGE__|$image|g" -e "s|__TURNS__|$turns|g" -e "s|__CTX_RATE__|$rate|g" "$here/pod.yaml" | $k apply -f -
live="$($k get pods -o name | grep '^pod/enterprise-ai-demo-' | head -1)"
$k wait --for=condition=Initialized pod/"$name" --timeout=0 >/dev/null 2>&1 || true
until $k exec "$name" -c seed -- true >/dev/null 2>&1; do sleep 2; done
$k exec "$live" -- tar -C /app/var -cf - voices voice-settings.json voice-cues | $k exec -i "$name" -c seed -- tar -C /app/var -xf -
$k exec "$name" -c seed -- touch /app/var/.seeded
echo "pod $name seeded from $live; waiting for results..."
for _ in $(seq 1 240); do
	state="$($k exec "$name" -c playwright -- ls /out 2>/dev/null || true)"
	case "$(echo "$state" | tr "\n" " ")" in *" DONE "* | "DONE "*) break ;; *FAILED*)
		echo "runner failed"
		break
		;;
	esac
	sleep 5
done
$k exec "$name" -c playwright -- tar -C /out -cf - . | tar -C "$dest" -xf -
$k logs "$name" -c app --tail=40 >"$dest/app.log" 2>&1 || true
cat "$dest/analysis.log" 2>/dev/null || cat "$dest/harness.log"
echo "results in $dest"
