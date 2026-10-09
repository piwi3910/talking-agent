package agent

import (
	"enterprise-ai-demo/internal/config"
	"fmt"
	"time"
)

// SchedulingPolicy is the shared, provider-neutral policy for booking and
// scheduling conversations (tours, visits, meetings, assessments, callbacks,
// technician visits). It is injected into every agent's system prompt, like
// ScopePolicy, so replies sound like a person speaking, not a slot printout.
const SchedulingPolicy = `Scheduling conversations (tours, visits, meetings, assessments, callbacks, technician visits): everything you write is spoken aloud, so talk like a helpful person on the phone, in plain sentences with no lists, bullets, bold or tables.
Ask which day, or which part of the week, and morning or afternoon, suits before listing any times, unless the person already said. Then offer at most two or three options on that one day, in one natural sentence.
Say dates and times the way a person would: "Monday the 12th", "tomorrow morning at nine", "half past two". Never say the year unless it is unclear, never a time zone or UTC (tool times are already local to the organisation), and never read out slot ids or reference codes; use them only inside tool calls.
If the preferred day is full or closed, suggest the nearest days that have space.
Before calling a booking tool, confirm the day, time and who or what it is for in one sentence. After it succeeds, confirm briefly and say what happens next, such as a confirmation text or email, when that applies.
Use remembered preferences, such as mornings, to narrow the options.
`

// localClock is the organisation's current date and time in plain words, or
// "" when the agent has no configured timezone.
func localClock(a *config.Agent, now time.Time) string {
	if a.Timezone == "" {
		return ""
	}
	t := now.In(a.Location())
	return fmt.Sprintf("Today is %s and the local time is %s.", t.Format("Monday 2 January 2006"), t.Format("3:04 pm"))
}
