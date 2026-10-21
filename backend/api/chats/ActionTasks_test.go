package chats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

func TestPendingActionsForMessageRecognizesKinds(t *testing.T) {
	toolCall := json.RawMessage(`{"id":"tc-1","name":"run_tool","status":"pending_confirmation"}`)
	toolCalls := []json.RawMessage{toolCall}

	cases := []struct {
		name      string
		meta      string
		toolCalls *[]json.RawMessage
		want      string
	}{
		{
			name: "confirmable_action",
			meta: `{"confirmable_actions":[{"action_id":"a1","status":"pending","target_tool_name":"ssh_exec","title":"Run"}]}`,
			want: ActionTaskKindConfirmableAction,
		},
		{
			name: "opencode_permission",
			meta: `{"opencode_permission":{"status":"pending","permission_type":"edit"}}`,
			want: ActionTaskKindOpencodePermission,
		},
		{
			name: "opencode_needs_action",
			meta: `{"opencode_needs_action":{"status":"pending","reason":"ApiError"}}`,
			want: ActionTaskKindOpencodeNeedsAction,
		},
		{
			name: "interaction_confirmation",
			meta: `{"interaction_confirmation":{"status":"pending","source_message_uuid":"src-1"}}`,
			want: ActionTaskKindInteractionConfirmation,
		},
		{
			name: "runtime_selection",
			meta: `{"runtime_selectors":[{"id":"tc-1","status":"pending","title":"Choose runtime","recommended":"opencode"}]}`,
			want: ActionTaskKindRuntimeSelection,
		},
		{
			name: "runtime_selection_single_object_without_status",
			meta: `{"runtime_selectors":{"id":"tc-2","title":"Choose runtime"}}`,
			want: ActionTaskKindRuntimeSelection,
		},
		{
			name:      "tool_confirmation",
			meta:      `{"finished":true}`,
			toolCalls: &toolCalls,
			want:      ActionTaskKindToolConfirmation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message := database.Message{MetaData: database.JSONRaw(tc.meta), ToolCalls: tc.toolCalls}
			actions := pendingActionsForMessage(message)
			if len(actions) != 1 {
				t.Fatalf("expected exactly one action, got %d (%+v)", len(actions), actions)
			}
			if actions[0].Kind != tc.want {
				t.Fatalf("expected kind %q, got %q", tc.want, actions[0].Kind)
			}
		})
	}

	resolved := database.Message{MetaData: database.JSONRaw(`{
		"confirmable_actions":[{"action_id":"a1","status":"executed"}],
		"opencode_permission":{"status":"resolved"},
		"opencode_needs_action":{"status":"resolved"},
		"interaction_confirmation":{"status":"approved"},
		"runtime_selectors":[{"id":"tc-1","status":"confirmed"},{"id":"tc-2","status":"cancelled"}]
	}`)}
	if actions := pendingActionsForMessage(resolved); len(actions) != 0 {
		t.Fatalf("expected no pending actions on a resolved message, got %+v", actions)
	}
}

func TestPendingConfirmableActionCarriesProposedParams(t *testing.T) {
	message := database.Message{MetaData: database.JSONRaw(`{"confirmable_actions":[{
		"action_id":"a1","status":"pending","target_tool_name":"coder_write",
		"title":"Write file","confirm_label":"Write it","continue_after_execute":true,
		"input":{"path":"src/app.ts","content":"export const answer = 42;"}
	}]}`)}

	actions := pendingActionsForMessage(message)
	if len(actions) != 1 {
		t.Fatalf("expected exactly one action, got %d (%+v)", len(actions), actions)
	}
	got := actions[0]
	if got.Input["path"] != "src/app.ts" || got.Input["content"] != "export const answer = 42;" {
		t.Fatalf("proposed params not carried on the action: %+v", got.Input)
	}
	if got.ConfirmLabel != "Write it" {
		t.Fatalf("expected confirm_label to be carried, got %q", got.ConfirmLabel)
	}
	if !got.ContinueAfterExecute {
		t.Fatalf("expected continue_after_execute to be carried")
	}
}

