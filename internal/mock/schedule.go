package mock

import (
	"enterprise-ai-demo/internal/tools"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // zone data for minimal container images
)

// Each mock organisation publishes appointment times in its own local time.
// The zones match the `timezone` set in the matching agents/*/agent.yaml (a
// test keeps them in step); times are never labelled UTC.
var (
	schoolZone  = mustZone("America/New_York") // Willowbrook School
	telecomZone = mustZone("America/New_York") // Nova Telecom
)

func mustZone(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// industryZones maps a backend industry to its local zone.
func industryZones() map[string]*time.Location {
	return map[string]*time.Location{"aquila": dubai, "school": schoolZone, "telecom": telecomZone}
}

// localMidnight is the start of the local calendar day containing now, as a
// time in loc.
func localMidnight(now time.Time, loc *time.Location) time.Time {
	l := now.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// clockWords writes a time the way a schedule reads: "9 am", "2:30 pm".
func clockWords(t time.Time) string {
	h, m := t.Hour(), t.Minute()
	suffix := "am"
	if h >= 12 {
		suffix = "pm"
	}
	if h = h % 12; h == 0 {
		h = 12
	}
	if m == 0 {
		return fmt.Sprintf("%d %s", h, suffix)
	}
	return fmt.Sprintf("%d:%02d %s", h, m, suffix)
}

// dayLabel is a weekday and date without a year: "Monday 12 October".
func dayLabel(t time.Time) string {
	return fmt.Sprintf("%s %d %s", t.Weekday(), t.Day(), t.Month())
}

// whenWords is "Monday 12 October, 2:30 pm".
func whenWords(t time.Time) string { return dayLabel(t) + ", " + clockWords(t) }

// localTime reads a record's RFC 3339 start in the offset it was written with.
func localTime(r tools.Record) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, r.Start)
	return t, err == nil
}

// mustTime is localTime for records the backend itself created.
func mustTime(r tools.Record) time.Time {
	t, _ := localTime(r)
	return t
}

// slotFuture reports whether a slot starts after now.
func slotFuture(r tools.Record, now time.Time) bool {
	t, ok := localTime(r)
	return ok && t.After(now)
}

// slotRecord builds a slot whose Start carries the local offset and whose
// Description is the human day and time. The ID stays machine-only.
func slotRecord(id, kind, name string, at time.Time, location string) tools.Record {
	return tools.Record{ID: id, Kind: kind, Name: name, Description: whenWords(at), Start: at.Format(time.RFC3339), Status: "available", Location: location}
}

// scheduleByDay renders slots (in the order given) grouped by local day, with
// an optional per-slot label such as "virtual tour": "Monday 12 October: 9 am,
// 2:30 pm. Tuesday 13 October: ...".
func scheduleByDay(rs []tools.Record, label func(tools.Record) string) string {
	type group struct {
		day    string
		labels []string
		times  map[string][]string
	}
	var groups []*group
	for _, r := range rs {
		t, ok := localTime(r)
		if !ok {
			continue
		}
		day := dayLabel(t)
		if len(groups) == 0 || groups[len(groups)-1].day != day {
			groups = append(groups, &group{day: day, times: map[string][]string{}})
		}
		g := groups[len(groups)-1]
		l := ""
		if label != nil {
			l = label(r)
		}
		if _, seen := g.times[l]; !seen {
			g.labels = append(g.labels, l)
		}
		g.times[l] = append(g.times[l], clockWords(t))
	}
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		var items []string
		for _, l := range g.labels {
			times := strings.Join(g.times[l], ", ")
			if l != "" {
				times = l + " " + times
			}
			items = append(items, times)
		}
		parts = append(parts, g.day+": "+strings.Join(items, "; ")+".")
	}
	return strings.Join(parts, " ")
}

const slotGuidance = tools.GuidanceMarker + "Times are local to the organisation: never mention a time zone. Each record's id is the slot_id for the booking tool and is never spoken; its description gives the day and time. Ask which day suits, then offer two or three times on that day. Never offer a day or time that is not listed here."

// availabilitySummary is the tool summary for a list of slots: the intro, then
// the slots grouped by day, then how to use them.
func availabilitySummary(intro string, rs []tools.Record, label func(tools.Record) string) string {
	if len(rs) == 0 {
		return intro + " No times are free in that range." + tools.GuidanceMarker + "Suggest other days or dates: ask whether another day or week suits, then check that range; never invent times."
	}
	return intro + " By day: " + scheduleByDay(rs, label) + slotGuidance
}
