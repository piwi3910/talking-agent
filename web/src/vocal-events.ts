// Mirrors the stage directions the server drops from spoken text (speech.Spoken). Only these four are recognised; other parentheticals are untouched.
const PATTERN = "\\((?:laugh|cough|clears throat|sigh)\\)";
/** Removes allowlisted inline vocal events and collapses the leftover spaces. For chat display only: the server drops them from spoken text too. */
export function hideVocalEvents(text: string): string {
  // Stage directions the model sometimes echoes, e.g. "[Phone call answered]".
  const STAGE = /\[(?:phone |outbound |inbound )?call[^\]\n]{0,40}\]\s*/gi;
  if (!new RegExp(PATTERN, "i").test(text) && !STAGE.test(text)) return text;
  return text
    .replace(STAGE, "")
    .replace(new RegExp(PATTERN, "gi"), "")
    .replace(/ {2,}/g, " ")
    .replace(/^ +/, "");
}
