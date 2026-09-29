package tools

import (
	"strings"
	"testing"
)

func TestResultDatesAndMoneyComeFromServiceRecords(t *testing.T) {
	text := FormatResult(Result{Summary: "Appointment booked.", Records: []Record{{ID: "A-001", Name: "Dr. Ahmed", Start: "2026-09-30T00:30:00+02:00", Status: "booked", Currency: "USD", Amount: 0}}})
	for _, expected := range []string{"Appointment booked.", "Tue 2026-09-29 22:30 UTC", "ID: A-001", "USD 0.00"} {
		if !strings.Contains(text, expected) {
			t.Fatal(text)
		}
	}
	if FormatResult(Failure("unavailable", "Slot taken")) != "" {
		t.Fatal("failure formatted as success")
	}
	empty := FormatResult(Result{Summary: "No appointments available.", Records: []Record{}})
	if empty != "No appointments available." {
		t.Fatal(empty)
	}
}
