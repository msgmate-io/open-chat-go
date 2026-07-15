package bots

import "testing"

// TestOwnerKeyForDispatchDerivesLaneOwner covers the (ticket, lane) dedupe key
// derivation wired into the dispatch gate (ci#747 RC7/QW4): declaring a lane
// claims that lane of that ticket exclusively, an explicit DedupeKey keeps its
// historical meaning, and a dispatch that names no unit of work is never
// de-duplicated (it must not silently adopt an unrelated lane).
func TestOwnerKeyForDispatchDerivesLaneOwner(t *testing.T) {
	cases := []struct {
		name        string
		tags        []string
		explicit    string
		wantKey     string
		wantEnforce bool
	}{
		{
			name:        "ticket and lane",
			tags:        []string{"repo:open-chat-go", "issue:747", "lane:wiring"},
			wantKey:     "owner:747:wiring",
			wantEnforce: true,
		},
		{
			name:        "cosmetic differences do not fork the owner",
			tags:        []string{"ISSUE:747", "Lane:Wiring"},
			wantKey:     "owner:747:wiring",
			wantEnforce: true,
		},
		{
			name:        "lane without a ticket is not an owner",
			tags:        []string{"repo:open-chat-go", "lane:wiring"},
			wantKey:     "",
			wantEnforce: false,
		},
		{
			name:        "un-laned dispatch keeps the historical behaviour",
			tags:        []string{"repo:open-chat-go", "issue:747"},
			wantKey:     "",
			wantEnforce: false,
		},
		{
			name:        "explicit dedupe key wins and keeps enforcing",
			tags:        []string{"issue:747"},
			explicit:    "lead:open-chat-go-ci",
			wantKey:     "lead:open-chat-go-ci",
			wantEnforce: true,
		},
		{
			name:        "no tags at all",
			tags:        nil,
			wantKey:     "",
			wantEnforce: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, enforce := ownerKeyForDispatch(tc.tags, tc.explicit)
			if key != tc.wantKey || enforce != tc.wantEnforce {
				t.Fatalf("ownerKeyForDispatch(%v, %q) = (%q,%v), want (%q,%v)",
					tc.tags, tc.explicit, key, enforce, tc.wantKey, tc.wantEnforce)
			}
		})
	}
}

// TestOwnerKeyForDispatchSeparatesLanes guards the deliberate granularity of the
// key: two different lanes of one ticket are separate owners, so a ticket can be
// worked in parallel lanes without one lane blocking the other.
func TestOwnerKeyForDispatchSeparatesLanes(t *testing.T) {
	wiring, _ := ownerKeyForDispatch([]string{"issue:747", "lane:wiring"}, "")
	canary, _ := ownerKeyForDispatch([]string{"issue:747", "lane:canary"}, "")
	if wiring == "" || canary == "" || wiring == canary {
		t.Fatalf("lanes must have distinct, non-empty owner keys: %q vs %q", wiring, canary)
	}
}

// TestOwnerKeyForDispatchSameLaneCollides is the regression guard for the
// duplicate-interaction failure (ci#744): the same lane of the same ticket
// dispatched twice must produce the same enforcing owner key, whatever the
// cosmetic differences between the two dispatches.
func TestOwnerKeyForDispatchSameLaneCollides(t *testing.T) {
	first, enforceFirst := ownerKeyForDispatch([]string{"repo:open-chat-go", "issue:744", "lane:wiring"}, "")
	second, enforceSecond := ownerKeyForDispatch([]string{"repo:open-chat-go", "CI#744", "lane:WIRING"}, "")
	if !enforceFirst || !enforceSecond {
		t.Fatalf("both dispatches must enforce lane ownership: %v %v", enforceFirst, enforceSecond)
	}
	if first != second {
		t.Fatalf("the same lane must resolve to one owner key: %q vs %q", first, second)
	}
}
