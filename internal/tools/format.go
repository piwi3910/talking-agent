package tools

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// GuidanceMarker separates a result summary the person may read from guidance
// meant only for the model. FormatResult drops everything after it.
const GuidanceMarker = "\n\nGuidance: "

// SpokenSummary is the part of a result meant to be said aloud: the summary
// without model guidance or the raw records (those are shown, not read).
func SpokenSummary(result Result) string {
	if result.Error != nil {
		return ""
	}
	summary, _, _ := strings.Cut(result.Summary, GuidanceMarker)
	return scrubInternalRefs(strings.TrimSpace(summary))
}

// FormatSpokenResult formats only explicitly user-facing fields. Unlike
// FormatResult, it never emits record identifiers or internal references.
func FormatSpokenResult(result Result) string {
	if result.Error != nil || strings.TrimSpace(result.Summary) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(SpokenSummary(result))
	limit := len(result.Records)
	if limit > 50 {
		limit = 50
	}
	for _, record := range result.Records[:limit] {
		fields := []string{}
		for _, value := range []string{record.Name, record.Description} {
			if value != "" && !(value == record.Description && record.Start != "" && strings.HasSuffix(record.Kind, "_slot")) {
				fields = append(fields, scrubInternalRefs(value))
			}
		}
		if record.Start != "" {
			if at, err := time.Parse(time.RFC3339, record.Start); err == nil {
				fields = append(fields, at.Format("Monday 2 January, 3:04 pm"))
			}
		}
		for _, value := range []string{record.Specialty, record.Location, record.Status} {
			if value != "" {
				fields = append(fields, scrubInternalRefs(value))
			}
		}
		if len(fields) > 0 {
			b.WriteString("\n- ")
			b.WriteString(strings.Join(fields, " · "))
		}
	}
	return strings.TrimSpace(b.String())
}

var internalRefPattern = regexp.MustCompile(`(?i)(?:related[- ]?id|internal reference|reference(?: code)?|(?:booking|slot|record)\s+id|\bid)\s*[:#]\s*[[:alnum:]_-]+`)

func scrubInternalRefs(s string) string {
	return strings.TrimSpace(internalRefPattern.ReplaceAllString(s, ""))
}

// FormatResult renders service facts without inference or industry-specific logic.
// It is used for acknowledged mutations and recovery from empty model answers.
func FormatResult(result Result) string {
	if result.Error != nil || strings.TrimSpace(result.Summary) == "" {
		return ""
	}
	var b strings.Builder
	summary, _, _ := strings.Cut(result.Summary, GuidanceMarker)
	b.WriteString(summary)
	limit := len(result.Records)
	if limit > 50 {
		limit = 50
	}
	for _, record := range result.Records[:limit] {
		fields := []string{}
		for _, value := range []string{record.Name, record.Description} {
			// A slot's description repeats its start time, which is rendered below.
			if value != "" && !(value == record.Description && record.Start != "" && strings.HasSuffix(record.Kind, "_slot")) {
				fields = append(fields, value)
			}
		}
		if record.Start != "" {
			if at, err := time.Parse(time.RFC3339, record.Start); err == nil {
				fields = append(fields, at.Format("Monday 2 January, 3:04 pm"))
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
