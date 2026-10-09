package mock

import (
	"enterprise-ai-demo/internal/tools"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (b *Backend) seedSchool(now time.Time) {
	// Tours run Monday to Friday at 9:30 and 14:30, school local time.
	day := localMidnight(now, schoolZone)
	for offset := 1; offset <= 14; offset++ {
		date := day.AddDate(0, 0, offset)
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}
		for _, hour := range []int{9, 14} {
			at := time.Date(date.Year(), date.Month(), date.Day(), hour, 30, 0, 0, schoolZone)
			b.records["school_tours"] = append(b.records["school_tours"], slotRecord(fmt.Sprintf("TOUR-%s-%02d", at.Format("20060102"), hour), "tour_slot", "Campus tour with admissions", at, "Main reception"))
		}
	}
	for id := range b.users["school"] {
		if id == "F001" {
			continue
		}
		status, description := "under review", "Admissions staff are reviewing the application. No decision or place is guaranteed."
		if id == "F002" {
			status, description = "awaiting documents", "Previous-school report outstanding. Contact admissions for secure submission instructions; do not upload documents in chat."
		}
		b.records["school_applications"] = append(b.records["school_applications"], tools.Record{ID: "APP-" + id, Kind: "application", UserID: id, RelatedID: "primary", Name: "Primary School application", Status: status, Description: description})
	}
}

func (b *Backend) school(u *User, name string, a map[string]string) tools.Result {
	switch name {
	case "family.profile":
		return result("Selected family profile.", u.Record)
	case "admissions.programs":
		return result("Published programs and annual tuition in USD. Meals, transport and optional clubs excluded; admissions staff confirm eligibility and places.", filter(b.catalogs["programs"], a["query"])...)
	case "admissions.status":
		rs := owned(b.records["school_applications"], u.ID)
		if len(rs) == 0 {
			return result("There is no application on file for this family. An enquiry or tour does not submit an application.")
		}
		return result("Your family’s application progress.", rs...)
	case "admissions.enquire":
		p, ok := find(b.catalogs["programs"], a["program_id"])
		if !ok {
			return tools.Failure("not_found", "School program not found")
		}
		if strings.TrimSpace(a["summary"]) == "" {
			return tools.Failure("invalid_input", "Enquiry summary is required")
		}
		return b.create("school_requests", "admissions_enquiry", u.ID, p.Name+": "+a["summary"])
	case "school.information":
		return result("Published school information. All listed contact addresses are fictional demo addresses.", filter(b.catalogs["school_info"], a["query"])...)
	case "reception.create_request":
		return b.create("school_requests", "reception_request", u.ID, a["summary"])
	case "reception.requests":
		return result("Your family’s reception messages and admissions enquiries.", owned(b.records["school_requests"], u.ID)...)
	case "tour.availability":
		for _, key := range []string{"from", "to"} {
			if a[key] != "" {
				if _, err := time.Parse("2006-01-02", a[key]); err != nil {
					return tools.Failure("invalid_input", "Dates must use YYYY-MM-DD")
				}
			}
		}
		if a["from"] != "" && a["to"] != "" && a["from"] > a["to"] {
			return tools.Failure("invalid_input", "Date range is reversed")
		}
		if a["time_preference"] != "" && a["time_preference"] != "morning" && a["time_preference"] != "afternoon" {
			return tools.Failure("invalid_input", "Time preference must be morning or afternoon")
		}
		rs := []tools.Record{}
		for _, r := range b.records["school_tours"] {
			if r.Status != "available" || !slotFuture(r, time.Now()) {
				continue
			}
			date := r.Start[:10] // local date: Start carries the school's offset
			if a["from"] != "" && date < a["from"] || a["to"] != "" && date > a["to"] {
				continue
			}
			if a["time_preference"] == "morning" && r.Start[11:13] >= "12" || a["time_preference"] == "afternoon" && r.Start[11:13] < "12" {
				continue
			}
			rs = append(rs, r)
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i].Start < rs[j].Start })
		if len(rs) > 10 {
			rs = rs[:10]
		}
		return result(availabilitySummary("Available school tours (Monday to Friday, morning and afternoon). Availability is checked again at booking; a tour does not reserve a school place.", rs, nil), rs...)
	case "tour.list":
		return result("Your family’s school visits.", owned(b.records["school_visits"], u.ID)...)
	case "tour.book":
		for i, r := range b.records["school_tours"] {
			if r.ID != a["slot_id"] || r.Status != "available" || !slotFuture(r, time.Now()) {
				continue
			}
			b.records["school_tours"][i].Status = "booked"
			b.records["school_tours"][i].UserID = u.ID
			b.serial++
			visit := r
			visit.ID = fmt.Sprintf("VISIT-%d", b.serial)
			visit.Kind = "school_visit"
			visit.RelatedID = r.ID
			visit.UserID = u.ID
			visit.Status = "booked"
			b.records["school_visits"] = append(b.records["school_visits"], visit)
			return result("Your campus tour is booked for "+whenWords(mustTime(r))+". Your confirmation number is "+confirmationNo(visit.ID)+". A confirmation email and text are on their way. Please check in at reception about ten minutes early; the tour takes around 45 minutes. This does not reserve a school place.", visit)
		}
		return tools.Failure("slot_unavailable", "This tour slot is no longer available")
	case "tour.cancel":
		for i, r := range b.records["school_visits"] {
			if r.ID != a["visit_id"] || r.UserID != u.ID || r.Status != "booked" {
				continue
			}
			b.records["school_visits"][i].Status = "cancelled"
			for j, slot := range b.records["school_tours"] {
				if slot.ID == r.RelatedID && slot.UserID == u.ID {
					b.records["school_tours"][j].Status = "available"
					b.records["school_tours"][j].UserID = ""
				}
			}
			return result("Your campus tour is cancelled.", b.records["school_visits"][i])
		}
		return tools.Failure("not_found", "Owned active school visit not found")
	}
	return tools.Failure("unknown_tool", "Tool not available in school backend")
}