func TestActionTaskKeyIsStableAndNormalized(t *testing.T) {
	first := actionTaskKey("  CHAT-UUID  ", " Message-UUID ")
	second := actionTaskKey("chat-uuid", "message-uuid")
	if first != second {
		t.Fatalf("expected normalized task keys to match, got %q and %q", first, second)
	}
	if first != "chat-uuid:message-uuid" {
		t.Fatalf("unexpected task key format: %q", first)
	}
}

func createActionTaskMessage(t *testing.T, DB *gorm.DB, chat database.Chat, sender, receiver *database.User, meta string) database.Message {
	t.Helper()
	text := "Please confirm this action."
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   sender.ID,
		ReceiverId: receiver.ID,
		DataType:   "text",
		Text:       &text,
		MetaData:   database.JSONRaw(meta),
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}
	return message
}

func withActionTasksUser(req *http.Request, DB *gorm.DB, user *database.User) *http.Request {
	//nolint:staticcheck // the "db"/"user" string keys are the fixed context keys read by util.GetDBAndUser
	ctx := context.WithValue(req.Context(), "db", DB)
	//nolint:staticcheck // see above
	ctx = context.WithValue(ctx, "user", user)
	return req.WithContext(ctx)
}

func actionTasksRequest(DB *gorm.DB, user *database.User, browserToken bool) *httptest.ResponseRecorder {
	return actionTasksRequestQuery(DB, user, browserToken, "")
}

func actionTasksRequestQuery(DB *gorm.DB, user *database.User, browserToken bool, rawQuery string) *httptest.ResponseRecorder {
	target := "/api/v1/chats/action-tasks"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := httptest.NewRequest("GET", target, nil)
	if browserToken {
		req = browserTokenContext(req, DB, user)
	} else {
		req = withActionTasksUser(req, DB, user)
	}
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.GetActionTasks(rr, req)
	return rr
}

func decodeActionTasks(t *testing.T, rr *httptest.ResponseRecorder) ActionTasksResponse {
	t.Helper()
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var response ActionTasksResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode action tasks response: %v", err)
	}
	return response
}

func TestGetActionTasksCountsPendingTasksAndHonorsDismissal(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-bot@example.com", false)
	chat := createChatOfType(t, DB, owner, bot, "conversation")
	message := createActionTaskMessage(t, DB, chat, bot, owner, `{"confirmable_actions":[{"action_id":"a1","status":"pending","target_tool_name":"ssh_exec"}]}`)

	response := decodeActionTasks(t, actionTasksRequest(DB, owner, false))
	if response.Count != 1 || len(response.Rows) != 1 {
		t.Fatalf("expected exactly one pending task, got count=%d rows=%d", response.Count, len(response.Rows))
	}
	if response.Rows[0].TaskKey != actionTaskKey(chat.UUID, message.UUID) {
		t.Fatalf("unexpected task key %q", response.Rows[0].TaskKey)
	}
	if response.Rows[0].ChatUUID != chat.UUID {
		t.Fatalf("unexpected chat uuid %q", response.Rows[0].ChatUUID)
	}
	if len(response.Rows[0].Actions) != 1 || response.Rows[0].Actions[0].Kind != ActionTaskKindConfirmableAction {
		t.Fatalf("unexpected actions %+v", response.Rows[0].Actions)
	}

	dismissal := database.ActionTaskDismissal{UserId: owner.ID, ChatId: chat.ID, TaskKey: actionTaskKey(chat.UUID, message.UUID)}
	if err := DB.Create(&dismissal).Error; err != nil {
		t.Fatalf("failed to create dismissal: %v", err)
	}

	response = decodeActionTasks(t, actionTasksRequest(DB, owner, false))
	if response.Count != 0 || len(response.Rows) != 0 {
		t.Fatalf("expected dismissed task to be hidden, got count=%d rows=%d", response.Count, len(response.Rows))
	}
}

