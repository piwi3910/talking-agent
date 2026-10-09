#!/usr/bin/env python3
"""Analyse the audio recorded by harness.mjs.

For every spoken phrase the audio the browser rendered (tap on the Voice gain
node) is aligned with the PCM the server streamed (resampled to the context
rate) and compared. Findings, all relative to the server PCM so that natural
pauses and the TTS's own artefacts are not blamed on the browser:

  SEAM CLICK    sample-to-sample step at a buffer seam far larger than in the source
  LEVEL         10 ms windows where rendered and source RMS differ by >= 6 dB
                (dropouts, ducking, doubled samples)
  GAP           silence >= 20 ms in the rendered audio where the source has speech
  SCHEDULE GAP  a buffer scheduled later than the end of the previous one while that was still playing
  ONE-SAMPLE GAP  consecutive buffers separated by one frame of silence (rounding)
  UNDERRUN      a buffer that started after the previous one had finished playing

Usage: analyze.py <outdir>   (writes <outdir>/report.md, summary.json, wav/*.wav)
"""

import json
import os
import sys
from math import gcd

import numpy as np
from scipy.io import wavfile
from scipy.signal import fftconvolve, resample_poly

OUT = sys.argv[1]
SRC_RATE = 24000
SEAM_MIN = 0.03  # a seam step must exceed this ...
SEAM_RATIO = 3.0  # ... and this many times the source's step to count as a click
LEVEL_DB = 6.0
GAP_MS = 20.0


def read(path):
    with open(path) as f:
        return [json.loads(line) for line in f if line.strip()]


