"""Real TTS -> STT smoke test, plus duplex recognition while audio is arriving.

Run from inside KW (defaults to the namespace aliases), or port-forward the Kuvryn worker Services and set TTS_URL/STT_URL.
Output artifacts stay outside the repository. This test does not modify customer data.
"""

import array
import http.client
import io
import json
import math
import os
from pathlib import Path
import sys
import threading
import time
import urllib.parse
import urllib.request
import wave

TTS = os.getenv(
    "TTS_URL", "http://speech-tts.enterprise-ai-demo.svc.cluster.local:8094"
)
STT = os.getenv(
    "STT_URL", "http://speech-stt.enterprise-ai-demo.svc.cluster.local:8093"
)
TEXT = "Hello. I can help you find an appointment for Tuesday morning."
output = Path(os.getenv("SPEECH_TEST_DIR", "/tmp/enterprise-speech-validation"))
output.mkdir(parents=True, exist_ok=True)
report = {"text": TEXT, "tts": []}

for base in [TTS, STT]:
    with urllib.request.urlopen(base + "/health", timeout=10) as response:
        assert response.status == 200

pcm = b""
for attempt in range(3):
    body = json.dumps(
        {
            "model": "qwen3-tts-custom",
            "input": TEXT,
            "voice": "Ryan",
            "stream": False,
            "response_format": "pcm",
            "options": {"seed": "42"},
        }
    ).encode()
    started = time.monotonic()
    req = urllib.request.Request(
        TTS + "/v1/audio/speech", body, {"Content-Type": "application/json"}
    )
    with urllib.request.urlopen(req, timeout=120) as response:
        first = response.read(2)
        first_ms = (time.monotonic() - started) * 1000
        pcm = first + response.read()
        headers = dict(response.headers)
    assert len(pcm) > 4800 and len(pcm) % 2 == 0, "Missing or malformed PCM"
    duration = len(pcm) / 48000
    samples = array.array("h", pcm)
    if sys.byteorder != "little":
        samples.byteswap()
    rms = math.sqrt(sum(s * s for s in samples) / len(samples))
    assert rms > 10, "Silent audio"
    elapsed = time.monotonic() - started
    report["tts"].append(
        {
            "attempt": attempt + 1,
            "first_bytes_ms": round(first_ms, 1),
            "total_ms": round(elapsed * 1000, 1),
            "audio_seconds": round(duration, 2),
            "rtf": round(elapsed / duration, 3),
            "rms": round(rms, 1),
        }
    )
    print("TTS", json.dumps(report["tts"][-1]), flush=True)

# audio.cpp's speech stream is mono signed 16-bit little endian PCM, 24 kHz.
with wave.open(str(output / "tts.wav"), "wb") as wav:
    wav.setnchannels(1)
    wav.setsampwidth(2)
    wav.setframerate(24000)
    wav.writeframes(pcm)
# Linear resampling suffices for this test; the live client needs its audio resampler.
samples = array.array("h", pcm)
if sys.byteorder != "little":
    samples.byteswap()
resampled = array.array("h")
for i in range(int(len(samples) * 16000 / 24000)):
    p = i * 1.5
    low = int(p)
    fraction = p - low
    resampled.append(
        round(
            samples[low] * (1 - fraction)
            + samples[min(low + 1, len(samples) - 1)] * fraction
        )
    )
if sys.byteorder != "little":
    resampled.byteswap()
raw = resampled.tobytes()
buf = io.BytesIO()
with wave.open(buf, "wb") as wav:
    wav.setnchannels(1)
    wav.setsampwidth(2)
    wav.setframerate(16000)
    wav.writeframes(raw)
boundary = "enterprise-speech-validation"
body = (
    (
        f'--{boundary}\r\nContent-Disposition: form-data; name="model"\r\n\r\nnemotron-3.5-asr\r\n--{boundary}\r\nContent-Disposition: form-data; name="language"\r\n\r\nen-US\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="speech.wav"\r\nContent-Type: audio/wav\r\n\r\n'
    ).encode()
    + buf.getvalue()
    + f"\r\n--{boundary}--\r\n".encode()
)
started = time.monotonic()
req = urllib.request.Request(
    STT + "/v1/audio/transcriptions",
    body,
    {"Content-Type": "multipart/form-data; boundary=" + boundary},
)
with urllib.request.urlopen(req, timeout=60) as response:
    result = json.load(response)
assert all(
    word in result["text"].lower() for word in ["appointment", "tuesday", "morning"]
), result
report["stt"] = {
    "wall_ms": round((time.monotonic() - started) * 1000, 1),
    "result": result,
}
print("STT", json.dumps(report["stt"]), flush=True)

# Send 100ms of PCM every 100ms, concurrently read transcript SSE.
u = urllib.parse.urlsplit(STT)
connection = http.client.HTTPConnection(u.hostname, u.port or 80, timeout=60)
path = (
    u.path.rstrip("/")
    + "/v1/audio/transcriptions/live?model=nemotron-3.5-asr&sample_rate=16000&channels=1&sample_format=s16le&language=en-US"
)
connection.putrequest("POST", path)
connection.putheader("Content-Type", "application/octet-stream")
connection.putheader("Transfer-Encoding", "chunked")
connection.endheaders()
# Keep the upload socket: HTTPConnection may detach it on a close-delimited response.
send_socket = connection.sock
finished_sending = threading.Event()
failures = []
# Include trailing silence to allow recognition to flush before the body closes.
stream_audio = raw + bytes(16000)


def send_audio():
    try:
        for offset in range(0, len(stream_audio), 3200):
            block = stream_audio[offset : offset + 3200]
            send_socket.sendall(f"{len(block):x}\r\n".encode() + block + b"\r\n")
            time.sleep(0.1)
        send_socket.sendall(b"0\r\n\r\n")
    except Exception as e:
        failures.append(str(e))
    finally:
        finished_sending.set()


thread = threading.Thread(target=send_audio, daemon=True)
thread.start()
started = time.monotonic()
response = connection.getresponse()
assert response.status == 200, response.read().decode()
events = []
while line := response.readline():
    if line.startswith(b"data: ") and line.strip() != b"data: [DONE]":
        event = json.loads(line[6:])
        assert event.get("type") != "error", event
        events.append(
            {
                "ms": round((time.monotonic() - started) * 1000, 1),
                "before_input_end": not finished_sending.is_set(),
                "event": event,
            }
        )
thread.join(timeout=5)
connection.close()
assert not failures, failures
assert any(
    e["before_input_end"] and e["event"].get("type") == "transcript.text.delta"
    for e in events
), "No incremental transcript before input ended"
assert any(e["event"].get("type") == "transcript.text.done" for e in events), (
    "No final transcript"
)
report["live_stt"] = events
(output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
print("Duplex STT passed. Report:", output / "report.json", flush=True)
