package chats

import (
	"encoding/json"
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

// seedChatWithTagsAndState creates a chat carrying tags whose newest message
// resolves it to a state (empty metadata models a chat with no agent outcome,
// which resolves to idle). chat_types/tags/default metadata for the newest
// message are all covered so the combined-filter tests can seed one row that
// differs from its siblings in exactly one dimension.
func seedChatWithTagsAndState(
	t *testing.T,
	DB *gorm.DB,
	owner *database.User,
	bot *database.User,
	chatType string,
	tags database.StringSliceJSON,
	metadata string,
	createdAt time.Time,
) database.Chat {
	t.Helper()
	chat := database.Chat{
		User1Id:  owner.ID,
		User2Id:  bot.ID,
		ChatType: chatType,
		Tags:     tags,
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create tagged chat: %v", err)
	}
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

// TestListStateFilterCombinesWithOtherFilters guards that `state` narrows the
// result set together with the sibling filters instead of replacing them. The
// state-filtered path builds its candidates through the same newFilteredQuery
// closure as the unfiltered page, so tags / chat_types / q must survive the
// detour through the state resolver (ci#761).
func TestListStateFilterCombinesWithOtherFilters(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "state-filter-combo-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "state-filter-combo-bot@example.com", false)

	now := time.Now()
	// Matching on every axis: finished + tag "deploy" + interaction chat type.
	matching := seedChatWithTagsAndState(t, DB, owner, bot, "interaction:deploy", database.StringSliceJSON{"deploy", "repo:open-chat-go"}, finishedMetadata, now)
	// Same tags and type, but idle: must be dropped by the state filter.
	seedChatWithTagsAndState(t, DB, owner, bot, "interaction:deploy", database.StringSliceJSON{"deploy"}, "", now.Add(-time.Minute))
	// Same state, different tag: must be dropped by the tags filter.
	seedChatWithTagsAndState(t, DB, owner, bot, "interaction:deploy", database.StringSliceJSON{"chat"}, finishedMetadata, now.Add(-2*time.Minute))
	// Same state and tags, different chat type: must be dropped by chat_types.
	seedChatWithTagsAndState(t, DB, owner, bot, "conversation", database.StringSliceJSON{"deploy"}, finishedMetadata, now.Add(-3*time.Minute))

	page := decodeListedChats(t, stateFilterListRequest(t, DB, owner,
		"/api/v1/chats/list?page=1&limit=10&state=finished&tags=deploy&chat_types=interaction"))

	if len(page.Rows) != 1 {
		t.Fatalf("expected exactly 1 chat matching state+tags+chat_types, got %d rows", len(page.Rows))
	}
	if page.Rows[0].UUID != matching.UUID {
		t.Fatalf("expected the finished deploy interaction %s, got %s", matching.UUID, page.Rows[0].UUID)
	}
	if page.Rows[0].State != "finished" {
		t.Fatalf("expected state=finished on the combined-filter row, got %q", page.Rows[0].State)
	}
	if page.Total != 1 || page.TotalPages != 1 {
		t.Fatalf("expected total/total_pages over the combined filter, got total=%d total_pages=%d", page.Total, page.TotalPages)
	}

	// Dropping the state filter must widen to the two chats that match the
	// remaining filters, proving state is applied on top of them rather than
	// filtering on its own.
	unfiltered := decodeListedChats(t, stateFilterListRequest(t, DB, owner,
		"/api/v1/chats/list?page=1&limit=10&tags=deploy&chat_types=interaction"))
	if unfiltered.Total != 2 {
		t.Fatalf("expected tags+chat_types alone to match 2 chats, got %d", unfiltered.Total)
	}
}

// TestListStateFilterCombinesWithSearch guards the `q` (title search) sibling of
// the state filter: titles live in the chat settings blob and are matched by a
// subquery built inside newFilteredQuery, so the state-filtered path must keep
// that constraint as well (ci#761).
func TestListStateFilterCombinesWithSearch(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "state-filter-search-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "state-filter-search-bot@example.com", false)

	now := time.Now()
	titled := seedChatWithTagsAndState(t, DB, owner, bot, "interaction:deploy", nil, failedMetadata, now)
	settings := database.ChatSettings{
		ChatId:     titled.ID,
		ConfigData: json.RawMessage(`{"title":"Deploy pipeline fix"}`),
	}
	if err := DB.Create(&settings).Error; err != nil {
		t.Fatalf("failed to create chat settings: %v", err)
	}
	// Fails the same state filter but carries another title, so a broken q
	// constraint would let it through.
	other := seedChatWithTagsAndState(t, DB, owner, bot, "interaction:deploy", nil, failedMetadata, now.Add(-time.Minute))
	if err := DB.Create(&database.ChatSettings{ChatId: other.ID, ConfigData: json.RawMessage(`{"title":"Unrelated chat"}`)}).Error; err != nil {
		t.Fatalf("failed to create chat settings: %v", err)
	}
	// Matches the search term but resolves to idle, so a broken state
	// constraint would let it through.
	idleTitled := seedChatWithTagsAndState(t, DB, owner, bot, "interaction:deploy", nil, "", now.Add(-2*time.Minute))
	if err := DB.Create(&database.ChatSettings{ChatId: idleTitled.ID, ConfigData: json.RawMessage(`{"title":"Deploy notes"}`)}).Error; err != nil {
		t.Fatalf("failed to create chat settings: %v", err)
	}

	page := decodeListedChats(t, stateFilterListRequest(t, DB, owner,
		"/api/v1/chats/list?page=1&limit=10&state=failed&q=Deploy"))

	if page.Total != 1 || len(page.Rows) != 1 {
		t.Fatalf("expected exactly the titled failed chat for state+q, got total=%d rows=%d", page.Total, len(page.Rows))
	}
	if page.Rows[0].UUID != titled.UUID {
		t.Fatalf("expected the q-matching chat %s, got %s", titled.UUID, page.Rows[0].UUID)
	}
}

// TestListStateFilterAgreesWithUnfilteredRowStates guards the inconsistency the
// ci#761 report was actually about: the sidebar badge comes from the resolved
// state on the unfiltered page while reachability comes from `?state=`, so both
// must agree. Every chat the unfiltered page resolves to a state must be
// returned by `state=<that state>` and vice versa.
func TestListStateFilterAgreesWithUnfilteredRowStates(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "state-filter-agree-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "state-filter-agree-bot@example.com", false)
	seedStateFilterChats(t, DB, owner, bot)

	all := decodeListedChats(t, stateFilterListRequest(t, DB, owner, "/api/v1/chats/list?page=1&limit=10"))
	if all.Total != 4 || len(all.Rows) != 4 {
		t.Fatalf("expected the unfiltered page to carry all 4 seeded chats, got total=%d rows=%d", all.Total, len(all.Rows))
	}

	byState := map[string]map[string]bool{}
	for _, row := range all.Rows {
		if row.State == "" {
			t.Fatalf("unfiltered row %s carries no resolved state", row.UUID)
		}
		if byState[row.State] == nil {
			byState[row.State] = map[string]bool{}
		}
		byState[row.State][row.UUID] = true
	}
	for _, state := range []string{"idle", "finished", "failed", "needs_confirmation"} {
		if len(byState[state]) != 1 {
			t.Fatalf("expected exactly 1 chat resolving to %s on the unfiltered page, got %d", state, len(byState[state]))
		}
	}

	for state, want := range byState {
		page := decodeListedChats(t, stateFilterListRequest(t, DB, owner,
			"/api/v1/chats/list?page=1&limit=10&state="+state))
		if page.Total != len(want) || len(page.Rows) != len(want) {
			t.Fatalf("state=%s returned total=%d rows=%d, but the unfiltered page resolved %d chats to it", state, page.Total, len(page.Rows), len(want))
		}
		got := map[string]bool{}
		for _, row := range page.Rows {
			if row.State != state {
				t.Fatalf("row %s returned for state=%s reports state=%s", row.UUID, state, row.State)
			}
			got[row.UUID] = true
		}
		for uuid := range want {
			if !got[uuid] {
				t.Fatalf("chat %s resolves to %s on the unfiltered page but is missing from state=%s", uuid, state, state)
			}
		}
	}
}
