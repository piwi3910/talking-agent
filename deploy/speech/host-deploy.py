"""Idempotent installer for only the enterprise-speech Compose project."""
import fcntl
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import time
import urllib.request

root = Path('/home/piwi/enterprise-speech')
release = Path(sys.argv[1]).resolve()
if release.parent != root / 'releases':
    raise SystemExit('Unexpected release directory')
root.mkdir(exist_ok=True)
lock = (root / 'deploy.lock').open('w')
fcntl.flock(lock, fcntl.LOCK_EX)
models = root / 'models'
models.mkdir(exist_ok=True)
manifest = json.loads((release / 'models.json').read_text())

def checksum(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(8 << 20), b''):
            h.update(block)
    return h.hexdigest()

for item in manifest['files']:
    destination = models / item['name']
    if destination.exists() and checksum(destination) == item['sha256']:
        print('Verified cached model:', item['name'], flush=True)
        continue
    if shutil.disk_usage(root).free < item['size'] + (12 << 30):
        raise SystemExit('Insufficient disk headroom for speech download')
    partial = destination.with_suffix('.partial')
    url = f"https://huggingface.co/{manifest['repository']}/resolve/{manifest['revision']}/{item['path']}?download=true"
    print('Downloading pinned model:', item['name'], flush=True)
    subprocess.run(['curl', '--fail', '--location', '--silent', '--show-error', '--retry', '5', '--retry-delay', '3', '--connect-timeout', '20', '--max-time', '1800', '--continue-at', '-', '--output', str(partial), url], check=True)
    if partial.stat().st_size != item['size'] or checksum(partial) != item['sha256']:
        partial.unlink()
        raise SystemExit('Model integrity verification failed; retry will download again')
    partial.chmod(0o644)
    partial.replace(destination)
    print('Verified model:', item['name'], flush=True)

compose = ['docker', 'compose', '--project-name', 'enterprise-speech', '--file', str(release / 'compose.json')]
subprocess.run(compose + ['pull'], check=True)
subprocess.run(compose + ['up', '-d', '--wait', '--wait-timeout', '300'], check=True)
for port in [8092, 8093]:
    with urllib.request.urlopen(f'http://192.168.10.246:{port}/v1/models', timeout=20) as response:
        print('Loaded models:', response.read().decode(), flush=True)
(root / 'current-release').write_text(str(release) + '\n')
subprocess.run(compose + ['ps'], check=True)
