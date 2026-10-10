package agent

import (
	"regexp"

	"enterprise-ai-demo/internal/skills"
)

// claimPattern matches a reply that says a booking or change is done or about
// to be done by the agent ("I'll book you", "you're booked", "I've cancelled").
var claimPattern = regexp.MustCompile(`(?i)\b(?:` +
	// "I'll book you", "you're booked", "I've cancelled", "it's now scheduled"
	`(?:i['’]?ve|i have|i['’]?ll|i will|i['’]?m going to|i am going to|you['’]?re|you are|it['’]?s|it is|that['’]?s|has been|have been|is now|are now|the\s+(?:cancellation|booking|appointment|reservation|change))\s+(?:now\s+|all\s+|just\s+|gone\s+ahead\s+and\s+)?(?:book(?:ed)?|schedul(?:e|ed)|reserv(?:e|ed)|confirm(?:ed)?|cancel(?:l?ed)?|reschedul(?:e|ed)|chang(?:e|ed)|updat(?:e|d)|restart(?:ed)?|optimi[sz](?:e|ed)|sen[dt]|logg?ed|set\s+up|complete|completed|arranged|sorted|moved|placed|put)` +
	`|(?:your\s+(?:appointment|booking|reservation|cancellation|change)|the\s+(?:cancellation|booking|appointment|reservation|change))\s+(?:will be|is|was|has been)\s+(?:set\s+up|arranged|sorted|complete|completed|moved|changed|cancelled|canceled|confirmed)` +
	`|(?:i['’]?ll|i will|we can)\s+(?:get\s+that\s+(?:arranged|sorted)|have\s+that\s+(?:moved|changed|rescheduled)|(?:book|schedule|arrange|sort|move|cancel|change)\s+(?:you|that|it))` +
	`|i['’]?ve\s+(?:put\s+that\s+in\s+(?:the\s+)?(?:diary|calendar)|(?:arranged|sorted|moved|changed|cancelled|canceled|completed)\b)` +
	// "I'm booking Omar in", "I am scheduling that now"
	`|(?:i['’]?m|i am)\s+(?:now\s+|just\s+)?(?:booking|scheduling|reserving|cancell?ing|rescheduling|changing|updating|restarting|sending|logging|setting\s+up)` +
	`)\b`)

// ClaimsAction reports whether text claims a booking or change by the agent.
func ClaimsAction(text string) bool { return claimPattern.MatchString(text) }

// unbackedClaimCheck is sent to the model when it claimed an action without
// calling any booking or change tool in the turn.
const unbackedClaimCheck = "[System check, not from the caller] Your last reply said something was booked, scheduled or changed, but no booking or change tool was called in this turn, so nothing happened. If the person agreed, call the right tool now with their choice. Otherwise, in one short sentence, correct yourself: say it is not done yet and ask what you need. Do not mention this check."

// catalogMutates reports whether any of the agent's skills can book or change things.
func catalogMutates(catalog skills.Catalog) bool {
	for _, skill := range catalog {
		for _, d := range skill.Tools {
			if d.Mutation {
				return true
			}
		}
	}
	return false
}
