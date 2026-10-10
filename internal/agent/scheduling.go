package agent

import (
	"enterprise-ai-demo/internal/config"
	"fmt"
	"strings"
	"time"
)

// SchedulingPolicy is the shared, provider-neutral policy for booking and
// scheduling conversations (tours, visits, meetings, assessments, callbacks,
// technician visits). It is injected into every agent's system prompt, like
// ScopePolicy, so replies sound like a person speaking, not a slot printout.
const SchedulingPolicy = `Scheduling conversations (tours, visits, meetings, assessments, callbacks, technician visits): everything you write is spoken aloud. Talk like a helpful person on the phone, in plain sentences with no lists, bullets, bold or tables.
Ask which day or part of the week, and morning or afternoon, suits before listing times unless already stated. Then offer at most two or three options on that day in one natural sentence.
Say dates and times naturally: "Monday the 12th", "tomorrow morning at nine", "half past two". Never say the year unless unclear, never a time zone or UTC (tool times are local), or slot ids/reference codes aloud; use codes only in tool calls. You may say a numeric confirmation number once after a booking succeeds.
Use weekday and date anchors in context to resolve relative dates; only offer dates from tool results. Never invent or calculate dates.
If the preferred day is full or closed, suggest the nearest days with space.
Before booking, confirm the day, time and who or what it is for in one sentence. After success, confirm briefly and say what happens next, such as a confirmation text or email when applicable.
Ask one thing at a time: one short question per reply, never a numbered list. Ask for details such as a name only once; if the person moves on, continue and ask again only when booking needs it.
Use remembered preferences, such as mornings, to narrow the options.
`

// localClock is the organisation's current date and time in plain words, or
// "" when the agent has no configured timezone.
func localClock(a *config.Agent, now time.Time) string {
	if a.Timezone == "" {
		return ""
	}
	t := now.In(a.Location())
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	// Monday of next week; on a weekend "next week" is the coming Monday.
	toMonday := (8 - int(day.Weekday())) % 7
	if toMonday == 0 {
		toMonday = 7
	}
	next := day.AddDate(0, 0, toMonday)
	weekdays := make([]string, 0, 15)
	for offset := 0; offset <= 14; offset++ {
		weekdays = append(weekdays, day.AddDate(0, 0, offset).Format("Monday 2 January"))
	}
	return fmt.Sprintf("Today is %s and the local time is %s. Tomorrow is %s. This week's date anchors are %s. \"Next week\" means %s to %s.",
		t.Format("Monday 2 January 2006"), t.Format("3:04 pm"), day.AddDate(0, 0, 1).Format("Monday 2 January"), strings.Join(weekdays, "; "),
		next.Format("Monday 2 January"), next.AddDate(0, 0, 6).Format("Sunday 2 January"))
}
