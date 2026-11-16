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

// ci#1026 (Phase 1): the known coder harness bots are exempt from the
// consecutive-automated rule. 15 consecutive turns from
// msgmate-coder-implement-bot must never be refused, even though 15 is past
// MaxConsecutiveAutomated (12).
func TestLoopBreaker_ExemptsCoderHarness(t *testing.T) {
	now := time.Now()
	cfg := DefaultConfig()
	harness := uint(7)
	automated := map[uint]bool{human: false, harness: true}
	names := map[uint]string{harness: "msgmate-coder-implement-bot"}

	messages := make([]database.Message, 0, 15)
	for i := 0; i < 15; i++ {
		// Non-empty, distinct text, well outside the rapid window so only the
		// consecutive rule could fire.
		messages = append(messages, msg(harness, fmt.Sprintf("phase 1 step %d", i), now.Add(-time.Duration(10+i)*time.Minute)))
	}
	if decision := DetectWithNames(messages, automated, names, cfg, now); decision.Loop {
		t.Fatalf("a coder harness run must never be refused: %+v", decision)
	}
}

// The exemption is name-based, not automation-based: the very same shape of run
// from an arbitrary automated peer is still a no-progress loop. In-round
// harness turns are not always text-bearing, so this is the case the plain
// progress heuristic alone used to miss.
func TestLoopBreaker_HarnessExemptionIsNameBased(t *testing.T) {
	now := time.Now()
	cfg := DefaultConfig()
	peer := uint(8)
	automated := map[uint]bool{human: false, peer: true}

	messages := make([]database.Message, 0, cfg.MaxConsecutiveAutomated+3)
	for i := 0; i < cfg.MaxConsecutiveAutomated+3; i++ {
		messages = append(messages, msg(peer, "", now.Add(-time.Duration(10+i)*time.Minute)))
	}

	// Not a harness bot: the long empty automated dump is a loop, as before.
	if decision := Detect(messages, automated, cfg, now); !decision.Loop || decision.Reason != "consecutive_automated" {
		t.Fatalf("an anonymous single-sender empty dump is still a loop, got %+v", decision)
	}

	// The same dump from a harness bot is exempt.
	names := map[uint]string{peer: "msgmate-coder-implement-bot"}
	if decision := DetectWithNames(messages, automated, names, cfg, now); decision.Loop {
		t.Fatalf("the same run from a coder harness must be exempt: %+v", decision)
	}

	// Detect must keep behaving exactly like an unnamed DetectWithNames.
	if got, want := Detect(messages, automated, cfg, now), DetectWithNames(messages, automated, nil, cfg, now); got != want {
		t.Fatalf("Detect and DetectWithNames(nil names) diverged: %+v vs %+v", got, want)
	}
}

// A real harness spin (two automated senders alternating) still trips: the
// exemption widens tolerance for a single working sender, it does not disable
// the breaker.
func TestLoopBreaker_HarnessAlternationStillTrips(t *testing.T) {
	now := time.Now()
	messages := make([]database.Message, 0, 14)
	for i := 0; i < 14; i++ {
		sender := botA
		if i%2 == 1 {
			sender = botB
		}
		messages = append(messages, msg(sender, fmt.Sprintf("ping %d", i), now.Add(-time.Duration(10+i)*time.Minute)))
	}
	names := map[uint]string{botA: "msgmate-coder-implement-bot", botB: "msgmate-coder-repl-bot"}
	decision := DetectWithNames(messages, automation(), names, DefaultConfig(), now)
	if !decision.Loop || decision.Reason != "consecutive_automated" {
		t.Fatalf("harness bots alternating is still a loop, got %+v", decision)
	}
}

// DetectForChat resolves the sender's bot-runtime name from the user record, so
// the exemption works on the production path (the bot-reply task).
func TestDetectForChatExemptsCoderHarnessSender(t *testing.T) {
	DB := database.SetupTestDatabase(t, database.DBConfig{
		Backend:  "sqlite",
		FilePath: t.TempDir() + "/botloop_harness_test.db",
		Debug:    false,
		ResetDB:  true,
	})
	humanUser := database.User{Name: "h", Username: "h", Email: "h@example.com", IsAutomated: false}
	harnessUser := database.User{Name: "msgmate-coder-implement-bot", Username: "msgmate-coder-implement-bot", Email: "c@example.com", IsAutomated: true}
	if err := DB.Create(&humanUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&harnessUser).Error; err != nil {
		t.Fatal(err)
	}
	chat := database.Chat{User1Id: humanUser.ID, User2Id: harnessUser.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 15; i++ {
		// Spaced out and textless, so neither the rapid nor the identical rule
		// can hide a consecutive-rule regression.
		at := now.Add(-time.Duration(10+i) * time.Minute)
		if err := DB.Create(&database.Message{Model: database.Model{CreatedAt: at}, ChatId: chat.ID, SenderId: harnessUser.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if decision := DetectForChat(DB, chat.ID, DefaultConfig(), now); decision.Loop {
		t.Fatalf("DetectForChat must exempt the coder harness sender: %+v", decision)
	}
}
