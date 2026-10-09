package mock

import (
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/tools"
	"strings"
	"testing"
	"time"
)

type availabilityCase struct {
	industry, user, tool string
	zone                 *time.Location
}

var availabilityCases = []availabilityCase{
	{"school", "F001", "tour.availability", schoolZone},
	{"telecom", "C001", "technician.availability", telecomZone},
	{"aquila", "L001", "tour.availability", dubai},
	{"aquila", "L002", "assessment.availability", dubai},
}

func offsetAt(l *time.Location, t time.Time) int {
	_, off := t.In(l).Zone()
	return off
}

func TestAvailabilityIsLocalTimeGroupedByDayWithoutUTC(t *testing.T) {
	b := fixture(t)
	for _, c := range availabilityCases {
		t.Run(c.tool+"/"+c.industry, func(t *testing.T) {
			res := call(b, c.industry, c.user, c.tool, nil)
			if res.Error != nil || len(res.Records) == 0 {
				t.Fatalf("no availability: %+v", res)
			}
			if strings.Contains(strings.ToUpper(res.Summary), "UTC") || strings.Contains(res.Summary, "Dubai time") {
				t.Fatalf("summary names a time zone: %s", res.Summary)
			}
			if !strings.Contains(res.Summary, "By day: ") {
				t.Fatalf("summary is not grouped by day: %s", res.Summary)
			}
			head, _, _ := strings.Cut(res.Summary, tools.GuidanceMarker)
			for _, r := range res.Records {
				if r.ID == "" || strings.Contains(head, r.ID) || strings.Contains(res.Summary, r.ID) {
					t.Fatalf("slot id must stay machine-only: %+v", r)
				}
				at, ok := localTime(r)
				if !ok {
					t.Fatalf("slot start is not RFC 3339: %+v", r)
				}
				if _, off := at.Zone(); off != offsetAt(c.zone, at) {
					t.Fatalf("slot %s is not in local time: %s", r.ID, r.Start)
				}
				if !strings.HasPrefix(r.Description, at.Weekday().String()+" ") || strings.Contains(strings.ToUpper(r.Description+r.Name+r.Location), "UTC") {
					t.Fatalf("slot description must be a human day and time: %+v", r)
				}
				if !strings.Contains(head, dayLabel(at)+": ") || !strings.Contains(head, clockWords(at)) {
					t.Fatalf("slot %s missing from the by-day summary: %s", r.ID, head)
				}
				if at.Weekday() == time.Sunday {
					t.Fatalf("no one books on a Sunday: %+v", r)
				}
			}
			// Each day appears once and in calendar order.
			last := ""
			for _, r := range res.Records {
				at, _ := localTime(r)
				if day := at.Format("2006-01-02"); day < last {
					t.Fatalf("slots are not in date order: %s after %s", day, last)
				} else {
					last = day
				}
			}
		})
	}
}

func TestSchoolToursRunOnWorkingDaysInLocalHours(t *testing.T) {
	b := fixture(t)
	res := call(b, "school", "F001", "tour.availability", nil)
	for _, r := range res.Records {
		at, _ := localTime(r)
		if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
			t.Fatalf("school tours are Monday to Friday: %+v", r)
		}
		if at.Hour() != 9 && at.Hour() != 14 {
			t.Fatalf("unexpected tour hour: %+v", r)
		}
	}
	// The local-time filters read the local date and hour.
	morning := call(b, "school", "F001", "tour.availability", map[string]string{"time_preference": "morning"})
	afternoon := call(b, "school", "F001", "tour.availability", map[string]string{"time_preference": "afternoon"})
	for _, r := range morning.Records {
		if at, _ := localTime(r); at.Hour() >= 12 {
			t.Fatalf("afternoon in morning results: %+v", r)
		}
	}
	for _, r := range afternoon.Records {
		if at, _ := localTime(r); at.Hour() < 12 {
			t.Fatalf("morning in afternoon results: %+v", r)
		}
	}
}

func TestBookingWithReturnedSlotIDsStillWorks(t *testing.T) {
	b := fixture(t)
	for _, c := range []struct {
		availability availabilityCase
		book         string
		args         map[string]string
	}{
		{availabilityCases[0], "tour.book", nil},
		{availabilityCases[1], "technician.book", nil},
		{availabilityCases[2], "tour.book", nil},
		{availabilityCases[3], "assessment.book", map[string]string{"child_name": "Arjun"}},
	} {
		a := c.availability
		t.Run(c.book+"/"+a.industry, func(t *testing.T) {
			slot := call(b, a.industry, a.user, a.tool, nil).Records[0]
			args := map[string]string{"slot_id": slot.ID}
			for k, v := range c.args {
				args[k] = v
			}
			booked := call(b, a.industry, a.user, c.book, args)
			if booked.Error != nil {
				t.Fatalf("booking with id %s failed: %+v", slot.ID, booked.Error)
			}
			if strings.Contains(strings.ToUpper(booked.Summary), "UTC") || strings.Contains(booked.Summary, "Dubai time") {
				t.Fatalf("confirmation names a time zone: %s", booked.Summary)
			}
			at, _ := localTime(slot)
			if !strings.Contains(booked.Summary, whenWords(at)) && !strings.Contains(booked.Summary, dayLabel(at)) {
				t.Fatalf("confirmation should say the day and time in words: %s", booked.Summary)
			}
			for _, r := range call(b, a.industry, a.user, a.tool, nil).Records {
				if r.ID == slot.ID {
					t.Fatalf("booked slot %s is still offered", slot.ID)
				}
			}
		})
	}
}

func TestNoAvailabilityTellsTheModelToSuggestOtherDays(t *testing.T) {
	res := availabilitySummary("Available tours.", nil, nil)
	head, guidance, _ := strings.Cut(res, tools.GuidanceMarker)
	if strings.Contains(head, "By day") || !strings.Contains(guidance, "other days") {
		t.Fatal(res)
	}
}

func TestScheduleByDayGroupsLabelsWithinADay(t *testing.T) {
	loc := time.FixedZone("x", 4*3600)
	rec := func(h int, spec string) tools.Record {
		return tools.Record{Specialty: spec, Start: time.Date(2026, 10, 12, h, 30, 0, 0, loc).Format(time.RFC3339)}
	}
	rs := []tools.Record{rec(9, "in-person"), rec(10, "in-person"), rec(14, "virtual"), {Specialty: "in-person", Start: time.Date(2026, 10, 13, 9, 0, 0, 0, loc).Format(time.RFC3339)}}
	got := scheduleByDay(rs, aquilaSlotLabel)
	want := "Monday 12 October: in person 9:30 am, 10:30 am; virtual tour 2:30 pm. Tuesday 13 October: in person 9 am."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestAgentTimezonesMatchMockZones(t *testing.T) {
	agents, err := config.Load("../../agents")
	if err != nil {
		t.Fatal(err)
	}
	zones := industryZones()
	for id, a := range agents {
		zone, ok := zones[a.Industry]
		if !ok {
			continue // no scheduling backend for this agent
		}
		if a.Timezone == "" {
			t.Fatalf("%s: agent.yaml needs an explicit timezone", id)
		}
		for _, month := range []time.Month{time.January, time.July} {
			at := time.Date(2027, month, 15, 12, 0, 0, 0, time.UTC)
			if offsetAt(a.Location(), at) != offsetAt(zone, at) {
				t.Fatalf("%s: agent.yaml timezone %s disagrees with the %s mock backend", id, a.Timezone, a.Industry)
			}
		}
	}
}
