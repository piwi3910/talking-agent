package speech

import (
	"encoding/binary"
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
func WAV(pcm []byte) []byte {
	h := make([]byte, 44, 44+len(pcm))
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], 24000)
	binary.LittleEndian.PutUint32(h[28:], 48000)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return append(h, pcm...)
}
