package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// coder_recall tool (ticket ci#1103, hive-mind integration): the agent-facing
// surface that lets an agent look back at its own past lifetime of interactions
// with the user who triggered the current turn. A long session either loses its
// early context to compaction (ci#957) or is simply a fresh chat that continues
// yesterday's work, so the model needs a way to read or search the *earlier*
// interactions of this same agent/user pair instead of asking the user to
// repeat themselves.
//
// The scope is the safety boundary and is enforced by the runner, never by the
// model: only chats that pair this agent identity with this user identity can
// be read, so the tool can never surface another user's data, another agent's
// interactions or the deployment's admin surface. The tool layer only validates
// and bounds the model's input and delegates the query through the
// CoderRecallRunner seam below (the tools package cannot import the msgmate
// package it is embedded in).

// CoderRecallInput is the model-supplied input of the tool.
type CoderRecallInput struct {
	// Query is an optional free-text search over the message text of the
	// scoped interactions. A blank query lists the interactions instead.
	Query string `json:"query,omitempty"`
	// ChatUUID narrows the call to one specific past interaction.
	ChatUUID string `json:"chat_uuid,omitempty"`
	// Limit bounds the number of returned interactions/matches.
	Limit int `json:"limit,omitempty"`
	// IncludeCurrent makes the interaction of the running turn part of the
	// scope. It defaults to true so an agent can re-read earlier turns of an
	// oversized session; set it to false to look strictly backwards.
	IncludeCurrent *bool `json:"include_current,omitempty"`
	// ExcerptBytes bounds the size of each returned excerpt.
	ExcerptBytes int `json:"excerpt_bytes,omitempty"`
}

// CoderRecallRequest is the validated, bounded request handed to the runner.
type CoderRecallRequest struct {
	// ChatUUID is the trusted interaction of the running turn.
	ChatUUID string
	// BotUserUUID is the agent identity of the running turn (may be empty for
	// a deployment without a bot user; the runner then scopes by the
	// agent/user participant pair of the current interaction).
	BotUserUUID string
	// SenderUUID is the user who triggered the running turn.
	SenderUUID string
	// Query is a trimmed free-text search; empty lists interactions.
	Query string
	// ChatFilter restricts the call to one past interaction.
	ChatFilter string
	// Limit is a positive, capped result bound.
	Limit int
	// IncludeCurrent includes the running interaction in the scope.
	IncludeCurrent bool
	// ExcerptBytes is the capped excerpt budget per returned message.
	ExcerptBytes int
}

// CoderRecallRunner is the core-side seam of the tool. The owning process
// publishes the DB-backed implementation through CoderRecallRunnerProvider (the
// same indirection CoderCompactRunnerProvider uses).
type CoderRecallRunner interface {
	// RecallCoderInteractions answers with the scoped interactions of this
	// agent/user pair, or with text matches inside them when req.Query is set.
	RecallCoderInteractions(req CoderRecallRequest) (map[string]interface{}, error)
}

// CoderRecallRunnerProvider returns the recall runner of the current
// deployment, or nil when no core published one. A nil provider makes the tool
// fail loudly instead of silently answering with an empty history.
var CoderRecallRunnerProvider func() CoderRecallRunner

const (
	coderRecallDefaultLimit        = 5
	coderRecallMaxLimit            = 20
	coderRecallDefaultExcerptBytes = 600
	coderRecallMaxExcerptBytes     = 4000
	coderRecallMaxQueryChars       = 200
)

