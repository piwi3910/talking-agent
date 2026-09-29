#!/usr/bin/env python3
"""Reuse existing KW BuildKit TLS and Nexus credentials without printing secrets."""
import base64
import json
import os
from pathlib import Path
import subprocess


def secret(namespace, name):
    return json.loads(subprocess.check_output([
        "kubectl", "--context", "kw", "-n", namespace, "get", "secret", name, "-o", "json"
    ]))["data"]


def private_file(path, content):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "wb") as handle:
        handle.write(content)


root = Path.home() / ".config/buildkit/kw"
tls = secret("buildkit", "buildkit-client-tls")
for key in ("ca.crt", "tls.crt", "tls.key"):
    private_file(root / key, base64.b64decode(tls[key]))
registry = json.loads(base64.b64decode(secret("novaforge", "nexus-pull")[".dockerconfigjson"]))
auth = next((v for k, v in registry.get("auths", {}).items() if k in ("192.168.10.131", "192.168.10.131:5000")), None)
if auth is None:
    raise SystemExit("The existing Nexus secret has no credential for the KW registry")
private_file(root / "docker/config.json", json.dumps({"auths": {"192.168.10.131:5000": auth}}).encode())
print("KW build client configured from existing cluster secrets; credentials remain outside the repository.")
