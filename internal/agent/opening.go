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
			Opening:     "[Call answered] This note is an internal cue, not something the caller said: never describe or narrate it. If a contact.profile or crm.history tool is available, call it first. Then greet the caller in one or two sentences. Only if the relevant memories or those tools contain facts about this caller, greet them by name as a returning contact and mention one specific detail from them, using only names and facts that literally appear there. If nothing is known, give a normal friendly first-time greeting: never say you remember them, never invent names, family members or past conversations.",
			MemoryQuery: query,
		}, true
	case OpeningOutbound:
		who := contact
		if who == "" {
			who = "the selected contact"
		}
		return Turn{
			Opening:     "[Outbound call connected] This note is an internal cue: never narrate it. You placed this call to " + who + ". If a contact.profile or crm.history tool is available, call it first. Then introduce yourself and the school, check it's a good time, and reference their history and the reason for your call, in two sentences, using only names and facts that literally appear in those sources and memory; never invent history.",
			MemoryQuery: query,
		}, true
	}
	return Turn{}, false
}
