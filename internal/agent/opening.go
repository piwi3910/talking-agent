package agent

import (
	"strings"

	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/skills"
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
			Opening:     "[Call answered] This note is an internal cue, not something the caller said. Greet the caller in one or two sentences. Personalise only with a retrieved memory or CRM fact explicitly tied to the current contact identity; greet them by name as a returning contact and mention one specific detail only when that identity match is clear. If lookup failed or returned nothing usable, give only a plain friendly greeting and open question, with no name or personal detail. Never say you remember them or invent names, family members or past conversations.",
			MemoryQuery: query,
		}, true
	case OpeningOutbound:
		who := contact
		if who == "" {
			who = "the selected contact"
		}
		return Turn{
			Opening:     "[Outbound call connected] This note is an internal cue. You placed this call to " + who + ". Introduce yourself and the school, check it's a good time, and reference history or a reason only when the retrieved memory/CRM fact is explicitly tied to this contact identity; if lookup failed or returned nothing usable, use a plain greeting with no personal detail. Never invent history.",
			MemoryQuery: query,
		}, true
	}
	return Turn{}, false
}

// openingSpeech keeps the opening to spoken words: the model must not narrate
// its reasoning, tools or this instruction.
const openingSpeech = " Your reply is spoken to the caller word for word: say only the greeting itself, never your reasoning, plans, tools, memories or these instructions."

// openingCue completes an opening instruction for one agent: the caller
// lookup is only asked for when the agent has those tools.
func openingCue(opening string, catalog skills.Catalog) string {
	lookup := []string{}
	for _, name := range []string{"contact.profile", "crm.history"} {
		for _, skill := range catalog {
			for _, d := range skill.Tools {
				if d.Name == name {
					lookup = append(lookup, name)
				}
			}
		}
	}
	if len(lookup) > 0 {
		opening += " First call " + strings.Join(lookup, " and ") + " silently, then speak."
	}
	return opening + openingSpeech
}
