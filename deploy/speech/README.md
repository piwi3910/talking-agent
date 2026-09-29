# Speech services on DGX .246

Breeze TTS 2 and Nemotron 3.5 ASR run as separate audio.cpp GPU containers on `192.168.10.246` (`gx10-48f4`, ARM64 NVIDIA GB10). The existing models and Laya remain in their own containers.

## Delivery

KW Sync applies the versioned ConfigMap and deployment Job in `deploy/kw/manifests/speech.json.yaml`. The Job delivers only the `enterprise-speech` Docker Compose project over host-key-verified SSH. A dedicated restricted SSH key lives in Kubernetes Secret `speech-dgx-ssh`; there are no login passwords or private keys in Git. Registry publishing uses the existing KW BuildKit/Nexus credentials.

The Job downloads pinned GGUFs on the DGX, verifies their exact sizes and SHA-256 hashes, and starts both containers only after verification. The immutable audio.cpp image digest is in `compose.json`; the Hugging Face repository revision and checksums are in `models.json`. Original model identities are `BreezeBlue/Breeze-TTS-2` and `nvidia/nemotron-3.5-asr-streaming-0.6b`; the deployed Q8 files are from `audio-cpp/audio.cpp-gguf`.

- Remote directory: `/home/piwi/enterprise-speech`
- Models: persistent `/home/piwi/enterprise-speech/models`
- Releases: `/home/piwi/enterprise-speech/releases/<content-hash>`
- Active release pointer: `/home/piwi/enterprise-speech/current-release`
- Project: `enterprise-speech`
- Containers: `enterprise-speech-breeze`, `enterprise-speech-nemotron`
- Restart policy: `unless-stopped`
- Resource caps: Breeze 16 GiB/6 CPU, Nemotron 8 GiB/6 CPU; limits are ceilings, not reservations.

Sync manages the deployment Job and cluster routing resources. Docker handles process restarts on the external host. Sync does not continuously reconcile individual external Docker settings; rerun a release through a new versioned Job if manual drift occurs. A failed deployment is visible in the Job and Sync; remote model files persist for retries. Deleting the Kubernetes Job does not stop remote containers. Remote rollback requires deploying the desired previous speech configuration through a new release; do not assume Sync's Kubernetes rollback also rolls back Docker.

## Endpoints

| Service | Inside KW | DGX LAN |
| --- | --- | --- |
| TTS | `http://speech-tts.enterprise-ai-demo.svc.cluster.local` | `http://192.168.10.246:8092` |
| STT | `http://speech-stt.enterprise-ai-demo.svc.cluster.local` | `http://192.168.10.246:8093` |

These are internal inference services, with no public ingress. The app proxies live microphone transcription and streamed speech at its public HTTPS hostname. See `docs/voice.md` for the browser controls and protocol.

TTS model ID: `breeze`. POST `/v1/audio/speech` with `input`, `stream: true`, `stream_format: "audio"`, `response_format: "pcm"`. Output is mono signed 16-bit little-endian PCM at 24 kHz. `options.instruction` controls delivery. Inline `voice_ref` and `reference_text` are supported for a stable reference voice; Sara/Maya references have not yet been selected. Defaults follow the Pithagoras Fast baseline: guidance 1, seed 42, eight frames/event, lookahead four, two reference-cache slots.

STT model ID: `nemotron-3.5-asr`. POST `/v1/audio/transcriptions` accepts multipart `model`, `language`, `file`. For live microphone capture, POST chunked raw PCM to `/v1/audio/transcriptions/live?model=nemotron-3.5-asr&sample_rate=16000&channels=1&sample_format=s16le&language=en-US`. Read SSE `transcript.text.delta` and `transcript.text.done` concurrently with audio upload. Uploading a complete WAV with `stream=true` is output streaming, not live capture. Set the input's actual sample rate correctly.

Both services expose `/health` and `/v1/models`. Healthchecks run every 15 seconds. Models stay loaded; no idle eviction is configured. Each service currently serializes access to its model. This is a low-concurrency demo deployment, not a throughput sizing result.

## Changing a release

1. Edit the speech source/config files in this directory.
2. Run `python3 deploy/speech/render.py`. It versions the ConfigMap and Job by payload content hash.
3. Commit and push using `deploy/kw/push-source.sh`; Sync reconciles the new release. Bootstrap RBAC in `deploy/kw/bootstrap.yaml` includes ConfigMaps, Jobs and EndpointSlices.
4. Check the new Job logs, remote container health, and Sync state.
5. Run `python3 deploy/speech/verify.py` from a host with LAN access. Override `TTS_URL`/`STT_URL` for tests inside KW. Audio and timing reports go to `/tmp/enterprise-speech-validation` by default.

No customer records are changed by verification. It synthesizes a fixed test sentence, checks non-silent audio, transcribes it, and verifies partial transcripts during paced live audio input. Report first-request and warmed measurements separately; these exclude the agent LLM, tool work, endpointing and browser playback.

Breeze model weights retain their research/non-commercial license. The user selected Breeze for this demo after that distinction was discussed; a later commercial service needs appropriate rights.
