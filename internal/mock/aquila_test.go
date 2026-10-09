package mock

import (
	"enterprise-ai-demo/internal/tools"
	"strings"
	"testing"
)

func aq(b *Backend, user, name string, args map[string]string) tools.Result {
	return call(b, "aquila", user, name, args)
}

func TestAquilaContacts(t *testing.T) {
	b := fixture(t)
	users := b.Users("aquila")
	if len(users) != 7 {
		t.Fatalf("expected 7 contacts, got %d", len(users))
	}
	for _, u := range users {
		if u.Description == "" {
			t.Fatalf("%s has no scenario for the identity dropdown", u.ID)
		}
		if r := aq(b, u.ID, "contact.profile", nil); r.Error != nil || len(r.Records) == 0 || r.Records[0].ID != u.ID {
			t.Fatalf("%s profile: %+v", u.ID, r)
		}
	}
	if got := aq(b, "F003", "crm.history", nil); got.Error != nil || len(got.Records) != 0 || !strings.Contains(got.Summary, "first contact") {
		t.Fatalf("new enquiry must have no history: %+v", got)
	}
	if got := aq(b, "L001", "contact.profile", nil); len(got.Records) != 3 {
		t.Fatalf("Sarah should list herself and two children: %+v", got.Records)
	}
	if got := aq(b, "L001", "crm.history", nil); len(got.Records) < 3 {
		t.Fatalf("Sarah history too thin: %+v", got)
	}
	if got := aq(b, "L002", "application.status", nil); len(got.Records) != 1 || !strings.Contains(got.Records[0].Status, "CAT4") {
		t.Fatalf("Menon application: %+v", got)
	}
	if call(b, "aquila", "C001", "crm.history", nil).Error == nil {
		t.Fatal("a telecom identity must not be an aquila contact")
	}
	if len(call(b, "aquila", "L001", "bogus.op", nil).Summary) != 0 || call(b, "aquila", "L001", "bogus.op", nil).Error == nil {
		t.Fatal("unknown aquila tool accepted")
	}
}

