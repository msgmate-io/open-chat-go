package nativecoder

import (
	"strings"
)

// This file derives the default tags a native msgmate-coder interaction carries
// (ci#650). It mirrors the opencode integration, which stamps its interaction
// chats with a single `opencode` tag: a coder chat is labelled with a stable
// per-bot tag (`bot:<bot-name>`) and a per-repo tag (`repo:<repo>`), so coder
// interactions can be grouped per bot and per repository in the UI and filtered
// through the agent list API (`tags=repo:...`).

const (
	// CoderInteractionTag marks every native coder interaction, the analogue of
	// the opencode integration's `opencode` tag. It is applied to any chat whose
	// shared config declares a native coder agent.
	CoderInteractionTag = "msgmate-coder"
	// RepoTagPrefix labels the repository a coder interaction works on.
	RepoTagPrefix = "repo:"
	// BotTagPrefix labels the bot a coder interaction was dispatched to.
	BotTagPrefix = "bot:"
	// DefaultBotTagSlug is used as the per-bot tag value when the bot carries no
	// usable name, so every coder chat still gets a per-bot label.
	DefaultBotTagSlug = "coder"
)

// repoConfigKeys are the shared-config keys a dispatch may carry the repository
// in when no explicit `repo:<...>` tag was passed.
var repoConfigKeys = []string{"repo", "repository", "github_repo", "git_repo"}

// DefaultInteractionTags returns the tags a coder interaction should carry on
// top of the caller's explicit ones. A config that does not declare a native
// coder agent is returned unchanged, so non-coder bots keep exactly the tags
// they asked for.
//
// An explicit `repo:<...>` tag always wins; otherwise the repository is derived
// from one of repoConfigKeys when present. The per-bot tag is derived from the
// bot name.
func DefaultInteractionTags(config map[string]interface{}, botName string, explicit []string) []string {
	if !IsCoderAgentConfig(config) {
		return explicit
	}
	tags := append([]string{}, explicit...)
	if !hasExactTag(tags, CoderInteractionTag) {
		tags = append(tags, CoderInteractionTag)
	}
	slug := slugify(botName)
	if slug == "" {
		slug = DefaultBotTagSlug
	}
	if !hasTagWithPrefix(tags, BotTagPrefix) {
		tags = append(tags, BotTagPrefix+slug)
	}
	if !hasTagWithPrefix(tags, RepoTagPrefix) {
		if repo := repoFromConfig(config); repo != "" {
			tags = append(tags, RepoTagPrefix+repo)
		}
	}
	return tags
}

// repoFromConfig returns the repository declared by a dispatch's shared config,
// normalized to `owner/name`, or "" when the config declares none.
func repoFromConfig(config map[string]interface{}) string {
	for _, key := range repoConfigKeys {
		value, ok := config[key].(string)
		if !ok {
			continue
		}
		if repo := NormalizeRepo(value); repo != "" {
			return repo
		}
	}
	return ""
}

// NormalizeRepo and slugify live in repo.go: they are shared with the
// development-lead router (ci#608) and the auto-consolidation planner, so a
// single implementation is kept there.

func hasExactTag(tags []string, want string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), want) {
			return true
		}
	}
	return false
}

func hasTagWithPrefix(tags []string, prefix string) bool {
	for _, tag := range tags {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(tag)), prefix) {
			return true
		}
	}
	return false
}
