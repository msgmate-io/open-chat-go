package tui

import (
	"testing"
	"time"

	"backend/sandboxsession"
)

func TestCoreCommandsPublishesSessionsBotsAndCatalog(t *testing.T) {
	commands := CoreCommands()
	if len(commands) != 4 {
		t.Fatalf("expected 4 core commands, got %d", len(commands))
	}
	byName := map[string]Command{}
	for _, command := range commands {
		byName[command.Name] = command
		if command.Description == "" {
			t.Errorf("command %q has no description", command.Name)
		}
	}
	sessions, ok := byName["sessions"]
	if !ok {
		t.Fatalf("missing :sessions command")
	}
	if sessions.Action != ActionPickSession {
		t.Errorf(":sessions action = %q, want %q", sessions.Action, ActionPickSession)
	}
	if sessions.RequiresChat {
		t.Errorf(":sessions must be usable before a chat is attached")
	}
	bots, ok := byName["bots"]
	if !ok {
		t.Fatalf("missing :bots command")
	}
	if bots.Action != ActionPickBot {
		t.Errorf(":bots action = %q, want %q", bots.Action, ActionPickBot)
	}
	if !bots.RequiresChat {
		t.Errorf(":bots switches the bot of the current interaction, so it requires a chat")
	}
	models, ok := byName["models"]
	if !ok {
		t.Fatalf("missing :models command")
	}
	if models.Action != ActionPickModel {
		t.Errorf(":models action = %q, want %q", models.Action, ActionPickModel)
	}
	if models.RequiresChat {
		t.Errorf(":models only browses the catalog, so it must not require a chat")
	}
	tools, ok := byName["tools"]
	if !ok {
		t.Fatalf("missing :tools command")
	}
	if tools.Action != ActionPickTool {
		t.Errorf(":tools action = %q, want %q", tools.Action, ActionPickTool)
	}
	if tools.RequiresChat {
		t.Errorf(":tools only browses the catalog, so it must not require a chat")
	}
}

func TestCoreCommandsReturnsCopy(t *testing.T) {
	first := CoreCommands()
	first[0].Aliases[0] = "mutated"
	first[0].Name = "mutated"
	second := CoreCommands()
	if second[0].Name == "mutated" {
		t.Fatalf("CoreCommands leaked the command struct to the caller")
	}
	if second[0].Aliases[0] == "mutated" {
		t.Fatalf("CoreCommands leaked the alias slice to the caller")
	}
}

func TestMatchCommands(t *testing.T) {
	commands := CoreCommands()
	cases := []struct {
		input string
		want  []string
	}{
		{input: "", want: []string{"bots", "models", "sessions", "tools"}},
		{input: ":", want: []string{"bots", "models", "sessions", "tools"}},
		{input: ":se", want: []string{"sessions"}},
		{input: ":sess abc", want: []string{"sessions"}},
		{input: ":bo", want: []string{"bots"}},
		{input: ":mo", want: []string{"models"}},
		{input: ":to", want: []string{"tools"}},
		{input: ":tool grep", want: []string{"tools"}},
		{input: ":attach", want: []string{"sessions"}},
		{input: ":switch-bot", want: []string{"bots"}},
		{input: ":nope", want: []string{}},
	}
	for _, testCase := range cases {
		matches := MatchCommands(commands, testCase.input)
		if matches == nil {
			t.Fatalf("MatchCommands(%q) returned nil, want an empty slice", testCase.input)
		}
		got := make([]string, 0, len(matches))
		for _, match := range matches {
			got = append(got, match.Name)
		}
		if len(got) != len(testCase.want) {
			t.Fatalf("MatchCommands(%q) = %v, want %v", testCase.input, got, testCase.want)
		}
		for i := range got {
			if got[i] != testCase.want[i] {
				t.Fatalf("MatchCommands(%q) = %v, want %v", testCase.input, got, testCase.want)
			}
		}
	}
}

func TestFindCommand(t *testing.T) {
	commands := CoreCommands()
	if command, ok := FindCommand(commands, ":sessions"); !ok || command.Name != "sessions" {
		t.Fatalf("FindCommand(:sessions) = %q, %v", command.Name, ok)
	}
	if command, ok := FindCommand(commands, " :attach abc"); !ok || command.Name != "sessions" {
		t.Fatalf("FindCommand(:attach abc) = %q, %v", command.Name, ok)
	}
	if _, ok := FindCommand(commands, ":se"); ok {
		t.Fatalf("FindCommand(:se) resolved an incomplete command")
	}
	if _, ok := FindCommand(commands, ":"); ok {
		t.Fatalf("FindCommand(:) resolved an empty command")
	}
}

