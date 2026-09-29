#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source "$script_dir/kw.env"
: "${KW_TAG:?Set KW_TAG to a unique immutable release tag}"
python3 "$script_dir/configure-build-client.py"
cd "$script_dir/../.."
metadata="$(mktemp)"
trap 'rm -f -- "$metadata"' EXIT
DOCKER_CONFIG="$KW_BUILD_CONFIG/docker" buildctl --addr "$KW_BUILDKIT_ADDR" \
  --tlscacert "$KW_BUILD_CONFIG/ca.crt" --tlscert "$KW_BUILD_CONFIG/tls.crt" --tlskey "$KW_BUILD_CONFIG/tls.key" \
  build --frontend dockerfile.v0 --local context=. --local dockerfile=docker \
  --opt filename=Dockerfile --opt platform=linux/arm64 \
  --output "type=image,name=$KW_REGISTRY_PUSH/enterprise-ai-demo:$KW_TAG,push=true" --metadata-file "$metadata"
python3 - "$metadata" "$KW_REGISTRY_PULL" <<'PY'
import json, sys
metadata = json.load(open(sys.argv[1]))
print('Published image for Sync manifests: ' + sys.argv[2] + '/enterprise-ai-demo@' + metadata['containerimage.digest'])
PY
