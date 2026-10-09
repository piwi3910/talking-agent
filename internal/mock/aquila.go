package mock

import (
	"enterprise-ai-demo/internal/tools"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dubai is UTC+4 without daylight saving. Slot times are published in Dubai time.
var dubai = time.FixedZone("GST", 4*3600)

// closures are 2026-27 school closures (inclusive ISO dates) when no tours or assessments are offered.
var aquilaClosures = [][2]string{
	{"2026-10-12", "2026-10-16"},
	{"2026-12-02", "2026-12-04"},
	{"2026-12-12", "2027-01-03"},
	{"2027-03-08", "2027-03-12"},
	{"2027-04-03", "2027-04-11"},
	{"2027-05-17", "2027-05-18"},
	{"2027-07-03", "2027-08-31"},
}

func aquilaClosed(iso string) bool {
	for _, c := range aquilaClosures {
		if iso >= c[0] && iso <= c[1] {
			return true
		}
	}
	return false
}

func aquilaClock(hour int) string {
	suffix, h := "am", hour
	if hour >= 12 {
		suffix = "pm"
		if hour > 12 {
			h = hour - 12
		}
	}
	return fmt.Sprintf("%d:00 %s", h, suffix)
}

func aquilaAED(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// aquilaYear normalises free text such as "year 7", "Y7", "FS 2" or "7".
func aquilaYear(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.NewReplacer("year", "", "yr", "", "grade", "", " ", "", "-", "").Replace(s)
	switch s {
	case "fs1", "foundationstage1", "nursery":
		return "FS1"
	case "fs2", "foundationstage2", "reception":
		return "FS2"
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "y"))
	if err != nil || n < 1 || n > 13 {
		return ""
	}
	return "Year " + strconv.Itoa(n)
}

func aquilaYearNumber(key string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(key, "Year "))
	return n
}

// aquilaTuition returns the KHDA-approved 2026-27 annual tuition in AED.
func aquilaTuition(key string) int {
	switch key {
	case "FS1":
		return 48673
	case "FS2":
		return 51917
	}
	switch n := aquilaYearNumber(key); {
	case n <= 0:
		return 0
	case n <= 2:
		return 54081
	case n <= 4:
		return 56244
	case n <= 6:
		return 59489
	case n <= 8:
		return 64897
	case n <= 11:
		return 71386
	}
	return 77876
}

func aquilaStage(key string) string {
	n := aquilaYearNumber(key)
	switch {
	case key == "FS1" || key == "FS2":
		return "EYFS"
	case n <= 6:
		return "Primary"
	case n <= 11:
		return "Secondary"
	}
	return "Post-16"
}

func aquilaAges(key string) string {
	switch key {
	case "FS1":
		return "ages 3 to 4"
	case "FS2":
		return "ages 4 to 5"
	}
	n := aquilaYearNumber(key)
	return fmt.Sprintf("ages %d to %d", n+4, n+5)
}

func aquilaYearGroups() []string {
	out := []string{"FS1", "FS2"}
	for n := 1; n <= 13; n++ {
		out = append(out, "Year "+strconv.Itoa(n))
	}
	return out
}

func aquilaStaff(q string) string {
	s := strings.ToLower(q)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("inclusion", "hemam", "claire", "hitchings"):
		return "Claire Hitchings, Head of Inclusion"
	case has("primary", "kylie", "cleworth"):
		return "Kylie Cleworth, Head of Primary"
	case has("secondary", "yasmine", "dannawy"):
		return "Yasmine Dannawy, Head of Secondary"
	case has("arabic", "islamic", "khalil"):
		return "Dr Mahmoud Khalil, Head of Arabic and Islamic"
	case has("safeguard", "lamond"):
		return "Pauline Lamond, Safeguarding Lead"
	case has("principal", "howsen"):
		return "Wayne Howsen, Principal"
	}
	return strings.TrimSpace(q)
}

type aquilaZoneInfo struct {
	zone  int
	fee   int
	areas []string
	label string
}

var aquilaZones = []aquilaZoneInfo{
	{1, 6877, []string{"dubailand", "falcon city", "silicon", "dso", "academic city"}, "Dubailand, Falcon City, Dubai Silicon Oasis and Academic City"},
	{2, 8927, []string{"arabian ranches", "motor city", "mudon", "mirdif"}, "Arabian Ranches, Motor City, Mudon and Mirdif"},
	{3, 9588, []string{"damac hills", "jvc", "jumeirah village"}, "Damac Hills 2 and JVC"},
}

