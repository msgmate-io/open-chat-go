package tui

import (
	"sort"
	"strings"
)

// This file defines the `:command` palette contract for the TUI (ci#1107).
//
// The palette itself is drawn by the TUI integration, but which commands exist,
// how they are completed and what they are called must not drift between the
// integration and the core. The core therefore publishes the commands it owns
// (`:sessions`, `:bots`) as data; an integration renders them next to its own
// commands through the same MatchCommands completion so `:` behaves identically
// everywhere.

// Action names what a palette command does once the user selects it.
type Action string

const (
	// ActionPickSession opens the past-session picker: the rows come from
	// BuildSessionPicker over the user's persisted coding sessions, and the
	// selected row is re-attached through the session store (ci#1060).
	ActionPickSession Action = "sessions.pick"
	// ActionPickBot opens the bot picker: the rows come from BuildBotPicker
	// over the bots the user may talk to, and the selected row switches the
	// bot of the current interaction.
	ActionPickBot Action = "bots.pick"
)

// Command is one entry of the `:command` palette.
type Command struct {
	// Name is the command without the leading colon (for example "sessions").
	Name string `json:"name"`
	// Aliases are alternative spellings that complete to the same command.
	Aliases []string `json:"aliases,omitempty"`
	// Action is what selecting the command does.
	Action Action `json:"action"`
	// Description is the one-line help text shown next to the command.
	Description string `json:"description"`
	// ArgumentHint is the inline hint for the optional argument, for example
	// "<uuid|username>" or a search term.
	ArgumentHint string `json:"argument_hint,omitempty"`
	// RequiresChat marks a command that needs an open interaction, so the
	// palette can disable (rather than silently fail) it before a chat is
	// attached.
	RequiresChat bool `json:"requires_chat,omitempty"`
}

// CoreCommands returns the palette commands the core owns.
//
// The returned slice is a copy: callers append their integration's commands to
// it and must not mutate the core's list.
func CoreCommands() []Command {
	commands := []Command{
		{
			Name:         "sessions",
			Aliases:      []string{"session", "attach", "resume"},
			Action:       ActionPickSession,
			Description:  "Pick a past coding session of this agent and attach to it",
			ArgumentHint: "<search>",
		},
		{
			Name:         "bots",
			Aliases:      []string{"bot", "switch-bot"},
			Action:       ActionPickBot,
			Description:  "Switch the bot this interaction talks to",
			ArgumentHint: "<search>",
			RequiresChat: true,
		},
	}
	for i := range commands {
		commands[i].Aliases = append([]string(nil), commands[i].Aliases...)
	}
	return commands
}

// MatchCommands filters commands for a partially typed `:` input.
//
// input is what the user typed after the colon (the leading colon and any
// surrounding spaces are tolerated). Every whitespace separated token must
// prefix-match the command name or one of its aliases, so `:se` and
// `:sess abc` both complete `:sessions` while the trailing `abc` stays
// available as the search argument. An exact name/alias match sorts first so
// pressing Enter on a fully typed command always runs it, the rest follow by
// name; the result is deterministic for a given input.
//
// The returned slice is never nil, so the palette can render an empty list.
func MatchCommands(commands []Command, input string) []Command {
	query := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), ":")))
	tokens := strings.Fields(query)

	matches := make([]Command, 0, len(commands))
	for _, command := range commands {
		if matchesTokens(command, tokens) {
			matches = append(matches, command)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		leftExact, rightExact := exactMatch(matches[i], tokens), exactMatch(matches[j], tokens)
		if leftExact != rightExact {
			return leftExact
		}
		return matches[i].Name < matches[j].Name
	})
	return matches
}

// matchesTokens reports whether every token prefix-matches the command name or
// one of its aliases. Only the first token may address the command; the
// remaining tokens are the argument and are accepted as-is.
func matchesTokens(command Command, tokens []string) bool {
	if len(tokens) == 0 {
		return true
	}
	return prefixMatches(command, tokens[0])
}

// prefixMatches reports whether token prefixes the command's name or an alias.
func prefixMatches(command Command, token string) bool {
	if token == "" {
		return true
	}
	if strings.HasPrefix(strings.ToLower(command.Name), token) {
		return true
	}
	for _, alias := range command.Aliases {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(alias)), token) {
			return true
		}
	}
	return false
}

// exactMatch reports whether the first token is exactly the command name or an
// alias, i.e. the user has finished typing the command itself.
func exactMatch(command Command, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(command.Name), tokens[0]) {
		return true
	}
	for _, alias := range command.Aliases {
		if strings.EqualFold(strings.TrimSpace(alias), tokens[0]) {
			return true
		}
	}
	return false
}

// CommandArgument returns the argument typed after a palette command, i.e.
// everything following the first whitespace separated token. It is the initial
// search term the `:sessions` / `:bots` pickers open with, so `:bots cod`
// opens the picker already filtered.
func CommandArgument(input string) string {
	query := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), ":"))
	if idx := strings.IndexAny(query, " \t"); idx >= 0 {
		return strings.TrimSpace(query[idx+1:])
	}
	return ""
}

// FindCommand resolves the exact command a `:` input names, so a fully typed
// command runs without an intermediate palette step. It reports false when the
// input only completes several commands or names none.
func FindCommand(commands []Command, input string) (Command, bool) {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), ":"))))
	if len(tokens) == 0 {
		return Command{}, false
	}
	for _, command := range commands {
		if exactMatch(command, tokens) {
			return command, true
		}
	}
	return Command{}, false
}
