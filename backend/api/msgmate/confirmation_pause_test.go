package msgmate

import "testing"

func TestIsConfirmationPauseMessage(t *testing.T) {
	withMarker := map[string]interface{}{confirmationPauseMetadataKey: true}
	other := map[string]interface{}{"confirmable_actions": []interface{}{}}
	wrongType := map[string]interface{}{confirmationPauseMetadataKey: "true"}
	empty := map[string]interface{}{}

	cases := []struct {
		name string
		meta *map[string]interface{}
		want bool
	}{
		{"nil map", nil, false},
		{"nil value", (*map[string]interface{})(nil), false},
		{"empty map", &empty, false},
		{"unrelated metadata", &other, false},
		{"wrong type", &wrongType, false},
		{"pause marker", &withMarker, true},
	}

	for _, tc := range cases {
		if got := isConfirmationPauseMessage(tc.meta); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestConfirmationPauseMetadataKeyIsStable(t *testing.T) {
	// The key is persisted on the message row, so it must stay wire-stable.
	if confirmationPauseMetadataKey != "confirmation_pause" {
		t.Fatalf("metadata key changed: %q", confirmationPauseMetadataKey)
	}
}
