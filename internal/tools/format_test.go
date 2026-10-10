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

func TestFormatSpokenResultOmitsInternalIdentifiers(t *testing.T) {
	got := FormatSpokenResult(Result{Summary: "Found your appointment. ID: BK-44 Related ID: C-12", Records: []Record{{ID: "A-001", RelatedID: "C-12", Name: "Dr. Ahmed", Status: "booked", Start: "2026-09-30T00:30:00+02:00"}}})
	for _, secret := range []string{"A-001", "C-12", "BK-44", "ID:"} {
		if strings.Contains(got, secret) {
			t.Fatalf("spoken output leaked %q: %s", secret, got)
		}
	}
	for _, safe := range []string{"Dr. Ahmed", "booked", "Wednesday 30 September"} {
		if !strings.Contains(got, safe) {
			t.Fatalf("spoken output missing %q: %s", safe, got)
		}
	}
}

func TestFormatSpokenResultSanitizesMutationWithoutSpokenSummary(t *testing.T) {
	got := FormatSpokenResult(Result{Summary: GuidanceMarker + "internal only", Records: []Record{{ID: "APP-88", RelatedID: "CH-17", Name: "Application started", Description: "Your application is ready. Internal reference: APP-88", Status: "started"}}})
	for _, secret := range []string{"APP-88", "CH-17", "Internal reference", "internal only"} {
		if strings.Contains(got, secret) {
			t.Fatalf("spoken fallback leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "Application started") || !strings.Contains(got, "started") {
		t.Fatalf("spoken fallback omitted user-facing fields: %s", got)
	}
}
