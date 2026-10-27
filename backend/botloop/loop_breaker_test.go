package botloop

import (
	"fmt"
	"testing"
	"time"

	"backend/database"
)

const (
	human = uint(1)
	botA  = uint(2)
	botB  = uint(3)
)

func msg(sender uint, text string, at time.Time) database.Message {
	body := text
	return database.Message{Model: database.Model{CreatedAt: at}, SenderId: sender, Text: &body}
}

func automation() map[uint]bool { return map[uint]bool{human: false, botA: true, botB: true} }

func TestDetectAllowsNormalHumanBotConversation(t *testing.T) {
	now := time.Now()
	messages := []database.Message{
		msg(botA, "done", now),
		msg(human, "thanks", now.Add(-time.Minute)),
		msg(botA, "working on it", now.Add(-2*time.Minute)),
		msg(human, "please fix the test", now.Add(-3*time.Minute)),
	}
	if decision := Detect(messages, automation(), DefaultConfig(), now); decision.Loop {
		t.Fatalf("a human/bot conversation must not trip the breaker: %+v", decision)
	}
}

func TestDetectFlagsConsecutiveAutomatedRun(t *testing.T) {
	now := time.Now()
	messages := make([]database.Message, 0, 14)
	for i := 0; i < 14; i++ {
		sender := botA
		if i%2 == 1 {
			sender = botB
		}
		messages = append(messages, msg(sender, fmt.Sprintf("turn %d", i), now.Add(-time.Duration(i)*time.Second)))
	}
	decision := Detect(messages, automation(), DefaultConfig(), now)
	if !decision.Loop || decision.Reason != "consecutive_automated" {
		t.Fatalf("expected consecutive_automated loop, got %+v", decision)
	}
}

func TestDetectFlagsIdenticalMessages(t *testing.T) {
	now := time.Now()
	messages := []database.Message{
		msg(botB, "I paused this response.", now),
		msg(botA, "I paused this response.", now.Add(-time.Second)),
		msg(botB, "  i paused THIS response.  ", now.Add(-2*time.Second)),
	}
	decision := Detect(messages, automation(), DefaultConfig(), now)
	if !decision.Loop || decision.Reason != "identical_messages" {
		t.Fatalf("expected identical_messages loop, got %+v", decision)
	}
}

func TestDetectFlagsRapidTurns(t *testing.T) {
	now := time.Now()
	cfg := DefaultConfig()
	messages := make([]database.Message, 0, cfg.RapidCount)
	for i := 0; i < cfg.RapidCount; i++ {
		// Distinct text so only the burst rule can fire.
		messages = append(messages, msg(botA, fmt.Sprintf("distinct %d", i), now.Add(-time.Duration(i)*time.Second)))
	}
	decision := Detect(messages, automation(), cfg, now)
	if !decision.Loop || decision.Reason != "rapid_turns" {
		t.Fatalf("expected rapid_turns loop, got %+v", decision)
	}
}

func TestDetectIgnoresSlowDistinctAutomatedCoordination(t *testing.T) {
	now := time.Now()
	// 10 automated turns, distinct content, several minutes apart: healthy
	// coordination, below the consecutive and burst thresholds.
	messages := make([]database.Message, 0, 10)
	for i := 0; i < 10; i++ {
		messages = append(messages, msg(botA, fmt.Sprintf("status %d", i), now.Add(-time.Duration(i)*5*time.Minute)))
	}
	if decision := Detect(messages, automation(), DefaultConfig(), now); decision.Loop {
		t.Fatalf("slow distinct coordination must not trip the breaker: %+v", decision)
	}
}

func TestDetectForChatLoadsAndDetects(t *testing.T) {
	DB := database.SetupTestDatabase(t, database.DBConfig{
		Backend:  "sqlite",
		FilePath: t.TempDir() + "/botloop_test.db",
		Debug:    false,
		ResetDB:  true,
	})
	humanUser := database.User{Name: "h", Username: "h", Email: "h@example.com", IsAutomated: false}
	botUser := database.User{Name: "b", Username: "b", Email: "b@example.com", IsAutomated: true}
	if err := DB.Create(&humanUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&botUser).Error; err != nil {
		t.Fatal(err)
	}
	chat := database.Chat{User1Id: humanUser.ID, User2Id: botUser.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 14; i++ {
		text := fmt.Sprintf("loop %d", i)
		if err := DB.Create(&database.Message{Model: database.Model{CreatedAt: now.Add(-time.Duration(i) * time.Second)}, ChatId: chat.ID, SenderId: botUser.ID, Text: &text}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if decision := DetectForChat(DB, chat.ID, DefaultConfig(), now); !decision.Loop {
		t.Fatalf("expected DetectForChat to flag the loop, got %+v", decision)
	}
	// A chat with no bot-only run is not flagged.
	calm := database.Chat{User1Id: humanUser.ID, User2Id: botUser.ID, ChatType: "interaction"}
	if err := DB.Create(&calm).Error; err != nil {
		t.Fatal(err)
	}
	if decision := DetectForChat(DB, calm.ID, DefaultConfig(), now); decision.Loop {
		t.Fatalf("empty calm chat must not be flagged: %+v", decision)
	}
}
