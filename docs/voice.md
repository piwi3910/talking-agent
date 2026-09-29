# Phase 2: browser voice

Open https://agent.kw.watteel.lab, start a session, then **Start voice** and allow microphone access. Speak normally; 700 ms of non-speech finalizes the utterance. **Finish speaking** forces finalization. **Mute mic** keeps replies audible; **Stop audio** interrupts playback; **End voice** releases the microphone. Text and confirmations remain available. Headphones reduce acoustic echo and accidental interruptions.

The right panel always shows live/latest server stage timings. Activity retains memory, tools, token counts and events; browser audio timings expand below the latency grid. TTFT means first visible answer text, not a hidden reasoning token. End-of-speech to playback is a browser scheduling estimate, not an acoustic measurement. First partial STT includes time spent capturing speech. Finalization starts when the browser closes the utterance upload.

## Runtime and transport

The LLM still uses go-ai-sdk v0.6.0. KW uses gateway model `qwen3-6-35b-a3b`. `LLM_DISABLE_THINKING=true` sends `chat_template_kwargs.enable_thinking=false` through SDK provider options. Other endpoints can leave this setting unset. Nothing in the shared agent runtime depends on Web Audio or the speech engine.

`GET /api/voice` reports configuration. A session-scoped same-origin WebSocket at `/api/sessions/{id}/transcribe` accepts 16 kHz mono signed PCM16 binary frames and a text `{"type":"finish"}` command. The server streams the upload into audio.cpp while forwarding partial/final transcripts. Captures are limited to 60 seconds, connections to 70 seconds, and frames to 64 KiB. Only a finalized utterance is submitted to the existing message API; account mutations retain explicit click confirmation.

`POST /api/sessions/{id}/speech` accepts bounded `text` and `turn_id` and proxies streamed Breeze PCM16 at 24 kHz. Browser sentence segmentation starts synthesis before the entire agent turn finishes. Playback and next-phrase synthesis overlap, with bounded queued text/audio. Cancellation aborts synthesis, clears scheduled playback and cancels an in-flight agent turn on barge-in. Old turn deltas cannot restart interrupted speech. Changing identity or persona releases capture and playback.

Speech services are server-side `STT_URL` and `TTS_URL`; no inference credentials or internal endpoint URLs go to the browser. The audio.cpp streaming protocol has its own small Go adapter because it is different from a buffered speech-generation call. Style instructions come from `persona.voice_style`. Each agent config selects a fixed synthetic WAV and transcript under `voice/`; every phrase sends the same reference to Breeze, preserving speaker identity across sentence requests.

## Streaming behavior

Real LLM deltas pass through SDK → runtime journal → flushed SSE → React. No simulated typing delay is added. Deterministic safety responses and exact backend record displays can arrive as one immediate result; they do not contain generated tokens to stream. The browser shows a cursor during generation. Thinking previously added a long silent interval before visible text.

## Validation and limits

Run `go test ./...`, `npm --prefix web run build`, and the Playwright suite. Opt-in `VOICE_TESTS=true` uses a prerecorded WAV as Chromium's microphone and the actual deployed speech services; set `VOICE_WAV` to a mono WAV containing a short English utterance and trailing silence. `LIVE_MODEL_TESTS=true` exercises real model appointment tools on fictional P018 records.

This is a low-concurrency English demo. Silero v6 classifies speech in the browser, alongside browser noise suppression and echo cancellation. A turn starts only after 224 ms of confident speech; playback ducks after 64 ms of credible speech and interrupts after 128 ms; normal listening still requires 224 ms. This rejects many non-speech noises but cannot distinguish a nearby person or television speech from the operator. Noisy rooms and loudspeakers still need hands-on tuning. Browser tests verify real inference, audio scheduling, cancellation and UI behavior, not subjective naturalness or acoustic echo cancellation. GPU contention can add queuing latency. Speech model services serialize inference. No voice authentication, voice cloning, WebRTC, avatar, or production call-center concurrency is claimed.

Sync pruning is disabled for this app: generated EndpointSlices inherit its Service labels and were being incorrectly pruned, causing intermittent 503s. Self-healing and automatic Git deployment remain enabled; obsolete resources need explicit removal until the Sync controller handles owned children correctly.

VAD/ONNX assets are pinned by the npm lockfile and copied to the app during build; the browser does not depend on a public CDN. Voice startup loads the detector once. Asset preparation also runs before Vite development.

Sara’s synthetic reference was regenerated with an explicit native General American English female voice direction and a different seed after the neutral-English design still sounded accented to the operator. All Sara cues were regenerated from that reference. Cue URLs include the reference revision to avoid stale cached audio. Breeze officially supports English and Chinese. The Voice tab has a reference preview for subjective listening review.


## Prerecorded conversational cues

Each persona has 19 reference-conditioned WAVs in `voice/cues`, with a manifest and regeneration script `scripts/generate-voice-cues.py`. They are downloaded once when voice starts; no live TTS inference is required for a cue. Categories cover waiting, lookups, network checks or availability, listening, acknowledgements and stopping. The Voice tab can disable them.

Selection is random within the relevant category, avoids immediate repeats, uses an eight-second cooldown and at most two cues per turn. A 1–1.6 second delay suppresses cues on fast responses. Tool-specific cues require an actual running tool; completion/failure clears them. Listening prompts occur when voice is activated, not over the user's speech. Generated reply audio and user speech take priority and stop any cue immediately. Cues never enter LLM history and do not count as first answer audio. No canned clip claims a booking, payment or account change succeeded.

“Stop”, “stop talking”, “stop speaking”, “pause”, and “be quiet” (optionally “please”) halt speech without creating another LLM turn. They do not cancel a booking or other account action. Speech onset itself interrupts before ASR finishes. The interrupted turn is blocked even when no text token has arrived yet, preventing late audio from restarting.

Phrase selection follows [Google's acknowledgement guidance](https://developers.google.com/assistant/conversation-design/acknowledgements) on short acknowledgements, variation and avoiding overuse, and [Amazon's progressive response guidance](https://developer.amazon.com/en-US/docs/alexa/custom-skills/send-the-user-a-progressive-response.html) on short waiting feedback. These sources provide design examples, not a ranked English frequency corpus.
