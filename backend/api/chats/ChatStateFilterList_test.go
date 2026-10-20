package chats

import (
	"net/http/httptest"
	"testing"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// stateFilterListRequest drives ChatsHandler.List with the authenticated user in
// the request context, exactly like the HTTP layer does.
func stateFilterListRequest(t *testing.T, DB *gorm.DB, user *database.User, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	req = withActionTasksUser(req, DB, user)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)
	return rr
}

// seedChatWithLatestMessage creates a chat plus its newest message, which is
// what the state resolver reads. metadata may be empty to model a chat without
// any agent outcome (which resolves to idle).
func seedChatWithLatestMessage(
	t *testing.T,
	DB *gorm.DB,
	owner *database.User,
	bot *database.User,
	chatType string,
	metadata string,
	createdAt time.Time,
) database.Chat {
	t.Helper()
	chat := createChatOfType(t, DB, owner, bot, chatType)
	if metadata == "" {
		return chat
	}

	text := "seed message"
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		DataType:   "text",
		Text:       &text,
		MetaData:   database.JSONRaw(metadata),
	}
	message.CreatedAt = createdAt
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}
	if err := DB.Model(&chat).Update("latest_message_id", message.ID).Error; err != nil {
		t.Fatalf("failed to link latest message: %v", err)
	}
	return chat
}

const (
	finishedMetadata          = `{"finished":true}`
	failedMetadata            = `{"finished":true,"error":true}`
	needsConfirmationMetadata = `{"finished":true,"interaction_confirmation":{"status":"pending"}}`
)

// seedStateFilterChats creates one chat per interaction state, oldest first:
// idle, finished, failed, needs_confirmation. It returns them by state.
func seedStateFilterChats(t *testing.T, DB *gorm.DB, owner, bot *database.User) map[string]database.Chat {
	t.Helper()
	now := time.Now()
	chats := map[string]database.Chat{
		"idle":               seedChatWithLatestMessage(t, DB, owner, bot, "conversation", "", now),
		"finished":           seedChatWithLatestMessage(t, DB, owner, bot, "conversation", finishedMetadata, now.Add(-time.Minute)),
		"failed":             seedChatWithLatestMessage(t, DB, owner, bot, "conversation", failedMetadata, now.Add(-2*time.Minute)),
		"needs_confirmation": seedChatWithLatestMessage(t, DB, owner, bot, "conversation", needsConfirmationMetadata, now.Add(-3*time.Minute)),
	}
	return chats
}

// TestListStateFilterReturnsOnlyMatchingChats guards ci#761: `?state=` must be
// evaluated server-side so the page only carries chats in the requested state,
// and `total` / `total_pages` must describe that filtered subset. Before the
// fix the parameter was ignored and the client filtered the already-paginated
// page, so an old active interaction was unreachable without pressing
// "load more".
func TestListStateFilterReturnsOnlyMatchingChats(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "state-filter-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "state-filter-bot@example.com", false)
	chats := seedStateFilterChats(t, DB, owner, bot)

	rr := stateFilterListRequest(t, DB, owner, "/api/v1/chats/list?page=1&limit=10&state=finished")
	page := decodeListedChats(t, rr)

	if len(page.Rows) != 1 {
		t.Fatalf("expected exactly 1 finished chat, got %d rows", len(page.Rows))
	}
	row := page.Rows[0]
	if row.UUID != chats["finished"].UUID {
		t.Fatalf("expected the finished chat %s, got %s", chats["finished"].UUID, row.UUID)
	}
	if row.State != "finished" {
		t.Fatalf("expected the row to carry state=finished, got %q", row.State)
	}
	if page.Total != 1 || page.TotalPages != 1 {
		t.Fatalf("expected total/total_pages to describe the filtered subset, got total=%d total_pages=%d", page.Total, page.TotalPages)
	}
}

// TestListStateFilterAcceptsMultipleStates guards the comma-separated form:
// several states widen the filter instead of narrowing it to the last value.
func TestListStateFilterAcceptsMultipleStates(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "state-filter-multi-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "state-filter-multi-bot@example.com", false)
	chats := seedStateFilterChats(t, DB, owner, bot)

	rr := stateFilterListRequest(t, DB, owner, "/api/v1/chats/list?page=1&limit=10&state=failed,finished")
	page := decodeListedChats(t, rr)

	if page.Total != 2 {
		t.Fatalf("expected 2 chats for state=failed,finished, got %d", page.Total)
	}
	seen := map[string]string{}
	for _, row := range page.Rows {
		seen[row.UUID] = row.State
	}
	for _, want := range []string{"finished", "failed"} {
		state, ok := seen[chats[want].UUID]
		if !ok {
			t.Fatalf("expected the %s chat in the filtered page", want)
		}
		if state != want {
			t.Fatalf("expected state=%q for the %s chat, got %q", want, want, state)
		}
	}
	if _, ok := seen[chats["needs_confirmation"].UUID]; ok {
		t.Fatal("state filter leaked a needs_confirmation chat")
	}
	if _, ok := seen[chats["idle"].UUID]; ok {
		t.Fatal("state filter leaked an idle chat")
	}
}

