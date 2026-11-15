package chats

import (
	"testing"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// ci#1101: a coder TUI that runs over a local ssh link arms a lease by
// heartbeating; when the link ends (cleanly or by lease expiry) the interaction
// it drove must stop instead of running on headless, while a brief reconnect
// inside the grace period must leave a healthy run alone.

// localLinkTestChat provisions one interaction chat owned by a human with an
// automated bot as the counterpart, which is the shape the local-link stop path
// expects.
func localLinkTestChat(t *testing.T, DB *gorm.DB, name string) database.Chat {
	t.Helper()
	owner := createUserForChatsTest(t, DB, "link-owner-"+name+"@example.com", false)
	bot := createUserForChatsTest(t, DB, "link-bot-"+name+"@example.com", false)
	bot.IsAutomated = true
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to mark the bot automated: %v", err)
	}
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create the interaction chat: %v", err)
	}
	return chat
}

func TestLocalLinkLeaseGracePeriod(t *testing.T) {
	resetLocalLinkLeasesForTest()
	t.Cleanup(resetLocalLinkLeasesForTest)

	start := time.Now()
	lease := TouchLocalLinkLease("chat-grace", start, 0)
	if lease.Grace != DefaultLocalLinkGrace {
		t.Fatalf("Grace = %s, want the default %s", lease.Grace, DefaultLocalLinkGrace)
	}

	// A brief reconnect inside the grace period: the heartbeat moves LastSeen
	// forward, so the lease stays healthy.
	reconnect := start.Add(DefaultLocalLinkGrace - time.Second)
	refreshed := TouchLocalLinkLease("chat-grace", reconnect, 0)
	if _, armed, expired := LocalLinkLeaseStatus("chat-grace", reconnect); !armed || expired {
		t.Fatalf("lease must survive a reconnect inside the grace period (armed=%v expired=%v)", armed, expired)
	}

	// Nothing has lapsed one second before the refreshed deadline, so a sweep
	// must leave the run alone and the lease armed.
	if stopped := SweepExpiredLocalLinkLeases(nil, nil, refreshed.expiresAt().Add(-time.Second)); stopped != 0 {
		t.Fatalf("sweep stopped %d interaction(s) before the grace period lapsed", stopped)
	}
	if _, armed, _ := LocalLinkLeaseStatus("chat-grace", reconnect); !armed {
		t.Fatal("the lease must still be armed after an early sweep")
	}

	// At the deadline the lease is expired and the sweep removes it, so each
	// chat is stopped exactly once.
	if _, _, expired := LocalLinkLeaseStatus("chat-grace", refreshed.expiresAt()); !expired {
		t.Fatal("the lease must be expired at its expiry instant")
	}
	SweepExpiredLocalLinkLeases(nil, nil, refreshed.expiresAt())
	if _, armed, _ := LocalLinkLeaseStatus("chat-grace", refreshed.expiresAt()); armed {
		t.Fatal("the sweep must remove an expired lease")
	}
	if _, armed, _ := LocalLinkLeaseStatus("chat-other", time.Now()); armed {
		t.Fatal("an unarmed chat must never report a lease")
	}
}

func TestLocalLinkHeartbeatClampsRequestedGrace(t *testing.T) {
	resetLocalLinkLeasesForTest()
	t.Cleanup(resetLocalLinkLeasesForTest)

	cases := []struct {
		requested time.Duration
		want      time.Duration
	}{
		{0, DefaultLocalLinkGrace},
		{time.Second, MinLocalLinkGrace},
		{time.Hour, MaxLocalLinkGrace},
		{45 * time.Second, 45 * time.Second},
	}
	for _, tc := range cases {
		lease := TouchLocalLinkLease("chat-clamp", time.Now(), tc.requested)
		if lease.Grace != tc.want {
			t.Fatalf("grace for %s = %s, want %s", tc.requested, lease.Grace, tc.want)
		}
	}
}

// TestLocalLinkEndStopsInteraction is the stop-on-disconnect case: a clean
// session end latches the auto-continuation loop off, so the interaction cannot
// resume headless.
func TestLocalLinkEndStopsInteraction(t *testing.T) {
	resetLocalLinkLeasesForTest()
	t.Cleanup(resetLocalLinkLeasesForTest)

	DB := setupChatsTestDB(t)
	chat := localLinkTestChat(t, DB, "end")

	TouchLocalLinkLease(chat.UUID, time.Now(), 0)
	if lease, armed := EndLocalLinkLease(chat.UUID); !armed || lease.ChatUUID != chat.UUID {
		t.Fatalf("EndLocalLinkLease = (%+v, %v), want the armed lease", lease, armed)
	}
	if _, armed, _ := LocalLinkLeaseStatus(chat.UUID, time.Now()); armed {
		t.Fatal("a clean link end must drop the lease")
	}

	StopInteractionForLocalLinkLoss(DB, nil, chat, localLinkStopReason)

	state := loadAutoContinueState(DB, chat.ID)
	if !state.Stopped {
		t.Fatal("the auto-continuation loop must be latched off after a local link end")
	}
	if state.StopReason != localLinkStopReason {
		t.Fatalf("StopReason = %q, want %q", state.StopReason, localLinkStopReason)
	}

	// A repeated end is idempotent and keeps the recorded reason.
	StopInteractionForLocalLinkLoss(DB, nil, chat, localLinkStopReason)
	if state := loadAutoContinueState(DB, chat.ID); state.StopReason != localLinkStopReason {
		t.Fatalf("StopReason after a repeated stop = %q, want %q", state.StopReason, localLinkStopReason)
	}

	// Recovery is a normal terminal stop: a nudge resumes the loop.
	if err := ResumeAutoContinueForChat(DB, chat.ID); err != nil {
		t.Fatalf("ResumeAutoContinueForChat: %v", err)
	}
	if state := loadAutoContinueState(DB, chat.ID); state.Stopped || state.StopReason != "" {
		t.Fatalf("resume left {stopped:%v reason:%q}, want a clean loop", state.Stopped, state.StopReason)
	}
}

