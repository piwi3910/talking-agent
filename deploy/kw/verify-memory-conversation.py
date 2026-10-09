#!/usr/bin/env python3
"""Verify preference storage/recall through the deployed API. Use store, redeploy, recall."""

import argparse
import json
from pathlib import Path
import ssl
import urllib.request

BASE = "https://agent.kw.watteel.lab"
TLS = ssl.create_default_context(cafile=str(Path.home() / ".config/buildkit/kw/ca.crt"))
FACT = "Customer prefers troubleshooting before technician dispatch."


def request(path, data=None):
    req = urllib.request.Request(
        BASE + path,
        data=json.dumps(data).encode() if data is not None else None,
        headers={"Content-Type": "application/json"},
    )
    return urllib.request.urlopen(req, context=TLS, timeout=180)


def turn(agent, user, message, expect_fact, store=False):
    session = json.load(request("/api/sessions", {"agent_id": agent, "user_id": user}))[
        "id"
    ]
    turn_id = json.load(
        request("/api/sessions/" + session + "/messages", {"message": message})
    )["turn_id"]
    memories, response, stored, completed = [], "", False, False
    errors = []
    with request("/api/sessions/" + session + "/events") as stream:
        for line in stream:
            if not line.startswith(b"data:"):
                continue
            event = json.loads(line[5:])
            if event.get("turn_id") != turn_id:
                continue
            kind, data = event["type"], event["data"]
            if kind == "memory.retrieval.completed":
                assert data["provider"] == "novamem", data["provider"]
                memories = data["memories"]
            if kind.endswith(".failed") or kind == "agent.error":
                errors.append(kind)
            if kind == "memory.store.completed":
                assert data["provider"] == "novamem"
                stored = data["memory"] == FACT
            if kind == "agent.response.completed":
                response = data["text"]
            if kind == "turn.completed":
                completed = True
            if completed and (not store or stored or errors):
                break
    assert not errors, errors
    assert response.strip(), "No assistant response"
    if store:
        assert stored, "Preference storage not acknowledged"
    else:
        assert any(m["text"] == FACT for m in memories) == expect_fact, (
            "Memory isolation/recall failed"
        )
    print(
        json.dumps(
            {
                "agent": agent,
                "user": user,
                "provider": "novamem",
                "stored": stored,
                "memories": [m["text"] for m in memories],
                "response": response,
            }
        ),
        flush=True,
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=["store", "recall"])
    stage = parser.parse_args().stage
    if stage == "store":
        turn(
            "telecom-support",
            "C006",
            "I prefer troubleshooting before you send a technician.",
            False,
            store=True,
        )
    else:
        query = "My Wi-Fi is down again; can you send a technician?"
        turn("telecom-support", "C006", query, True)
        turn("telecom-support", "C007", query, False)
        turn("school-services", "F001", query, False)