func TestGetActionTasksCountOnly(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-count@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-count-bot@example.com", false)
	chat := createChatOfType(t, DB, owner, bot, "conversation")
	createActionTaskMessage(t, DB, chat, bot, owner, `{"opencode_permission":{"status":"pending"}}`)

	req := withActionTasksUser(httptest.NewRequest("GET", "/api/v1/chats/action-tasks?count_only=1", nil), DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.GetActionTasks(rr, req)

	response := decodeActionTasks(t, rr)
	if response.Count != 1 {
		t.Fatalf("expected count 1, got %d", response.Count)
	}
	if len(response.Rows) != 0 {
		t.Fatalf("expected no rows for count_only, got %d", len(response.Rows))
	}
}

func TestGetActionTasksBrowserTokenRestrictsToInteractions(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-browser@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-browser-bot@example.com", false)

	interactionChat := createChatOfType(t, DB, owner, bot, "interaction")
	conversationChat := createChatOfType(t, DB, owner, bot, "conversation")
	createActionTaskMessage(t, DB, interactionChat, bot, owner, `{"opencode_permission":{"status":"pending"}}`)
	createActionTaskMessage(t, DB, conversationChat, bot, owner, `{"opencode_permission":{"status":"pending"}}`)

	response := decodeActionTasks(t, actionTasksRequest(DB, owner, true))
	if response.Count != 1 || len(response.Rows) != 1 {
		t.Fatalf("expected browser token to see only the interaction task, got count=%d rows=%d", response.Count, len(response.Rows))
	}
	if response.Rows[0].ChatUUID != interactionChat.UUID {
		t.Fatalf("expected interaction chat %q, got %q", interactionChat.UUID, response.Rows[0].ChatUUID)
	}
}

func TestGetActionTasksNoPendingTasks(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-empty@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-empty-bot@example.com", false)
	chat := createChatOfType(t, DB, owner, bot, "conversation")
	createActionTaskMessage(t, DB, chat, bot, owner, `{"finished":true}`)

	response := decodeActionTasks(t, actionTasksRequest(DB, owner, false))
	if response.Count != 0 || len(response.Rows) != 0 {
		t.Fatalf("expected no tasks, got count=%d rows=%d", response.Count, len(response.Rows))
	}
}

func dismissAllActionTasksRequest(DB *gorm.DB, user *database.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/chats/action-tasks/dismiss-all", nil)
	req = withActionTasksUser(req, DB, user)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.DismissAllActionTasks(rr, req)
	return rr
}

func TestDismissAllActionTasksRequiresAdmin(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-bulk-nonadmin@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-bulk-nonadmin-bot@example.com", false)
	chat := createChatOfType(t, DB, owner, bot, "conversation")
	createActionTaskMessage(t, DB, chat, bot, owner, `{"opencode_permission":{"status":"pending"}}`)

	rr := dismissAllActionTasksRequest(DB, owner)
	if rr.Code != 403 {
		t.Fatalf("expected 403 for a non-admin, got %d: %s", rr.Code, rr.Body.String())
	}

	var dismissals int64
	if err := DB.Model(&database.ActionTaskDismissal{}).Count(&dismissals).Error; err != nil {
		t.Fatalf("failed to count dismissals: %v", err)
	}
	if dismissals != 0 {
		t.Fatalf("expected no dismissals written for a non-admin, got %d", dismissals)
	}
}

func TestDismissAllActionTasksClearsAdminScope(t *testing.T) {
	DB := setupChatsTestDB(t)
	admin := createUserForChatsTest(t, DB, "actions-bulk-admin@example.com", true)
	ownerA := createUserForChatsTest(t, DB, "actions-bulk-owner-a@example.com", false)
	ownerB := createUserForChatsTest(t, DB, "actions-bulk-owner-b@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-bulk-bot@example.com", false)

	chatA := createChatOfType(t, DB, ownerA, bot, "conversation")
	chatB := createChatOfType(t, DB, ownerB, bot, "conversation")
	createActionTaskMessage(t, DB, chatA, bot, ownerA, `{"opencode_permission":{"status":"pending"}}`)
	createActionTaskMessage(t, DB, chatB, bot, ownerB, `{"opencode_permission":{"status":"pending"}}`)

	adminScopeAll := func() ActionTasksResponse {
		req := withActionTasksUser(httptest.NewRequest("GET", "/api/v1/chats/action-tasks?scope=all", nil), DB, admin)
		rr := httptest.NewRecorder()
		h := &ChatsHandler{}
		h.GetActionTasks(rr, req)
		return decodeActionTasks(t, rr)
	}

	if response := adminScopeAll(); response.Count != 2 {
		t.Fatalf("expected admin scope to see 2 pending tasks, got %d", response.Count)
	}

	rr := dismissAllActionTasksRequest(DB, admin)
	if rr.Code != 200 {
		t.Fatalf("expected 200 from dismiss-all, got %d: %s", rr.Code, rr.Body.String())
	}
	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode dismiss-all response: %v", err)
	}
	if dismissed, ok := result["dismissed"].(float64); !ok || int(dismissed) != 2 {
		t.Fatalf("expected dismissed=2, got %v", result["dismissed"])
	}

	if response := adminScopeAll(); response.Count != 0 {
		t.Fatalf("expected admin scope to be cleared after dismiss-all, got %d", response.Count)
	}

	// Each owner keeps their own pending task: the admin dismissal is keyed to
	// the admin and must not leak into the owners' stacks.
	if response := decodeActionTasks(t, actionTasksRequest(DB, ownerA, false)); response.Count != 1 {
		t.Fatalf("expected owner A to keep 1 pending task, got %d", response.Count)
	}
	if response := decodeActionTasks(t, actionTasksRequest(DB, ownerB, false)); response.Count != 1 {
		t.Fatalf("expected owner B to keep 1 pending task, got %d", response.Count)
	}
}

