# Speech services on the KW DGX nodes

Breeze TTS 2 and Nemotron 3.5 ASR run as separate audio.cpp GPU workers that Kuvryn manages in namespace `kuvryn-ai-workloads`, on the DGX nodes `gx10-9c17` (TTS) and `gx10-48f4` (STT) of the KW cluster. Kuvryn owns the model files, container image and lifecycle. This repository only routes to them.

The weights are the Q8 GGUFs from `audio-cpp/audio.cpp-gguf`. The original models are `BreezeBlue/Breeze-TTS-2` and `nvidia/nemotron-3.5-asr-streaming-0.6b`.

## Routing

`deploy/kw/manifests/speech.json.yaml` defines `speech-tts` and `speech-stt` in `enterprise-ai-demo` as ExternalName aliases for the generated Kuvryn worker Services. The app reaches the workers only through these aliases.

| Service | App URL                                                       | Kuvryn worker Service (`kuvryn-ai-workloads`)          |
| ------- | ------------------------------------------------------------- | ------------------------------------------------------ |
| TTS     | `http://speech-tts.enterprise-ai-demo.svc.cluster.local:8092` | `kuvryn-29f86189-9566-4f0d-89fe-6c3b75174446-b4616c22` |
| STT     | `http://speech-stt.enterprise-ai-demo.svc.cluster.local:8093` | `kuvryn-0b03dce0-1eac-40dd-8541-59321391fac5-a46ea12d` |

ExternalName is a DNS CNAME, so the app must use the worker's own port. Kuvryn generates the worker Service names per deployment and node. If Kuvryn redeploys or moves a worker, update `externalName` (and the port, if it changes) in the speech manifest, then push. To find the current names:

```sh
kubectl --context kw -n kuvryn-ai-workloads get svc
```

The FastLLM proxy also lists `breeze` and `nemotron-3.5-asr`. However, it has no route for `/v1/audio/transcriptions/live`, which the app needs for partial transcripts while the user is still speaking. So the app calls the workers directly.

These are internal inference services, with no public ingress. The app proxies live microphone transcription and streamed speech at its public HTTPS hostname. See `docs/voice.md` for the browser controls and protocol.

## API

TTS model ID: `breeze`.

- **Request:** POST `/v1/audio/speech` with `input`, `stream: true`, `stream_format: "audio"` and `response_format: "pcm"`.
- **Output:** mono signed 16-bit little-endian PCM at 24 kHz.
- **Delivery:** `options.instruction` controls delivery.
- **Reference voice:** inline `voice_ref` and `reference_text` give a stable reference voice. The Sara/Maya reference WAVs and transcripts live in their agent directories, and the app attaches them to every phrase.

STT model ID: `nemotron-3.5-asr`.

- **File upload:** POST `/v1/audio/transcriptions` takes multipart `model`, `language` and `file`.
- **Live capture:** POST chunked raw PCM to `/v1/audio/transcriptions/live?model=nemotron-3.5-asr&sample_rate=16000&channels=1&sample_format=s16le&language=en-US`. Read the SSE `transcript.text.delta` and `transcript.text.done` events while the audio is still uploading.
- **Sample rate:** set it to the input's actual rate.

Both services expose `/health` and `/v1/models`. Each service handles one request at a time, so this is a low-concurrency demo deployment.

## Verification

Run `python3 deploy/speech/verify.py` from inside KW. To run it from a workstation instead, port-forward the worker Services and set `TTS_URL`/`STT_URL`. The test synthesizes a fixed sentence, checks that the audio is not silent, transcribes it, and checks for partial transcripts during paced live input. It changes no customer data.

Breeze model weights carry a research/non-commercial license. The user chose Breeze for this demo after that limitation was discussed. A later commercial service needs the appropriate rights.
