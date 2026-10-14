package msgmate

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

func setupStreamingPersistenceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "streaming_persistence_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

// TestStreamingSnapshotPersistsToolCalls guards ci#332 item 7: persisting a
// multi-tool snapshot must not fail with "SQL logic error: row value misused".
// GORM map updates bypass the struct field JSON serializers, so []string /
// []json.RawMessage values previously reached the SQLite driver raw and the
// snapshot was silently dropped, losing tool-call state for tool-using bots.
func TestStreamingSnapshotPersistsToolCalls(t *testing.T) {
	db := setupStreamingPersistenceTestDB(t)

	row := &database.StreamingMessage{SessionID: "sess", SenderId: 1, ReceiverId: 2}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("create snapshot row: %v", err)
	}

	p := &partialMessagePersister{db: db, chatUUID: "chat", rowID: row.ID}
	toolCalls := []interface{}{
		map[string]interface{}{"id": "call-1", "name": "coder_bash", "arguments": `{"command":"ls"}`},
		map[string]interface{}{"id": "call-2", "name": "coder_read", "arguments": `{"path":"main.go"}`},
	}
	p.flush(7, "streaming text", []string{"thinking"}, toolCalls)

	var got database.StreamingMessage
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatalf("reload snapshot: %v", err)
	}
	if got.Seq != 7 || got.Text != "streaming text" {
		t.Fatalf("snapshot scalar fields not persisted: seq=%d text=%q", got.Seq, got.Text)
	}
	if len(got.ToolCalls) != 2 {
		t.Fatalf("expected 2 persisted tool calls, got %d (%s)", len(got.ToolCalls), string(mustJSON(got.ToolCalls)))
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(got.ToolCalls[0], &decoded); err != nil {
		t.Fatalf("persisted tool call is not valid JSON: %v", err)
	}
	if decoded["name"] != "coder_bash" {
		t.Fatalf("unexpected persisted tool call: %#v", decoded)
	}
	if len(got.Reasoning) != 1 || got.Reasoning[0] != "thinking" {
		t.Fatalf("reasoning not persisted: %#v", got.Reasoning)
	}
}

func mustJSON(v interface{}) []byte {
	encoded, _ := json.Marshal(v)
	return encoded
}
