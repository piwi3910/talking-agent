package speech

import (
	"regexp"
	"strings"
)

// EventsPrompt is appended to the system prompt of personas that may use Breeze
// inline vocal events. The allowlist below must stay in sync with it.
const EventsPrompt = "\nYour replies are spoken aloud. Sparingly, and only where it fits naturally, you may include exactly one of (laugh), (sigh), (clears throat) or (cough) inline in a reply: at most once per reply, never when the caller is upset and never when giving bad news. Never use any other bracketed stage directions or emotes.\n"

var vocalEvent = regexp.MustCompile(`(?i)\((laugh|cough|clears throat|sigh)\)`)
var doubleSpace = regexp.MustCompile(` {2,}`)

// VocalEvents applies the persona's inline vocal event policy. Only the four
// events Breeze understands are touched; other parentheticals are left alone.
// Allowed events are normalised to lowercase; otherwise they are removed.
func VocalEvents(text string, allow bool) string {
	if !vocalEvent.MatchString(text) {
		return text
	}
	if allow {
		return vocalEvent.ReplaceAllStringFunc(text, strings.ToLower)
	}
	return strings.TrimSpace(doubleSpace.ReplaceAllString(vocalEvent.ReplaceAllString(text, ""), " "))
}

// WAV wraps 24 kHz mono signed 16-bit PCM in a RIFF header.
func WAV(pcm []byte) []byte { return WAVAt(pcm, 24000) }
