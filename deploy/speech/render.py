"""Regenerate the versioned Sync payload and Job after editing speech sources."""
import hashlib
import json
from pathlib import Path
root = Path(__file__).resolve().parent
manifest = root.parent / 'kw/manifests/speech.json.yaml'
resources = [json.loads(doc) for doc in manifest.read_text().split('\n---\n')]
names = ['dispatch.py', 'host-deploy.py', 'models.json', 'compose.json', 'breeze.json', 'nemotron.json']
data = {name: (root / name).read_text() for name in names}
release = hashlib.sha256(json.dumps(data, sort_keys=True).encode()).hexdigest()[:12]
config, job = resources[:2]
config['metadata']['name'] = 'speech-release-' + release
config['data'] = data
job['metadata']['name'] = 'speech-deploy-' + release
pod = job['spec']['template']['spec']
pod['containers'][0]['env'][0]['value'] = release
pod['volumes'][0]['configMap']['name'] = config['metadata']['name']
manifest.write_text('\n---\n'.join(json.dumps(r, indent=2) for r in resources) + '\n')
print(release)
