# Phase 2: customer experience and streaming speech

Status on 2026-09-29: the branded customer chat and right-hand operator playground are implemented. Breeze TTS 2 and Nemotron 3.5 ASR are now deployed on DGX 192.168.10.246 through a KW Sync delivery Job; browser voice integration is still pending. The existing Go runtime, go-ai-sdk integration, FastLLM Qwen3.5-9B model, and NovaMem scopes remain in place.

## Selected deployment direction

| Purpose | Exact model | Reason |
| --- | --- | --- |
| STT | `nvidia/nemotron-3.5-asr-streaming-0.6b` | Native cache-aware streaming, configurable 80/160/320/560/1120 ms chunks, multilingual, released June 4, 2026. Start benchmarking at 160 or 320 ms; chunk size is not total turn latency. |
| TTS (user selected) | `BreezeBlue/Breeze-TTS-2` | Follow Pithagoras’s tested audio.cpp Q8 runtime, persistent reference voice and streaming PCM output. Released August 25, 2026. |
| Comparison STT | `microsoft/VibeVoice-ASR-Streaming-1.5B` | September 3 release, speaker-attributed transcription, hotwords, ten languages. Benchmark before substituting it: a more recent release does not alone establish lower interactive delay. |
| Alternative TTS | `openbmb/VoxCPM2` | Apache-2.0 alternative for a later commercial offering; not the selected demo voice. |

