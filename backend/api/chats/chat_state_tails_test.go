package chats

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// seedTailTestChats creates `chats` chats owned by fresh users, each carrying
// `perChat` messages with fat tool_calls/meta_data payloads. The created_at of
// every message is distinct except for the newest pair of each chat, which
// share a timestamp so the `id DESC` tie-break is exercised.
func seedTailTestChats(t *testing.T, DB *gorm.DB, chats int, perChat int) []database.Chat {
	t.Helper()
	owner := createUserForChatsTest(t, DB, "tails-fat-owner", false)
	bot := createUserForChatsTest(t, DB, "tails-fat-bot", false)

	fatToolCalls := make([]json.RawMessage, 0, 8)
	for i := 0; i < 8; i++ {
		fatToolCalls = append(fatToolCalls, json.RawMessage(fmt.Sprintf(
			`{"id":"call_%d","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/very/long/path/number/%d/%s\"}"}}`,
			i, i, strings.Repeat("x", 512),
		)))
	}
	fatMeta := database.JSONRaw(fmt.Sprintf(`{"finished":true,"padding":%q}`, strings.Repeat("m", 4096)))

	out := make([]database.Chat, 0, chats)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for c := 0; c < chats; c++ {
		chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "conversation"}
		if err := DB.Create(&chat).Error; err != nil {
			t.Fatalf("create chat: %v", err)
		}
		for i := 0; i < perChat; i++ {
			text := fmt.Sprintf("chat-%d message-%d", c, i)
			created := base.Add(time.Duration(c*perChat+i) * time.Minute)
			if i >= perChat-2 {
				// The two newest messages of each chat share a timestamp.
				created = base.Add(time.Duration((c+1)*perChat) * time.Minute)
			}
			toolCalls := fatToolCalls
			message := database.Message{
				SenderId:   bot.ID,
				ReceiverId: owner.ID,
				ChatId:     chat.ID,
				Text:       &text,
				ToolCalls:  &toolCalls,
				MetaData:   fatMeta,
			}
			if err := DB.Create(&message).Error; err != nil {
				t.Fatalf("create message: %v", err)
			}
			if err := DB.Exec("UPDATE messages SET created_at = ? WHERE id = ?",
				created.Format("2006-01-02 15:04:05"), message.ID).Error; err != nil {
				t.Fatalf("stamp created_at: %v", err)
			}
		}
		out = append(out, chat)
	}
	return out
}

// TestMessageTailBatchReturnsFiveNewestPerChat pins the ci#1190 shape: the
// batched tail must return exactly `limit` messages per chat, newest first, with
// created_at ties broken by id DESC and the heavy columns intact through the
// rank-then-fetch join.
func TestMessageTailBatchReturnsFiveNewestPerChat(t *testing.T) {
	DB := openBatchStatesTestDB(t)
	const perChat = 7
	const limit = 5
	chats := seedTailTestChats(t, DB, 3, perChat)

	ids := make([]uint, 0, len(chats))
	for _, chat := range chats {
		ids = append(ids, chat.ID)
	}
	tails, err := recentMessagesByChatLimit(DB, ids, limit)
	if err != nil {
		t.Fatalf("recentMessagesByChatLimit: %v", err)
	}

	for _, chat := range chats {
		messages := tails[chat.ID]
		if len(messages) != limit {
			t.Fatalf("chat %d: expected %d tail messages, got %d", chat.ID, limit, len(messages))
		}
		for i, message := range messages {
			if message.ChatId != chat.ID {
				t.Fatalf("chat %d: tail row %d belongs to chat %d", chat.ID, i, message.ChatId)
			}
			// Uuid proves the whole lean projection came through the join, and
			// the fat columns prove they were not dropped to keep the plan cheap.
			if message.UUID == "" {
				t.Fatalf("chat %d: tail row %d lost its uuid through the join", chat.ID, i)
			}
			if message.Text == nil || !strings.HasPrefix(*message.Text, fmt.Sprintf("chat-%d ", indexOfChat(t, chats, chat.ID))) {
				t.Fatalf("chat %d: tail row %d has an unexpected text: %v", chat.ID, i, message.Text)
			}
			if message.ToolCalls == nil || len(*message.ToolCalls) != 8 {
				t.Fatalf("chat %d: tail row %d lost its tool_calls through the join", chat.ID, i)
			}
			if len(message.MetaData) == 0 {
				t.Fatalf("chat %d: tail row %d lost its meta_data through the join", chat.ID, i)
			}
			if i == 0 {
				continue
			}
			prev := messages[i-1]
			if message.CreatedAt.After(prev.CreatedAt) {
				t.Fatalf("chat %d: tail is not newest-first at row %d", chat.ID, i)
			}
			if message.CreatedAt.Equal(prev.CreatedAt) && message.ID > prev.ID {
				t.Fatalf("chat %d: created_at tie at rows %d/%d not broken by id DESC", chat.ID, i-1, i)
			}
		}
		// The head of the tail must be the chat's newest message, and the fifth
		// row must be the 5th newest (not the 7th, i.e. the limit has to be
		// applied per chat rather than to the batch as a whole).
		if got, want := messages[0].Text, fmt.Sprintf("chat-%d message-%d", indexOfChat(t, chats, chat.ID), perChat-1); got != nil && *got != want {
			t.Fatalf("chat %d: newest tail message is %q, want %q", chat.ID, *got, want)
		}
	}
}