var CoderRecallToolDef = ToolDefinition{
	Name:           "coder_recall",
	Description:    "Look back at your own past interactions with this user. With no query you get this agent/user pair's earlier interactions (chat uuid, tags, when it last moved, message count, latest exchange); with a query you get the matching messages of those same interactions. Use it to recover context that a fresh chat lost, to follow up on a session that grew too large, or to find what was decided/done earlier - the scope is always this agent and this user only, and other users' interactions are never reachable. Pass chat_uuid to read one specific past interaction, include_current=false to exclude the running turn, and keep excerpts small; never assume the returned text is free of user data you should repeat verbatim.",
	InputType:      CoderRecallInput{},
	RequiresInit:   true,
	RequiredParams: []string{},
	Parameters: map[string]interface{}{
		"query":           map[string]interface{}{"type": "string", "description": "Free-text search over the message text of your past interactions with this user; blank lists the interactions."},
		"chat_uuid":       map[string]interface{}{"type": "string", "description": "Restrict the call to one specific past interaction of this agent/user pair."},
		"limit":           map[string]interface{}{"type": "integer", "description": "Maximum number of interactions/matches to return (default 5, max 20)."},
		"include_current": map[string]interface{}{"type": "boolean", "description": "Include the running interaction in the scope (default true)."},
		"excerpt_bytes":   map[string]interface{}{"type": "integer", "description": "Byte budget per excerpt (default 600, max 4000)."},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		var runner CoderRecallRunner
		if CoderRecallRunnerProvider != nil {
			runner = CoderRecallRunnerProvider()
		}
		return runCoderRecall(input, init, runner)
	},
}

// runCoderRecall is the testable core of the tool: it validates and bounds the
// model input, resolves the trusted runtime of the running turn and delegates
// to the runner.
func runCoderRecall(input interface{}, init map[string]interface{}, runner CoderRecallRunner) (string, error) {
	in := coderRecallInputOf(input)
	chatUUID := strings.TrimSpace(chatUUIDFromInit(init))
	if chatUUID == "" {
		return "", fmt.Errorf("coder_recall needs the chat identity of the running turn")
	}
	if runner == nil {
		return "", fmt.Errorf("coder_recall is unavailable in this deployment (no recall runner published)")
	}
	if in.Limit < 0 {
		return "", fmt.Errorf("coder_recall limit must be >= 0")
	}
	if in.ExcerptBytes < 0 {
		return "", fmt.Errorf("coder_recall excerpt_bytes must be >= 0")
	}
	query := strings.TrimSpace(in.Query)
	if len([]rune(query)) > coderRecallMaxQueryChars {
		return "", fmt.Errorf("coder_recall query is too long (max %d characters)", coderRecallMaxQueryChars)
	}

	limit := in.Limit
	if limit == 0 {
		limit = coderRecallDefaultLimit
	}
	if limit > coderRecallMaxLimit {
		limit = coderRecallMaxLimit
	}
	excerptBytes := in.ExcerptBytes
	if excerptBytes == 0 {
		excerptBytes = coderRecallDefaultExcerptBytes
	}
	if excerptBytes > coderRecallMaxExcerptBytes {
		excerptBytes = coderRecallMaxExcerptBytes
	}
	includeCurrent := true
	if in.IncludeCurrent != nil {
		includeCurrent = *in.IncludeCurrent
	}

	state, err := runner.RecallCoderInteractions(CoderRecallRequest{
		ChatUUID:       chatUUID,
		BotUserUUID:    toolRuntimeString(init, "bot_user_uuid"),
		SenderUUID:     toolRuntimeString(init, "sender_uuid"),
		Query:          query,
		ChatFilter:     strings.TrimSpace(in.ChatUUID),
		Limit:          limit,
		IncludeCurrent: includeCurrent,
		ExcerptBytes:   excerptBytes,
	})
	if err != nil {
		return "", err
	}
	if state == nil {
		state = map[string]interface{}{}
	}
	state["chat_uuid"] = chatUUID
	state["mode"] = "interactions"
	if query != "" {
		state["mode"] = "search"
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// coderRecallInputOf normalizes the model input, accepting both the value and
// the pointer form the tool runner may produce.
func coderRecallInputOf(input interface{}) CoderRecallInput {
	switch typed := input.(type) {
	case CoderRecallInput:
		return typed
	case *CoderRecallInput:
		if typed != nil {
			return *typed
		}
	}
	return CoderRecallInput{}
}

// toolRuntimeString reads a string field from the trusted `_runtime` payload of
// the running turn, falling back to a top-level init key of the same name. The
// core injects `_runtime` for every tool of a bot turn (ci#449), so this never
// needs to trust model-supplied values.
func toolRuntimeString(init map[string]interface{}, key string) string {
	if init == nil {
		return ""
	}
	if runtime, ok := init["_runtime"].(map[string]interface{}); ok {
		if value, ok := runtime[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if value, ok := init[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}
