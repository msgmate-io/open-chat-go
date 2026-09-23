package cmd

import (
	"os"
	"strings"
	"time"

	"backend/runtimecfg"

	"github.com/urfave/cli/v3"
)

const (
	triggerPollEnabledEnv  = "OPEN_CHAT_TRIGGER_POLL_ENABLED"
	triggerPollIntervalEnv = "OPEN_CHAT_TRIGGER_POLL_INTERVAL"

	// Integration-owned runtime env keys (settable via open-chat.json
	// integrations.git.trigger_poll_enabled / trigger_poll_interval). They take
	// precedence over the process-level flags so a deployment can configure the
	// poll from its config file.
	triggerPollEnabledOCI  = "OCI_GIT_TRIGGER_POLL_ENABLED"
	triggerPollIntervalOCI = "OCI_GIT_TRIGGER_POLL_INTERVAL"
)

// GetTriggerPollFlags declares the git trigger poll scheduler flags shared by
// the server and worker commands.
func GetTriggerPollFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Sources: cli.EnvVars(triggerPollEnabledEnv),
			Name:    "trigger-poll-enabled",
			Usage:   "Run the periodic git provider notification poll (issue assignments/@mentions without per-repo workflows)",
			Value:   true,
		},
		&cli.StringFlag{
			Sources: cli.EnvVars(triggerPollIntervalEnv),
			Name:    "trigger-poll-interval",
			Usage:   "Interval between git provider notification polls (Go duration, e.g. 60s)",
			Value:   "60s",
		},
	}
}

func triggerPollRuntimeValue(key string) (string, bool) {
	if value, ok := runtimecfg.GetAll()[key]; ok {
		if trimmed := strings.TrimSpace(value.Value); trimmed != "" {
			return trimmed, true
		}
	}
	if trimmed := strings.TrimSpace(os.Getenv(key)); trimmed != "" {
		return trimmed, true
	}
	return "", false
}

// resolveTriggerPollEnabled resolves the effective scheduler enabled state.
func resolveTriggerPollEnabled(c *cli.Command) bool {
	if raw, ok := triggerPollRuntimeValue(triggerPollEnabledOCI); ok {
		switch strings.ToLower(raw) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return c.Bool("trigger-poll-enabled")
}

// resolveTriggerPollInterval resolves the effective scheduler interval,
// falling back to the default when the value is missing or invalid.
func resolveTriggerPollInterval(c *cli.Command) time.Duration {
	raw := strings.TrimSpace(c.String("trigger-poll-interval"))
	if override, ok := triggerPollRuntimeValue(triggerPollIntervalOCI); ok {
		raw = override
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return time.Minute
	}
	return interval
}