// TestLocalLinkSweepStopsExpiredLeaseOnly is the grace-period case against real
// chats: a lease that reconnected inside its grace period is never stopped by a
// sweep, and an expired one is stopped exactly once.
func TestLocalLinkSweepStopsExpiredLeaseOnly(t *testing.T) {
	resetLocalLinkLeasesForTest()
	t.Cleanup(resetLocalLinkLeasesForTest)

	DB := setupChatsTestDB(t)
	liveChat := localLinkTestChat(t, DB, "live")
	idleChat := localLinkTestChat(t, DB, "idle")

	now := time.Now()
	TouchLocalLinkLease(liveChat.UUID, now, 0)
	TouchLocalLinkLease(idleChat.UUID, now, 0)
	// The healthy chat reconnected; the other one went silent.
	TouchLocalLinkLease(liveChat.UUID, now.Add(DefaultLocalLinkGrace-time.Second), 0)

	// A sweep inside the grace period stops nothing.
	if stopped := SweepExpiredLocalLinkLeases(DB, nil, now.Add(DefaultLocalLinkGrace-time.Second)); stopped != 0 {
		t.Fatalf("an in-grace sweep stopped %d interaction(s), want 0", stopped)
	}
	for _, chat := range []database.Chat{liveChat, idleChat} {
		if state := loadAutoContinueState(DB, chat.ID); state.Stopped {
			t.Fatalf("chat %s was stopped inside its grace period (reason %q)", chat.UUID, state.StopReason)
		}
	}

	// Long after the last heartbeat of the silent chat (and past the reconnected
	// one's deadline too) both leases have lapsed.
	sweepAt := now.Add(DefaultLocalLinkGrace + time.Minute)
	if stopped := SweepExpiredLocalLinkLeases(DB, nil, sweepAt); stopped != 2 {
		t.Fatalf("sweep stopped %d interaction(s), want 2", stopped)
	}
	for _, chat := range []database.Chat{liveChat, idleChat} {
		state := loadAutoContinueState(DB, chat.ID)
		if !state.Stopped || state.StopReason != localLinkStopReason {
			t.Fatalf("chat %s state after the sweep = {stopped:%v reason:%q}, want the local-link stop", chat.UUID, state.Stopped, state.StopReason)
		}
	}
	// A repeat sweep stops nothing again: each chat stops once.
	if stopped := SweepExpiredLocalLinkLeases(DB, nil, sweepAt.Add(time.Minute)); stopped != 0 {
		t.Fatalf("a repeat sweep stopped %d interaction(s), want 0", stopped)
	}
}

// TestLocalLinkLeaseStatusUnknownChat pins the registry keying: an unknown or
// empty chat UUID never reports a lease and a padded UUID is trimmed.
func TestLocalLinkLeaseStatusUnknownChat(t *testing.T) {
	resetLocalLinkLeasesForTest()
	t.Cleanup(resetLocalLinkLeasesForTest)

	if _, armed, expired := LocalLinkLeaseStatus("", time.Now()); armed || expired {
		t.Fatalf("an empty chat UUID must not report a lease (armed=%v expired=%v)", armed, expired)
	}
	TouchLocalLinkLease("  chat-space  ", time.Now(), 0)
	if _, armed, _ := LocalLinkLeaseStatus("chat-space", time.Now()); !armed {
		t.Fatal("a padded chat UUID must be trimmed before it is keyed")
	}
}

// TestLocalLinkSweeperDrainsExpiredLease exercises the background sweeper: it
// drains a lease that is already expired and stops promptly.
func TestLocalLinkSweeperDrainsExpiredLease(t *testing.T) {
	resetLocalLinkLeasesForTest()
	t.Cleanup(resetLocalLinkLeasesForTest)

	// Armed an hour ago, so the lease lapsed long before the first tick.
	TouchLocalLinkLease("chat-sweep", time.Now().Add(-time.Hour), 0)
	stop := StartLocalLinkLeaseSweeper(nil, nil, 5*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, armed, _ := LocalLinkLeaseStatus("chat-sweep", time.Now()); !armed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	if _, armed, _ := LocalLinkLeaseStatus("chat-sweep", time.Now()); armed {
		t.Fatal("the sweeper never drained the expired lease")
	}
}