func aquilaZoneFor(area string) (aquilaZoneInfo, bool) {
	s := strings.ToLower(area)
	for _, z := range aquilaZones {
		for _, k := range z.areas {
			if strings.Contains(s, k) {
				return z, true
			}
		}
	}
	return aquilaZoneInfo{}, false
}

func aquilaZoneRecord(z aquilaZoneInfo) tools.Record {
	return tools.Record{ID: "BUS-ZONE-" + strconv.Itoa(z.zone), Kind: "bus_zone", Name: fmt.Sprintf("Zone %d: %s", z.zone, z.label), Description: fmt.Sprintf("School bus, AED %s per year, paid termly.", aquilaAED(z.fee)), Amount: float64(z.fee), Currency: "AED"}
}

func aquilaNorm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return " " + strings.Join(strings.Fields(b.String()), " ") + " "
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' }

// aquilaMatches finds key at a word start. Short keys must be whole words (optionally plural).
func aquilaMatches(padded, key string) bool {
	needle := strings.TrimRight(aquilaNorm(key), " ")
	from := 0
	for {
		i := strings.Index(padded[from:], needle)
		if i < 0 {
			return false
		}
		end := from + i + len(needle)
		if len(needle)-1 > 4 || end >= len(padded) || !isASCIILetter(padded[end]) {
			return true
		}
		if padded[end] == 's' && (end+1 >= len(padded) || !isASCIILetter(padded[end+1])) {
			return true
		}
		from += i + 1
	}
}

func (b *Backend) seedAquila(now time.Time) {
	for _, c := range aquilaChildren {
		b.records["aq_children"] = append(b.records["aq_children"], tools.Record{ID: "CH-" + c.user + "-" + strings.ToLower(c.name), Kind: "child", UserID: c.user, Name: c.name, Specialty: c.year, Status: c.status, Description: c.detail, Value: c.age, Unit: "years"})
	}
	b.records["aq_history"] = aquilaHistoryRecords()
	b.records["aq_apps"] = []tools.Record{{ID: "APP-L002", Kind: "application", UserID: "L002", Name: "Application for Arjun (Year 7)", Status: "started, CAT4 not yet booked", Description: "Started online on 2 June 2026. Outstanding: CAT4 booking and documents (passports, vaccination record, attested transfer certificate). Next step: book the CAT4 and meet Yasmine Dannawy, Head of Secondary."}}
	// Closures only apply when they leave something to offer, so the demo never runs dry.
	for _, respect := range []bool{true, false} {
		b.seedAquilaSlots(now, respect)
		if len(b.records["aq_slots"]) > 0 {
			break
		}
	}
}

func (b *Backend) seedAquilaSlots(now time.Time, respectClosures bool) {
	local := now.In(dubai)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	for offset := 2; offset <= 15; offset++ {
		date := day.AddDate(0, 0, offset)
		if respectClosures && aquilaClosed(date.Format("2006-01-02")) {
			continue
		}
		switch date.Weekday() {
		case time.Saturday:
			b.aquilaSlot("tour_slot", "TS", "OM", date, 10, "Saturday open morning", "open-morning", "Main reception (10:00 am to 12:00 pm)")
		case time.Monday, time.Tuesday, time.Wednesday, time.Thursday:
			for _, h := range []int{9, 10, 11} {
				b.aquilaSlot("tour_slot", "TS", "IP", date, h, "In-person campus tour", "in-person", "Main reception")
			}
			for _, h := range []int{14, 15} {
				b.aquilaSlot("tour_slot", "TS", "VT", date, h, "Virtual campus tour", "virtual", "Online; the link is emailed after booking")
			}
			for _, h := range []int{9, 10} {
				b.aquilaSlot("assessment_slot", "AS", "C4", date, h, "CAT4 assessment (Secondary entry)", "cat4", "Admissions suite")
			}
			for _, h := range []int{10, 11} {
				b.aquilaSlot("assessment_slot", "AS", "MG", date, h, "Meet and greet with the school leadership", "meet-and-greet", "Admissions suite")
			}
		}
	}
}

func (b *Backend) aquilaSlot(kind, prefix, code string, date time.Time, hour int, name, typ, location string) {
	when := fmt.Sprintf("%s %d %s, %s (Dubai time)", date.Weekday(), date.Day(), date.Month(), aquilaClock(hour))
	b.records["aq_slots"] = append(b.records["aq_slots"], tools.Record{ID: fmt.Sprintf("%s-%s-%02d00-%s", prefix, date.Format("20060102"), hour, code), Kind: kind, Name: name, Description: when, Specialty: typ, Location: location, Status: "available"})
}

func aquilaSlotDate(id string) string {
	if len(id) < 11 {
		return ""
	}
	return id[3:7] + "-" + id[7:9] + "-" + id[9:11]
}

