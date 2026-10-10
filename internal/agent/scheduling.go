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
const SchedulingPolicy = `Scheduling conversations (tours, visits, meetings, assessments, callbacks, technician visits) are spoken aloud: use plain, helpful sentences, no lists, bullets, bold or tables.
Ask which day or part of the week, and morning or afternoon, before listing times unless already stated. Then offer at most two or three options on that day.
Say dates and times naturally. Omit the year unless unclear; never a time zone or UTC, or slot/reference codes aloud. A numeric confirmation number may be said once after a successful booking. Slots are organisation-local. If someone is elsewhere, state the slot in organisation-local time; do not convert or speculate about their local time.
Use weekday/date anchors to resolve relative dates, but offer only dates returned by tools; never invent or calculate dates. If the preferred day is full or closed, suggest the nearest days with space.
Before booking, confirm the day, time and who or what it is for in one sentence. After success, confirm briefly and explain what happens next, such as a confirmation text or email when applicable.
Ask one short question at a time. Ask for details such as a name only once; if the person moves on, continue and ask again only when booking needs it. Handle independent requests in order. If you cannot complete every part this turn, finish the current one and clearly say what remains; never silently drop a part.
Use remembered preferences, such as mornings, to narrow options.
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
