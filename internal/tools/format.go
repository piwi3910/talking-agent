package tools

import (
	"fmt"
	"strings"
	"time"
)

// FormatResult renders service facts without inference or industry-specific logic.
// It is used for acknowledged mutations and recovery from empty model answers.
func FormatResult(result Result) string {
	if result.Error != nil || strings.TrimSpace(result.Summary) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(result.Summary)
	limit := len(result.Records)
	if limit > 50 {
		limit = 50
	}
	for _, record := range result.Records[:limit] {
		fields := []string{}
		for _, value := range []string{record.Name, record.Description} {
			if value != "" {
				fields = append(fields, value)
			}
		}
		if record.Start != "" {
			if at, err := time.Parse(time.RFC3339, record.Start); err == nil {
				fields = append(fields, at.UTC().Format("Mon 2006-01-02 15:04 UTC"))
			} else {
				fields = append(fields, "Start: "+record.Start)
			}
		}
		for _, value := range []string{record.Specialty, record.Location, record.Status} {
			if value != "" {
				fields = append(fields, value)
			}
		}
		if record.Currency != "" {
			fields = append(fields, fmt.Sprintf("%s %.2f", record.Currency, record.Amount))
		} else if record.Amount != 0 {
			fields = append(fields, fmt.Sprintf("Amount: %g", record.Amount))
		}
		if record.Unit != "" || record.Value != 0 {
			fields = append(fields, strings.TrimSpace(fmt.Sprintf("%g %s", record.Value, record.Unit)))
		}
		if record.RelatedID != "" {
			fields = append(fields, "Related ID: "+record.RelatedID)
		}
		if record.ID != "" {
			fields = append(fields, "ID: "+record.ID)
		}
		if len(fields) > 0 {
			b.WriteString("\n- ")
			b.WriteString(strings.Join(fields, " · "))
		}
	}
	if len(result.Records) > limit {
		fmt.Fprintf(&b, "\nShowing %d of %d returned records.", limit, len(result.Records))
	}
	return b.String()
}
