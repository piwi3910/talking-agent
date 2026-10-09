package agent

import (
	"regexp"

	"enterprise-ai-demo/internal/tools"
)

// claimPattern matches a reply that says a booking or change is done or about
// to be done by the agent ("I'll book you", "you're booked", "I've cancelled").
var claimPattern = regexp.MustCompile(`(?i)\b(i'?ve|i have|i'?ll|i will|i'?m going to|i am going to|you'?re|you are|it'?s|it is|that'?s|has been|have been|is now|are now)\s+(?:now\s+|all\s+|just\s+|gone\s+ahead\s+and\s+)?(book(?:ed)?|schedul(?:e|ed)|reserv(?:e|ed)|confirm(?:ed)?|cancel(?:l?ed)?|reschedul(?:e|ed)|chang(?:e|ed)|updat(?:e|ed)|restart(?:ed)?|optimi[sz](?:e|ed)|sen[dt]|logg?ed|set\s+up)\b`)

// ClaimsAction reports whether text claims a booking or change by the agent.
func ClaimsAction(text string) bool { return claimPattern.MatchString(text) }

// unbackedClaimCheck is sent to the model when it claimed an action without
// calling any booking or change tool in the turn.
const unbackedClaimCheck = "[System check, not from the caller] Your last reply said something was booked, scheduled or changed, but no booking or change tool was called in this turn, so nothing happened. If the person agreed, call the right tool now with their choice. Otherwise, in one short sentence, correct yourself: say it is not done yet and ask what you need. Do not mention this check."

func hasMutation(defs map[string]tools.Definition) bool {
	for _, d := range defs {
		if d.Mutation {
			return true
		}
	}
	return false
}