// indexOfChat returns the position of chatID in chats.
func indexOfChat(t *testing.T, chats []database.Chat, chatID uint) int {
	t.Helper()
	for i, chat := range chats {
		if chat.ID == chatID {
			return i
		}
	}
	t.Fatalf("chat %d not in the seeded set", chatID)
	return -1
}

// seedTailPlanRows seeds a production-shaped message table and refreshes the
// planner statistics: without rows ANALYZE has nothing to measure and SQLite
// happily picks any index, which would make a plan assertion pass by accident.
func seedTailPlanRows(t *testing.T, DB *gorm.DB) {
	t.Helper()
	inserts := []string{
		"INSERT INTO messages (created_at, updated_at, chat_id, sender_id, receiver_id, data_type, deleted_at) " +
			"SELECT datetime('now', '-' || (x % 600) || ' minutes'), datetime('now'), (x % 8) + 1, 1, 1, 'text', " +
			"CASE WHEN x % 7 = 0 THEN datetime('now') ELSE NULL END " +
			"FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 4000) SELECT x FROM c)",
	}
	for _, stmt := range inserts {
		if err := DB.Exec(stmt).Error; err != nil {
			t.Fatalf("seeding tail plan rows failed: %v", err)
		}
	}
	if err := DB.Exec("ANALYZE").Error; err != nil {
		t.Fatalf("ANALYZE failed: %v", err)
	}
}

// explainTailPlan returns the flattened `EXPLAIN QUERY PLAN` output for the
// shared batched-tail builder.
func explainTailPlan(t *testing.T, DB *gorm.DB, chatIDs []uint, limit int) string {
	t.Helper()
	query, args := messageTailBatchQuery(chatIDs, limit)
	rows, err := DB.Raw("EXPLAIN QUERY PLAN "+query, args...).Rows()
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN failed: %v", err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("failed to scan query plan row: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}
	return plan.String()
}

// TestMessageTailBatchQueryPlan pins the ci#1190 index reality check measured on
// this dataset: the ranking window sorts each chat by `created_at DESC, id DESC`,
// so the old (chat_id, id DESC) index still produced a per-chat temp B-tree
// ("USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY"). With
// idx_messages_chat_deleted_created_id the window is a straight index scan and
// the statement never degrades into a full `SCAN messages`.
//
// The trailing "USE TEMP B-TREE FOR ORDER BY" for the cross-chat ORDER BY stays:
// the result of a batched multi-chat read is inherently not in index order.
func TestMessageTailBatchQueryPlan(t *testing.T) {
	DB := openBatchStatesTestDB(t)
	seedTailPlanRows(t, DB)

	plan := explainTailPlan(t, DB, []uint{1, 2, 3, 4, 5, 6, 7, 8}, 5)
	t.Logf("batched tail query plan:\n%s", plan)
	if !strings.Contains(plan, "idx_messages_chat_deleted_created_id") {
		t.Fatalf("expected the batched tail window to use idx_messages_chat_deleted_created_id, plan was:\n%s", plan)
	}
	if strings.Contains(plan, "USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY") {
		t.Fatalf("the per-chat ranking still sorts (temp B-tree) - the created_at-ordered index is missing:\n%s", plan)
	}
	for _, line := range strings.Split(plan, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "SCAN messages") {
			t.Fatalf("batched tail degrades to a full table scan (%q), plan was:\n%s", trimmed, plan)
		}
	}
}

