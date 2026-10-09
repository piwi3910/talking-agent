# Speech services on the KW DGX nodes

Qwen3-TTS and Nemotron 3.5 ASR run as separate GPU workers that Kuvryn manages in namespace `kuvryn-ai-workloads`, on the DGX nodes `gx10-9c17` (TTS) and `gx10-48f4` (STT) of the KW cluster. Kuvryn owns the model files, container image and lifecycle. This repository only routes to them.

The audio.cpp weights are the Q8 GGUFs from `audio-cpp/audio.cpp-gguf`. The original models are `Qwen/Qwen3-TTS-12Hz-1.7B-Base`, `-CustomVoice` and `-VoiceDesign`, and `nvidia/nemotron-3.5-asr-streaming-0.6b`.

## Routing

`deploy/kw/manifests/speech.json.yaml` defines `speech-tts` and `speech-stt` in `enterprise-ai-demo` as ExternalName aliases for the generated Kuvryn worker Services. The app reaches the workers only through these aliases.

| Service                           | App URL                                                              | Kuvryn worker Service (`kuvryn-ai-workloads`)          |
| --------------------------------- | -------------------------------------------------------------------- | ------------------------------------------------------ |
| TTS (stream, vLLM-Omni Qwen3-TTS) | `http://speech-tts-stream.enterprise-ai-demo.svc.cluster.local:8095` | `kuvryn-9034ee12-b1c9-4b94-9cda-d1a9c6c0a366-a46ea12d` |
| TTS (render, audio.cpp Qwen3-TTS) | `http://speech-tts.enterprise-ai-demo.svc.cluster.local:8094`        | `kuvryn-67579477-05ed-45a7-9903-c2dc7aabfac4-b4616c22` |
| STT                               | `http://speech-stt.enterprise-ai-demo.svc.cluster.local:8093`        | `kuvryn-0b03dce0-1eac-40dd-8541-59321391fac5-a46ea12d` |

ExternalName is a DNS CNAME, so the app must use the worker's own port. Kuvryn generates the worker Service names per deployment and node. If Kuvryn redeploys or moves a worker, update `externalName` (and the port, if it changes) in the speech manifest, then push. To find the current names:

```sh
kubectl --context kw -n kuvryn-ai-workloads get svc
```

The FastLLM proxy also lists `nemotron-3.5-asr`. However, it has no route for `/v1/audio/transcriptions/live`, which the app needs for partial transcripts while the user is still speaking. So the app calls the workers directly.

These are internal inference services, with no public ingress. The app proxies live microphone transcription and streamed speech at its public HTTPS hostname. See `docs/voice.md` for the browser controls and protocol.

## Text to speech (`TTS_PROVIDER`)

`omni` is the default. `TTS_URL` is the vLLM-Omni server (`speech-tts-stream`, 8095) and `TTS_RENDER_URL`, which is required, the audio.cpp worker (`speech-tts`, 8094).

- **Live speech:** every voice is cloned. The app POSTs `/v1/audio/speech` with `model: qwen3-tts-base`, `task_type: Base`, `language`, `response_format: pcm`, `stream: true`, `stream_format: audio`, `ref_audio` (a WAV data URI), `ref_text` and `initial_codec_chunk_frames: 8`, and forwards the chunked s16le 24 kHz PCM to the browser (and, downsampled to 8 kHz G.711, to SIP callers) as it arrives.
- **Render worker:** the Base model has no preset speakers or instructions. Presets (`qwen3-tts-custom`, with a speaker name) and designed voices (`qwen3-tts-design`, with a description) are therefore rendered once through the audio.cpp worker, stored as a reference WAV and transcript under the voice store, and cloned from then on. Until a sample exists the voice plays through the offline render path. Missing samples are rendered in the background, only while no caller is being spoken to, and retried every 30 seconds.
- **Cues:** acknowledgement cue wording ships in `agents/*/voice/cues.json`. The audio is rendered with the same clone path in the persona's current voice.
- **Built-in voices:** nine presets and five voices designed in code (`internal/voices/qwen.go`). Each agent's `voice.default` in `agent.yaml` names one of them. A saved persona that still points at a retired per-agent reference voice (`ref-<agent>`) moves to its agent's default at the next start.
- **Delivery:** a preset's delivery direction only applies while it renders offline. Bracketed stage directions such as `(laugh)` are removed from spoken text.

`TTS_PROVIDER=qwen3` sends every phrase, unstreamed, to the audio.cpp worker at `TTS_URL` (8094) and needs no vLLM-Omni server.

STT model ID: `nemotron-3.5-asr`.

- **File upload:** POST `/v1/audio/transcriptions` takes multipart `model`, `language` and `file`.
- **Live capture:** POST chunked raw PCM to `/v1/audio/transcriptions/live?model=nemotron-3.5-asr&sample_rate=16000&channels=1&sample_format=s16le&language=en-US`. Read the SSE `transcript.text.delta` and `transcript.text.done` events while the audio is still uploading.
- **Sample rate:** set it to the input's actual rate.

Both services expose `/health` and `/v1/models`. Each service handles one request at a time, so this is a low-concurrency demo deployment.

## Verification

Run `python3 deploy/speech/verify.py` from inside KW. To run it from a workstation instead, port-forward the worker Services and set `TTS_URL`/`STT_URL` (`TTS_URL` is the audio.cpp render worker). The test synthesizes a fixed sentence with a preset speaker, checks that the audio is not silent, transcribes it, and checks for partial transcripts during paced live input. It changes no customer data.

Check the licence of each Qwen3-TTS checkpoint before using the voices in a commercial service.
