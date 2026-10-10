package mock

import (
	"enterprise-ai-demo/internal/tools"
	"net/http/httptest"
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
func TestTechnicianAtomicBookingsAndIsolation(t *testing.T) {
	b := fixture(t)
	available := call(b, "telecom", "C001", "technician.availability", nil)
	if len(available.Records) < 2 {
		t.Fatal(available)
	}
	slot := available.Records[0].ID
	var wg sync.WaitGroup
	out := make(chan tools.Result, 2)
	for _, user := range []string{"C001", "C002"} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			out <- call(b, "telecom", u, "technician.book", map[string]string{"slot_id": slot})
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
	if call(b, "telecom", "F001", "technician.availability", nil).Error == nil {
		t.Fatal("cross-domain identity accepted")
	}
	if call(b, "telecom", "C001", "family.profile", nil).Error == nil {
		t.Fatal("cross-domain tool accepted")
	}
}

func TestReservedAdversarialContactsAreHiddenButUsable(t *testing.T) {
	b := fixture(t)
	for _, industry := range []string{"aquila", "school"} {
		for _, user := range b.Users(industry) {
			if strings.HasPrefix(user.ID, "ADV-") {
				t.Fatalf("reserved contact leaked into %s users: %s", industry, user.ID)
			}
		}
	}
	for industry, ids := range map[string][]string{
		"aquila": {"ADV-AQUILA-ADMISSIONS-001", "ADV-AQUILA-ADMISSIONS-002", "ADV-AQUILA-ADMISSIONS-003", "ADV-AQUILA-RECEPTION-001", "ADV-AQUILA-RECEPTION-002", "ADV-AQUILA-RECEPTION-003"},
		"school": {"ADV-SCHOOL-SERVICES-001", "ADV-SCHOOL-SERVICES-002", "ADV-SCHOOL-SERVICES-003"},
	} {
		for _, id := range ids {
			tool := "contact.profile"
			if industry == "school" {
				tool = "family.profile"
			}
			if got := call(b, industry, id, tool, nil); got.Error != nil || len(got.Records) == 0 || got.Records[0].ID != id {
				t.Fatalf("reserved identity %s not usable: %+v", id, got)
			}
		}
	}
}

func TestReservedContactsRequireAdversarialListMarker(t *testing.T) {
	b := fixture(t)
	ordinary := httptest.NewRecorder()
	b.Handler().ServeHTTP(ordinary, httptest.NewRequest("GET", "/users/aquila", nil))
	markedRequest := httptest.NewRequest("GET", "/users/aquila", nil)
	markedRequest.Header.Set("X-Adversarial-Test", "reserved-contacts")
	marked := httptest.NewRecorder()
	b.Handler().ServeHTTP(marked, markedRequest)
	if strings.Contains(ordinary.Body.String(), "ADV-AQUILA") {
		t.Fatal("ordinary CRM listing exposed reserved contacts")
	}
	if !strings.Contains(marked.Body.String(), "ADV-AQUILA-ADMISSIONS-001") {
		t.Fatal("marked adversarial listing omitted reserved contact")
	}
}