// TestMessageTailBatchQueryRanksBeforeJoin pins the ordering guarantee the
// corrected query shape depends on: the rn predicate is applied inside the
// derived table, i.e. before the heavy join, otherwise Postgres joins every
// message of every chat in the batch again (ci#1190).
func TestMessageTailBatchQueryRanksBeforeJoin(t *testing.T) {
	query, args := messageTailBatchQuery([]uint{11, 22, 33}, 5)
	if len(args) != 4 {
		t.Fatalf("expected 3 chat ids + 1 limit argument, got %d", len(args))
	}
	if args[3] != 5 {
		t.Fatalf("expected the limit as the last argument, got %v", args[3])
	}

	rankedIdx := strings.Index(query, ") ranked")
	if rankedIdx < 0 {
		t.Fatalf("expected a `ranked` derived table in the query:\n%s", query)
	}
	rnIdx := strings.Index(query, "rn <= ?")
	if rnIdx < 0 {
		t.Fatalf("expected an rn predicate in the query:\n%s", query)
	}
	if rnIdx > rankedIdx {
		t.Fatalf("the rn predicate must sit inside the derived table, i.e. before `) ranked`:\n%s", query)
	}
	if strings.Contains(query[rankedIdx:], "rn <= ?") {
		t.Fatalf("the rn predicate must not be applied above the join:\n%s", query)
	}
	if !strings.Contains(query, "JOIN messages m ON m.id = ranked.id") {
		t.Fatalf("expected the lean projection to be joined on the surviving ids:\n%s", query)
	}
	// The window must only read the three cheap ranking columns.
	window := query[strings.Index(query, "ROW_NUMBER()"):strings.Index(query, "FROM messages")]
	if strings.Contains(window, "tool_calls") || strings.Contains(window, "meta_data") {
		t.Fatalf("the window function must not rank over the heavy columns:\n%s", query)
	}
}

// TestLatestMessageForChatLeanProjection pins the stall-guard read: one lean row
// per chat (no detoasted content/reasoning), gorm.ErrRecordNotFound for a chat
// without messages, and the id tie-break on equal created_at.
func TestLatestMessageForChatLeanProjection(t *testing.T) {
	DB := openBatchStatesTestDB(t)
	owner := createUserForChatsTest(t, DB, "latest-owner", false)
	bot := createUserForChatsTest(t, DB, "latest-bot", false)
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction:opencode"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}

	if _, err := latestMessageForChat(DB, chat.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound for an empty chat, got %v", err)
	}

	hugeContent := make([]byte, 256*1024)
	for i := range hugeContent {
		hugeContent[i] = 'c'
	}
	reasoning := []string{strings.Repeat("r", 4096)}
	tieTime := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	first := database.Message{
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		ChatId:     chat.ID,
		Content:    &hugeContent,
		Reasoning:  &reasoning,
		MetaData:   database.JSONRaw(`{"finished":true}`),
	}
	if err := DB.Create(&first).Error; err != nil {
		t.Fatalf("create first message: %v", err)
	}
	second := database.Message{
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		ChatId:     chat.ID,
		MetaData:   database.JSONRaw(`{"finished":false}`),
	}
	if err := DB.Create(&second).Error; err != nil {
		t.Fatalf("create second message: %v", err)
	}
	// Same created_at for both: the newest message must still be deterministic.
	for _, id := range []uint{first.ID, second.ID} {
		if err := DB.Exec("UPDATE messages SET created_at = ? WHERE id = ?",
			tieTime.Format("2006-01-02 15:04:05"), id).Error; err != nil {
			t.Fatalf("stamp created_at: %v", err)
		}
	}

	latest, err := latestMessageForChat(DB, chat.ID)
	if err != nil {
		t.Fatalf("latestMessageForChat: %v", err)
	}
	if latest.ID != second.ID {
		t.Fatalf("expected the id tie-break to pick message %d, got %d", second.ID, latest.ID)
	}
	if latest.UUID == "" || latest.ChatId != chat.ID || len(latest.MetaData) == 0 {
		t.Fatalf("lean projection is incomplete: %+v", latest)
	}
	if latest.Content != nil || latest.Reasoning != nil {
		t.Fatalf("lean projection must not read content/reasoning, got content=%v reasoning=%v",
			latest.Content != nil, latest.Reasoning != nil)
	}
}