def pcm16(path):
    with open(path, "rb") as f:
        b = f.read()
    return np.frombuffer(b[: len(b) // 2 * 2], dtype="<i2").astype(np.float32) / 32768.0


def resample(x, a, b):
    g = gcd(a, b)
    return resample_poly(x, b // g, a // g).astype(np.float32)


def locate(rendered, ref, guess, rate, search_s=1.5, use_s=1.5):
    """Sample index in `rendered` where `ref` starts (normalised cross-correlation)."""
    use = ref[: int(rate * use_s)]
    lo = max(0, guess - int(search_s * rate))
    hi = min(len(rendered), guess + int(search_s * rate) + len(use))
    seg = rendered[lo:hi]
    if len(seg) < len(use) or np.sum(use**2) < 1e-6:
        return None, 0.0
    c = fftconvolve(seg, use[::-1], mode="valid")
    e = np.sqrt(np.maximum(fftconvolve(seg**2, np.ones(len(use)), mode="valid"), 1e-12))
    nc = c / (e * np.sqrt(np.sum(use**2)))
    k = int(np.argmax(nc))
    return lo + k, float(nc[k])


def windows_db(x, hop):
    n = len(x) // hop
    return 10 * np.log10(np.mean(x[: n * hop].reshape(n, hop) ** 2, axis=1) + 1e-12)


def runs(mask, hop_ms):
    out, i, n = [], 0, len(mask)
    while i < n:
        if mask[i]:
            j = i
            while j < n and mask[j]:
                j += 1
            out.append((i * hop_ms, (j - i) * hop_ms))
            i = j
        else:
            i += 1
    return out


def wav(path, x, rate):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    wavfile.write(path, rate, (np.clip(x, -1, 1) * 32767).astype(np.int16))


def analyse_agent(agent, report, summary):
    d = os.path.join(OUT, agent)
    with open(os.path.join(d, "meta.json")) as f:
        meta = json.load(f)
    rate = int(meta["rate"])
    x = np.fromfile(os.path.join(d, "rendered.f32"), dtype="<f4")
    blocks = meta["blocks"]
    f0 = blocks[0]["frame"]
    ev = read(os.path.join(d, "trace.jsonl"))
    cl = [e for e in ev if e.get("src") == "client"]
    holes = sum(
        1
        for i in range(1, len(blocks))
        if blocks[i]["frame"] - blocks[i - 1]["frame"] != blocks[i - 1]["n"]
    )
    report.append(
        f"\n## Agent `{agent}`: AudioContext {rate} Hz, {len(x) / rate:.1f} s recorded, tap discontinuities {holes}\n"
    )

    queued = {e["data"]["id"]: e["data"] for e in cl if e["type"] == "phrase.queued"}
    requests = [
        e["data"]
        for e in cl
        if e["type"] == "speech.request" and e["data"]["attempt"] == 0
    ]
    scheds, bursts = {}, {}
    for e in cl:
        if e["type"] == "audio.schedule":
            scheds.setdefault(e["data"]["phrase"], []).append(e["data"])
        if e["type"] == "speech.burst":
            bursts.setdefault(e["data"]["id"], []).append(e["data"])
    gains = [e["data"] for e in cl if e["type"] == "gain.target"]

    used, pcm_of = set(), {}
    for r in requests:
        text = queued.get(r["id"], {}).get("text")
        for i, s in enumerate(meta["speech"]):
            if i not in used and s["text"] == text and s["status"] == 200:
                p = os.path.join(d, f"server-{i:02d}.pcm")
                if os.path.exists(p) and os.path.getsize(p) > 0:
                    pcm_of[r["id"]] = p
                    used.add(i)
                    break

    # The tap and the schedule share one clock, so every phrase sits at the same small
    # offset from its scheduled time. Learn it from phrases that align cleanly and use
    # it for all of them (a phrase with a dropout would otherwise align badly).
    srcs, offsets = {}, []
    for pid in pcm_of:
        if pid not in scheds:
            continue
        src = resample(pcm16(pcm_of[pid]), SRC_RATE, rate)
        srcs[pid] = src
        loud = np.flatnonzero(np.abs(src) > 0.01)
        if len(loud) == 0 or len(src) < 2 * rate:
            continue
        a = max(0, int(loud[0]) - rate // 20)
        guess = int(round(scheds[pid][0]["at"] * rate)) - f0 + a
        found, c = locate(x, src[a:], guess, rate, search_s=0.5, use_s=0.6)
        if found is not None and c > 0.98:
            offsets.append(found - guess)
    shift0 = int(np.median(offsets)) if offsets else 0
    report.append(
        f"Clock offset between schedule and tap: {shift0 / rate * 1000:+.1f} ms (from {len(offsets)} cleanly aligned phrases)\n"
    )

    turns = {}
    for pid, q in queued.items():
        turns.setdefault(q["turn"], []).append(pid)
    n_reply = 0
    for turn, pids in turns.items():
        played = [p for p in pids if p in scheds and p in pcm_of]
        if not played:
            continue
        n_reply += 1
        report.append(
            f"\n### Reply {n_reply} (turn {turn[:8]}): {len(played)} of {len(pids)} phrases played\n"
        )
        rend_all, src_all = [], []
        for pid in played:
            sch = scheds[pid]
            src = srcs[pid]
            bs = bursts.get(pid, [])
            findings, seams = [], []
            cum = 0
            r0 = int(round(sch[0]["at"] * rate)) - f0 + shift0
            if r0 + len(src) // 2 > len(x):
                report.append(f"- {pid}: recording ends before the phrase")
                continue
            # Compare buffer by buffer, each at its own scheduled time, so that a late
            # buffer is reported once instead of shifting everything after it.
            for j in range(min(len(sch), len(bs))):
                nbytes = bs[j]["bytes"]
                sa = int(round(cum / 2 * rate / SRC_RATE))
                sb = int(round((cum + nbytes) / 2 * rate / SRC_RATE))
                cum += nbytes
                ra = int(round(sch[j]["at"] * rate)) - f0 + shift0
                sseg, rseg = src[sa:sb], x[ra : ra + (sb - sa)]
                if len(rseg) < len(sseg) or len(sseg) < 200:
                    continue
                base = sch[j]["at"] - sch[0]["at"]
                if j > 0:
                    a_, b_ = src[sa - 3 : sa + 3], x[ra - 3 : ra + 3]
                    sr, ss = (
                        float(np.abs(np.diff(b_)).max()),
                        float(np.abs(np.diff(a_)).max()),
                    )
                    seams.append((base, sr, ss))
                    if sr > SEAM_MIN and sr > SEAM_RATIO * max(ss, 0.005):
                        findings.append(
                            (
                                "SEAM CLICK",
                                base,
                                f"step {sr:.3f} vs {ss:.3f} in the server PCM at the seam",
                            )
                        )
                hop = int(rate * 0.010)
                es, er = windows_db(sseg, hop), windows_db(rseg, hop)
                m = min(len(es), len(er))
                bad = (es[:m] > -45) & (np.abs(er[:m] - es[:m]) >= LEVEL_DB)
                for t_ms, dur in runs(bad, 10):
                    i = t_ms // 10
                    findings.append(
                        (
                            "LEVEL",
                            base + t_ms / 1000,
                            f"{dur} ms, rendered {er[i] - es[i]:+.1f} dB vs source",
                        )
                    )
                hop2 = int(rate * 0.002)
                es2, er2 = windows_db(sseg, hop2), windows_db(rseg, hop2)
                m2 = min(len(es2), len(er2))
                for t_ms, dur in runs((er2[:m2] < -55) & (es2[:m2] > -40), 2):
                    if dur >= GAP_MS:
                        findings.append(
                            (
                                "GAP",
                                base + t_ms / 1000,
                                f"{dur} ms silent in the render, speech in the source",
                            )
                        )
            end = (
                int(round(sch[-1]["at"] * rate))
                - f0
                + shift0
                + int(round(sch[-1]["duration_ms"] * rate / 1000))
            )
            seg = x[r0:end]
            rend_all.append(seg)
            src_all.append(src)
            for s in sch[1:]:
                g = s.get("gap_ms")
                if (
                    g is not None
                    and s.get("previous_end")
                    and s["previous_end"] >= s["ct"]
                    and g > 0.1
                ):
                    findings.append(
                        (
                            "SCHEDULE GAP",
                            s["at"] - sch[0]["at"],
                            f"buffer {s['buffer']} scheduled {g:.0f} ms after the previous one ends, which was still playing ({(s['previous_end'] - s['ct']) * 1000:.0f} ms left)",
                        )
                    )
                elif (
                    g is not None
                    and 0 < g <= 0.1
                    and s.get("previous_end")
                    and s["previous_end"] >= s["ct"]
                ):
                    findings.append(
                        (
                            "ONE-SAMPLE GAP",
                            s["at"] - sch[0]["at"],
                            f"buffer {s['buffer']} starts {g * 1000:.0f} us late: one frame of silence at the seam",
                        )
                    )
            for s in sch:
                if "UNDERRUN" in s["flags"]:
                    findings.append(
                        (
                            "UNDERRUN",
                            s["at"] - sch[0]["at"],
                            f"buffer {s['buffer']} started {s['gap_ms']:.0f} ms after the previous ended",
                        )
                    )
            first = pid == played[0]
            b = bs
            report.append(
                f'- **{pid}** {"(first phrase) " if first else ""}"{queued[pid]["text"][:50]}"; '
                f"bursts(audio ms @ ms since request)={[(round(u['audio_ms']), round(u['since_request_ms'])) for u in b[:4]]}; "
                f"seam steps(t, rendered, source)={[(round(t, 2), round(r_, 3), round(s_, 3)) for t, r_, s_ in seams[:3]]}"
            )
            for kind, t, text in sorted(findings, key=lambda f: f[1]):
                report.append(f"    - {kind} at +{t * 1000:.0f} ms: {text}")
            summary.setdefault(agent, []).append(
                {
                    "reply": n_reply,
                    "phrase": pid,
                    "first": first,
                    "findings": [
                        {"kind": k, "t": t, "text": tx} for k, t, tx in findings
                    ],
                    "seams": seams,
                }
            )
        if rend_all:
            wav(
                os.path.join(OUT, "wav", f"{agent}_reply{n_reply:02d}_rendered.wav"),
                np.concatenate(rend_all),
                rate,
            )
            wav(
                os.path.join(OUT, "wav", f"{agent}_reply{n_reply:02d}_source.wav"),
                np.concatenate(src_all),
                rate,
            )
        s0 = scheds[played[0]][0]
        near = [
            (round(g["ct"] - s0["at"], 3), g["target"])
            for g in gains
            if s0["at"] - 0.5 <= g["ct"] <= s0["at"] + 5
        ]
        if near:
            report.append(
                f"    - gain.target events within 5 s of reply start (s after start, target): {near}"
            )


def main():
    report = ["# Audio E2E report\n"]
    summary = {}
    agents = sorted(
        a for a in os.listdir(OUT) if os.path.exists(os.path.join(OUT, a, "meta.json"))
    )
    for a in agents:
        analyse_agent(a, report, summary)
    rows = [r for a in agents for r in summary.get(a, [])]
    kinds = {}
    for r in rows:
        for f in r["findings"]:
            kinds[f["kind"]] = kinds.get(f["kind"], 0) + 1
    first_rows = [r for r in rows if r["first"]]
    first_seam = [
        r
        for r in first_rows
        if any(f["kind"] == "SEAM CLICK" and f["t"] < 1.0 for f in r["findings"])
    ]
    first_any = [
        r
        for r in first_rows
        if any(f["t"] < 1.5 and f["kind"] != "UNDERRUN" for f in r["findings"])
    ]
    all_seams = sum(1 for r in rows for f in r["findings"] if f["kind"] == "SEAM CLICK")
    first_rate = sum(
        1
        for r in rows
        for f in r["findings"]
        if f["kind"] == "SEAM CLICK" and f["t"] < 1.0
    )
    report.append(
        "\n## Totals\n"
        f"phrases analysed: {len(rows)}; replies (first phrases): {len(first_rows)}\n"
        f"findings by kind: {kinds}\n"
        f"first seam (~0.64 s) click in the first phrase of a reply: {len(first_seam)} of {len(first_rows)} replies\n"
        f"first seam (~0.64 s) click in any phrase: {first_rate} of {len(rows)} phrases\n"
        f"any rendered glitch in the first 1.5 s of a reply (excluding underruns): {len(first_any)} of {len(first_rows)}\n"
        f"seam clicks anywhere: {all_seams}\n"
    )
    with open(os.path.join(OUT, "report.md"), "w") as f:
        f.write("\n".join(report))
    with open(os.path.join(OUT, "summary.json"), "w") as f:
        json.dump(summary, f, indent=1)
    print("\n".join(report))


main()
