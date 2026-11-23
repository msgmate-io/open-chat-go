package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// coder_dispatch_agent (ci#1189 step 4): the tool a lead agent uses to start a
// sibling agent interaction itself, instead of asking the operator to dispatch
// it by hand.
//
// Shape of the tool: the model only names the bot, the message and optional
// tags. The repo/issue identity the dispatch is scoped to is NEVER taken from
// the model's arguments - it is read from the tool's init data, ie from the
// `repo` / `issue_number` pin the core writes for the interaction
// (database.ToolInitPinnedFieldRepo / database.ToolInitPinnedFieldIssueNumber,
// see ToolInitPinsFromTrigger). When either pin is absent the tool fails closed:
// it dispatches nothing and returns an error, so a model can never widen its own
// scope to an unrelated repo/issue by passing arguments.
//
// The tools package cannot import the bots package that owns the dispatch
// handler (bots imports msgmate -> msgmate imports tools), so the actual
// dispatch lives behind the DispatchAgentRunner seam below, published by the
// process that owns the queue - the same indirection
// CoderScheduleRunnerProvider and ContextUsageLookup use.
const (
	// CoderDispatchAgentToolName is the tool name the plan bots are granted.
	CoderDispatchAgentToolName = "coder_dispatch_agent"
)

// CoderDispatchAgentInput is the model-facing input. It deliberately carries no
// repo, no issue number and no chat identity: all of those come from the trusted
// init data of the running turn.
type CoderDispatchAgentInput struct {
	Bot     string   `json:"bot,omitempty"`
	Message string   `json:"message,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// CoderDispatchAgentRequest is the resolved, trusted dispatch the runner
// executes. Repo and IssueNumber are already taken from the interaction pin;
// they are not model-controlled.
type CoderDispatchAgentRequest struct {
	Repo        string
	IssueNumber string
	Bot         string
	Message     string
	Tags        []string
}

// CoderDispatchAgentResult is what the runner reports back after it re-read the
// created interaction. A runner MUST only return a result once it has read the
// interaction back from the store/API; a dispatch it cannot re-read is an error,
// not a success.
type CoderDispatchAgentResult struct {
	ChatUUID     string `json:"chat_uuid,omitempty"`
	Bot          string `json:"bot,omitempty"`
	Repo         string `json:"repo,omitempty"`
	IssueNumber  string `json:"issue_number,omitempty"`
	Deduplicated bool   `json:"deduplicated,omitempty"`
}

// CoderDispatchAgentRunner is the core-side seam of the tool: it builds the
// agent-dispatch request (bots.AgentDispatchRequest), invokes the existing
// dispatch handler and re-reads the interaction it created/returned.
type CoderDispatchAgentRunner interface {
	DispatchAgent(req CoderDispatchAgentRequest) (CoderDispatchAgentResult, error)
}

// CoderDispatchAgentRunnerProvider returns the dispatch runner of the current
// deployment, or nil when no queue-owning process published one. A nil provider
// makes the tool fail loudly instead of silently swallowing a dispatch.
var CoderDispatchAgentRunnerProvider func() CoderDispatchAgentRunner

// CoderDispatchAgentToolDef is the tool definition of coder_dispatch_agent.
var CoderDispatchAgentToolDef = ToolDefinition{
	Name: CoderDispatchAgentToolName,
	Description: "Dispatch an agent interaction for this repo/issue and return its chat UUID. The dispatch is pinned to the repo and issue number of the interaction running this turn, so repo/issue are not arguments. " +
		"Arguments: bot (required, the bot/agent to start, eg issue-repl-bot or msgmate-coder), message (required, the task message), tags (optional, extra repo:/issue: tags). " +
		"The tool re-reads the created interaction before reporting success; a deduplicated dispatch returns the existing interaction with deduplicated=true, which you steer with coder_dispatch_agent's sibling tools instead of dispatching a second agent.",
	InputType: CoderDispatchAgentInput{},
	// RequiresInit: the dispatch identity (repo + issue number) always comes from
	// the interaction's tool-init pin, never from the model's arguments.
	RequiresInit:   true,
	RequiredParams: []string{"bot", "message"},
	Parameters: map[string]interface{}{
		"bot":     map[string]interface{}{"type": "string", "description": "Bot/agent to start, eg issue-repl-bot or msgmate-coder."},
		"message": map[string]interface{}{"type": "string", "description": "Task message the dispatched agent starts from."},
		"tags": map[string]interface{}{
			"type":        "array",
			"description": "Optional extra tags to attach to the dispatch (eg repo:open-chat-go).",
			"items":       map[string]interface{}{"type": "string"},
		},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		var runner CoderDispatchAgentRunner
		if CoderDispatchAgentRunnerProvider != nil {
			runner = CoderDispatchAgentRunnerProvider()
		}
		return runCoderDispatchAgent(input, init, runner)
	},
}

// runCoderDispatchAgent is the testable core of the tool: it validates the
// model input, resolves the trusted repo/issue pin from init data and delegates
// the dispatch to the runner.
func runCoderDispatchAgent(input interface{}, init map[string]interface{}, runner CoderDispatchAgentRunner) (string, error) {
	in := castCoderDispatchAgentInput(input)
	bot := strings.TrimSpace(in.Bot)
	message := strings.TrimSpace(in.Message)
	if bot == "" {
		return "", fmt.Errorf("%s requires a bot to dispatch", CoderDispatchAgentToolName)
	}
	if message == "" {
		return "", fmt.Errorf("%s requires a message for the dispatched agent", CoderDispatchAgentToolName)
	}
	// Fail closed: without both pins the tool has no scope it may dispatch into.
	repo := strings.TrimSpace(coderDispatchInitString(init, "repo"))
	issueNumber := strings.TrimSpace(coderDispatchInitString(init, "issue_number"))
	if repo == "" || issueNumber == "" {
		return "", fmt.Errorf("%s needs the pinned repo and issue number of this interaction (init data is missing %q/%q); no agent was dispatched", CoderDispatchAgentToolName, "repo", "issue_number")
	}
	if runner == nil {
		return "", fmt.Errorf("%s is unavailable in this deployment (no dispatch handles published); no agent was dispatched", CoderDispatchAgentToolName)
	}
	result, err := runner.DispatchAgent(CoderDispatchAgentRequest{
		Repo:        repo,
		IssueNumber: issueNumber,
		Bot:         bot,
		Message:     message,
		Tags:        normalizeCoderDispatchTags(in.Tags),
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.ChatUUID) == "" {
		return "", fmt.Errorf("%s could not verify the dispatched interaction: the runner reported no chat UUID", CoderDispatchAgentToolName)
	}
	return encodeCoderDispatchAgentResult(result)
}

// coderDispatchInitString reads one string field of the tool init data.
func coderDispatchInitString(init map[string]interface{}, key string) string {
	if init == nil {
		return ""
	}
	value, _ := init[key].(string)
	return value
}

// normalizeCoderDispatchTags trims the model's tags, drops empties and
// de-duplicates them case-insensitively.
func normalizeCoderDispatchTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, trimmed)
	}
	return normalized
}

// castCoderDispatchAgentInput accepts both value and pointer inputs, like the
// other coder_* tools.
func castCoderDispatchAgentInput(input interface{}) CoderDispatchAgentInput {
	if value, ok := input.(CoderDispatchAgentInput); ok {
		return value
	}
	if pointer, ok := input.(*CoderDispatchAgentInput); ok && pointer != nil {
		return *pointer
	}
	return CoderDispatchAgentInput{}
}

func encodeCoderDispatchAgentResult(result CoderDispatchAgentResult) (string, error) {
	note := fmt.Sprintf("Dispatched %s for %s#%s; the interaction exists and is verifiable (chat_uuid=%s).", result.Bot, result.Repo, result.IssueNumber, result.ChatUUID)
	if result.Deduplicated {
		note = fmt.Sprintf("%s already has a live interaction for %s#%s; it was returned instead of starting a second agent (chat_uuid=%s).", result.Bot, result.Repo, result.IssueNumber, result.ChatUUID)
	}
	payload := map[string]interface{}{
		"dispatched":   true,
		"verified":     true,
		"chat_uuid":    result.ChatUUID,
		"bot":          result.Bot,
		"repo":         result.Repo,
		"issue_number": result.IssueNumber,
		"deduplicated": result.Deduplicated,
		"note":         note,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
