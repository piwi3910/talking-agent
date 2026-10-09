#!/usr/bin/env bash
# Runs inside the Playwright container of the throwaway pod.
set -uo pipefail
mkdir -p /work && cd /work
npm init -y >/dev/null 2>&1
npm i playwright-core@1.63.0 >/dev/null 2>&1 || {
	echo "npm install failed"
	touch /out/FAILED
	sleep 900
	exit 1
}
(apt-get update -qq && apt-get install -y -qq python3-numpy python3-scipy) >/dev/null 2>&1 || echo "apt install of numpy/scipy failed"
cp /scripts/harness.mjs /work/harness.mjs
for i in $(seq 1 120); do
	curl -sf http://localhost:8080/api/health >/dev/null && break
	sleep 1
done
curl -s http://localhost:8080/api/voice
echo
node /work/harness.mjs 2>&1 | tee /out/harness.log
python3 /scripts/analyze.py /out 2>&1 | tee /out/analysis.log
touch /out/DONE
sleep 900
