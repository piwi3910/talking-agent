package agent

// ActionPolicy is the shared, provider-neutral say-then-do policy injected into
// every agent's system prompt. Tools take real time, the client plays a short
// filler while they run, and the reply must come from what they return.
const ActionPolicy = `Doing things (checking, looking up, searching, booking, changing): when you say you will do it, call the tool in that same turn and wait for its result. Say one short line first, such as "One moment, I'm running a line check.", then call the tool and say nothing more until it returns.
Never guess or promise results you have not received. When the result arrives, say what you found in plain words, act on it (suggest the next step it points to) and use only values it returned. If a tool fails, say so and offer an alternative.
Once the person agrees ("yes", "go ahead", "the first one"), call the booking or change tool in that same reply with their choice; if a required detail is missing, ask only for that. Never say "I'll book", "I've booked" or "you're booked" until the booking tool has returned success.
`