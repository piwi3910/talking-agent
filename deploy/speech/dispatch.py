"""Sync Job: deliver this release to the external DGX host over pinned SSH."""
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

os.umask(0o077)
shutil.copyfile('/credentials/id_ed25519', '/tmp/speech-key')
base = ['ssh', '-T', '-i', '/tmp/speech-key', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=/credentials/known_hosts', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=4', 'piwi@192.168.10.246']
revision = os.environ['RELEASE_ID']
assert len(revision) == 12 and all(c in '0123456789abcdef' for c in revision)
destination = '/home/piwi/enterprise-speech/releases/' + revision
payload = io.BytesIO()
with tarfile.open(fileobj=payload, mode='w') as archive:
    for name in ['host-deploy.py', 'models.json', 'compose.json', 'breeze.json', 'nemotron.json']:
        archive.add('/release/' + name, arcname=name)
subprocess.run(base + [f'mkdir -p {destination} && tar -xf - -C {destination}'], input=payload.getvalue(), check=True)
subprocess.run(base + [f'python3 -u {destination}/host-deploy.py {destination}'], check=True)
# Prove cluster-to-DGX reachability as well as remote local health.
import json, urllib.request
for port in [8092, 8093]:
    with urllib.request.urlopen(f'http://192.168.10.246:{port}/health', timeout=10) as response:
        print(f'Cluster health {port}:', response.read().decode(), flush=True)
print('Speech release deployed and reachable from KW.', flush=True)
