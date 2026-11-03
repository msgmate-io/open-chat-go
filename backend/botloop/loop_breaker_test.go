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

// ci#915: a single automated sender that is still making progress (an ongoing
// coder/harness tool round) must never be refused by the consecutive-automated
// rule, even when the run is long. Bot-to-bot spins are two-sender alternations
// (covered above) or empty-text dumps (this test), and both still trip.
func TestDetectAllowsLongSingleSenderAutomatedProgress(t *testing.T) {
	now := time.Now()
	cfg := DefaultConfig()
	messages := make([]database.Message, 0, cfg.MaxConsecutiveAutomated+2)
	for i := 0; i < cfg.MaxConsecutiveAutomated+2; i++ {
		// A coder harness round: distinct tool-result content each step, all
		// from the same bot.
		messages = append(messages, msg(botA, fmt.Sprintf("step %d done", i), now.Add(-time.Duration(i)*time.Second)))
	}
	// The rapid window is well past, so only the consecutive rule could fire.
	for i := range messages {
		messages[i].CreatedAt = now.Add(-time.Duration(10+i) * time.Minute)
	}
	if decision := Detect(messages, automation(), cfg, now); decision.Loop {
		t.Fatalf("a long single-sender run with progress must not be refused: %+v", decision)
	}
}

func TestDetectFlagsSingleSenderEmptyAutomatedDump(t *testing.T) {
	now := time.Now()
	cfg := DefaultConfig()
	messages := make([]database.Message, 0, cfg.MaxConsecutiveAutomated+2)
	for i := 0; i < cfg.MaxConsecutiveAutomated+2; i++ {
		messages = append(messages, msg(botA, "", now.Add(-time.Duration(10+i)*time.Minute)))
	}
	if decision := Detect(messages, automation(), cfg, now); !decision.Loop || decision.Reason != "consecutive_automated" {
		t.Fatalf("a single-sender empty dump is a no-progress loop, got %+v", decision)
	}
}

func TestDetectAllowsSingleSenderWithHumanParticipantEvenPastThreshold(t *testing.T) {
	now := time.Now()
	messages := []database.Message{
		msg(botA, "still working", now),
		msg(human, "ok", now.Add(-time.Minute)),
		msg(botA, "checking", now.Add(-2*time.Minute)),
		msg(human, "thanks", now.Add(-3*time.Minute)),
	}
	if decision := Detect(messages, automation(), DefaultConfig(), now); decision.Loop {
		t.Fatalf("a human-in-the-loop run must never trip the breaker: %+v", decision)
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
