#!/usr/bin/env bash
set -euo pipefail
mkdir -p /work
cd /work
npm init -y >/dev/null 2>&1
npm i playwright-core@1.63.0 >/dev/null 2>&1
cp /scripts/harness.mjs /work/harness.mjs
node /work/harness.mjs 2>&1 | tee /out/harness.log
touch /out/DONE
sleep 900