func aquilaToday() string { return time.Now().In(dubai).Format("2006-01-02") }

// aquilaLog stores a user-owned event that later reads (history, status) expose.
func (b *Backend) aquilaLog(user, kind, ref, name, description, status, related string) tools.Record {
	b.serial++
	r := tools.Record{ID: fmt.Sprintf("%s-%d", ref, 1000+b.serial), Kind: kind, UserID: user, Name: aquilaToday() + " " + name, Description: description, Status: status, RelatedID: related}
	b.records["aq_events"] = append(b.records["aq_events"], r)
	return r
}

func (b *Backend) aquilaSlotsFor(kind string, a map[string]string, quotas map[string]int) (tools.Result, bool) {
	for _, key := range []string{"from", "to"} {
		if a[key] != "" {
			if _, err := time.Parse("2006-01-02", a[key]); err != nil {
				return tools.Failure("invalid_input", "Dates must use YYYY-MM-DD"), false
			}
		}
	}
	if a["from"] != "" && a["to"] != "" && a["from"] > a["to"] {
		return tools.Failure("invalid_input", "Date range is reversed"), false
	}
	typ := a["type"]
	if typ != "" {
		quotas = map[string]int{typ: 8}
	}
	today := aquilaToday()
	all := []tools.Record{}
	for _, r := range b.records["aq_slots"] {
		date := aquilaSlotDate(r.ID)
		if r.Kind != kind || r.Status != "available" || date <= today {
			continue
		}
		if a["from"] != "" && date < a["from"] || a["to"] != "" && date > a["to"] {
			continue
		}
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	counts := map[string]int{}
	out := []tools.Record{}
	for _, r := range all {
		if counts[r.Specialty] < quotas[r.Specialty] {
			counts[r.Specialty]++
			out = append(out, r)
		}
	}
	return tools.Result{Records: out}, true
}

func (b *Backend) aquilaChildNamed(user, name string) (tools.Record, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range owned(b.records["aq_children"], user) {
		if name != "" && (strings.Contains(name, strings.ToLower(c.Name)) || strings.Contains(strings.ToLower(c.Name), name)) {
			return c, true
		}
	}
	return tools.Record{}, false
}

func (b *Backend) aquila(u *User, name string, a map[string]string) tools.Result {
	switch name {
	case "contact.profile":
		contact := u.Record
		contact.Description = u.Scenario
		children := owned(b.records["aq_children"], u.ID)
		summary := fmt.Sprintf("Contact profile for %s (%s).", u.Name, u.Status)
		if len(children) == 0 {
			summary += " No children are on file yet; this looks like a first contact."
		}
		return result(summary, append([]tools.Record{contact}, children...)...)
	case "crm.history":
		rs := append(owned(b.records["aq_history"], u.ID), owned(b.records["aq_events"], u.ID)...)
		if len(rs) == 0 {
			return result("No previous interactions are on file. This is a first contact, so welcome them warmly and learn about their family.")
		}
		return result(fmt.Sprintf("%d earlier interactions, notes and bookings on file, oldest first.", len(rs)), rs...)
	case "crm.note":
		note := strings.TrimSpace(a["summary"])
		if note == "" {
			return tools.Failure("invalid_input", "A note summary is required")
		}
		return result("Noted on the family record.", b.aquilaLog(u.ID, "note", "NOTE", "Note", note, "recorded", ""))
	case "school.information":
		return b.aquilaInformation(a["topic"])
	case "admissions.availability":
		return b.aquilaAvailability(a["year_group"])
	case "admissions.fee_quote":
		return b.aquilaFeeQuote(a)
	case "admissions.enquire":
		key := aquilaYear(a["year_group"])
		if key == "" {
			return tools.Failure("invalid_input", "Year group must be FS1, FS2 or Year 1 to Year 13")
		}
		if strings.TrimSpace(a["summary"]) == "" {
			return tools.Failure("invalid_input", "Enquiry summary is required")
		}
		r := b.aquilaLog(u.ID, "enquiry", "ENQ", "Admissions enquiry", key+": "+strings.TrimSpace(a["summary"]), "received", "")
		return result("Enquiry logged for the admissions team; a personal reply follows within one working day.", r)
	case "application.start":
		key := aquilaYear(a["year_group"])
		child := strings.TrimSpace(a["child_name"])
		if key == "" || child == "" {
			return tools.Failure("invalid_input", "A child name and a year group (FS1, FS2 or Year 1 to 13) are required")
		}
		for _, app := range owned(b.records["aq_apps"], u.ID) {
			if strings.Contains(strings.ToLower(app.Name), strings.ToLower(child)) {
				return result("An application for "+child+" is already in progress.", app)
			}
		}
		next := "meet and greet with the Head of Primary or a phase leader"
		switch aquilaStage(key) {
		case "Secondary":
			next = "CAT4 assessment and an interview with the Head of Secondary"
		case "Post-16":
			next = "interview with the Head of Secondary and a subject pathway discussion"
		}
		b.serial++
		app := tools.Record{ID: fmt.Sprintf("APP-%d", 1000+b.serial), Kind: "application", UserID: u.ID, Name: fmt.Sprintf("Application for %s (%s)", child, key), Status: "started, documents pending", Description: "Online application opened (no application fee). Next: upload passports, visas, Emirates IDs, the vaccination record and the attested transfer certificate through the secure parent portal, then book the " + next + "."}
		if a["intake"] != "" {
			app.Description += " Intake: " + strings.TrimSpace(a["intake"]) + "."
		}
		b.records["aq_apps"] = append(b.records["aq_apps"], app)
		b.aquilaLog(u.ID, "application", "APPLOG", "Application started for "+child, key, "started", app.ID)
		return result(fmt.Sprintf("Application started for %s (%s). There is no application fee and no place is promised until the offer.", child, key), app)
	case "application.status":
		rs := owned(b.records["aq_apps"], u.ID)
		if len(rs) == 0 {
			return result("No application has been started yet. It takes a few minutes online, there is no application fee, and the team can begin it as soon as the family is ready.")
		}
		return result("Application progress for this family.", rs...)
	case "assessment.availability":
		typ := ""
		t := strings.ToLower(a["type"])
		switch {
		case strings.Contains(t, "cat"):
			typ = "cat4"
		case strings.Contains(t, "meet") || strings.Contains(t, "greet") || strings.Contains(t, "interview"):
			typ = "meet-and-greet"
		}
		query := map[string]string{"type": typ, "from": a["from"], "to": a["to"]}
		res, ok := b.aquilaSlotsFor("assessment_slot", query, map[string]int{"cat4": 4, "meet-and-greet": 4})
		if !ok {
			return res
		}
		return result("Available assessment slots (all times Dubai time). CAT4 is for Secondary entry (Years 7 to 11); the meet and greet is for FS1 to Year 6 and for Post-16 conversations. Choose a slot ID to book.", res.Records...)
	case "assessment.book":
		child := strings.TrimSpace(a["child_name"])
		if child == "" {
			return tools.Failure("invalid_input", "Child name is required")
		}
		for i, s := range b.records["aq_slots"] {
			if s.ID != a["slot_id"] || s.Kind != "assessment_slot" || s.Status != "available" || aquilaSlotDate(s.ID) <= aquilaToday() {
				continue
			}
			b.records["aq_slots"][i].Status = "booked"
			b.records["aq_slots"][i].UserID = u.ID
			booking := b.aquilaLog(u.ID, "assessment_booking", "ASM", s.Name+" for "+child, s.Description, "booked", s.ID)
			for j, app := range b.records["aq_apps"] {
				if app.UserID == u.ID && strings.Contains(strings.ToLower(app.Name), strings.ToLower(child)) {
					b.records["aq_apps"][j].Status = "assessment booked"
					b.records["aq_apps"][j].Description += " Assessment booked: " + s.Description + "."
				}
			}
			return result(fmt.Sprintf("Assessment booked for %s: %s, %s. Please arrive 10 minutes early and bring the child's latest school report.", child, s.Name, s.Description), booking)
		}
		return tools.Failure("slot_unavailable", "That assessment slot is no longer available; please offer another from the availability list")
	case "tour.availability":
		typ := ""
		t := strings.ToLower(a["type"])
		switch {
		case strings.Contains(t, "virtual") || strings.Contains(t, "online") || strings.Contains(t, "video"):
			typ = "virtual"
		case strings.Contains(t, "saturday") || strings.Contains(t, "open"):
			typ = "open-morning"
		case strings.Contains(t, "person") || strings.Contains(t, "campus"):
			typ = "in-person"
		}
		query := map[string]string{"type": typ, "from": a["from"], "to": a["to"]}
		res, ok := b.aquilaSlotsFor("tour_slot", query, map[string]int{"in-person": 4, "virtual": 2, "open-morning": 2})
		if !ok {
			return res
		}
		return result("Available tours (all times Dubai time): weekday morning tours, virtual tours and the Saturday open morning. Choose a slot ID to book.", res.Records...)
	case "tour.book":
		for i, s := range b.records["aq_slots"] {
			if s.ID != a["slot_id"] || s.Kind != "tour_slot" || s.Status != "available" || aquilaSlotDate(s.ID) <= aquilaToday() {
				continue
			}
			b.records["aq_slots"][i].Status = "booked"
			b.records["aq_slots"][i].UserID = u.ID
			booking := b.aquilaLog(u.ID, "tour_booking", "TOUR", s.Name, s.Description, "booked", s.ID)
			booking.Location = s.Location
			b.records["aq_events"][len(b.records["aq_events"])-1].Location = s.Location
			return result(fmt.Sprintf("Tour booked: %s, %s. A confirmation email is on its way; please check in at main reception.", s.Name, s.Description), booking)
		}
		return tools.Failure("slot_unavailable", "That tour slot is no longer available; please offer another from the availability list")
	case "tour.cancel":
		for i, r := range b.records["aq_events"] {
			if r.ID != a["booking_id"] || r.UserID != u.ID || r.Kind != "tour_booking" || r.Status != "booked" {
				continue
			}
			b.records["aq_events"][i].Status = "cancelled"
			for j, s := range b.records["aq_slots"] {
				if s.ID == r.RelatedID && s.UserID == u.ID {
					b.records["aq_slots"][j].Status = "available"
					b.records["aq_slots"][j].UserID = ""
				}
			}
			return result("Tour booking cancelled. The slot has been released and a new one can be booked at any time.", b.records["aq_events"][i])
		}
		return tools.Failure("not_found", "No active tour booking with that reference; check crm.history for the booking reference")
	case "scholarship.check":
		return b.aquilaScholarship(a["year_group"])
	case "staff.meeting_book":
		who, topic := aquilaStaff(a["staff"]), strings.TrimSpace(a["topic"])
		if who == "" || topic == "" {
			return tools.Failure("invalid_input", "Both the colleague and the topic are required")
		}
		local := time.Now().In(dubai)
		date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 3)
		for date.Weekday() == time.Friday || date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			date = date.AddDate(0, 0, 1)
		}
		hour := 9 + b.serial%3
		when := fmt.Sprintf("%s %d %s, %s (Dubai time)", date.Weekday(), date.Day(), date.Month(), aquilaClock(hour))
		r := b.aquilaLog(u.ID, "meeting", "MTG", "Meeting with "+who, when+". Topic: "+topic+". On campus or by video, whichever suits the family.", "confirmed", "")
		return result(fmt.Sprintf("Meeting booked with %s on %s about %s. A calendar invitation follows by email.", who, when, topic), r)
	case "reception.report_absence":
		child, date, reason := strings.TrimSpace(a["child"]), strings.TrimSpace(a["date"]), strings.TrimSpace(a["reason"])
		if child == "" || date == "" || reason == "" {
			return tools.Failure("invalid_input", "Child, date and reason are required")
		}
		switch strings.ToLower(date) {
		case "today":
			date = aquilaToday()
		case "tomorrow":
			date = time.Now().In(dubai).AddDate(0, 0, 1).Format("2006-01-02")
		}
		known := owned(b.records["aq_children"], u.ID)
		if len(known) > 0 {
			c, ok := b.aquilaChildNamed(u.ID, child)
			if !ok {
				return tools.Failure("not_found", "No pupil with that name is linked to this family; ask which child they mean")
			}
			child = c.Name
		}
		r := b.aquilaLog(u.ID, "absence", "ABS", "Absence for "+child, date+": "+reason, "recorded", "")
		return result(fmt.Sprintf("Absence recorded for %s on %s. The class teacher and attendance team have been told, so nothing more is needed today. We hope %s feels better soon.", child, date, child), r)
	case "reception.message_staff":
		who, msg := aquilaStaff(a["staff"]), strings.TrimSpace(a["message"])
		if who == "" || msg == "" {
			return tools.Failure("invalid_input", "Both the colleague and the message are required")
		}
		r := b.aquilaLog(u.ID, "message", "MSG", "Message to "+who, msg, "sent", "")
		return result("Message sent to "+who+". They will reply within one working day.", r)
	case "transport.quote":
		if z, ok := aquilaZoneFor(a["area"]); ok {
			termly := (z.fee + 1) / 3
			return result(fmt.Sprintf("%s is on the school bus network, Zone %d: AED %s per year, paid termly (about AED %s a term). Contact bus@theaquilaschool.com for stop times.", strings.TrimSpace(a["area"]), z.zone, aquilaAED(z.fee), aquilaAED(termly)), aquilaZoneRecord(z))
		}
		rs := []tools.Record{}
		for _, z := range aquilaZones {
			rs = append(rs, aquilaZoneRecord(z))
		}
		area := strings.TrimSpace(a["area"])
		if area == "" {
			area = "that area"
		}
		return result("The school runs buses across Dubai priced by zone. For "+area+" the transport team will confirm the exact route; the indicative fee is between AED 8,927 and AED 9,588 a year, paid termly. All zones are below.", rs...)
	case "transport.request":
		area, child := strings.TrimSpace(a["area"]), strings.TrimSpace(a["child_name"])
		if area == "" || child == "" {
			return tools.Failure("invalid_input", "Area and child name are required")
		}
		fee := "indicative fee AED 8,927 to 9,588 a year"
		if z, ok := aquilaZoneFor(area); ok {
			fee = fmt.Sprintf("Zone %d, AED %s a year", z.zone, aquilaAED(z.fee))
		}
		r := b.aquilaLog(u.ID, "bus_request", "BUS", "Bus seat for "+child, area+" ("+fee+")", "requested", "")
		return result(fmt.Sprintf("Bus seat requested for %s from %s (%s, paid termly). The transport team confirms the stop and times within two working days.", child, area, fee), r)
	case "uniform.info":
		return result("Uniform is from Trutex (trutex.ae, school code TAQS-0001). A full set is about AED 600 to 900.",
			tools.Record{ID: "uniform-eyfs-primary", Kind: "uniform", Name: "EYFS and Primary", Description: "Aquila blue polo shirt."},
			tools.Record{ID: "uniform-secondary", Kind: "uniform", Name: "Secondary", Description: "Striped shirt."},
			tools.Record{ID: "uniform-post16", Kind: "uniform", Name: "Post-16", Description: "Black collared T-shirt."})
	case "clubs.list":
		q := aquilaNorm(a["query"])
		rs := []tools.Record{}
		for i, c := range aquilaClubs {
			hay := aquilaNorm(c.name + " " + c.phase + " " + c.text)
			if strings.TrimSpace(q) != "" {
				matched := false
				for _, w := range strings.Fields(q) {
					if len(w) > 2 && strings.Contains(hay, " "+w) {
						matched = true
					}
				}
				if !matched {
					continue
				}
			}
			rs = append(rs, tools.Record{ID: "club-" + strconv.Itoa(i+1), Kind: "club", Name: c.name, Specialty: c.phase, Status: c.cost, Description: c.text})
		}
		if len(rs) == 0 {
			for i, c := range aquilaClubs {
				rs = append(rs, tools.Record{ID: "club-" + strconv.Itoa(i+1), Kind: "club", Name: c.name, Specialty: c.phase, Status: c.cost, Description: c.text})
			}
			return result("Nothing matched that exact word, so here is the full programme. Many clubs are free; specialist clubs and wrap-around care cost AED 400 to 1,500 per term.", rs...)
		}
		return result("Clubs and wrap-around care. Many clubs are free; specialist clubs and wrap-around care cost AED 400 to 1,500 per term.", rs...)
	case "outreach.leads":
		rs := []tools.Record{}
		ids := []string{}
		for id, user := range b.users["aquila"] {
			if user.Status == "lost lead" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			user := b.users["aquila"][id]
			r := user.Record
			r.Description = user.Scenario
			rs = append(rs, r)
		}
		if len(rs) == 0 {
			return result("Every lead has been followed up for now. The re-engaged families are in their history.")
		}
		return result(fmt.Sprintf("%d lost leads ready for a friendly follow-up call.", len(rs)), rs...)
	case "outreach.log_outcome":
		outcome, notes := strings.TrimSpace(a["outcome"]), strings.TrimSpace(a["notes"])
		valid := map[string]string{"interested": "re-engaged", "tour_booked": "re-engaged", "callback_requested": "re-engaged", "needs_time": "", "not_interested": "do not contact", "no_answer": ""}
		status, ok := valid[outcome]
		if !ok {
			return tools.Failure("invalid_input", "Outcome must be interested, tour_booked, callback_requested, needs_time, not_interested or no_answer")
		}
		if notes == "" {
			return tools.Failure("invalid_input", "Notes are required")
		}
		if status != "" {
			u.Status = status
		}
		r := b.aquilaLog(u.ID, "outreach_outcome", "OUT", "Outreach outcome: "+outcome, notes, "recorded", "")
		return result("Outcome logged: "+outcome+".", r)
	case "callback.schedule":
		when, topic := strings.TrimSpace(a["when"]), strings.TrimSpace(a["topic"])
		if when == "" || topic == "" {
			return tools.Failure("invalid_input", "Both when and topic are required")
		}
		r := b.aquilaLog(u.ID, "callback", "CB", "Callback", when+". Topic: "+topic, "scheduled", "")
		return result("Callback scheduled for "+when+" about "+topic+". The family will be called on the number we hold.", r)
	}
	return tools.Failure("unknown_tool", "Tool not available in aquila backend")
}