func TestCommandArgument(t *testing.T) {
	cases := map[string]string{
		":bots cod":           "cod",
		":bots   cod ":        "cod",
		"bots":                "",
		":":                   "",
		"  :sessions foo bar": "foo bar",
	}
	for input, want := range cases {
		if got := CommandArgument(input); got != want {
			t.Fatalf("CommandArgument(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildSessionPicker(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	detached := now.Add(-time.Hour)
	records := []sandboxsession.SessionRecord{
		{
			UUID:       "sess-old",
			Provider:   "netns",
			ChatUUID:   "chat-a",
			ExpiresAt:  now.Add(time.Hour),
			DetachedAt: &detached,
			CreatedAt:  now.Add(-2 * time.Hour),
		},
		{
			// Same chat, newer record: the picker offers the chat once.
			UUID:       "sess-new",
			Provider:   "netns",
			ChatUUID:   "chat-a",
			ExpiresAt:  now.Add(time.Hour),
			DetachedAt: &detached,
			CreatedAt:  now.Add(-time.Hour),
		},
		{
			UUID:      "sess-live",
			ChatUUID:  "chat-b",
			ExpiresAt: now.Add(time.Hour),
			CreatedAt: now.Add(-30 * time.Minute),
		},
		{
			UUID:        "sess-destroyed",
			ChatUUID:    "chat-c",
			ExpiresAt:   now.Add(time.Hour),
			DestroyedAt: &detached,
			CreatedAt:   now,
		},
		{
			UUID:      "sess-expired",
			ChatUUID:  "chat-d",
			ExpiresAt: now.Add(-time.Minute),
			CreatedAt: now.Add(-time.Minute),
		},
		{
			UUID:      "",
			ChatUUID:  "chat-e",
			CreatedAt: now,
		},
	}

	entries := BuildSessionPicker(records, "sess-live", now)
	if len(entries) != 2 {
		t.Fatalf("expected 2 pickable sessions, got %d (%+v)", len(entries), entries)
	}
	if entries[0].SessionUUID != "sess-live" || !entries[0].Current {
		t.Fatalf("newest session should come first and be marked current: %+v", entries[0])
	}
	if entries[1].SessionUUID != "sess-new" {
		t.Fatalf("chat-a should collapse to its newest record, got %q", entries[1].SessionUUID)
	}
	if entries[1].Current || !entries[1].Detached || entries[1].Label != "chat-a" {
		t.Fatalf("unexpected collapsed row: %+v", entries[1])
	}
	if empty := BuildSessionPicker(nil, "", now); empty == nil || len(empty) != 0 {
		t.Fatalf("BuildSessionPicker(nil) = %v, want empty non-nil slice", empty)
	}
}

func TestBuildBotPicker(t *testing.T) {
	entries := BuildBotPicker([]BotCandidate{
		{UUID: "bot-z", Name: "zeta", Username: "zeta"},
		{UUID: "bot-a", Username: "alpha"},
		{UUID: "bot-c", Name: "Coder", Username: "coder", IntegrationName: "msgmate_coder"},
		{UUID: "bot-a", Name: "duplicate"},
		{UUID: ""},
	}, "bot-z")

	if len(entries) != 3 {
		t.Fatalf("expected 3 bot rows, got %d (%+v)", len(entries), entries)
	}
	if entries[0].UUID != "bot-z" || !entries[0].Current {
		t.Fatalf("current bot must be the first row: %+v", entries[0])
	}
	if entries[1].Label != "alpha" {
		t.Fatalf("label should fall back to the username, got %q", entries[1].Label)
	}
	if entries[2].Label != "Coder" || entries[2].IntegrationName != "msgmate_coder" {
		t.Fatalf("unexpected third row: %+v", entries[2])
	}
	if empty := BuildBotPicker(nil, ""); empty == nil || len(empty) != 0 {
		t.Fatalf("BuildBotPicker(nil) = %v, want empty non-nil slice", empty)
	}
}

func TestCatalogRoutesAreReadOnly(t *testing.T) {
	cases := map[Action]string{
		ActionPickBot:   "/bots/list",
		ActionPickModel: "/models",
		ActionPickTool:  "/tools",
	}
	for action, want := range cases {
		route, ok := CatalogRoute(action)
		if !ok || route != want {
			t.Fatalf("CatalogRoute(%q) = %q, %v, want %q", action, route, ok, want)
		}
	}
	// :sessions reads the local session store, it must not resolve to a route.
	if route, ok := CatalogRoute(ActionPickSession); ok {
		t.Fatalf("CatalogRoute(:sessions) = %q, want no catalog route", route)
	}
}

func TestBuildModelPicker(t *testing.T) {
	entries := BuildModelPicker([]ModelCandidate{
		{ID: "gpt-4o-mini", Name: "GPT-4o mini", Provider: "openai", ContextWindow: 128000},
		{ID: "claude-x", Provider: "anthropic"},
		{ID: "gpt-4o-mini", Name: "duplicate"},
		{ID: "  "},
	}, "claude-x")

	if len(entries) != 2 {
		t.Fatalf("expected 2 model rows, got %d (%+v)", len(entries), entries)
	}
	if entries[0].ID != "claude-x" || !entries[0].Current {
		t.Fatalf("current model must be the first row: %+v", entries[0])
	}
	if entries[1].Label != "GPT-4o mini" || entries[1].Provider != "openai" || entries[1].ContextWindow != 128000 {
		t.Fatalf("unexpected model row: %+v", entries[1])
	}
	if empty := BuildModelPicker(nil, ""); empty == nil || len(empty) != 0 {
		t.Fatalf("BuildModelPicker(nil) = %v, want empty non-nil slice", empty)
	}
}

func TestBuildToolPicker(t *testing.T) {
	entries := BuildToolPicker([]ToolCandidate{
		{Name: "web_search", Description: "Search the web", Category: "research"},
		{Name: "run_code"},
		{Name: "run_code", Description: "duplicate"},
		{Name: ""},
	})

	if len(entries) != 2 {
		t.Fatalf("expected 2 tool rows, got %d (%+v)", len(entries), entries)
	}
	if entries[0].Name != "run_code" || entries[0].Label != "run_code" {
		t.Fatalf("tool rows should be sorted by name: %+v", entries[0])
	}
	if entries[1].Name != "web_search" || entries[1].Category != "research" || entries[1].Description != "Search the web" {
		t.Fatalf("unexpected tool row: %+v", entries[1])
	}
	if empty := BuildToolPicker(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("BuildToolPicker(nil) = %v, want empty non-nil slice", empty)
	}
}
