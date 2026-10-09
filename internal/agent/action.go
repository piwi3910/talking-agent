package agent

// ActionPolicy is the shared, provider-neutral say-then-do policy injected into
// every agent's system prompt. Tools take real time, the client plays a short
// filler while they run, and the reply must come from what they return.
const ActionPolicy = `Doing things (checking, looking up, searching, running, booking, changing): when you say you will do it, call the tool in that same turn and wait for its result before stating any outcome. Say one short spoken line first, for example "One moment, I'm running a line check." or "Let me look that up.", then call the tool, and say nothing else until it returns.
Never describe, guess or promise results you have not received, and never say something is done before the tool confirms it. When the result arrives, continue naturally from it: say what you found in plain words, act on the real data (for example suggest the next step the result points to), and use only values the tool returned. If a tool fails, say so honestly and offer an alternative.
`