func (b *Backend) aquilaInformation(topic string) tools.Result {
	padded := aquilaNorm(topic)
	matches := []aquilaTopic{}
	for _, t := range aquilaTopics {
		for _, k := range t.keys {
			if aquilaMatches(padded, k) {
				matches = append(matches, t)
				break
			}
		}
		if len(matches) == 3 {
			break
		}
	}
	rs := []tools.Record{}
	if len(matches) == 0 {
		rs = append(rs, tools.Record{ID: "info-overview", Kind: "information", Name: aquilaOverview.title, Description: aquilaOverview.text})
		return result("Here is what we can say with confidence about The Aquila School. A colleague can add detail on that specific point (info@theaquilaschool.com, +971 4 586 2700), or we can arrange a tour so the family sees it first-hand.", rs...)
	}
	for i, t := range matches {
		rs = append(rs, tools.Record{ID: "info-" + strconv.Itoa(i+1), Kind: "information", Name: t.title, Description: t.text})
	}
	return result("Published Aquila School information on "+matches[0].title+".", rs...)
}

func (b *Backend) aquilaAvailability(year string) tools.Result {
	groups := aquilaYearGroups()
	if strings.TrimSpace(year) != "" {
		key := aquilaYear(year)
		if key == "" {
			return tools.Failure("invalid_input", "Year group must be FS1, FS2 or Year 1 to Year 13")
		}
		groups = []string{key}
	}
	rs := []tools.Record{}
	for _, g := range groups {
		status, note := "places available", "Sibling priority applies."
		if g == "FS2" || g == "Year 1" {
			status, note = "small waiting list", "Families who complete the meet and greet are placed on the priority list, and sibling priority applies. A waiting-list offer is held for one week."
		}
		if g == "Year 10" {
			status, note = "places available (small waiting list for some option blocks)", "Option blocks can be matched at the meet and greet."
		}
		rs = append(rs, tools.Record{ID: "avail-" + strings.ReplaceAll(strings.ToLower(g), " ", ""), Kind: "availability", Name: g, Status: status, Specialty: aquilaStage(g), Description: fmt.Sprintf("%s, %s. Classes of about 20 to 24 pupils. %s", g, aquilaAges(g), note), Amount: float64(aquilaTuition(g)), Currency: "AED"})
	}
	return result("2026-27 and 2027-28 availability with annual tuition in AED. Most year groups have places; FS2 and Year 1 have small waiting lists.", rs...)
}

