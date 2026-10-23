package coordination

import "testing"

func TestOwnerKeyNormalisesTicketAndLane(t *testing.T) {
	cases := []struct {
		name         string
		ticket, lane string
		want         string
	}{
		{"plain", "ci#747", "wiring", "owner:ci#747:wiring"},
		{"case insensitive", "CI#747", "Wiring", "owner:ci#747:wiring"},
		{"whitespace collapsed", " ci#747 ", "  wiring  ", "owner:ci#747:wiring"},
		{"empty lane is default", "ci#747", "", "owner:ci#747:default"},
		{"blank lane is default", "ci#747", "   ", "owner:ci#747:default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OwnerKey(tc.ticket, tc.lane); got != tc.want {
				t.Fatalf("OwnerKey(%q,%q) = %q, want %q", tc.ticket, tc.lane, got, tc.want)
			}
		})
	}
}

func TestOwnerKeyEmptyTicketIsNotAnOwner(t *testing.T) {
	// A dispatch that names no ticket must not be de-duplicable: inventing an
	// owner here would make every unidentified dispatch collide with every
	// other one.
	for _, ticket := range []string{"", "   "} {
		if got := OwnerKey(ticket, "wiring"); got != "" {
			t.Fatalf("OwnerKey(%q, ...) = %q, want empty", ticket, got)
		}
	}
}

func TestSameOwnerOnlyForSameTicketAndLane(t *testing.T) {
	if !SameOwner("ci#747", "wiring", "CI#747", "wiring") {
		t.Fatal("cosmetic differences must not create a second owner")
	}
	if !SameOwner("ci#747", "", "ci#747", LaneDefault) {
		t.Fatal("an omitted lane is the default lane")
	}
	if SameOwner("ci#747", "wiring", "ci#747", "canary") {
		t.Fatal("different lanes of one ticket are separate owners")
	}
	if SameOwner("ci#747", "wiring", "ci#748", "wiring") {
		t.Fatal("different tickets are separate owners")
	}
	if SameOwner("", "", "", "") {
		t.Fatal("two unidentified dispatches must not collide")
	}
}

func TestTicketFromTagsRoundTripsTheOwnerKey(t *testing.T) {
	cases := []struct {
		name       string
		tags       []string
		wantTicket string
		wantLane   string
	}{
		{"issue tag", []string{"repo:open-chat-go", "issue:747", "lane:wiring"}, "747", "wiring"},
		{"ci reference", []string{"ci#747", "lane:canary"}, "ci#747", "canary"},
		{"ticket tag", []string{"ticket:747"}, "747", LaneDefault},
		{"default lane", []string{"issue:747"}, "747", LaneDefault},
		{"lane without ticket", []string{"lane:wiring"}, "", ""},
		{"no unit of work", []string{"repo:open-chat-go"}, "", ""},
		{"nil tags", nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ticket, lane := TicketFromTags(tc.tags)
			if ticket != tc.wantTicket || lane != tc.wantLane {
				t.Fatalf("TicketFromTags(%v) = (%q,%q), want (%q,%q)",
					tc.tags, ticket, lane, tc.wantTicket, tc.wantLane)
			}
		})
	}
}

func TestTicketFromTagsReconcilesToTheDispatchedOwnerKey(t *testing.T) {
	// The reconcile step recovers (ticket, lane) from tags and must land on the
	// same owner key the dispatch stamped, otherwise reconcile reports a
	// duplicate that does not exist (or misses one that does).
	dispatched := OwnerKeyTag("ci#747", "wiring")
	ticket, lane := TicketFromTags([]string{"repo:open-chat-go", "issue:ci#747", "lane:wiring"})
	if got := OwnerKeyTag(ticket, lane); got != dispatched {
		t.Fatalf("reconciled owner key %q != dispatched %q", got, dispatched)
	}
}
