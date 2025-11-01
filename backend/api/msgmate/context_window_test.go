package msgmate

import (
	"testing"

	client "github.com/msgmate-io/go-client-integration/goclient"
)

func listedMessage(dataType, text string, meta *map[string]interface{}) client.ListedMessage {
	return client.ListedMessage{
		DataType: dataType,
		Text:     text,
		MetaData: meta,
	}
}

func eventRequestedMessage() client.ListedMessage {
	meta := map[string]interface{}{"event_type": "confirmable_action_execute"}
	return listedMessage("event", "", &meta)
}

func TestIsNonConversationalMessage(t *testing.T) {
	if !isNonConversationalMessage(eventRequestedMessage()) {
		t.Fatalf("event data type should be non-conversational")
	}
	meta := map[string]interface{}{"event_type": "confirmable_action_execute"}
	if !isNonConversationalMessage(listedMessage("text", "", &meta)) {
		t.Fatalf("confirmable_action_execute meta should be non-conversational")
	}
	if isNonConversationalMessage(listedMessage("text", "hello", nil)) {
		t.Fatalf("plain text message should be conversational")
	}
}

func TestCountConversationalMessages(t *testing.T) {
	rows := []client.ListedMessage{
		listedMessage("text", "final", nil),
		eventRequestedMessage(),
		eventRequestedMessage(),
		listedMessage("text", "assistant", nil),
		eventRequestedMessage(),
		listedMessage("text", "task", nil),
	}
	if got := countConversationalMessages(rows); got != 3 {
		t.Fatalf("expected 3 conversational messages, got %d", got)
	}
}

func TestTrimMessagesToContextWindowExcludesEvents(t *testing.T) {
	rows := []client.ListedMessage{
		listedMessage("text", "final", nil),
		eventRequestedMessage(),
		eventRequestedMessage(),
		listedMessage("text", "assistant", nil),
		eventRequestedMessage(),
		listedMessage("text", "task", nil),
	}

	trimmed := trimMessagesToContextWindow(rows, 2)
	if got := countConversationalMessages(trimmed); got != 2 {
		t.Fatalf("expected 2 conversational messages after trim, got %d", got)
	}
	if trimmed[len(trimmed)-1].Text != "assistant" {
		t.Fatalf("expected to keep the newest 2 conversational messages, tail is %q", trimmed[len(trimmed)-1].Text)
	}

	all := trimMessagesToContextWindow(rows, 10)
	if len(all) != len(rows) {
		t.Fatalf("expected all rows when limit exceeds conversational count, got %d", len(all))
	}
}