func TestDismissActionTaskRejectsForeignChat(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-dismiss-owner@example.com", false)
	other := createUserForChatsTest(t, DB, "actions-dismiss-other@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-dismiss-bot@example.com", false)
	chat := createChatOfType(t, DB, other, bot, "conversation")
	message := createActionTaskMessage(t, DB, chat, bot, other, `{"opencode_permission":{"status":"pending"}}`)

	body, _ := json.Marshal(map[string]string{"message_uuid": message.UUID})
	req := httptest.NewRequest("POST", "/api/v1/chats/"+chat.UUID+"/action-tasks/dismiss", bytes.NewReader(body))
	req.SetPathValue("chat_uuid", chat.UUID)
	req = withActionTasksUser(req, DB, owner)

	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.DismissActionTask(rr, req)

	if rr.Code != 404 {
		t.Fatalf("expected 404 for a chat not owned by the user, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestParseActionTasksPageNormalizesParams(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantLimit int
		wantSkip  int
		wantCount bool
	}{
		{name: "defaults", query: "", wantLimit: actionTasksDefaultPageSize},
		{name: "explicit", query: "limit=5&offset=10", wantLimit: 5, wantSkip: 10},
		{name: "zero limit falls back", query: "limit=0", wantLimit: actionTasksDefaultPageSize},
		{name: "negative limit falls back", query: "limit=-3", wantLimit: actionTasksDefaultPageSize},
		{name: "garbage falls back", query: "limit=abc&offset=xyz", wantLimit: actionTasksDefaultPageSize},
		{name: "limit clamped", query: "limit=9999", wantLimit: actionTasksMaxPageSize},
		{name: "negative offset clamped", query: "offset=-7", wantLimit: actionTasksDefaultPageSize},
		{name: "count only", query: "count_only=1&limit=1", wantLimit: 1, wantCount: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page := parseActionTasksPage(url.Values{})
			if test.query != "" {
				parsed, err := url.ParseQuery(test.query)
				if err != nil {
					t.Fatalf("failed to parse query %q: %v", test.query, err)
				}
				page = parseActionTasksPage(parsed)
			}
			if page.Limit != test.wantLimit {
				t.Fatalf("expected limit %d, got %d", test.wantLimit, page.Limit)
			}
			if page.Offset != test.wantSkip {
				t.Fatalf("expected offset %d, got %d", test.wantSkip, page.Offset)
			}
			if page.CountOnly != test.wantCount {
				t.Fatalf("expected count_only %v, got %v", test.wantCount, page.CountOnly)
			}
		})
	}
}

// createPendingActionTaskChats creates one chat with a pending action task per
// call and returns the created chats in creation order.
func createPendingActionTaskChats(t *testing.T, DB *gorm.DB, owner, bot *database.User, count int) []database.Chat {
	t.Helper()
	chats := make([]database.Chat, 0, count)
	for i := 0; i < count; i++ {
		chat := createChatOfType(t, DB, owner, bot, "conversation")
		createActionTaskMessage(t, DB, chat, bot, owner, `{"opencode_permission":{"status":"pending"}}`)
		chats = append(chats, chat)
	}
	return chats
}

