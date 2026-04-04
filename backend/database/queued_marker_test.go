package database

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openMarkerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	DB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "queued-marker.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	if err := DB.AutoMigrate(&Chat{}, &User{}, &Message{}); err != nil {
		t.Fatalf("failed to migrate test schema: %v", err)
	}
	return DB
}

// createMarkerTestMessage persists a message in chatID (0 creates a fresh chat)
// and returns it with its UUID populated.
func createMarkerTestMessage(t *testing.T, DB *gorm.DB, chatID uint, meta string) *Message {
	t.Helper()
	if chatID == 0 {
		chat := Chat{}
		if err := DB.Create(&chat).Error; err != nil {
			t.Fatalf("failed to create chat: %v", err)
		}
		chatID = chat.ID
	}
	message := Message{ChatId: chatID, DataType: "text"}
	if meta != "" {
		message.MetaData = JSONRaw(meta)
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}
	return &message
}

func loadMarkerTestMessage(t *testing.T, DB *gorm.DB, uuid string) Message {
	t.Helper()
	var persisted Message
	if err := DB.Where("uuid = ?", uuid).First(&persisted).Error; err != nil {
		t.Fatalf("failed to reload message %s: %v", uuid, err)
	}
	return persisted
}

func TestMarkMessageQueuedIsIdempotentAndPreservesOtherMetadata(t *testing.T) {
	meta := map[string]interface{}{"finished": true}

	if !MarkMessageQueued(meta) {
		t.Fatalf("expected the first mark to change the metadata")
	}
	if state, _ := meta[QueueStateMetaKey].(string); state != QueueStateQueued {
		t.Fatalf("expected %s=%q, got %#v", QueueStateMetaKey, QueueStateQueued, meta[QueueStateMetaKey])
	}
	if MarkMessageQueued(meta) {
		t.Fatalf("expected a second mark to be a no-op")
	}

	if !ClearMarkerFromMeta(meta) {
		t.Fatalf("expected the marker to be cleared")
	}
	if _, ok := meta[QueueStateMetaKey]; ok {
		t.Fatalf("expected the marker to be gone, got %#v", meta)
	}
	if ClearMarkerFromMeta(meta) {
		t.Fatalf("expected a second clear to be a no-op")
	}
	if finished, _ := meta["finished"].(bool); !finished {
		t.Fatalf("the drained metadata must keep unrelated keys: %#v", meta)
	}
}

func TestMarkerHelpersTolerateNilMetadata(t *testing.T) {
	if MarkMessageQueued(nil) {
		t.Fatalf("a nil metadata map cannot be marked")
	}
	if ClearMarkerFromMeta(nil) {
		t.Fatalf("a nil metadata map cannot be cleared")
	}
	if IsMessageQueued(Message{}) {
		t.Fatalf("a message without metadata is not queued")
	}
}

func TestSetMessageQueuedPersistsMarkerAndRefreshesTheInMemoryMessage(t *testing.T) {
	DB := openMarkerTestDB(t)
	message := createMarkerTestMessage(t, DB, 0, `{"finished":true}`)

	marked, err := SetMessageQueued(DB, message)
	if err != nil {
		t.Fatalf("SetMessageQueued failed: %v", err)
	}
	if !marked {
		t.Fatalf("expected the first mark to persist the marker")
	}
	if !IsMessageQueued(*message) {
		t.Fatalf("expected the in-memory message to carry the marker, got %s", message.MetaData)
	}

	persisted := loadMarkerTestMessage(t, DB, message.UUID)
	if !IsMessageQueued(persisted) {
		t.Fatalf("expected the persisted message to carry the marker, got %s", persisted.MetaData)
	}
	meta := messageMeta(persisted)
	if meta[QueueStateMetaKey] != QueueStateQueued {
		t.Fatalf("expected %s=%q, got %#v", QueueStateMetaKey, QueueStateQueued, meta[QueueStateMetaKey])
	}
	if finished, _ := meta["finished"].(bool); !finished {
		t.Fatalf("marking must not drop unrelated metadata: %#v", meta)
	}

	if markedAgain, err := SetMessageQueued(DB, message); err != nil || markedAgain {
		t.Fatalf("expected a second mark to be a no-op, got marked=%v err=%v", markedAgain, err)
	}
}

func TestSetMessageQueuedHandlesNilInputs(t *testing.T) {
	DB := openMarkerTestDB(t)

	if marked, err := SetMessageQueued(DB, nil); err != nil || marked {
		t.Fatalf("a nil message cannot be marked, got marked=%v err=%v", marked, err)
	}
	if marked, err := SetMessageQueued(nil, &Message{}); err != nil || marked {
		t.Fatalf("a nil DB cannot mark, got marked=%v err=%v", marked, err)
	}

	// A message without a UUID was never persisted, so it cannot be updated.
	unpersisted := Message{}
	if marked, err := SetMessageQueued(DB, &unpersisted); err != nil || marked {
		t.Fatalf("a message without a UUID cannot be marked, got marked=%v err=%v", marked, err)
	}
}

