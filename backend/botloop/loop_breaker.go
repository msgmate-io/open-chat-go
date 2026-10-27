package botloop

import (
	"os"
	"strconv"
	"strings"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// This package implements the bot-to-bot loop breaker (ci#747 RC-8). Two
// automated participants can get stuck replying to each other: each reply
// re-enqueues the peer's reply, so the chat spins, burning provider tokens and
// occupying async session slots (observed on ci.msgmate.io: 265-result rounds
// and thousands of queued bot-reply tasks from ~6 chats).
//
// The breaker runs *before* a bot reply is generated and refuses to add another
// turn to a chat that already looks like a tight loop. It is deliberately
// conservative: it only fires for an automated peer (bot-to-bot) and only on a
// strong signal (a long all-automated run, a run of identical messages, or a
// burst of turns inside a short window), so legitimate slow agent-to-agent
// coordination is untouched.

// Config are the loop-breaker thresholds. All are "0 disables the check".
type Config struct {
	// MaxConsecutiveAutomated is the number of trailing messages, with no human
	// participant in between, above which the chat is a bot loop. Answering is
	// refused at or above this count.
	MaxConsecutiveAutomated int
	// MaxIdenticalRun is the number of trailing messages whose normalised text is
	// identical (non-empty) above which the chat is a no-progress loop.
	MaxIdenticalRun int
	// RapidWindowSeconds / RapidCount flag a burst: RapidCount trailing messages
	// all within RapidWindowSeconds of now.
	RapidWindowSeconds int
	RapidCount         int
}

// DefaultConfig is the compiled-in policy. The thresholds are high enough that
// normal agent-to-agent coordination (a handful of turns, distinct content,
// minutes apart) never trips them, but a spin loop (dozens of turns seconds
// apart, identical/empty text) does.
func DefaultConfig() Config {
	return Config{
		MaxConsecutiveAutomated: 12,
		MaxIdenticalRun:         3,
		RapidWindowSeconds:      60,
		RapidCount:              10,
	}
}

// ConfigFromEnv overrides the defaults from OCI_BOT_LOOP_* variables.
func ConfigFromEnv() Config {
	cfg := DefaultConfig()
	if v := envInt("OCI_BOT_LOOP_MAX_CONSECUTIVE"); v >= 0 {
		cfg.MaxConsecutiveAutomated = v
	}
	if v := envInt("OCI_BOT_LOOP_MAX_IDENTICAL"); v >= 0 {
		cfg.MaxIdenticalRun = v
	}
	if v := envInt("OCI_BOT_LOOP_RAPID_WINDOW_SECONDS"); v >= 0 {
		cfg.RapidWindowSeconds = v
	}
	if v := envInt("OCI_BOT_LOOP_RAPID_COUNT"); v >= 0 {
		cfg.RapidCount = v
	}
	return cfg
}

func envInt(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return -1
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return -1
	}
	return value
}

// Decision is the breaker's verdict for one prospective reply.
type Decision struct {
	Loop   bool
	Reason string
	Detail string
}

// Detect inspects the trailing messages of a chat (newest first) and reports
// whether answering would extend a bot-to-bot loop.
func Detect(messages []database.Message, automated map[uint]bool, cfg Config, now time.Time) Decision {
	if len(messages) == 0 {
		return Decision{}
	}

	// 1. A long trailing run of automated senders with no human in between.
	if cfg.MaxConsecutiveAutomated > 0 {
		consecutive := 0
		for _, message := range messages {
			if automated[message.SenderId] {
				consecutive++
				continue
			}
			break
		}
		if consecutive >= cfg.MaxConsecutiveAutomated {
			return Decision{
				Loop:   true,
				Reason: "consecutive_automated",
				Detail: strconv.Itoa(consecutive) + " consecutive automated messages",
			}
		}
	}

	// 2. A run of identical (non-empty) trailing messages: no progress.
	if cfg.MaxIdenticalRun > 0 {
		newest := normalizedText(messages[0])
		if newest != "" {
			run := 1
			for i := 1; i < len(messages); i++ {
				if normalizedText(messages[i]) != newest {
					break
				}
				run++
			}
			if run >= cfg.MaxIdenticalRun {
				return Decision{
					Loop:   true,
					Reason: "identical_messages",
					Detail: strconv.Itoa(run) + " identical consecutive messages",
				}
			}
		}
	}

	// 3. A burst: many trailing turns inside a short window.
	if cfg.RapidWindowSeconds > 0 && cfg.RapidCount > 0 {
		cutoff := now.Add(-time.Duration(cfg.RapidWindowSeconds) * time.Second)
		count := 0
		for _, message := range messages {
			if message.CreatedAt.After(cutoff) {
				count++
				continue
			}
			break
		}
		if count >= cfg.RapidCount {
			return Decision{
				Loop:   true,
				Reason: "rapid_turns",
				Detail: strconv.Itoa(count) + " turns within " + strconv.Itoa(cfg.RapidWindowSeconds) + "s",
			}
		}
	}

	return Decision{}
}

// DetectForChat loads the trailing messages of a chat plus the automation flag
// of each sender and runs Detect. It is the convenience entry point for the
// bot-reply task.
func DetectForChat(db *gorm.DB, chatID uint, cfg Config, now time.Time) Decision {
	if db == nil || chatID == 0 {
		return Decision{}
	}
	var messages []database.Message
	if err := db.Where("chat_id = ?", chatID).Order("created_at desc").Limit(40).Find(&messages).Error; err != nil {
		return Decision{}
	}
	if len(messages) == 0 {
		return Decision{}
	}

	seen := map[uint]struct{}{}
	ids := make([]uint, 0, len(messages))
	for _, message := range messages {
		if _, ok := seen[message.SenderId]; ok {
			continue
		}
		seen[message.SenderId] = struct{}{}
		ids = append(ids, message.SenderId)
	}
	var users []database.User
	if err := db.Where("id IN ?", ids).Find(&users).Error; err != nil {
		return Decision{}
	}
	automated := make(map[uint]bool, len(users))
	for _, user := range users {
		automated[user.ID] = user.IsAutomated
	}
	return Detect(messages, automated, cfg, now)
}

// normalizedText is the message text with whitespace collapsed and lowercased,
// so cosmetic differences do not hide an identical-message loop.
func normalizedText(message database.Message) string {
	if message.Text == nil {
		return ""
	}
	return strings.ToLower(strings.Join(strings.Fields(*message.Text), " "))
}
