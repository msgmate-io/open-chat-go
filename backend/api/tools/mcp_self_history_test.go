package tools

import (
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

// Tests for the ci#1103 `openchat_self_history` tool. They pin the authorisation
// predicate of selfHistoryQuery (backend/api/tools/mcp_self_history.go): the
// caller only ever sees interactions it ran for the acting user, or ones that
// were explicitly shared with that acting user. The DB comes from the shared
// helpers in confirmable_actions_uuid_test.go (same package).

func newSelfHistoryAgent(t *testing.T, DB *gorm.DB, name string) *database.User {
	t.Helper()
	user := createUserForToolsTest(t, DB, name)
	if err := DB.Model(user).Update("is_automated", true).Error; err != nil {
		t.Fatalf("mark %q automated: %v", name, err)
	}
	user.IsAutomated = true
	return user
}

func newSelfHistoryInteraction(t *testing.T, DB *gorm.DB, user1, user2 *database.User, tags ...string) *database.Chat {
	t.Helper()
	chat := &database.Chat{
		User1Id:  user1.ID,
		User2Id:  user2.ID,
		ChatType: "interaction",
		Tags:     database.NormalizeChatTags(tags),
	}
	if err := DB.Create(chat).Error; err != nil {
		t.Fatalf("create interaction: %v", err)
	}
	return chat
}

func selfHistoryRowCount(t *testing.T, DB *gorm.DB, self, acting *database.User) int {
	t.Helper()
	chats := []database.Chat{}
	if err := selfHistoryQuery(DB, self, acting, map[string]interface{}{}).Find(&chats).Error; err != nil {
		t.Fatalf("self history query: %v", err)
	}
	return len(chats)
}

// TestSelfHistoryOwnInteractionVisible is the happy path: the agent's own
// interaction with its acting user is returned.
func TestSelfHistoryOwnInteractionVisible(t *testing.T) {
	DB := setupToolsTestDB(t)
	agent := newSelfHistoryAgent(t, DB, "agent-bot")
	owner := createUserForToolsTest(t, DB, "owner")
	newSelfHistoryInteraction(t, DB, agent, owner, "repo:open-chat-go")

	if got := selfHistoryRowCount(t, DB, agent, owner); got != 1 {
		t.Fatalf("expected 1 own interaction, got %d", got)
	}
}

// TestSelfHistoryOtherUserInteractionRefused is the authz guard: a third
// party's interaction stays invisible, and the agent's own interaction does not
// leak to a different acting user.
func TestSelfHistoryOtherUserInteractionRefused(t *testing.T) {
	DB := setupToolsTestDB(t)
	agent := newSelfHistoryAgent(t, DB, "agent-bot")
	owner := createUserForToolsTest(t, DB, "owner")
	strangerA := createUserForToolsTest(t, DB, "stranger-a")
	strangerB := createUserForToolsTest(t, DB, "stranger-b")

	newSelfHistoryInteraction(t, DB, agent, owner)
	newSelfHistoryInteraction(t, DB, strangerA, strangerB)

	if got := selfHistoryRowCount(t, DB, agent, owner); got != 1 {
		t.Fatalf("expected only the agent's own interaction, got %d", got)
	}
	// Same agent, different acting user: the participant pair no longer
	// matches, so the agent's chat with "owner" must not leak.
	if got := selfHistoryRowCount(t, DB, agent, strangerA); got != 0 {
		t.Fatalf("expected 0 rows for a foreign acting user, got %d", got)
	}
}

// TestSelfHistoryExplicitShareVisible keeps the one legitimate widening: a chat
// the agent takes part in that was explicitly shared with the acting user.
func TestSelfHistoryExplicitShareVisible(t *testing.T) {
	DB := setupToolsTestDB(t)
	agent := newSelfHistoryAgent(t, DB, "agent-bot")
	owner := createUserForToolsTest(t, DB, "owner")
	other := createUserForToolsTest(t, DB, "other-user")

	shared := newSelfHistoryInteraction(t, DB, agent, other)
	share := &database.SharedChatInstance{
		ChatId:        shared.ID,
		OwningUserId:  agent.ID,
		ChatShareUUID: "share-self-history-test",
		AccessMode:    "user",
		TargetUserId:  &owner.ID,
	}
	if err := DB.Create(share).Error; err != nil {
		t.Fatalf("create share: %v", err)
	}

	if got := selfHistoryRowCount(t, DB, agent, owner); got != 1 {
		t.Fatalf("expected the explicitly shared interaction, got %d", got)
	}
}

// TestSelfHistoryEmptyHistory covers the no-history case.
func TestSelfHistoryEmptyHistory(t *testing.T) {
	DB := setupToolsTestDB(t)
	agent := newSelfHistoryAgent(t, DB, "fresh-agent")
	owner := createUserForToolsTest(t, DB, "owner")

	if got := selfHistoryRowCount(t, DB, agent, owner); got != 0 {
		t.Fatalf("expected empty history, got %d rows", got)
	}
}

// TestSelfHistoryRejectsNonAutomatedCaller keeps the tool agent-only.
func TestSelfHistoryRejectsNonAutomatedCaller(t *testing.T) {
	DB := setupToolsTestDB(t)
	human := createUserForToolsTest(t, DB, "human")

	if _, _, err := selfHistoryActors(DB, human); err == nil {
		t.Fatal("expected a non-automated caller to be refused")
	}
}
