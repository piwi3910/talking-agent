#!/usr/bin/env python3
"""Render a Kubernetes List for an explicitly selected KW deployment target."""
import argparse
import json
import re


def render(image, host, ingress_class, namespace, tls_secret=None, pull_secret=None):
    labels = {"app.kubernetes.io/name": "enterprise-ai-demo"}
    metadata = {"name": "enterprise-ai-demo", "namespace": namespace, "labels": labels}
    pod_spec = {
        "automountServiceAccountToken": False,
        "terminationGracePeriodSeconds": 30,
        "securityContext": {"runAsNonRoot": True, "runAsUser": 10001, "runAsGroup": 10001, "seccompProfile": {"type": "RuntimeDefault"}},
        "containers": [{
            "name": "demo", "image": image, "imagePullPolicy": "IfNotPresent",
            "ports": [{"name": "http", "containerPort": 8080}],
            "env": [
                {"name": "LISTEN_ADDR", "value": "0.0.0.0:8080"},
                {"name": "LLM_PROVIDER", "value": "demo"},
                {"name": "MEMORY_PROVIDER", "value": "in-memory"},
                {"name": "MAX_ITERATIONS", "value": "12"},
            ],
            "resources": {"requests": {"cpu": "100m", "memory": "128Mi"}, "limits": {"cpu": "1", "memory": "512Mi"}},
            "securityContext": {"allowPrivilegeEscalation": False, "readOnlyRootFilesystem": True, "capabilities": {"drop": ["ALL"]}},
            "startupProbe": {"httpGet": {"path": "/api/health", "port": "http"}, "periodSeconds": 2, "failureThreshold": 30},
            "readinessProbe": {"httpGet": {"path": "/api/health", "port": "http"}, "periodSeconds": 5},
            "livenessProbe": {"httpGet": {"path": "/api/health", "port": "http"}, "periodSeconds": 15, "failureThreshold": 3},
        }],
    }
    if pull_secret:
        pod_spec["imagePullSecrets"] = [{"name": pull_secret}]
    deployment = {
        "apiVersion": "apps/v1", "kind": "Deployment", "metadata": metadata,
        "spec": {
            "replicas": 1, "revisionHistoryLimit": 3, "strategy": {"type": "Recreate"},
            "selector": {"matchLabels": labels},
            "template": {"metadata": {"labels": labels}, "spec": pod_spec},
        },
    }
    service = {
        "apiVersion": "v1", "kind": "Service", "metadata": metadata,
        "spec": {"type": "ClusterIP", "selector": labels, "ports": [{"name": "http", "port": 80, "targetPort": "http"}]},
    }
    ingress_metadata = dict(metadata)
    if ingress_class == "nginx":
        ingress_metadata["annotations"] = {
            "nginx.ingress.kubernetes.io/proxy-buffering": "off",
            "nginx.ingress.kubernetes.io/proxy-read-timeout": "600",
            "nginx.ingress.kubernetes.io/proxy-send-timeout": "600",
        }
    ingress = {
        "apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": ingress_metadata,
        "spec": {
            "ingressClassName": ingress_class,
            "rules": [{"host": host, "http": {"paths": [{"path": "/", "pathType": "Prefix", "backend": {"service": {"name": "enterprise-ai-demo", "port": {"name": "http"}}}}]}}],
        },
    }
    if tls_secret:
        ingress["spec"]["tls"] = [{"hosts": [host], "secretName": tls_secret}]
    return {"apiVersion": "v1", "kind": "List", "items": [deployment, service, ingress]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True, help="Published registry image with immutable tag or digest")
    parser.add_argument("--host", required=True, help="DNS hostname routed to KW ingress")
    parser.add_argument("--ingress-class", required=True)
    parser.add_argument("--namespace", required=True, help="Existing deployment namespace")
    parser.add_argument("--tls-secret", help="Existing TLS secret in the deployment namespace")
    parser.add_argument("--pull-secret", help="Existing registry pull secret in the deployment namespace")
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?", args.host) or "." not in args.host:
        parser.error("--host must be a DNS hostname without scheme, path or port")
    for name in (args.namespace, args.ingress_class, args.tls_secret, args.pull_secret):
        if name is not None and (len(name) > 63 or not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?", name)):
            parser.error("namespace, ingress class and secret names must be DNS labels")
    if not re.fullmatch(r"[A-Za-z0-9._/@:-]+", args.image) or "/" not in args.image or (":" not in args.image.rsplit("/", 1)[-1]):
        parser.error("--image must include a registry/repository and explicit tag or digest")
    print(json.dumps(render(args.image, args.host, args.ingress_class, args.namespace, args.tls_secret, args.pull_secret), indent=2))


if __name__ == "__main__":
    main()
