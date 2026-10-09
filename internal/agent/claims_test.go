package agent

import "testing"

func TestClaimsAction(t *testing.T) {
	for _, s := range []string{
		"Perfect, I'll book you for Tuesday 20 October at 9 am.",
		"You're booked for Monday.",
		"I've cancelled that appointment.",
		"It's now scheduled for Thursday.",
		"I have restarted your router.",
		"Perfect! I’ve booked your in-person campus tour for Omar.",
		"That's confirmed for 10 am.",
	} {
		if !ClaimsAction(s) {
			t.Errorf("missed claim: %q", s)
		}
	}
	for _, s := range []string{
		"Would you like me to book the 9 am slot?",
		"I'll check what's available on Tuesday.",
		"Shall I go ahead and book it?",
		"Tours run on weekday mornings.",
		"Let me look that up.",
	} {
		if ClaimsAction(s) {
			t.Errorf("false claim: %q", s)
		}
	}
}
