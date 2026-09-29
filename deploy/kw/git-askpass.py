#!/usr/bin/env python3
"""Git-only credential helper using the existing cluster Git credential."""
import base64
import json
import subprocess
import sys

secret = json.loads(subprocess.check_output([
    "kubectl", "--context", "kw", "-n", "sera", "get", "secret", "sera-sync-git", "-o", "json"
]))["data"]
field = "username" if "username" in sys.argv[1].lower() else "token"
print(base64.b64decode(secret[field]).decode().strip())
