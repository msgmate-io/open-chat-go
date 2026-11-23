package bots

import (
	"backend/api/msgmate"
	"backend/logging"
	"strings"
)

// ci#1189 step 2: merge the role catalog into the tool selection of a bot.
//
// A role bot (`plan`, `review`, `indexer`, `chooser`) is registered from a
// stored bootstrap config, and those stored configs drift: a plan bot shipped
// with only the read-only repository tools can neither read the issue it was
// dispatched for nor post its plan nor hand the ticket to an implementer. The
// catalog in backend/api/msgmate/role_tool_grants.go is the authoritative
// minimum capability of each role; this file applies it to the interaction
// config the bot is about to run with.
//
// The merge is additive and idempotent (see msgmate.MergeRoleGrants): the
// configured tool order is preserved, only genuinely absent grants are
// appended, and a bot without a declared role is left untouched.

// botRoleConfigKeys are the default_shared_config keys that declare a bot's
// catalog role. `coder_mode` is the key the git integration's bootstrap configs
// use (`coder_mode: plan`, see frontend/pages/docs/content/bots-agents.mdx);
// `role` is accepted as the plain spelling. Only the *bot's stored* config is
// consulted - never a per-interaction config override - so no dispatch can
// grant itself a role it was not configured with.
var botRoleConfigKeys = []string{"coder_mode", "role"}

// botRoleFromConfig resolves the catalog role a bot declares in its stored
// default shared config. It reports "" when no role is declared, which makes
// applyRoleToolGrants a no-op for ordinary user bots.
func botRoleFromConfig(config map[string]interface{}) string {
	for _, key := range botRoleConfigKeys {
		raw, ok := config[key]
		if !ok {
			continue
		}
		role, ok := raw.(string)
		if !ok {
			continue
		}
		if trimmed := strings.TrimSpace(role); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// applyRoleToolGrants merges the role's minimum tool grants into the tool
// selection of the config being resolved for an interaction, logging one info
// line per tool actually added. It returns the config unchanged when no role is
// declared, the role is unknown to the catalog, or the selection already
// carries every grant (so applying it twice is a no-op in value terms).
func applyRoleToolGrants(config map[string]interface{}, role string) map[string]interface{} {
	if config == nil {
		return config
	}
	role = strings.TrimSpace(role)
	if role == "" {
		return config
	}
	tools, err := collectToolNames(config["tools"])
	if err != nil {
		// A malformed tool selection is reported by the validation that runs
		// right after this step; never mask it with a merge failure here.
		return config
	}
	toolInit, hasToolInit := config["tool_init"].(map[string]interface{})
	merged, mergedToolInit := msgmate.MergeRoleGrants(role, tools, toolInit)

	configured := make(map[string]bool, len(tools))
	for _, tool := range tools {
		configured[strings.ToLower(strings.TrimSpace(tool))] = true
	}
	// Only grant tools that actually resolve in this deployment: the catalog
	// names the capability a role needs, but the tool set is assembled from the
	// statically registered tools plus the integration-provided ones. A grant
	// that does not resolve is skipped here instead of failing the whole
	// interaction creation in the validation that runs right after this step.
	granted := make([]string, 0, len(merged))
	added := make([]string, 0, len(merged))
	for _, tool := range merged {
		key := strings.ToLower(strings.TrimSpace(tool))
		if configured[key] {
			granted = append(granted, tool)
			continue
		}
		if _, found := msgmate.NewToolByName(tool); !found {
			continue
		}
		granted = append(granted, tool)
		added = append(added, tool)
	}
	if len(added) == 0 {
		return config
	}

	grantedTools := make([]interface{}, 0, len(granted))
	for _, tool := range granted {
		grantedTools = append(grantedTools, tool)
	}
	config["tools"] = grantedTools
	if hasToolInit {
		config["tool_init"] = mergedToolInit
	}
	for _, tool := range added {
		logging.Infof("role grants: added %s for role %s", tool, role)
	}
	return config
}
