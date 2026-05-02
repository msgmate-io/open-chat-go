package chats

import (
	"errors"
	"testing"
)

func TestParseChatStateFilter(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		want   []string
	}{
		{name: "absent", values: nil, want: nil},
		{name: "empty", values: []string{""}, want: nil},
		{name: "single", values: []string{"active"}, want: []string{"active"}},
		{name: "comma separated", values: []string{"active,finished"}, want: []string{"active", "finished"}},
		{name: "repeated params", values: []string{"active", "failed"}, want: []string{"active", "failed"}},
		{name: "case and space tolerant", values: []string{" Active , FINISHED "}, want: []string{"active", "finished"}},
		{name: "deduplicated", values: []string{"active,active", "active"}, want: []string{"active"}},
		{
			name:   "all known states",
			values: []string{"idle,active,finished,failed,needs_confirmation"},
			want:   []string{"idle", "active", "finished", "failed", "needs_confirmation"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseChatStateFilter(tc.values)
			if err != nil {
				t.Fatalf("parseChatStateFilter(%v) returned error: %v", tc.values, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseChatStateFilter(%v) = %v, want %v", tc.values, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("parseChatStateFilter(%v) = %v, want %v", tc.values, got, tc.want)
				}
			}
		})
	}
}

func TestParseChatStateFilterRejectsUnknownState(t *testing.T) {
	for _, value := range []string{"running", "active,running", "done"} {
		_, err := parseChatStateFilter([]string{value})
		if err == nil {
			t.Fatalf("parseChatStateFilter(%q) unexpectedly succeeded", value)
		}
		if !errors.Is(err, ErrInvalidChatStateFilter) {
			t.Fatalf("parseChatStateFilter(%q) error = %v, want ErrInvalidChatStateFilter", value, err)
		}
	}
}

func TestChatStateMatchesFilter(t *testing.T) {
	// No filter matches every row: the list endpoint must behave exactly like
	// before for requests without a `state` parameter.
	if !ChatStateMatchesFilter("finished", nil) {
		t.Fatal("empty filter must match every state")
	}
	if !ChatStateMatchesFilter("needs_confirmation", []string{}) {
		t.Fatal("empty filter slice must match every state")
	}
	if !ChatStateMatchesFilter("active", []string{"active", "finished"}) {
		t.Fatal("row state in filter must match")
	}
	if ChatStateMatchesFilter("failed", []string{"active", "finished"}) {
		t.Fatal("row state outside filter must not match")
	}
	// A chat whose state could not be resolved is reported as idle, so an idle
	// filter must still surface it.
	if !ChatStateMatchesFilter("idle", []string{"idle"}) {
		t.Fatal("idle filter must match the idle fallback state")
	}
}

func TestValidChatStateFilterValue(t *testing.T) {
	for _, state := range []string{"idle", "active", "finished", "failed", "needs_confirmation"} {
		if !validChatStateFilterValue(state) {
			t.Fatalf("validChatStateFilterValue(%q) = false, want true", state)
		}
	}
	for _, state := range []string{"", "Idle", "running", "active ", "stopped"} {
		if validChatStateFilterValue(state) {
			t.Fatalf("validChatStateFilterValue(%q) = true, want false", state)
		}
	}
}
