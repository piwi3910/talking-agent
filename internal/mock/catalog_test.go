package mock

import (
	"enterprise-ai-demo/internal/skills"
	"enterprise-ai-demo/internal/tools"
	"testing"
	"time"
)

func TestEveryConfiguredToolHasWorkingBackend(t *testing.T) {
	for _, industry := range []string{"telecom", "hospital"} {
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
					slots := call(b, "hospital", "P001", "appointment.availability", nil)
					args := map[string]string{}
					examples := map[string]string{"subscription_id": "SUB-C001", "ticket_id": "T-C001", "summary": "Test service request", "status": "open", "plan_id": "fiber-500", "channel": "11", "slot_id": slots.Records[0].ID, "doctor_id": "D001", "department_id": "DEP01", "appointment_id": "A-P001", "provider": "DemoCare", "plan": "Plus", "facility_id": "parking", "from": time.Now().UTC().Format("2006-01-02")}
					if d.Name == "technician.book" {
						examples["slot_id"] = "TECH-01"
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
