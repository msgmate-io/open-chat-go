package chats

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

func decodeListedChats(t *testing.T, rr *httptest.ResponseRecorder) ListedChatsPage {
	t.Helper()
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var page ListedChatsPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	return page
}

func listedChatByUUID(page ListedChatsPage, uuid string) *ListedChat {
	for i := range page.Rows {
		if page.Rows[i].UUID == uuid {
			return &page.Rows[i]
		}
	}
	return nil
}

// TestListIncludesPinnedChatAgedOutOfFirstPage guards ci#512: a pinned chat
// that has become so old it is no longer among the most recent rows must still
// be returned on the first page as a pin, instead of silently disappearing.
func TestListIncludesPinnedChatAgedOutOfFirstPage(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-pin-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-pin-bot@example.com", false)

	// The pinned chat is the oldest row; newer chats push it out of the page.
	oldest := createChatOfType(t, DB, owner, bot, "conversation")
	if err := setChatPin(DB, oldest.ID, owner.ID, true); err != nil {
		t.Fatalf("failed to pin the oldest chat: %v", err)
	}
	for i := 0; i < 3; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	req := httptest.NewRequest("GET", "/api/v1/chats/list?page=1&limit=2", nil)
	req = withActionTasksUser(req, DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)

	page := decodeListedChats(t, rr)
	pinned := listedChatByUUID(page, oldest.UUID)
	if pinned == nil {
		t.Fatal("pinned chat aged out of the first page and disappeared (ci#512)")
	}
	if !pinned.Pinned {
		t.Fatal("expected the aged-out pinned chat to be flagged pinned")
	}

	occurrences := 0
	for _, row := range page.Rows {
		if row.UUID == oldest.UUID {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("expected the pinned chat exactly once, got %d", occurrences)
	}
}

// TestListPinnedTopUpRespectsActiveFilters guards that the pinned top-up does
// not surface a pinned chat that the active server-side filters exclude, so a
// filtered list stays filtered.
func TestListPinnedTopUpRespectsActiveFilters(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-filter-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-filter-bot@example.com", false)

	pinnedConversation := createChatOfType(t, DB, owner, bot, "conversation")
	if err := setChatPin(DB, pinnedConversation.ID, owner.ID, true); err != nil {
		t.Fatalf("failed to pin the conversation chat: %v", err)
	}
	for i := 0; i < 3; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	req := httptest.NewRequest("GET", "/api/v1/chats/list?page=1&limit=2&chat_types=interaction", nil)
	req = withActionTasksUser(req, DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)

	page := decodeListedChats(t, rr)
	if listedChatByUUID(page, pinnedConversation.UUID) != nil {
		t.Fatal("pinned top-up leaked a chat excluded by the active chat_types filter")
	}
}

// TestListPinnedChatAlreadyInPageNotDuplicated guards that a pinned chat that
// is already part of the page is not returned twice by the top-up.
func TestListPinnedChatAlreadyInPageNotDuplicated(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-dedupe-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-dedupe-bot@example.com", false)

	newest := createChatOfType(t, DB, owner, bot, "conversation")
	if err := setChatPin(DB, newest.ID, owner.ID, true); err != nil {
		t.Fatalf("failed to pin the newest chat: %v", err)
	}
	for i := 0; i < 2; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	req := httptest.NewRequest("GET", "/api/v1/chats/list?page=1&limit=40", nil)
	req = withActionTasksUser(req, DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)

	page := decodeListedChats(t, rr)
	occurrences := 0
	for _, row := range page.Rows {
		if row.UUID == newest.UUID {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("expected the in-page pinned chat exactly once, got %d", occurrences)
	}
	if row := listedChatByUUID(page, newest.UUID); row == nil || !row.Pinned {
		t.Fatal("expected the in-page pinned chat to be flagged pinned")
	}
}

// TestListReportsTotalMatchingChats guards ci#579: the list response carries the
// total number of chats matching the active filters, so the frontend can decide
// whether older chats remain to fetch without guessing from the page size.
func TestListReportsTotalMatchingChats(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-total-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-total-bot@example.com", false)

	const total = 5
	for i := 0; i < total; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	req := httptest.NewRequest("GET", "/api/v1/chats/list?page=1&limit=2", nil)
	req = withActionTasksUser(req, DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)

	page := decodeListedChats(t, rr)
	if page.Total != total {
		t.Fatalf("expected total=%d, got %d", total, page.Total)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("expected the page to honor limit=2, got %d rows", len(page.Rows))
	}
}

// TestListPinnedTopUpNotCappedByPageSize guards the ci#512 review: the pinned
// top-up must return EVERY matching pin, not just one page's worth. It used to
// reuse the already-paginated query (GORM mutates the statement in place when
// the pagination scope applies LIMIT/OFFSET), so a user with more pins than the
// page size silently lost the older ones.
func TestListPinnedTopUpNotCappedByPageSize(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-pin-cap-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-pin-cap-bot@example.com", false)

	const pinnedCount = 5
	pinnedUUIDs := make([]string, 0, pinnedCount)
	for i := 0; i < pinnedCount; i++ {
		chat := createChatOfType(t, DB, owner, bot, "conversation")
		if err := setChatPin(DB, chat.ID, owner.ID, true); err != nil {
			t.Fatalf("failed to pin chat: %v", err)
		}
		pinnedUUIDs = append(pinnedUUIDs, chat.UUID)
	}
	// Newer unpinned chats push every pin off the first page.
	for i := 0; i < 4; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	req := httptest.NewRequest("GET", "/api/v1/chats/list?page=1&limit=2", nil)
	req = withActionTasksUser(req, DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)

	page := decodeListedChats(t, rr)
	if len(page.Rows) < pinnedCount {
		t.Fatalf("expected at least the %d pinned chats on page 1, got %d rows", pinnedCount, len(page.Rows))
	}
	for _, uuid := range pinnedUUIDs {
		row := listedChatByUUID(page, uuid)
		if row == nil {
			t.Fatalf("pinned chat %s disappeared from the first page despite limit=2 (ci#512 top-up cap)", uuid)
		}
		if !row.Pinned {
			t.Fatalf("expected pinned chat %s to be flagged pinned", uuid)
		}
	}
}

// TestListCarriesPinsInFirstSmallBatch guards the ci#579 increment that fetches
// the chat list in small incremental batches (limit=5, newest first): the pins
// must travel with the batch the sidebar paints first, and the pinned top-up
// must stay outside the offset windows of the later batches. Otherwise the
// frontend's page-merge would either hide a pin or have to resolve overlapping
// rows for no reason.
func TestListCarriesPinsInFirstSmallBatch(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-pin-batch-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-pin-batch-bot@example.com", false)

	const pinnedCount = 3
	pinnedUUIDs := make([]string, 0, pinnedCount)
	for i := 0; i < pinnedCount; i++ {
		chat := createChatOfType(t, DB, owner, bot, "conversation")
		if err := setChatPin(DB, chat.ID, owner.ID, true); err != nil {
			t.Fatalf("failed to pin chat: %v", err)
		}
		pinnedUUIDs = append(pinnedUUIDs, chat.UUID)
	}
	// Enough newer chats that a limit=5 batch is fully occupied by non-pins.
	for i := 0; i < 8; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	requestBatch := func(page int) ListedChatsPage {
		req := httptest.NewRequest("GET", "/api/v1/chats/list?page="+strconv.Itoa(page)+"&limit=5", nil)
		req = withActionTasksUser(req, DB, owner)
		rr := httptest.NewRecorder()
		h := &ChatsHandler{}
		h.List(rr, req)
		return decodeListedChats(t, rr)
	}

	// The first batch is the one the sidebar paints first: it must carry every
	// pin, or a pinned chat would be invisible until the user loads more.
	first := requestBatch(1)
	for _, uuid := range pinnedUUIDs {
		row := listedChatByUUID(first, uuid)
		if row == nil {
			t.Fatalf("pinned chat %s missing from the first batch of a ci#579 incremental fetch", uuid)
		}
		if !row.Pinned {
			t.Fatalf("expected pinned chat %s to be flagged pinned in the first batch", uuid)
		}
	}
	if first.Total < pinnedCount {
		t.Fatalf("expected total to include the pins, got %d", first.Total)
	}

	// A pin must never be duplicated inside one batch, otherwise the merged list
	// would render the same chat twice.
	seenInFirst := map[string]int{}
	for _, row := range first.Rows {
		seenInFirst[row.UUID]++
	}
	for _, uuid := range pinnedUUIDs {
		if seenInFirst[uuid] != 1 {
			t.Fatalf("expected pinned chat %s exactly once in the first batch, got %d", uuid, seenInFirst[uuid])
		}
	}

	// Deeper batches are plain offset windows of the ordered history. They must
	// stay consistent (each chat once) so merging the batches cannot drop or
	// duplicate a row; the pins themselves only ride along with batch one.
	second := requestBatch(2)
	seenInSecond := map[string]int{}
	for _, row := range second.Rows {
		seenInSecond[row.UUID]++
	}
	for uuid, count := range seenInSecond {
		if count != 1 {
			t.Fatalf("expected chat %s exactly once in batch 2, got %d", uuid, count)
		}
	}
	if second.Total != first.Total {
		t.Fatalf("expected a stable total across batches, got %d then %d", first.Total, second.Total)
	}
	if len(second.Rows) == 0 {
		t.Fatal("expected batch 2 to still carry older chats")
	}
}
