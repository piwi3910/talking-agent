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
			Opening:     "[Call answered] If a contact.profile or crm.history tool is available, call it first. Then greet the caller in one or two sentences. The relevant memories are about this caller: if any exist, greet them by name as a returning contact and mention one specific remembered detail (for example a child's name, a planned move or their last conversation), using only names and facts from those sources. Only ask for basic details when nothing is known.",
			MemoryQuery: query,
		}, true
	case OpeningOutbound:
		who := contact
		if who == "" {
			who = "the selected contact"
		}
		return Turn{
			Opening:     "[Outbound call connected] You placed this call to " + who + ". If a contact.profile or crm.history tool is available, call it first. Then introduce yourself and the school, check it's a good time, and reference their history and the reason for your call, in two sentences, using only names and facts from those sources and memory.",
			MemoryQuery: query,
		}, true
	}
	return Turn{}, false
}
