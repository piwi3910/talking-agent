#!/usr/bin/env python3
"""Provision isolated NovaMem demo identities; keep all credentials in KW Secrets."""
import argparse
import base64
import hashlib
import http.cookiejar
import json
from pathlib import Path
import secrets
import ssl
import struct
import subprocess
import urllib.error
import urllib.request

CONTEXT = 'kw'
NAMESPACE = 'enterprise-ai-demo'
SECRET = 'novamem-identities'
BASE = 'https://novamem.kw.watteel.lab'
TLS = ssl.create_default_context(cafile=str(Path.home() / '.config/buildkit/kw/ca.crt'))
OPENER = urllib.request.build_opener(urllib.request.HTTPSHandler(context=TLS), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def kubectl(*args, data=None):
    return subprocess.check_output(['kubectl', '--context', CONTEXT, *args], input=data)


def request(url, data=None, token=None):
    headers = {'Content-Type': 'application/json', 'Origin': BASE}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(url, data=json.dumps(data).encode() if data is not None else None, headers=headers)
    try:
        with OPENER.open(req, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        # Provider error bodies can echo credentials; never print them.
        raise RuntimeError(f'NovaMem provisioning request failed: HTTP {error.code}') from None


def scope_key(scope):
    h = hashlib.sha256()
    for name in ['tenant', 'organization', 'domain', 'namespace', 'user']:
        value = scope[name].encode()
        h.update(struct.pack('>I', len(value)))
        h.update(value)
    return 'enterprise-demo-v1-' + h.hexdigest()


def save(credentials):
    obj = {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': SECRET, 'namespace': NAMESPACE}, 'type': 'Opaque',
           'data': {'credentials.json': base64.b64encode(json.dumps(credentials).encode()).decode()}}
    kubectl('-n', NAMESPACE, 'apply', '--server-side', '--field-manager=enterprise-ai-memory', '-f', '-', data=json.dumps(obj).encode())


def main():
    global SECRET
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--validation', action='store_true', help='Provision six disposable isolation-test identities in a separate Secret')
    options = parser.parse_args()
    if options.validation:
        SECRET = 'novamem-validation-identities'
    existing = kubectl('-n', NAMESPACE, 'get', 'secret', SECRET, '--ignore-not-found', '-o', 'json')
    credentials = json.loads(base64.b64decode(json.loads(existing)['data']['credentials.json'])) if existing.strip() else []
    by_scope = {scope_key(c['scope']): c for c in credentials}
    scopes = []
    if options.validation:
        base = dict(tenant='enterprise-demo-validation', organization='Integration test', domain='telecom', namespace='kw-integration', user='validation-user')
        scopes.append(base)
        for field in base:
            variant = base.copy()
            variant[field] += '-other'
            scopes.append(variant)
    else:
        agents = request('https://agent.kw.watteel.lab/api/agents')
        for agent in agents:
            config = agent['config']
            for user in agent['users']:
                scopes.append({'tenant': config['tenant'], 'organization': config['organization'], 'domain': config['id'],
                               'namespace': config['memory']['namespace'], 'user': user['id']})
    required = [scope for scope in scopes if scope_key(scope) not in by_scope]
    if not required:
        print(f'All {len(by_scope)} scoped NovaMem credentials already provisioned.')
        return
    bootstrap = json.loads(kubectl('-n', 'novamem', 'get', 'secret', 'novamem-secrets', '-o', 'json'))['data']
    login = request(BASE + '/api/auth/sign-in/email', {
        'email': base64.b64decode(bootstrap['NOVAMEM_BOOTSTRAP_ADMIN_EMAIL']).decode(),
        'password': base64.b64decode(bootstrap['NOVAMEM_BOOTSTRAP_ADMIN_PASSWORD']).decode()})
    admin = login.get('token')
    if not admin:
        raise RuntimeError('NovaMem sign-in did not return a session token')
    try:
        for scope in required:
            key = scope_key(scope)
            result = request(BASE + '/v1/admin/users', {
                'email': key + '@enterprise-ai-demo.invalid', 'password': secrets.token_urlsafe(36),
                'name': 'Enterprise demo ' + scope['domain'] + ' ' + scope['user'],
                'tokenLabel': 'KW enterprise demo memory'}, admin)
            if not result.get('token'):
                raise RuntimeError('Provisioned account returned no token; stop to prevent duplicate accounts')
            credentials.append({'scope': scope, 'token': result['token']})
            # Persist after each allocation: a later failure must not lose minted tokens.
            save(credentials)
            print('Provisioned isolated memory for ' + scope['domain'] + '/' + scope['user'], flush=True)
    finally:
        request(BASE + '/api/auth/sign-out', {}, admin)
    print(f'Saved {len(credentials)} scoped credentials to {NAMESPACE}/{SECRET}.')


if __name__ == '__main__':
    main()
