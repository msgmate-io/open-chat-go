package cmd

import (
	"os"
	"strings"
	"testing"
)

// ci#747 RC1: the self-message scheduler only keeps an idle interaction alive
// when the boot path actually arms it. The scheduler shipped in #852 was dead
// code (no caller), so `OPEN_CHAT_SELF_MESSAGE_SCHEDULES` was never read and no
// actor was ever scheduled. This guard fails loudly if that wiring is dropped
// again, which is exactly the regression that made the lead idle forever.
func TestServerBootArmsSelfMessageSchedules(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	boot := string(source)
	for _, needle := range []string{
		"msgmate.StartSelfMessageRuntime(",
		"msgmate.SelfMessageScheduleEnvVar",
	} {
		if !strings.Contains(boot, needle) {
			t.Errorf("backend/cmd/server.go no longer references %q: an idle interaction would never self-resume (ci#747 RC1)", needle)
		}
	}
}
