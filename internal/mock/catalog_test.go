package mock

import (
	"encoding/json"
	"enterprise-ai-demo/internal/skills"
	"enterprise-ai-demo/internal/tools"
	"strings"
	"testing"
	"time"
)

func TestEveryConfiguredToolHasWorkingBackend(t *testing.T) {
	for _, industry := range []string{"telecom", "hospital", "school"} {
		catalog, e := skills.Load("../../agents/" + industry)
		if e != nil {
			t.Fatal(e)
		}
		for _, skill := range catalog {
			for _, d := range skill.Tools {
				t.Run(d.Name, func(t *testing.T) {
					b := fixture(t)
					user := "C001"
					if industry == "hospital" {
						user = "P001"
					}
					if industry == "school" {
						user = "F001"
					}
					slots := call(b, "hospital", "P001", "appointment.availability", nil)
					args := map[string]string{}
					examples := map[string]string{"subscription_id": "SUB-C001", "ticket_id": "T-C001", "summary": "Test service request", "status": "open", "plan_id": "fiber-500", "channel": "11", "slot_id": slots.Records[0].ID, "doctor_id": "D001", "department_id": "DEP01", "appointment_id": "A-P001", "provider": "DemoCare", "plan": "Plus", "facility_id": "parking", "from": time.Now().UTC().Format("2006-01-02")}
					if d.Name == "technician.book" {
						examples["slot_id"] = "TECH-01"
					}
					if industry == "school" {
						examples["program_id"] = "primary"
						examples["slot_id"] = call(b, "school", user, "tour.availability", nil).Records[0].ID
						if d.Name == "tour.cancel" {
							examples["visit_id"] = call(b, "school", user, "tour.book", map[string]string{"slot_id": examples["slot_id"]}).Records[0].ID
						}
					}
					for key := range d.Input.Properties {
						if v, ok := examples[key]; ok {
							args[key] = v
						}
					}
					out := b.Execute(tools.Request{Industry: industry, UserID: user, Name: d.Name, Arguments: args})
					if out.Error != nil {
						t.Fatalf("%s: %+v", d.Name, out.Error)
					}
					if out.Summary == "" {
						t.Fatal("missing typed result summary")
					}
				})
			}
		}
	}
}

// Every Aquila persona exposes a subset of the aquila backend operations. Each configured
// tool must work, and together the personas must reach every operation the backend implements.
func TestEveryAquilaToolHasWorkingBackend(t *testing.T) {
	reached := map[string]bool{}
	for _, persona := range []string{"aquila-admissions", "aquila-reception", "aquila-outreach"} {
		catalog, e := skills.Load("../../agents/" + persona)
		if e != nil {
			t.Fatal(e)
		}
		for _, skill := range catalog {
			for _, d := range skill.Tools {
				reached[d.Name] = true
				t.Run(persona+"/"+d.Name, func(t *testing.T) {
					b := fixture(t)
					const user = "F001"
					tour := call(b, "aquila", user, "tour.availability", nil).Records[0]
					assessment := call(b, "aquila", user, "assessment.availability", nil).Records[0]
					examples := map[string]string{
						"summary": "2026-10-09: Test note.", "topic": "Hemam support", "year_group": "Year 7", "children": "2", "payment": "annual", "referral": "yes",
						"child_name": "Omar", "intake": "January 2027", "type": "in-person", "from": time.Now().UTC().Format("2006-01-02"),
						"slot_id": tour.ID, "when": tour.Description, "staff": "Head of Inclusion", "child": "Layla", "date": "today", "reason": "fever",
						"message": "Please call me back.", "area": "Arabian Ranches", "query": "Arabic", "outcome": "interested", "notes": "Keen to visit.",
					}
					switch d.Name {
					case "assessment.book":
						examples["slot_id"], examples["when"] = assessment.ID, assessment.Description
					case "tour.cancel":
						booked := call(b, "aquila", user, "tour.book", map[string]string{"slot_id": tour.ID, "when": tour.Description})
						examples["booking_id"] = booked.Records[0].ID
					case "callback.schedule":
						examples["when"] = "Sunday at 10 am"
					case "application.start":
						examples["year_group"] = "FS2"
					case "assessment.availability":
						examples["type"] = "cat4"
					}
					args := map[string]string{}
					for key := range d.Input.Properties {
						if v, ok := examples[key]; ok {
							args[key] = v
						}
					}
					for _, required := range d.Input.Required {
						if args[required] == "" {
							t.Fatalf("no example for required argument %s", required)
						}
					}
					if _, err := d.Validate(mustJSON(t, args)); err != nil {
						t.Fatal(err)
					}
					out := b.Execute(tools.Request{Industry: "aquila", UserID: user, Name: d.Name, Arguments: args})
					if out.Error != nil {
						t.Fatalf("%s: %+v", d.Name, out.Error)
					}
					if out.Summary == "" {
						t.Fatal("missing typed result summary")
					}
					if d.MemoryEffect != nil && !strings.Contains(out.Summary, d.MemoryEffect.Contains) {
						t.Fatalf("memory effect %q would never fire for summary %q", d.MemoryEffect.Contains, out.Summary)
					}
				})
			}
		}
	}
	for _, op := range []string{
		"contact.profile", "crm.history", "crm.note", "school.information", "admissions.availability", "admissions.fee_quote", "admissions.enquire",
		"application.start", "application.status", "assessment.availability", "assessment.book", "tour.availability", "tour.book", "tour.cancel",
		"scholarship.check", "staff.meeting_book", "reception.report_absence", "reception.message_staff", "transport.quote", "transport.request",
		"uniform.info", "clubs.list", "outreach.leads", "outreach.log_outcome", "callback.schedule",
	} {
		if !reached[op] {
			t.Errorf("operation %s is not exposed by any Aquila persona", op)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