func (b *Backend) aquilaScholarship(year string) tools.Result {
	key := ""
	if strings.TrimSpace(year) != "" {
		if key = aquilaYear(year); key == "" {
			return tools.Failure("invalid_input", "Year group must be FS1, FS2 or Year 1 to Year 13")
		}
	}
	n := aquilaYearNumber(key)
	rs := []tools.Record{}
	if key == "" || n >= 7 && n <= 10 || n == 12 {
		rs = append(rs, tools.Record{ID: "sch-isp", Kind: "scholarship", Name: "ISP Middle East Academic Excellence and International Learner scholarships", Status: "open each spring", Description: "Years 7 to 10 and Year 12. Awards of 25% to 100% of tuition from 100 ISP Middle East scholarships. Requirements: 95% attendance, two extra-curricular activities and a 300 to 500 word essay, then assessment by the senior leadership team and the Principal. Decisions by June."})
	}
	if key == "" || n == 12 {
		rs = append(rs, tools.Record{ID: "sch-leaders", Kind: "scholarship", Name: "Tomorrow's Leaders scholarship", Status: "open each spring", Description: "Year 12, with five or more GCSEs at grade 6 or above. Same essay, attendance and extra-curricular requirements."})
	}
	if key == "" || n >= 7 {
		rs = append(rs, tools.Record{ID: "sch-arts", Kind: "scholarship", Name: "Performing arts scholarships", Status: "open each spring", Description: "Awards of up to 75% of tuition for exceptional drama, dance and music."})
	}
	if len(rs) == 0 {
		rs = append(rs, tools.Record{ID: "sch-pathway", Kind: "scholarship", Name: "Scholarship pathway", Status: "from Year 7", Description: "Scholarships begin in Year 7, so a child in " + key + " can be flagged now for the first round they qualify for. In the meantime the sibling discount, early-bird full-year payment and the Family Circle referral can all reduce tuition today."})
	}
	return result("Scholarship options. Applications open each spring for the following September, with decisions by June.", rs...)
}

