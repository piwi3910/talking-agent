package agent

import (
	"strings"

	"enterprise-ai-demo/internal/config"
)

// Opening modes configured with persona.opening.
const (
	OpeningInbound  = "inbound"
	OpeningOutbound = "outbound"
)

// OpeningTurn builds the internal turn that lets a persona speak first. It
// reports false when the persona has no valid persona.opening setting. The
// contact name is optional; it only sharpens the instruction and memory query.
func OpeningTurn(a *config.Agent, contact string) (Turn, bool) {
	contact = strings.TrimSpace(contact)
	query := "previous enquiry tour application"
	if contact != "" {
		query = contact + " " + query
	}
	switch a.Persona["opening"] {
	case OpeningInbound:
		return Turn{
			Opening:     "[Call answered] Greet the caller in one or two sentences. If memory or the profile shows prior contact, greet them by name and briefly reference it.",
			MemoryQuery: query,
		}, true
	case OpeningOutbound:
		who := contact
		if who == "" {
			who = "the selected contact"
		}
		return Turn{
			Opening:     "[Outbound call connected] You placed this call to " + who + ". Introduce yourself and the school, check it's a good time, and reference their history and the reason for your call, in two sentences.",
			MemoryQuery: query,
		}, true
	}
	return Turn{}, false
}