func TestGetActionTasksPagesRowsAndKeepsAuthoritativeTotal(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-page-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-page-bot@example.com", false)
	chats := createPendingActionTaskChats(t, DB, owner, bot, 5)

	seen := map[string]int{}
	for offset := 0; offset < 6; offset += 2 {
		response := decodeActionTasks(t, actionTasksRequestQuery(DB, owner, false, fmt.Sprintf("limit=2&offset=%d", offset)))
		if response.Count != 5 || response.Total != 5 {
			t.Fatalf("offset=%d: expected authoritative total 5, got count=%d total=%d", offset, response.Count, response.Total)
		}
		if response.Limit != 2 || response.Offset != offset {
			t.Fatalf("offset=%d: expected echoed window limit=2 offset=%d, got limit=%d offset=%d", offset, offset, response.Limit, response.Offset)
		}
		wantRows := 2
		wantMore := true
		switch {
		case offset == 4:
			wantRows = 1
			wantMore = false
		case offset == 6:
			wantRows = 0
			wantMore = false
		}
		if len(response.Rows) != wantRows {
			t.Fatalf("offset=%d: expected %d rows, got %d", offset, wantRows, len(response.Rows))
		}
		if response.HasMore != wantMore {
			t.Fatalf("offset=%d: expected has_more=%v, got %v", offset, wantMore, response.HasMore)
		}
		for _, row := range response.Rows {
			if previous, duplicate := seen[row.ChatUUID]; duplicate {
				t.Fatalf("chat %q appeared in both offsets %d and %d", row.ChatUUID, previous, offset)
			}
			seen[row.ChatUUID] = offset
		}
	}

	if len(seen) != len(chats) {
		t.Fatalf("expected the pages to cover all %d chats, got %d", len(chats), len(seen))
	}
	for _, chat := range chats {
		if _, ok := seen[chat.UUID]; !ok {
			t.Fatalf("chat %q was never returned by any page", chat.UUID)
		}
	}
}

func TestGetActionTasksCountOnlyReturnsTotalWithoutRows(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-count-page@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-count-page-bot@example.com", false)
	createPendingActionTaskChats(t, DB, owner, bot, 3)

	response := decodeActionTasks(t, actionTasksRequestQuery(DB, owner, false, "count_only=1&limit=1"))
	if response.Count != 3 || response.Total != 3 {
		t.Fatalf("expected count_only to still report the total 3, got count=%d total=%d", response.Count, response.Total)
	}
	if len(response.Rows) != 0 {
		t.Fatalf("expected no rows for count_only, got %d", len(response.Rows))
	}
	if response.HasMore {
		t.Fatal("expected count_only to never advertise has_more")
	}
}

func TestGetActionTasksClampsPageLimit(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-clamp@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-clamp-bot@example.com", false)
	createPendingActionTaskChats(t, DB, owner, bot, 3)

	response := decodeActionTasks(t, actionTasksRequestQuery(DB, owner, false, "limit=9999"))
	if response.Limit != actionTasksMaxPageSize {
		t.Fatalf("expected limit clamped to %d, got %d", actionTasksMaxPageSize, response.Limit)
	}
	if len(response.Rows) != 3 {
		t.Fatalf("expected the clamped limit to still return all 3 tasks, got %d", len(response.Rows))
	}
	if response.HasMore {
		t.Fatal("expected has_more=false when the page covers every task")
	}
}

// A page request must be served by walking the candidate chats in batches: with
// more chats than one batch, every pending task still has to be found so the
// total stays authoritative (ci#801).
func TestGetActionTasksFindsTasksBeyondFirstScanBatch(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "actions-batch@example.com", false)
	bot := createUserForChatsTest(t, DB, "actions-batch-bot@example.com", false)
	chatCount := actionTasksScanBatchSize + 5
	createPendingActionTaskChats(t, DB, owner, bot, chatCount)

	response := decodeActionTasks(t, actionTasksRequestQuery(DB, owner, false, fmt.Sprintf("limit=%d", actionTasksMaxPageSize)))
	if response.Total != chatCount {
		t.Fatalf("expected the batched scan to find all %d tasks, got %d", chatCount, response.Total)
	}
	if len(response.Rows) != chatCount {
		t.Fatalf("expected %d rows, got %d", chatCount, len(response.Rows))
	}
}