Primary references:
- [Nemotron model card](https://huggingface.co/nvidia/nemotron-3.5-asr-streaming-0.6b)
- [VoxCPM2 model card](https://huggingface.co/openbmb/VoxCPM2) and [release documentation](https://github.com/OpenBMB/VoxCPM)
- [Microsoft VibeVoice](https://github.com/microsoft/VibeVoice) and [streaming model](https://huggingface.co/microsoft/VibeVoice-ASR-Streaming-1.5B)
- [Breeze model card and license](https://huggingface.co/BreezeBlue/Breeze-TTS-2)

Nemotron's current license is OpenMDW-1.1. Its advertised 40 locales include eight that require adaptation; 32 produce transcription out of the box. English, Dutch, French, German and Arabic are among its transcription-ready locales. VoxCPM2 includes those languages as well.

VoxCPM2's card reports about 8 GB runtime VRAM and a real-time factor around 0.30 on an RTX 4090. This is synthesis throughput, not time to first audio. Breeze's advertised sub-40 ms first audio is a warm H100 fast-path result; it is not a claim about consumer GPUs or this application. All latency and voice quality need measurement on the target speech host.

Breeze is the explicit user selection after the license distinction was discussed. Keep that choice for the demo; its research/non-commercial terms remain a separate constraint on a later sold managed service. Both selected models have been deployed and verified; see `deploy/speech/README.md` for endpoints and reproducible checks.

## vLLM compatibility, checked 2026-09-29

Breeze TTS 2 is now documented in vLLM-Omni, including PCM streaming and voice conditioning: [official serving recipe](https://github.com/vllm-project/vllm-omni/blob/main/recipes/BreezeBlue/Breeze-TTS-2.md). The documented command is `vllm serve BreezeBlue/Breeze-TTS-2 --omni --port 8091`. Use a pinned vLLM-Omni build containing this integration; ordinary vLLM alone is not sufficient. Older feature requests claiming Breeze is unsupported predate the current integration. This does not replace the selected audio.cpp Q8 baseline.

Nemotron 3.5 ASR has no documented native vLLM support in the current supported-model list, and [its support request](https://github.com/vllm-project/vllm/issues/47455) remains open. Serve it through NeMo or a verified native streaming audio.cpp build. The original HF weights and the audio.cpp GGUF packages are different artifacts; use the artifact appropriate to the runtime.

## Breeze baseline from Pithagoras

The historical measured Cortex runtime pinned audio.cpp `efb04233dab73aeee4b2912042a90e7b36329061` with package `breeze_tts_2_q8_0`. Its current managed configuration uses model ID `breeze`, family `breeze_tts`, mode `streaming`, and one reference-cache slot. The current request sends JSON to audio.cpp’s `/v1/audio/speech`, with `input`, `stream: true`, `stream_format: audio`, `response_format: pcm`, and options including `stream_frames_per_event: 8`, `stream_lookahead_margin: 4` and a fixed seed. Start with guidance 1 (Fast); compare Expressive only after latency and listening checks. Do not confuse this JSON adapter with the separate native Python Breeze multipart endpoint.

Pithagoras uses mono 24 kHz PCM output. Keep separate stable reference voices for Sara and Maya, with exact reference transcripts cached between phrases. These app-specific references still need to be selected; this repository does not contain or claim rights to Pithagoras’s Aria voice asset. Do not regenerate a new voice identity for each phrase.

## Serving and infrastructure

Use a pinned [audio.cpp](https://github.com/0xShug0/audio.cpp) build for Breeze Q8; its current catalog also exposes a native streaming Nemotron 3.5 path. It is the native runtime used by Pithagoras. Start with separate STT and TTS services so cancellation, health and concurrency can be measured independently. Keep models warm and do not unload them between turns.

Planning estimate: reserve one 16–24 GB NVIDIA GPU for the first low-concurrency combined speech evaluation, preferably 24 GB for headroom; this is not a validated sizing guarantee. Keep the existing LLM on its current inference backend to avoid prefill contending with synthesis. CPU inference is possible in native runtimes but must pass the same latency test before being chosen.

The KW nodes currently report ARM64 and no allocatable `nvidia.com/gpu`. DGX 192.168.10.246 is an external ARM64/GB10 Docker host managed by Kuvryn, and now hosts the speech containers. GPU workloads need an actual GPU host, or a GPU worker registered with the appropriate driver/device plugin; do not create GPU-requesting pods on the present nodes and expect them to schedule. Verify ARM64 image/CUDA compatibility if the speech host uses ARM64. App delivery remains through Kuvryn Sync.

Required endpoint capabilities, rather than invented provider APIs:
- STT: persistent audio input, incremental/partial and finalized transcripts, language selection, explicit end-of-audio and cancellation. PCM mono input with a documented sample rate.
- TTS: audio bytes before synthesis completes, documented PCM format/sample rate, stable voice selection or cached reference, bounded request queue and cancellation.
- Health/readiness after weights are warm; credentials server-side; no browser-to-inference credentials.

The exact adapter follows the chosen server's real protocol. An endpoint named `/v1/audio/speech` is not sufficient evidence of OpenAI JSON compatibility: Pithagoras documents Breeze's native multipart API. Inspect the deployed runtime's contract before wiring it.

The installed go-ai-sdk v0.6.0 already provides `provider.StreamingTranscriptionModel`, with duplex audio send/events/close. Its current `SpeechModel.GenerateSpeech` returns complete audio bytes. Streaming TTS needs an SDK extension or a dedicated streaming transport adapter; wrapping the existing buffered speech call would not make it truly streaming. Preserve the shared LLM SDK integration and keep speech outside `internal/agent`.

## What to take from Pithagoras

Reference inspected at commit `398e67caa584ec3fed0488444a8e4fae05657223`:
- [Voice guide](https://github.com/thecodacus/pithagoras/blob/main/docs/guide/voice.md)
- [Engineering and benchmark record](https://github.com/thecodacus/pithagoras/blob/main/VIDEO_CONTEXT.md)
- User video: [How I Built an End-to-End Local Voice Agent, and Made It Fast](https://www.youtube.com/watch?v=xbedfuqYQYA&t=778s). Title verified via YouTube metadata; direct video/transcript access was unavailable. No claim of having watched the video.

Adopt overlapping text generation, phrase synthesis and playback; bounded ordered queues; immediate cancellation of old audio on interruption; and timing each pipeline stage. Pithagoras explicitly distinguishes text chunking from streaming audio within each synthesis request. Its main recognition path is speculative clip-based Whisper, not native streaming ASR, so it is a pipeline reference rather than our recommended STT model.

Its recorded 2.98–4.10 s completed-turn latency is a specific shared RTX 3060 setup before later optimizations, not a general benchmark. Do not carry over a one-second endpointing wait plus a 650 ms playback buffer without measuring them.

## Planned integration boundary

Browser microphone → voice transport → streaming STT → finalized user text → existing runtime → speakable phrases → streaming TTS → browser playback.

Browser VAD, echo cancellation, endpointing and barge-in belong to the voice session coordinator, not the business agent. Recognizer segments are not necessarily complete user turns. Do not execute tools on partial transcripts. Barge-in stops playback and queued synthesis, cancels the active agent turn, and rejects late packets from the old turn. Confirmed writes retain the existing backend checks and cannot silently replay on interruption.

Use the existing event envelope for `vad.started/stopped`, `stt.partial/final`, `tts.started/audio/completed`, `speech.started/stopped` and `barge_in`. Keep binary audio on a suitable media transport and telemetry references in the journal; do not persist base64 audio in the ordinary SSE replay history. Select WebSocket or WebRTC according to the deployed audio service, without changing the runtime's business logic.

Natural delivery requires short specific replies, suitable prosody, consistent voices, and interruption handling. Sara should sound approachable and practical; Maya calm and clear. Do not add canned laughter, filler or repeated empathy to simulate a person. Preserve the AI disclosure. Separate spoken summaries from visual record details and IDs before enabling read-aloud of structured tool results.

## Acceptance targets (not measured results)

- Measure end-of-user-speech → first audible response, including endpointing, LLM, tools, TTS and playback buffering.
- Initial goal: p50 under 1 second and p95 under 1.5 seconds for warm, simple non-tool turns; report tool turns separately.
- Target playback stop within 200 ms of confirmed interruption.
- Measure partial/final transcription delay, names/numbers accuracy, first text, first audio bytes, audible start, underruns and queue depth.
- Test accents, silence, background noise, self-echo, interruption, reconnection and speech-service failures.
- Listen to appointment dates, prices and names in both personas. Check voice stability across multiple phrases.
- Run representative telecom/hospital scenarios with NovaMem and explicit mutation confirmations intact.