// TestListStateFilterPaginatesFilteredSubset guards that pagination applies to
// the state-matched rows (not to the unfiltered page), so page 2 keeps serving
// matching chats.
func TestListStateFilterPaginatesFilteredSubset(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "state-filter-page-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "state-filter-page-bot@example.com", false)
	chats := seedStateFilterChats(t, DB, owner, bot)

	first := decodeListedChats(t, stateFilterListRequest(t, DB, owner, "/api/v1/chats/list?page=1&limit=1&state=failed,finished"))
	if first.Total != 2 || first.TotalPages != 2 {
		t.Fatalf("expected total=2 total_pages=2 over the filtered subset, got total=%d total_pages=%d", first.Total, first.TotalPages)
	}
	if len(first.Rows) != 1 {
		t.Fatalf("expected 1 row on the first page, got %d", len(first.Rows))
	}
	if first.Rows[0].UUID != chats["failed"].UUID {
		t.Fatalf("expected the newest matching chat (failed) first, got %s", first.Rows[0].UUID)
	}

	second := decodeListedChats(t, stateFilterListRequest(t, DB, owner, "/api/v1/chats/list?page=2&limit=1&state=failed,finished"))
	if len(second.Rows) != 1 || second.Rows[0].UUID != chats["finished"].UUID {
		t.Fatalf("expected the second page to serve the remaining filtered chat, got %d rows", len(second.Rows))
	}
}

// TestListStateFilterKeepsMatchingPinVisible guards that the ci#512 pinned
// top-up of the filtered path only carries pins that pass the state filter, so
// a pinned chat in the requested state stays reachable on the first page while
// a pin in another state stays hidden.
func TestListStateFilterKeepsMatchingPinVisible(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "state-filter-pin-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "state-filter-pin-bot@example.com", false)

	now := time.Now()
	pinnedFinished := seedChatWithLatestMessage(t, DB, owner, bot, "conversation", finishedMetadata, now.Add(-10*time.Minute))
	if err := setChatPin(DB, pinnedFinished.ID, owner.ID, true); err != nil {
		t.Fatalf("failed to pin the finished chat: %v", err)
	}
	pinnedIdle := seedChatWithLatestMessage(t, DB, owner, bot, "conversation", "", now.Add(-9*time.Minute))
	if err := setChatPin(DB, pinnedIdle.ID, owner.ID, true); err != nil {
		t.Fatalf("failed to pin the idle chat: %v", err)
	}
	newerFinished := seedChatWithLatestMessage(t, DB, owner, bot, "conversation", finishedMetadata, now.Add(-time.Minute))

	page := decodeListedChats(t, stateFilterListRequest(t, DB, owner, "/api/v1/chats/list?page=1&limit=1&state=finished"))

	pinned := listedChatByUUID(page, pinnedFinished.UUID)
	if pinned == nil {
		t.Fatal("a pinned chat matching the state filter disappeared from the first page (ci#761/ci#512)")
	}
	if !pinned.Pinned {
		t.Fatal("expected the carried-over chat to be flagged pinned")
	}
	if listedChatByUUID(page, newerFinished.UUID) == nil {
		t.Fatal("expected the newest matching chat on the first page")
	}
	if listedChatByUUID(page, pinnedIdle.UUID) != nil {
		t.Fatal("the filtered pinned top-up leaked a chat that does not match the state filter")
	}
	if page.Total != 2 {
		t.Fatalf("expected total to count the matching subset (2), got %d", page.Total)
	}
}

// TestListStateFilterRejectsUnknownState guards the 400 contract: an unknown
// state must not silently return an unfiltered list.
func TestListStateFilterRejectsUnknownState(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "state-filter-invalid-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "state-filter-invalid-bot@example.com", false)
	seedStateFilterChats(t, DB, owner, bot)

	rr := stateFilterListRequest(t, DB, owner, "/api/v1/chats/list?page=1&limit=10&state=exploded")
	if rr.Code != 400 {
		t.Fatalf("expected status 400 for an unknown state, got %d: %s", rr.Code, rr.Body.String())
	}
}
