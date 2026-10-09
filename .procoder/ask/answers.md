# What a human decided

Written 2026-10-09 10:06 UTC. procoder reads this
file to avoid asking a question twice; edit an answer here to change what
it believes. Reword the question and it will be asked again.

## (no longer asked)

Key: speech-routing-after-dgx-kuvryn-move

Answer: Direct to Kuvryn pods — speech-stt/speech-tts are ExternalName aliases to the Kuvryn worker Services; live STT partials are kept (user, 2026-10-09).

## (no longer asked)

Key: voice-switch-cue-handling

Answer: Re-render that persona's cues in the new voice in the background (stored on the PVC, muted until ready); also add Breeze inline vocal events ((laugh), (sigh), (cough), (clears throat)) to make speech more natural (user, 2026-10-09).

## (no longer asked)

Key: go-ai-sdk-bump-downgrade-guard

Answer: Add a never-downgrade guard to the bump workflow and move main to go-ai-sdk v0.7.1 (user, 2026-10-09).

## (no longer asked)

Key: tts-choppy-inconsistent-plan

Answer: Swap straight to Qwen3-TTS 1.7B (Apache-2.0) without a bake-off; Claude deploys the workers via Kuvryn after confirming the exact change (user, 2026-10-09).

## (no longer asked)

Key: kuvryn-ai-db-disk-full

Answer: Resize kuvryn-ai-db storage to 20Gi in azrtydxb/kuvryn-ai and verify recovery (user, 2026-10-09).

## (no longer asked)

Key: kuvryn-qwen3-tts-worker

Answer: Create the Qwen3-TTS worker with Base + CustomVoice + VoiceDesign on the Breeze node, port 8094; keep Breeze until verified (user, 2026-10-09).
