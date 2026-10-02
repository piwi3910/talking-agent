# Hello SIP integration

The app implements the ordinary SIP/RTP agent service in [Hello section 25](https://github.com/azrtydxb/hello/blob/main/hello-pbx-spec.md#25-ai-voice-agent-integration). Hello handles phone registration, number routing and trunks. No Asterisk or FreeSWITCH is needed.

## Persona settings

Open `/settings` in the app, or click Settings. Assign one or more numbers/extensions to each persona, choose its optional demo account, and save. Numbers must be unique across personas. Changes apply immediately to new calls; current calls retain their persona/account. Calls to different personas and multiple calls to the same persona run simultaneously with separate sessions and history. Accounts share scoped long-term memory with web sessions for the same selected user.

There is no access code or keypad menu. The operator chooses the phone account in settings; all callers to that persona use that account. Caller-ID never chooses account identity. Leave the account empty for general questions with anonymous per-call memory. Hello peers are trusted through the configured network allowlist. Account actions retain the runtime's exact-action confirmation requirement.

`PHONE_SETTINGS_FILE` selects a writable persisted JSON file (default `var/phone-settings.json`). The KW deployment mounts a dedicated PVC at `/app/var`, so settings survive application restarts and image updates. Atomic saves prevent routing changes if storage fails; revision checks reject stale edits from another browser. `SIP_CONFIG_FILE` numbers seed the first settings view; the saved settings file becomes authoritative after the first save.

## Phone voice features

- Live PCM recognition and partial/final transcript events use the existing STT service.
- The existing agent runtime supplies skills, tools, safety policies and scoped memory.
- Streamed sentence replies use the same fixed reference voice and persona voice style as the web.
- Continuous RTP listening detects caller speech and cancels old generation/playback before recognition finishes. Say “stop,” “stop talking,” or “pause” to stop without starting a new agent request.
- Existing prerecorded acknowledgement/lookup/network cues play during longer waits. Configure the default per persona, or say “turn off acknowledgements” and “turn on acknowledgements” during a call.
- Say “turn off spoken replies” or “speak replies” to control output.
- Mutations are read aloud with the exact tool arguments. Say “confirm” or “I confirm” to approve, or “cancel” to reject. Only one pending action is presented at a time. Interrupting its details requires the full proposal to be replayed before approval. Expired actions cannot execute. No keypad controls are required.
- Agent failures are reported audibly so the caller can retry. Hangup cancels call work and deletes its session. Calls have a 30-minute maximum and a configurable concurrency cap.

Phone detection currently uses PCM energy with 128 ms sustained speech and 700 ms trailing silence, rather than the browser's Silero detector. Telephone G.711 audio has lower bandwidth than web PCM. Echo cancellation/noise suppression is provided by the handset or PBX media tier; noisy handset tests remain important. Send continuous RTP during listening; discontinuous transmission without trailing silence is not supported.

## SIP listener and Hello routes

Copy `config.example.json` into your deployment configuration and set `SIP_CONFIG_FILE` to its mounted path. Replace documentation IPs with real addresses. `advertise_ip` must be reachable by Hello signaling and the RTP sender; it is advertised in SIP Contact and SDP. `allowed_peers` contains Hello's actual source IPs, including NAT. Both `STT_URL` and `TTS_URL` are required. Without `SIP_CONFIG_FILE`, settings can be saved but the UI shows SIP not connected.

| Example number | Hello destination | Persona |
| --- | --- | --- |
| 500 | sip:500@AGENT_IP:5060 | telecom-support |
| 501 | sip:501@AGENT_IP:5060 | hospital-services |
| 502 | sip:502@AGENT_IP:5060 | school-services |

The number is the exact SIP Request-URI user. Configure actual DIDs as settings entries or rewrite them in Hello. SIP uses UDP/TCP and G.711 PCMU/PCMA at 8 kHz. Other codecs require Hello's media tier to transcode. Untrusted peers receive 403; unknown numbers receive 404; capacity exhaustion receives 486. TLS/SRTP and REGISTER-based trunks are not yet implemented.

## KW delivery and networking

Use Kuvryn Sync, committed manifests, ARM64 BuildKit images and immutable digests. Keep one replica while sessions and fixtures remain process-local. The bootstrap Role includes namespace-scoped PVC management for persistent settings.

SIP and RTP require dedicated networking, independently of the HTTPS ingress at agent.kw.watteel.lab. Expose UDP/TCP 5060 and UDP RTP/RTCP 10000–10199 together, preserving advertised mappings and routing each call to this replica. A reachable pod IP with Hello anchored media inside the cluster is one option; external callers need an appropriate L4/host network arrangement. Restrict both signaling and media to Hello. Choose and configure reachable SIP/media addresses before enabling the listener; the current deployment does not assume a PBX address.

## Verification

Run `CGO_ENABLED=0 go test ./...`. The telephony tests establish actual UDP/TCP dialogs, process simultaneous calls to different agents, transcribe caller audio, stream replies, approve mutations by voice, interrupt playback independently, reject invalid destinations and reclaim capacity on BYE. Settings tests cover restart persistence, duplicate numbers, stale edits, account scope validation and write failures. Browser tests verify the settings page, saves, reloads and mobile layout. If a C compiler is available, also run the race detector. Local test listeners are fixtures; delivery remains the KW cluster.

After Sync reports the intended revision Healthy and Synced, verify pinned TLS, HTTP health and the browser conversation/SSE path. Real two-handset tests need Hello running with its routes and reachable SIP/RTP networking.
