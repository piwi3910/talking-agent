package mock

import (
	"sync"
	"testing"
)

func TestSchoolBookingsOwnershipAndRequests(t *testing.T) {
	b := fixture(t)
	if len(b.Users("school")) != 20 {
		t.Fatal("missing families")
	}
	if got := call(b, "school", "F002", "admissions.status", nil); len(got.Records) != 1 || got.Records[0].Status != "awaiting documents" {
		t.Fatal(got)
	}
	slots := call(b, "school", "F001", "tour.availability", map[string]string{"time_preference": "morning"})
	if len(slots.Records) == 0 {
		t.Fatal("no tours")
	}
	for _, r := range slots.Records {
		if r.Start[11:13] >= "12" {
			t.Fatal("afternoon in morning results")
		}
	}
	var wg sync.WaitGroup
	successes := make(chan string, 2)
	for _, user := range []string{"F001", "F002"} {
		wg.Add(1)
		go func(user string) {
			defer wg.Done()
			r := call(b, "school", user, "tour.book", map[string]string{"slot_id": slots.Records[0].ID})
			if r.Error == nil {
				successes <- user
			}
		}(user)
	}
	wg.Wait()
	close(successes)
	if len(successes) != 1 {
		t.Fatal("tour double booked")
	}
	owner := <-successes
	other := "F001"
	if owner == other {
		other = "F002"
	}
	visits := call(b, "school", owner, "tour.list", nil)
	if len(visits.Records) != 1 {
		t.Fatal(visits)
	}
	id := visits.Records[0].ID
	if len(call(b, "school", other, "tour.list", nil).Records) != 0 {
		t.Fatal("visit leaked")
	}
	if call(b, "school", other, "tour.cancel", map[string]string{"visit_id": id}).Error == nil {
		t.Fatal("cancelled another family's visit")
	}
	if call(b, "school", owner, "tour.cancel", map[string]string{"visit_id": id}).Error != nil {
		t.Fatal("cancellation failed")
	}
	if call(b, "school", other, "tour.book", map[string]string{"slot_id": slots.Records[0].ID}).Error != nil {
		t.Fatal("slot not released")
	}
	if call(b, "school", "F001", "admissions.enquire", map[string]string{"program_id": "imaginary", "summary": "help"}).Error == nil {
		t.Fatal("invented program accepted")
	}
	if call(b, "school", "F001", "reception.create_request", map[string]string{"summary": "Please contact me about admissions"}).Error != nil {
		t.Fatal("request failed")
	}
	if len(call(b, "school", "F002", "reception.requests", nil).Records) != 0 {
		t.Fatal("request leaked")
	}
	if call(b, "telecom", "C001", "family.profile", nil).Error == nil {
		t.Fatal("cross-industry tool accepted")
	}
	if call(b, "school", "C001", "family.profile", nil).Error == nil {
		t.Fatal("cross-industry identity accepted")
	}
	if call(b, "school", "F001", "tour.availability", map[string]string{"from": "not-a-date"}).Error == nil {
		t.Fatal("invalid date accepted")
	}
}
