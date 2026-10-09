// Mirrors the Go allowlist (speech.VocalEvents). Only these four events are recognised; other parentheticals are untouched.
const PATTERN = "\\((?:laugh|cough|clears throat|sigh)\\)";
/** Removes allowlisted inline vocal events and collapses the leftover spaces. For chat display only; spoken text keeps them. */
export function hideVocalEvents(text: string): string {
  if (!new RegExp(PATTERN, "i").test(text)) return text;
  return text
    .replace(new RegExp(PATTERN, "gi"), "")
    .replace(/ {2,}/g, " ")
    .replace(/^ +/, "");
}
