#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir/../.."
expected=https://github.com/piwi3910/talking-agent.git
if [[ "$(git remote get-url --push origin)" != "$expected" ]]; then
  printf 'Refusing to send the KW credential to an unexpected remote.\n' >&2
  exit 1
fi
GIT_ASKPASS="$script_dir/git-askpass.py" GIT_TERMINAL_PROMPT=0 git -c credential.helper= push origin HEAD:main