// aquilaFeeQuote prices 2026-27 tuition in whole AED using integer arithmetic.
// Children are ordered by fee, highest first, so the oldest and most expensive is child one.
// Sibling, Family Circle (20%) and early-bird percentages are added and capped at 100%.
func (b *Backend) aquilaFeeQuote(a map[string]string) tools.Result {
	raw := strings.ReplaceAll(strings.ToLower(a["year_group"]), " and ", ",")
	groups := []string{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '&' || r == '+' }) {
		key := aquilaYear(part)
		if key == "" {
			return tools.Failure("invalid_input", "Year group must be FS1, FS2 or Year 1 to Year 13; list several separated by commas")
		}
		groups = append(groups, key)
	}
	if len(groups) == 0 {
		return tools.Failure("invalid_input", "At least one year group is required")
	}
	if n, err := strconv.Atoi(strings.TrimSpace(a["children"])); err == nil && n > len(groups) {
		for len(groups) < n && len(groups) <= 8 {
			groups = append(groups, groups[0])
		}
	}
	if len(groups) > 8 {
		return tools.Failure("invalid_input", "A quote covers up to 8 children")
	}
	sort.SliceStable(groups, func(i, j int) bool { return aquilaTuition(groups[i]) > aquilaTuition(groups[j]) })

	pay := strings.ToLower(a["payment"])
	annual := false
	for _, k := range []string{"annual", "full", "upfront", "up front", "advance", "early"} {
		if strings.Contains(pay, k) {
			annual = true
		}
	}
	early := 0
	if annual {
		early = 7
		if strings.Contains(pay, "june") {
			early = 5
		}
		if strings.Contains(pay, "july") {
			early = 3
		}
	}
	ref := strings.ToLower(strings.TrimSpace(a["referral"]))
	referral := ref != "" && ref != "no" && ref != "none" && ref != "false" && ref != "n" && ref != "0"

	siblings := []int{0, 5, 15, 25, 100}
	rs := []tools.Record{}
	standard, total := 0, 0
	for i, g := range groups {
		fee := aquilaTuition(g)
		sib := siblings[len(siblings)-1]
		if i < len(siblings) {
			sib = siblings[i]
		}
		pct, parts := sib+early, []string{}
		if sib > 0 {
			parts = append(parts, fmt.Sprintf("sibling %d%%", sib))
		}
		if referral {
			pct += 20
			parts = append(parts, "Family Circle referral 20%")
		}
		if early > 0 {
			parts = append(parts, fmt.Sprintf("early bird %d%%", early))
		}
		if pct > 100 {
			pct = 100
		}
		net := (fee*(100-pct) + 50) / 100
		standard += fee
		total += net
		detail := "no discounts"
		if len(parts) > 0 {
			detail = strings.Join(parts, " + ")
		}
		rs = append(rs, tools.Record{ID: fmt.Sprintf("fee-%d", i+1), Kind: "fee_line", Name: fmt.Sprintf("Child %d, %s", i+1, g), Description: fmt.Sprintf("Standard AED %s; %s; %d%% off", aquilaAED(fee), detail, pct), Amount: float64(net), Currency: "AED", Value: float64(pct), Unit: "% off"})
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "2026-27 tuition quote for %d child(ren), KHDA-approved fees in AED. Standard total AED %s; after discounts AED %s a year (saving AED %s).", len(groups), aquilaAED(standard), aquilaAED(total), aquilaAED(standard-total))
	if annual {
		fmt.Fprintf(&sb, " Paid in full in one payment: AED %s, with the %d%% early-bird rate included.", aquilaAED(total), early)
	} else {
		t1 := (total*40 + 50) / 100
		t2 := (total*30 + 50) / 100
		fmt.Fprintf(&sb, " Paid termly (40/30/30): AED %s, AED %s and AED %s.", aquilaAED(t1), aquilaAED(t2), aquilaAED(total-t1-t2))
		sb.WriteString(" Paying the full year up front earns an early-bird discount of 7% by 31 May, 5% by 30 June or 3% by 31 July.")
	}
	if !referral {
		sb.WriteString(" The Aquila Family Circle referral gives a new family 20% off 2026-27 tuition until 31 October 2026.")
	}
	if len(groups) == 1 {
		sb.WriteString(" Sibling discounts start at 5% for a second child.")
	}
	sb.WriteString(" Discounts are added together. A deposit of AED 5,000 is credited against Term 1 fees and there is no application fee. Transport, uniform and optional clubs are extra.")
	rs = append(rs, tools.Record{ID: "fee-total", Kind: "fee_total", Name: "Total annual tuition", Description: fmt.Sprintf("Standard AED %s; saving AED %s", aquilaAED(standard), aquilaAED(standard-total)), Amount: float64(total), Currency: "AED"})
	return result(sb.String(), rs...)
}
