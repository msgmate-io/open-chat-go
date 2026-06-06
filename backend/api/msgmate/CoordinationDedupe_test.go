package msgmate

import "testing"

// ci#747 (RC7/QW4): the owner key is stable, normalised, and separates lanes.
func TestCoordinationDedupeKey(t *testing.T) {
	tests := []struct {
		name   string
		ticket string
		lane   string
		want   string
	}{
		{name: "empty ticket has no owner", ticket: "", lane: "", want: ""},
		{name: "empty lane means the default lane", ticket: "ci#747", lane: "", want: "owner:ci#747:default"},
		{name: "whitespace lane means the default lane", ticket: "ci#747", lane: "   ", want: "owner:ci#747:default"},
		{name: "case is normalised", ticket: "CI#747", lane: "Lane", want: "owner:ci#747:lane"},
		{name: "surrounding whitespace is ignored", ticket: " ci#747 ", lane: " lane ", want: "owner:ci#747:lane"},
		{name: "lanes stay distinct", ticket: "ci#747", lane: "monorepo", want: "owner:ci#747:monorepo"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CoordinationDedupeKey(tc.ticket, tc.lane); got != tc.want {
				t.Fatalf("CoordinationDedupeKey(%q, %q) = %q, want %q", tc.ticket, tc.lane, got, tc.want)
			}
		})
	}
}

// A duplicate that differs only in case/whitespace is the same owner, while a
// different lane of the same ticket is not - that is the whole point of RC7.
func TestSameCoordinationOwner(t *testing.T) {
	tests := []struct {
		name           string
		ticketA, laneA string
		ticketB, laneB string
		want           bool
	}{
		{name: "cosmetic duplicate", ticketA: "ci#747", laneA: "monorepo", ticketB: " CI#747 ", laneB: "Monorepo", want: true},
		{name: "same ticket, different lane", ticketA: "ci#747", laneA: "monorepo", ticketB: "ci#747", laneB: "ci-config", want: false},
		{name: "different ticket", ticketA: "ci#747", laneA: "monorepo", ticketB: "ci#744", laneB: "monorepo", want: false},
		{name: "unidentified dispatches never collide", ticketA: "", laneA: "", ticketB: "", laneB: "", want: false},
		{name: "identified vs unidentified never collide", ticketA: "ci#747", laneA: "", ticketB: "", laneB: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameCoordinationOwner(tc.ticketA, tc.laneA, tc.ticketB, tc.laneB); got != tc.want {
				t.Fatalf("SameCoordinationOwner(%q, %q, %q, %q) = %t, want %t",
					tc.ticketA, tc.laneA, tc.ticketB, tc.laneB, got, tc.want)
			}
		})
	}
}
