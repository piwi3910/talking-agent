package mock

import (
	"enterprise-ai-demo/internal/tools"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) *Backend {
	t.Helper()
	b, e := Load("../../mock", time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func call(b *Backend, industry, user, name string, args map[string]string) tools.Result {
	return b.Execute(tools.Request{Industry: industry, UserID: user, Name: name, Arguments: args})
}
func TestTelecomScenarios(t *testing.T) {
	b := fixture(t)
	if len(b.Users("telecom")) < 20 {
		t.Fatal("not enough customers")
	}
	cases := []struct{ user, tool, want string }{{"C001", "wifi.diagnostics", "interference"}, {"C002", "network.outages", "regional outage"}, {"C003", "billing.explain", "overdue"}, {"C004", "billing.explain", "$35 roaming"}, {"C005", "usage.current", "throttled"}, {"C006", "network.diagnostics", "unhealthy"}, {"C007", "network.diagnostics", "technician"}, {"C008", "plan.compare", "fiber-100"}}
	for _, tc := range cases {
		t.Run(tc.user, func(t *testing.T) {
			r := call(b, "telecom", tc.user, tc.tool, nil)
			if r.Error != nil || !strings.Contains(r.Summary, tc.want) {
				t.Fatalf("%+v", r)
			}
		})
	}
	if call(b, "telecom", "C002", "wifi.restart", nil).Error == nil {
		t.Fatal("restart allowed during outage")
	}
	if r := call(b, "telecom", "C006", "wifi.restart", nil); r.Error != nil {
		t.Fatal(r)
	}
	if strings.Contains(call(b, "telecom", "C006", "network.diagnostics", nil).Summary, "unhealthy") {
		t.Fatal("restart did not repair router")
	}
	if r := call(b, "telecom", "C001", "wifi.optimize", map[string]string{"channel": "11"}); r.Error != nil {
		t.Fatal(r)
	}
	if call(b, "telecom", "C001", "wifi.diagnostics", nil).Records[0].Status != "healthy" {
		t.Fatal("optimization did not change state")
	}
}
func TestHospitalAtomicBookingsAndOwnership(t *testing.T) {
	b := fixture(t)
	if len(b.Users("hospital")) < 20 || len(b.catalogs["doctors"]) < 20 {
		t.Fatal("insufficient fixtures")
	}
	available := call(b, "hospital", "P001", "appointment.availability", map[string]string{"specialty": "Dermatology", "time_preference": "morning"})
	if len(available.Records) < 2 {
		t.Fatal(available)
	}
	slot := available.Records[0].ID
	var wg sync.WaitGroup
	out := make(chan tools.Result, 2)
	for _, user := range []string{"P001", "P002"} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			out <- call(b, "hospital", u, "appointment.book", map[string]string{"slot_id": slot})
		}(user)
	}
	wg.Wait()
	close(out)
	success := 0
	for r := range out {
		if r.Error == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("double booking: %d", success)
	}
	if call(b, "hospital", "P002", "appointment.cancel", map[string]string{"appointment_id": "A-P001"}).Error == nil {
		t.Fatal("cross-patient cancellation allowed")
	}
	r := call(b, "hospital", "P001", "appointment.reschedule", map[string]string{"appointment_id": "A-P001", "slot_id": available.Records[4].ID})
	if r.Error != nil {
		t.Fatal(r)
	}
	r = call(b, "hospital", "P001", "appointment.cancel", map[string]string{"appointment_id": "A-P001"})
	if r.Error != nil {
		t.Fatal(r)
	}
	r = call(b, "hospital", "P003", "appointment.book", map[string]string{"slot_id": available.Records[4].ID})
	if r.Error != nil {
		t.Fatal("cancelled slot was not released", r)
	}
}
func TestUnavailableDoctorInsuranceAndIsolation(t *testing.T) {
	b := fixture(t)
	today := time.Now().UTC()
	r := call(b, "hospital", "P002", "appointment.availability", map[string]string{"doctor_id": "D002", "from": today.Format("2006-01-02"), "to": today.AddDate(0, 0, 6).Format("2006-01-02")})
	if len(r.Records) != 0 {
		t.Fatal("doctor on leave has slots")
	}
	r = call(b, "hospital", "P001", "insurance.check", map[string]string{"provider": "DemoCare", "plan": "Gold"})
	if !strings.Contains(r.Summary, "not listed") {
		t.Fatal(r)
	}
	if call(b, "hospital", "C001", "patient.profile", nil).Error == nil {
		t.Fatal("cross-domain identity accepted")
	}
	if call(b, "hospital", "P001", "wifi.status", nil).Error == nil {
		t.Fatal("cross-domain tool accepted")
	}
}