func TestClearMessageQueuedDrainsOnlyMarkedMessages(t *testing.T) {
	DB := openMarkerTestDB(t)
	message := createMarkerTestMessage(t, DB, 0, `{"finished":true}`)

	if cleared, err := ClearMessageQueued(DB, message); err != nil || cleared {
		t.Fatalf("expected an unmarked message to be a no-op, got cleared=%v err=%v", cleared, err)
	}

	if _, err := SetMessageQueued(DB, message); err != nil {
		t.Fatalf("SetMessageQueued failed: %v", err)
	}
	if cleared, err := ClearMessageQueued(DB, message); err != nil || !cleared {
		t.Fatalf("expected the marker to be cleared, got cleared=%v err=%v", cleared, err)
	}
	if IsMessageQueued(*message) {
		t.Fatalf("expected the in-memory message to be drained, got %s", message.MetaData)
	}

	persisted := loadMarkerTestMessage(t, DB, message.UUID)
	if IsMessageQueued(persisted) {
		t.Fatalf("expected the marker to be gone, got %s", persisted.MetaData)
	}
	if finished, _ := messageMeta(persisted)["finished"].(bool); !finished {
		t.Fatalf("draining must not drop unrelated metadata: %s", persisted.MetaData)
	}

	if cleared, err := ClearMessageQueued(DB, message); err != nil || cleared {
		t.Fatalf("expected a second clear to be a no-op, got cleared=%v err=%v", cleared, err)
	}
}

func TestClearMessageQueuedForChatDrainsEveryMarkedMessageOfTheChat(t *testing.T) {
	DB := openMarkerTestDB(t)
	queuedA := createMarkerTestMessage(t, DB, 0, "")
	queuedB := createMarkerTestMessage(t, DB, queuedA.ChatId, "")
	// A queued message of another chat and an unmarked one of this chat: both
	// must survive an interrupt untouched.
	foreign := createMarkerTestMessage(t, DB, 0, "")
	other := createMarkerTestMessage(t, DB, queuedA.ChatId, `{"note":"keep"}`)

	for _, message := range []*Message{queuedA, queuedB, foreign} {
		if _, err := SetMessageQueued(DB, message); err != nil {
			t.Fatalf("SetMessageQueued failed: %v", err)
		}
	}

	cleared, err := ClearMessageQueuedForChat(DB, queuedA.ChatId)
	if err != nil {
		t.Fatalf("ClearMessageQueuedForChat failed: %v", err)
	}
	if cleared != 2 {
		t.Fatalf("expected 2 messages to be drained, got %d", cleared)
	}

	assertQueued := func(message *Message, want bool) {
		t.Helper()
		if got := IsMessageQueued(loadMarkerTestMessage(t, DB, message.UUID)); got != want {
			t.Fatalf("message %s queued=%v, want %v", message.UUID, got, want)
		}
	}
	assertQueued(queuedA, false)
	assertQueued(queuedB, false)
	assertQueued(foreign, true)
	assertQueued(other, false)

	// A chat without any marked message drains nothing.
	if cleared, err := ClearMessageQueuedForChat(DB, 0); err != nil || cleared != 0 {
		t.Fatalf("expected a nil chat to drain nothing, got cleared=%v err=%v", cleared, err)
	}
}

// The chat-wide drain runs on every send that supersedes a queued reply, so it
// prefilters on the metadata key instead of loading every message of the chat.
// The prefilter must not lose a marker that sits next to unrelated keys, and it
// must not touch a message whose metadata does not carry the key at all.
func TestClearMessageQueuedForChatFindsMarkersNextToOtherMetadata(t *testing.T) {
	DB := openMarkerTestDB(t)

	queued := createMarkerTestMessage(t, DB, 0, `{"finished":true,"reasoning":"kept"}`)
	// The key is present with a non-"queued" value: it is not pending, but the
	// marker must still be drained (any value of the key is stale).
	stale := createMarkerTestMessage(t, DB, queued.ChatId, `{"queue_state":"running"}`)
	// No queue_state at all: the prefilter must skip it entirely.
	untouched := createMarkerTestMessage(t, DB, queued.ChatId, `{"note":"keep"}`)
	// A message whose metadata merely *mentions* the key inside a value must
	// not be treated as marked.
	mention := createMarkerTestMessage(t, DB, queued.ChatId, `{"note":"queue_state"}`)

	if _, err := SetMessageQueued(DB, queued); err != nil {
		t.Fatalf("SetMessageQueued failed: %v", err)
	}

	cleared, err := ClearMessageQueuedForChat(DB, queued.ChatId)
	if err != nil {
		t.Fatalf("ClearMessageQueuedForChat failed: %v", err)
	}
	if cleared != 2 {
		t.Fatalf("expected the queued and the stale marker to be drained, got %d", cleared)
	}
	if IsMessageQueued(loadMarkerTestMessage(t, DB, queued.UUID)) {
		t.Fatalf("expected %s to be drained", queued.UUID)
	}
	if _, ok := messageMeta(loadMarkerTestMessage(t, DB, stale.UUID))[QueueStateMetaKey]; ok {
		t.Fatalf("expected the stale marker to be removed")
	}
	if note, _ := messageMeta(loadMarkerTestMessage(t, DB, untouched.UUID))["note"].(string); note != "keep" {
		t.Fatalf("expected the unmarked message to be untouched")
	}
	if note, _ := messageMeta(loadMarkerTestMessage(t, DB, mention.UUID))["note"].(string); note != "queue_state" {
		t.Fatalf("expected a message that only mentions the key to be untouched")
	}
}
