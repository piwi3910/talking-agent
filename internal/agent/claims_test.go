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
		"Perfect. I am booking Omar in for the in-person campus tour.",
		"I'm scheduling that callback now.",
		"That's confirmed for 10 am.",
		"I'll get that arranged.",
		"I will get that sorted.",
		"Your appointment will be set up for Monday.",
		"We can have that moved to Tuesday.",
		"The cancellation is complete.",
		"I’ve put that in the diary.",
		"I've put that in the diary.",
		"The booking has been completed.",
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
		"I can't move appointments.",
		"The system shows your booking.",
	} {
		if ClaimsAction(s) {
			t.Errorf("false claim: %q", s)
		}
	}
}
