package tools

import (
	"strings"
	"testing"
)

func TestResultDatesAndMoneyComeFromServiceRecords(t *testing.T) {
	text := FormatResult(Result{Summary: "Appointment booked.", Records: []Record{{ID: "A-001", Name: "Dr. Ahmed", Start: "2026-09-30T00:30:00+02:00", Status: "booked", Currency: "USD", Amount: 0}}})
	for _, expected := range []string{"Appointment booked.", "Wednesday 30 September, 12:30 am", "ID: A-001", "USD 0.00"} {
		if !strings.Contains(text, expected) {
			t.Fatal(text)
		}
	}
	if strings.Contains(text, "UTC") {
		t.Fatal(text)
	}
	guided := FormatResult(Result{Summary: "Open times." + GuidanceMarker + "Never read ids aloud.", Records: []Record{{ID: "S1", Kind: "tour_slot", Description: "Monday 12 October, 9 am", Start: "2026-10-12T09:00:00+04:00"}}})
	if strings.Contains(guided, "Never read") || strings.Contains(guided, "9 am") || !strings.Contains(guided, "Monday 12 October, 9:00 am") {
		t.Fatal(guided)
	}
	if FormatResult(Failure("unavailable", "Slot taken")) != "" {
		t.Fatal("failure formatted as success")
	}
	empty := FormatResult(Result{Summary: "No appointments available.", Records: []Record{}})
	if empty != "No appointments available." {
		t.Fatal(empty)
	}
}