func TestAquilaFeeQuoteStacksSiblingEarlyBirdAndReferral(t *testing.T) {
	b := fixture(t)
	got := aq(b, "L001", "admissions.fee_quote", map[string]string{"year_group": "FS1, FS2", "payment": "annual", "referral": "yes"})
	if got.Error != nil {
		t.Fatal(got.Error)
	}
	// Child 1 (FS2, highest fee): 20% referral + 7% early bird. Child 2 (FS1): plus 5% sibling.
	want := map[string]float64{"fee-1": 37899, "fee-2": 33098, "fee-total": 70997}
	for _, r := range got.Records {
		if amount, ok := want[r.ID]; ok {
			if r.Amount != amount || r.Currency != "AED" {
				t.Fatalf("%s: %+v, want AED %.0f", r.ID, r, amount)
			}
			delete(want, r.ID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing quote lines %v in %+v", want, got.Records)
	}
	for _, text := range []string{"Standard total AED 100,590", "AED 70,997 a year", "saving AED 29,593", "early-bird rate", "AED 5,000"} {
		if !strings.Contains(got.Summary, text) {
			t.Fatalf("summary lacks %q: %s", text, got.Summary)
		}
	}
	if !strings.Contains(got.Records[1].Description, "sibling 5%") || got.Records[0].Value != 27 || got.Records[1].Value != 32 {
		t.Fatalf("discount detail: %+v", got.Records)
	}

	termly := aq(b, "L003", "admissions.fee_quote", map[string]string{"year_group": "FS1"})
	if termly.Error != nil || termly.Records[0].Amount != 48673 {
		t.Fatalf("single child, no discounts: %+v", termly)
	}
	if !strings.Contains(termly.Summary, "AED 19,469, AED 14,602 and AED 14,602") || !strings.Contains(termly.Summary, "Family Circle") {
		t.Fatalf("termly instalments or referral hint missing: %s", termly.Summary)
	}

	same := aq(b, "L003", "admissions.fee_quote", map[string]string{"year_group": "Year 4", "children": "2"})
	if same.Error != nil || len(same.Records) != 3 || same.Records[0].Amount != 56244 || same.Records[1].Amount != 53432 {
		t.Fatalf("twins in Year 4 (second child 5%% off): %+v", same.Records)
	}
	for year, fee := range map[string]float64{"FS2": 51917, "Year 1": 54081, "Year 4": 56244, "Year 6": 59489, "Year 8": 64897, "Year 10": 71386, "Year 13": 77876} {
		if r := aq(b, "L001", "admissions.fee_quote", map[string]string{"year_group": year}); r.Error != nil || r.Records[0].Amount != fee {
			t.Fatalf("%s fee: %+v", year, r)
		}
	}
	if aq(b, "L001", "admissions.fee_quote", map[string]string{"year_group": "Year 14"}).Error == nil {
		t.Fatal("invalid year group accepted")
	}
	five := aq(b, "L001", "admissions.fee_quote", map[string]string{"year_group": "FS1, FS1, FS1, FS1, FS1"})
	if five.Error != nil || five.Records[4].Amount != 0 {
		t.Fatalf("fifth child should be free: %+v", five.Records)
	}
}

func TestAquilaInformationAlwaysSubstantive(t *testing.T) {
	b := fixture(t)
	topics := []string{"", "Hemam support", "school day timings", "bus from JVC", "scholarships", "Family Circle", "uniform", "xyzzy completely unknown topic", "message", "business studies"}
	for _, topic := range topics {
		got := aq(b, "F003", "school.information", map[string]string{"topic": topic})
		if got.Error != nil || got.Summary == "" || len(got.Records) == 0 || len(got.Records[0].Description) < 100 {
			t.Fatalf("topic %q was not answered substantively: %+v", topic, got)
		}
	}
	if got := aq(b, "F003", "school.information", map[string]string{"topic": "Hemam support"}); !strings.Contains(got.Records[0].Description, "Hemam") {
		t.Fatalf("Hemam topic: %+v", got.Records)
	}
	if got := aq(b, "F003", "school.information", map[string]string{"topic": "message"}); got.Records[0].ID != "info-overview" {
		t.Fatalf("substring keys must not match inside other words: %+v", got.Records)
	}
	if got := aq(b, "F003", "school.information", map[string]string{"topic": "unknown"}); got.Records[0].ID != "info-overview" || !strings.Contains(got.Summary, "tour") {
		t.Fatalf("unknown topics get the positive overview: %+v", got)
	}
}

func TestAquilaSlotsSimulateBookingAndWritesAreVisibleLater(t *testing.T) {
	b := fixture(t)
	tours := aq(b, "L001", "tour.availability", nil)
	if tours.Error != nil || len(tours.Records) < 3 {
		t.Fatalf("tours: %+v", tours)
	}
	types := map[string]bool{}
	for _, r := range tours.Records {
		types[r.Specialty] = true
		if !strings.Contains(r.Description, "Dubai time") || r.Status != "available" {
			t.Fatalf("slot %+v", r)
		}
	}
	if !types["in-person"] || !types["virtual"] {
		t.Fatalf("expected in-person and virtual options: %v", types)
	}
	inPerson := aq(b, "L001", "tour.availability", map[string]string{"type": "in-person"})
	slot := inPerson.Records[0]
	if aq(b, "L001", "tour.availability", map[string]string{"from": "2026-13-01"}).Error == nil {
		t.Fatal("bad date accepted")
	}
	booked := aq(b, "L001", "tour.book", map[string]string{"slot_id": slot.ID, "when": slot.Description})
	if booked.Error != nil || !strings.Contains(booked.Summary, "Tour booked") {
		t.Fatalf("booking: %+v", booked)
	}
	if aq(b, "L002", "tour.book", map[string]string{"slot_id": slot.ID, "when": "x"}).Error == nil {
		t.Fatal("slot double booked")
	}
	for _, r := range aq(b, "L002", "tour.availability", map[string]string{"type": "in-person"}).Records {
		if r.ID == slot.ID {
			t.Fatal("booked slot still offered")
		}
	}
	history := aq(b, "L001", "crm.history", nil)
	var bookingID string
	for _, r := range history.Records {
		if r.Kind == "tour_booking" && r.Status == "booked" {
			bookingID = r.ID
		}
	}
	if bookingID == "" {
		t.Fatalf("booking not visible in history: %+v", history.Records)
	}
	if aq(b, "L002", "crm.history", nil).Records[len(aq(b, "L002", "crm.history", nil).Records)-1].Kind == "tour_booking" {
		t.Fatal("booking leaked to another contact")
	}
	if aq(b, "L002", "tour.cancel", map[string]string{"booking_id": bookingID}).Error == nil {
		t.Fatal("cancelled another family's booking")
	}
	if r := aq(b, "L001", "tour.cancel", map[string]string{"booking_id": bookingID}); r.Error != nil {
		t.Fatalf("cancel: %+v", r)
	}
	found := false
	for _, r := range aq(b, "L002", "tour.availability", map[string]string{"type": "in-person"}).Records {
		found = found || r.ID == slot.ID
	}
	if !found {
		t.Fatal("cancelled slot not released")
	}

	note := aq(b, "L001", "crm.note", map[string]string{"summary": "2026-10-09: Sarah prefers morning tours."})
	if note.Error != nil || !strings.Contains(note.Summary, "Noted") {
		t.Fatalf("note: %+v", note)
	}
	last := aq(b, "L001", "crm.history", nil).Records
	if !strings.Contains(last[len(last)-1].Description, "prefers morning tours") {
		t.Fatalf("note not visible later: %+v", last[len(last)-1])
	}
	if aq(b, "L001", "crm.note", map[string]string{"summary": "  "}).Error == nil {
		t.Fatal("empty note accepted")
	}
}

func TestAquilaAssessmentAdvancesApplication(t *testing.T) {
	b := fixture(t)
	slots := aq(b, "L002", "assessment.availability", map[string]string{"type": "cat4"})
	if slots.Error != nil || len(slots.Records) == 0 || slots.Records[0].Specialty != "cat4" {
		t.Fatalf("CAT4 slots: %+v", slots)
	}
	slot := slots.Records[0]
	if r := aq(b, "L002", "assessment.book", map[string]string{"slot_id": slot.ID, "child_name": "Arjun", "when": slot.Description}); r.Error != nil || !strings.Contains(r.Summary, "Assessment booked") {
		t.Fatalf("assessment: %+v", r)
	}
	app := aq(b, "L002", "application.status", nil).Records
	if len(app) != 1 || app[0].Status != "assessment booked" {
		t.Fatalf("application did not advance: %+v", app)
	}
	started := aq(b, "L001", "application.start", map[string]string{"child_name": "Mia", "year_group": "FS2", "intake": "January 2027"})
	if started.Error != nil || !strings.Contains(started.Summary, "Application started") {
		t.Fatalf("start: %+v", started)
	}
	if again := aq(b, "L001", "application.start", map[string]string{"child_name": "Mia", "year_group": "FS2"}); again.Error != nil || !strings.Contains(again.Summary, "already in progress") {
		t.Fatalf("second start: %+v", again)
	}
	if status := aq(b, "L001", "application.status", nil).Records; len(status) != 1 || !strings.Contains(status[0].Name, "Mia") {
		t.Fatalf("started application not visible: %+v", status)
	}
	if aq(b, "F003", "application.status", nil).Records == nil || len(aq(b, "F003", "application.status", nil).Records) != 0 {
		t.Fatal("new enquiry should have no application")
	}
}

func TestAquilaReceptionOperations(t *testing.T) {
	b := fixture(t)
	if r := aq(b, "F001", "reception.report_absence", map[string]string{"child": "Layla", "date": "today", "reason": "fever"}); r.Error != nil || !strings.Contains(r.Summary, "Absence recorded for Layla") {
		t.Fatalf("absence: %+v", r)
	}
	if aq(b, "F001", "reception.report_absence", map[string]string{"child": "Zed", "date": "today", "reason": "fever"}).Error == nil {
		t.Fatal("absence accepted for a pupil outside the family")
	}
	if aq(b, "F001", "reception.report_absence", map[string]string{"child": "Layla", "date": "", "reason": "fever"}).Error == nil {
		t.Fatal("missing date accepted")
	}
	history := aq(b, "F001", "crm.history", nil).Records
	if got := history[len(history)-1]; got.Kind != "absence" || !strings.Contains(got.Description, "fever") {
		t.Fatalf("absence not visible later: %+v", got)
	}
	if r := aq(b, "F001", "reception.message_staff", map[string]string{"staff": "head of primary", "message": "Omar needs a catch-up plan."}); r.Error != nil || !strings.Contains(r.Summary, "Kylie Cleworth, Head of Primary") {
		t.Fatalf("message: %+v", r)
	}
	if r := aq(b, "L004", "staff.meeting_book", map[string]string{"staff": "Head of Inclusion", "topic": "Karim's support plan"}); r.Error != nil || !strings.Contains(r.Summary, "Claire Hitchings, Head of Inclusion") || !strings.Contains(r.Summary, "Dubai time") {
		t.Fatalf("meeting: %+v", r)
	}
	for area, want := range map[string]string{"Dubai Silicon Oasis": "AED 6,877", "Arabian Ranches": "AED 8,927", "JVC": "AED 9,588", "Mudon": "Zone 2"} {
		if r := aq(b, "F001", "transport.quote", map[string]string{"area": area}); r.Error != nil || !strings.Contains(r.Summary, want) {
			t.Fatalf("bus %s: %+v", area, r)
		}
	}
	if r := aq(b, "F001", "transport.quote", map[string]string{"area": "Downtown"}); r.Error != nil || len(r.Records) != 3 || !strings.Contains(r.Summary, "indicative") {
		t.Fatalf("unknown area must still get a positive answer: %+v", r)
	}
	if r := aq(b, "F001", "transport.request", map[string]string{"area": "Arabian Ranches", "child_name": "Omar"}); r.Error != nil || !strings.Contains(r.Summary, "Bus seat requested") {
		t.Fatalf("bus request: %+v", r)
	}
	if r := aq(b, "F001", "clubs.list", map[string]string{"query": "Arabic"}); r.Error != nil || len(r.Records) == 0 || !strings.Contains(r.Records[0].Name, "Arabic") {
		t.Fatalf("clubs: %+v", r)
	}
	if r := aq(b, "F001", "clubs.list", map[string]string{"query": "zzzz"}); r.Error != nil || len(r.Records) < 10 {
		t.Fatalf("unmatched club query should list everything: %+v", r)
	}
	if r := aq(b, "F001", "uniform.info", nil); r.Error != nil || len(r.Records) != 3 || !strings.Contains(r.Summary, "AED 600 to 900") {
		t.Fatalf("uniform: %+v", r)
	}
}

func TestAquilaScholarshipAndAvailability(t *testing.T) {
	b := fixture(t)
	if r := aq(b, "F002", "scholarship.check", map[string]string{"year_group": "Year 12"}); r.Error != nil || len(r.Records) != 3 {
		t.Fatalf("Year 12 scholarships: %+v", r)
	}
	if r := aq(b, "L002", "scholarship.check", map[string]string{"year_group": "Year 7"}); r.Error != nil || len(r.Records) != 2 {
		t.Fatalf("Year 7 scholarships: %+v", r)
	}
	if r := aq(b, "L003", "scholarship.check", map[string]string{"year_group": "Year 4"}); r.Error != nil || len(r.Records) != 1 || !strings.Contains(r.Records[0].Description, "Year 7") {
		t.Fatalf("Year 4 scholarship pathway: %+v", r)
	}
	if r := aq(b, "L001", "admissions.availability", map[string]string{"year_group": "fs2"}); r.Error != nil || len(r.Records) != 1 || r.Records[0].Status != "small waiting list" || r.Records[0].Amount != 51917 {
		t.Fatalf("FS2 availability: %+v", r)
	}
	if r := aq(b, "L001", "admissions.availability", nil); r.Error != nil || len(r.Records) != 15 {
		t.Fatalf("all year groups: %+v", r)
	}
}

func TestAquilaOutreachOutcomesChangeLaterReads(t *testing.T) {
	b := fixture(t)
	leads := aq(b, "L001", "outreach.leads", nil)
	if leads.Error != nil || len(leads.Records) != 3 {
		t.Fatalf("expected the three lost leads: %+v", leads)
	}
	if aq(b, "L001", "outreach.log_outcome", map[string]string{"outcome": "maybe", "notes": "x"}).Error == nil {
		t.Fatal("unknown outcome accepted")
	}
	if r := aq(b, "L001", "outreach.log_outcome", map[string]string{"outcome": "tour_booked", "notes": "Booked a Saturday tour."}); r.Error != nil || !strings.Contains(r.Summary, "Outcome logged") {
		t.Fatalf("outcome: %+v", r)
	}
	if aq(b, "L001", "contact.profile", nil).Records[0].Status != "re-engaged" {
		t.Fatal("outcome did not update the contact status")
	}
	if r := aq(b, "L002", "outreach.log_outcome", map[string]string{"outcome": "not_interested", "notes": "Chose another school."}); r.Error != nil {
		t.Fatal(r.Error)
	}
	for _, r := range aq(b, "L001", "outreach.leads", nil).Records {
		if r.ID == "L001" || r.ID == "L002" {
			t.Fatalf("%s should have left the lost-lead list", r.ID)
		}
	}
	if r := aq(b, "L003", "callback.schedule", map[string]string{"when": "Sunday 10 am", "topic": "visa and school start"}); r.Error != nil || !strings.Contains(r.Summary, "Callback scheduled") {
		t.Fatalf("callback: %+v", r)
	}
	history := aq(b, "L003", "crm.history", nil).Records
	if got := history[len(history)-1]; got.Kind != "callback" || !strings.Contains(got.Description, "Sunday 10 am") {
		t.Fatalf("callback not visible later: %+v", got)
	}
}

func TestAquilaEnquiryVisibleLater(t *testing.T) {
	b := fixture(t)
	if aq(b, "F003", "admissions.enquire", map[string]string{"year_group": "Reception", "summary": ""}).Error == nil {
		t.Fatal("empty enquiry accepted")
	}
	r := aq(b, "F003", "admissions.enquire", map[string]string{"year_group": "Year 2", "summary": "Looking for January entry"})
	if r.Error != nil || !strings.Contains(r.Summary, "Enquiry logged") {
		t.Fatalf("enquiry: %+v", r)
	}
	history := aq(b, "F003", "crm.history", nil)
	if len(history.Records) != 1 || !strings.Contains(history.Records[0].Description, "Year 2: Looking for January entry") {
		t.Fatalf("enquiry not visible later: %+v", history)
	}
}
