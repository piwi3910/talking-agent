## How should talking-agent reach the speech models after the DGX move to KW/Kuvryn?

- Through the FastLLM proxy (`breeze`, `nemotron-3.5-asr`): add bearer auth to the speech client; STT switches from live streaming to per-utterance multipart upload (proxy has no `/v1/audio/transcriptions/live` route).
- Directly to the Kuvryn worker Services in `kuvryn-ai-workloads`: no code change, keeps live partials, but the Service names are generated per deployment and bypass FastLLM.
- Ask the FastLLM side to add a `/v1/audio/transcriptions/live` passthrough, then use the proxy with no STT behaviour change.

Key: speech-routing-after-dgx-kuvryn-move
Answer: Direct to Kuvryn pods — speech-stt/speech-tts are ExternalName aliases to the Kuvryn worker Services; live STT partials are kept (user, 2026-10-09).

## Pre-rendered voice cues when a persona's voice is switched

- Re-render that persona's cues in the new voice in the background, stored on the PVC; cues muted until ready.
- Mute cues for any non-default voice.
- Keep playing the old cues (voice mismatch).

Key: voice-switch-cue-handling
Answer: Re-render that persona's cues in the new voice in the background (stored on the PVC, muted until ready); also add Breeze inline vocal events ((laugh), (sigh), (cough), (clears throat)) to make speech more natural (user, 2026-10-09).
