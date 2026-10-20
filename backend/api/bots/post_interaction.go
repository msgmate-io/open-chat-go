package bots

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"backend/api/chats"
	"backend/api/msgmate"
	"backend/chatstate"
	"backend/database"
	"backend/logging"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"backend/logging"
)

// This file implements the backend-agnostic "post the final message for them"
// hook (ci#649). An interaction's shared config may carry a `post_action` /
// `post_actions` entry attached by the dispatching workflow or trigger; when the
// interaction finishes, its final reply is posted back to the tracker as a
// GitHub issue/PR comment. opencode runs this from its own chat backend; a
// native backend (eg the msgmate_coder plan bot, which registers no
// ChatBackendFunc) has no such hook, so the core runs it here at the shared
// completion choke point. The executor is registered into api/chats at init.

func init() {
	chats.RegisterPostInteractionRunner(runPostInteractionActions)
}

const (
	// postGithubTokenEnv must hold a GitHub PAT able to comment on the target
	// repositories (same credential opencode's executor uses).
	postGithubTokenEnv = "OPEN_CHAT_GITHUB_POST_TOKEN"
	// postGithubCommentTimeout bounds each GitHub API call.
	postGithubCommentTimeout = 30 * time.Second
	// postGithubMaxCommentBody caps the comment body.
	postGithubMaxCommentBody = 60000

	// postActionKindGithubComment posts the final message as an issue comment.
	postActionKindGithubComment = "github_issue_comment"
	// postActionKindPlanAutoImplement is attached to planning dispatches; its
	// plan comment is posted here (the follow-up implementation dispatch is
	// handled by the trigger/loop for the native path).
	postActionKindPlanAutoImplement = "plan_auto_implement"

	postActionDefaultReportHeader    = "<!-- open-chat-post-interaction -->\n## Open-Chat interaction report"
	postActionPlanImplementHeader    = "<!-- open-chat-post-interaction -->\n## Implementation plan"
	postActionEmptyReplyPlaceholder  = "(the interaction finished without a final text message)"
	postActionDoneStateKey           = "post_interaction_done"
	postActionTruncationNoticeSuffix = "\n\n_(final message truncated by open-chat post-interaction)_"
)

// postActionGithubComment is the decoded github_issue_comment post-action.
type postActionGithubComment struct {
	Repo   string  `json:"repo"`
	Number int     `json:"number"`
	Issue  *string `json:"issue_url,omitempty"`
	Header *string `json:"header,omitempty"`
	Footer *string `json:"footer,omitempty"`
	MaxLen *int    `json:"max_len,omitempty"`
}

// postActionPlanAutoImplement is the decoded plan_auto_implement post-action:
// post the finished plan and dispatch the implementation agent.
type postActionPlanAutoImplement struct {
	Repo                string                 `json:"repo"`
	Number              int                    `json:"number"`
	Issue               *string                `json:"issue_url,omitempty"`
	Bot                 string                 `json:"bot"`
	RequireConfirmation *bool                  `json:"require_confirmation,omitempty"`
	ToolInit            map[string]interface{} `json:"tool_init,omitempty"`
	ConfigOverrides     map[string]interface{} `json:"config_overrides,omitempty"`
	Header              *string                `json:"header,omitempty"`
	Footer              *string                `json:"footer,omitempty"`
	MaxLen              *int                   `json:"max_len,omitempty"`
}

// runPostInteractionActions is the registered executor. It is best-effort: every
// error is logged and never aborts the interaction lifecycle.
func runPostInteractionActions(db *gorm.DB, queueClient *asynq.Client, queueInspector *asynq.Inspector, chat database.Chat, replyText string) {
	if db == nil || chat.ID == 0 {
		return
	}
	loaded := chat
	if loaded.SharedConfig == nil {
		if err := db.Preload("SharedConfig").Where("id = ?", chat.ID).First(&loaded).Error; err != nil {
			return
		}
	}
	if loaded.SharedConfig == nil || len(loaded.SharedConfig.ConfigData) == 0 {
		return
	}

	// A chat whose external backend runs its own post-actions (eg opencode) must
	// not be posted twice. The native coder registers no ChatBackendFunc, so
	// IsRegisteredChatBackend is false and this executor handles it.
	config := map[string]interface{}{}
	if err := json.Unmarshal(loaded.SharedConfig.ConfigData, &config); err != nil {
		return
	}
	if backend := chatstate.ChatBackendNameFromConfig(config); msgmate.IsRegisteredChatBackend(backend) {
		return
	}

	actions := loadChatPostActions(loaded.SharedConfig.ConfigData)
	if len(actions) == 0 {
		return
	}
	// Post at most once per interaction: an autonomous interaction may finish
	// several consecutive turns (auto-continue), and the plan must not be posted
	// once per turn.
	if postActionsAlreadyRan(db, loaded.ID) {
		return
	}

	text := strings.TrimSpace(replyText)
	if text == "" {
		text = postActionEmptyReplyPlaceholder
	}
	for _, action := range actions {
		switch kind, _ := action["kind"].(string); kind {
		case postActionKindPlanAutoImplement:
			if err := runPlanAutoImplement(db, queueClient, queueInspector, loaded, action, text); err != nil {
				logging.Errorf("post-interaction: plan_auto_implement failed for chat %s: %v", loaded.UUID, err)
			}
		case postActionKindPullRequestReview:
			// ci#622: the review role's durable output is a pull request review,
			// not an issue comment, so the review bot's turn ends with a review
			// on the fixture PR instead of a comment nobody asserted on.
			review, ok := decodePostActionPullRequestReview(action)
			if !ok {
				logging.Infof("post-interaction: skipping invalid pull request review action for chat %s", loaded.UUID)
				continue
			}
			if err := runPullRequestReview(review, text); err != nil {
				logging.Errorf("post-interaction: github_pull_request_review failed for chat %s: %v", loaded.UUID, err)
			}
		default:
			comment, ok := decodePostActionGithubComment(action)
			if !ok {
				logging.Infof("post-interaction: skipping unsupported post action for chat %s", loaded.UUID)
				continue
			}
			if err := postGithubInteractionComment(comment, text); err != nil {
				logging.Errorf("post-interaction: github_issue_comment failed for chat %s: %v", loaded.UUID, err)
			}
		}
	}
	markPostActionsRan(db, loaded.ID)
}

// loadChatPostActions reads the `post_action` entry (or the `post_actions`
// list) of an interaction chat's shared config, keeping only the kinds this
// executor understands.
func loadChatPostActions(configData []byte) []map[string]interface{} {
	payload := map[string]interface{}{}
	if err := json.Unmarshal(configData, &payload); err != nil {
		return nil
	}
	raw := []map[string]interface{}{}
	if single, ok := payload["post_action"].(map[string]interface{}); ok {
		raw = append(raw, single)
	}
	if list, ok := payload["post_actions"].([]interface{}); ok {
		for _, item := range list {
			if m, ok := item.(map[string]interface{}); ok {
				raw = append(raw, m)
			}
		}
	}
	selected := make([]map[string]interface{}, 0, len(raw))
	for _, action := range raw {
		switch kind, _ := action["kind"].(string); kind {
		case "", postActionKindGithubComment, postActionKindPlanAutoImplement, postActionKindPullRequestReview:
			selected = append(selected, action)
		}
	}
	return selected
}

// decodePostActionGithubComment accepts either a flat github comment entry or a
// typed one and validates that it names a target.
func decodePostActionGithubComment(raw map[string]interface{}) (postActionGithubComment, bool) {
	blob, err := json.Marshal(raw)
	if err != nil {
		return postActionGithubComment{}, false
	}
	var action postActionGithubComment
	if err := json.Unmarshal(blob, &action); err != nil {
		return postActionGithubComment{}, false
	}
	issueURL := ""
	if action.Issue != nil {
		issueURL = strings.TrimSpace(*action.Issue)
	}
	if issueURL == "" && (strings.TrimSpace(action.Repo) == "" || action.Number == 0) {
		return postActionGithubComment{}, false
	}
	return action, true
}

// composePostCommentBody renders header + final agent text + footer, capping the
// body length.
func composePostCommentBody(action postActionGithubComment, replyText, defaultHeader string) string {
	header := defaultHeader
	if action.Header != nil && strings.TrimSpace(*action.Header) != "" {
		header = *action.Header
	}
	maxLen := postGithubMaxCommentBody
	if action.MaxLen != nil && *action.MaxLen > 0 && *action.MaxLen < maxLen {
		maxLen = *action.MaxLen
	}
	if len(replyText) > maxLen {
		replyText = replyText[:maxLen] + postActionTruncationNoticeSuffix
	}
	body := header + "\n\n" + replyText
	if action.Footer != nil && strings.TrimSpace(*action.Footer) != "" {
		body += "\n\n" + *action.Footer
	}
	return body
}

// postGithubInteractionComment posts the composed comment to GitHub.
func postGithubInteractionComment(action postActionGithubComment, replyText string) error {
	token := strings.TrimSpace(os.Getenv(postGithubTokenEnv))
	if token == "" {
		return fmt.Errorf("%s is not configured; skipping github post action", postGithubTokenEnv)
	}
	body := composePostCommentBody(action, replyText, postActionDefaultReportHeader)
	return postGithubIssueComment(token, action, body)
}

// postGithubIssueComment POSTs a comment body to the action's target.
func postGithubIssueComment(token string, action postActionGithubComment, body string) error {
	endpoint := ""
	if action.Issue != nil && strings.TrimSpace(*action.Issue) != "" {
		endpoint = strings.TrimSuffix(strings.TrimSpace(*action.Issue), "/") + "/comments"
	} else if strings.TrimSpace(action.Repo) != "" && action.Number != 0 {
		endpoint = fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments", action.Repo, action.Number)
	}
	if endpoint == "" {
		return fmt.Errorf("no github target endpoint could be derived from the post action")
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: postGithubCommentTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("github api returned status %d posting issue comment", resp.StatusCode)
	}
	return nil
}

// postActionsAlreadyRan reports whether the interaction's post-actions were
// already executed (dedupe across auto-continued turns).
func postActionsAlreadyRan(db *gorm.DB, chatID uint) bool {
	var settings database.ChatSettings
	if err := db.Where("chat_id = ?", chatID).First(&settings).Error; err != nil {
		return false
	}
	if len(settings.ConfigData) == 0 {
		return false
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(settings.ConfigData, &config); err != nil {
		return false
	}
	done, _ := config[postActionDoneStateKey].(bool)
	return done
}

// markPostActionsRan records that the interaction's post-actions were executed.
func markPostActionsRan(db *gorm.DB, chatID uint) {
	if db == nil || chatID == 0 {
		return
	}
	settings := database.ChatSettings{}
	findErr := db.Where("chat_id = ?", chatID).First(&settings).Error
	if findErr != nil && findErr != gorm.ErrRecordNotFound {
		return
	}
	config := map[string]interface{}{}
	if findErr == nil && len(settings.ConfigData) > 0 {
		_ = json.Unmarshal(settings.ConfigData, &config)
	}
	config[postActionDoneStateKey] = true
	encoded, err := json.Marshal(config)
	if err != nil {
		return
	}
	if findErr == gorm.ErrRecordNotFound {
		_ = db.Create(&database.ChatSettings{ChatId: chatID, ConfigData: encoded}).Error
		return
	}
	_ = db.Model(&settings).Update("config_data", encoded).Error
}

// runPlanAutoImplement executes a plan_auto_implement action: it posts the
// finished plan to the triggering issue/PR and dispatches the implementation
// agent in a new interaction owned by the planning chat's human user.
func runPlanAutoImplement(db *gorm.DB, queueClient *asynq.Client, queueInspector *asynq.Inspector, chat database.Chat, raw map[string]interface{}, replyText string) error {
	action, ok := decodePostActionPlanAutoImplement(raw)
	if !ok {
		return fmt.Errorf("invalid plan_auto_implement action")
	}
	if strings.TrimSpace(action.Bot) == "" {
		return fmt.Errorf("plan_auto_implement action has no bot configured")
	}
	comment := postActionGithubComment{
		Repo:   action.Repo,
		Number: action.Number,
		Issue:  action.Issue,
		Header: action.Header,
		Footer: action.Footer,
		MaxLen: action.MaxLen,
	}
	if comment.Header == nil || strings.TrimSpace(*comment.Header) == "" {
		header := postActionPlanImplementHeader
		comment.Header = &header
	}
	commentErr := postGithubInteractionComment(comment, replyText)
	dispatchErr := dispatchPlanImplementation(db, queueClient, queueInspector, chat, action, replyText)
	if dispatchErr != nil {
		if commentErr != nil {
			return fmt.Errorf("plan comment failed: %v; implementation dispatch failed: %w", commentErr, dispatchErr)
		}
		return dispatchErr
	}
	return commentErr
}

// decodePostActionPlanAutoImplement decodes a plan_auto_implement action.
func decodePostActionPlanAutoImplement(raw map[string]interface{}) (postActionPlanAutoImplement, bool) {
	blob, err := json.Marshal(raw)
	if err != nil {
		return postActionPlanAutoImplement{}, false
	}
	var action postActionPlanAutoImplement
	if err := json.Unmarshal(blob, &action); err != nil {
		return postActionPlanAutoImplement{}, false
	}
	return action, true
}

// dispatchPlanImplementation starts the implementation agent in a new
// interaction owned by the planning chat's human user.
func dispatchPlanImplementation(db *gorm.DB, queueClient *asynq.Client, queueInspector *asynq.Inspector, chat database.Chat, action postActionPlanAutoImplement, replyText string) error {
	if queueClient == nil || queueInspector == nil {
		return fmt.Errorf("async queue unavailable for implementation dispatch")
	}
	owner, err := interactionHumanOwner(db, chat.UUID)
	if err != nil {
		return err
	}
	runtime, err := resolveReadableBot(db, owner, strings.TrimSpace(action.Bot))
	if err != nil {
		return fmt.Errorf("implementation bot %q not found: %w", action.Bot, err)
	}
	overrides := action.ConfigOverrides
	if overrides == nil {
		overrides = map[string]interface{}{}
	}
	req := CreateBotInteractionRequest{
		Message:         buildPlanImplementationPrompt(action, replyText),
		ConfigOverrides: overrides,
		ToolInit:        action.ToolInit,
		AutoShare:       true,
		RequireConfirmation: func() bool {
			if action.RequireConfirmation != nil {
				return *action.RequireConfirmation
			}
			return true
		}(),
	}
	if _, err := createBotInteraction(db, queueClient, queueInspector, owner, runtime, req, postInteractionBaseURL()); err != nil {
		return err
	}
	return nil
}

// interactionHumanOwner returns the human participant of an interaction chat.
func interactionHumanOwner(db *gorm.DB, chatUUID string) (*database.User, error) {
	chat := database.Chat{}
	if err := db.Preload("User1").Preload("User2").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		return nil, fmt.Errorf("failed to load chat %s: %w", chatUUID, err)
	}
	if !chat.User1.IsAutomated && chat.User1.ID != 0 {
		user := chat.User1
		return &user, nil
	}
	if !chat.User2.IsAutomated && chat.User2.ID != 0 {
		user := chat.User2
		return &user, nil
	}
	return nil, fmt.Errorf("chat %s has no human owner to dispatch the implementation interaction", chatUUID)
}

// buildPlanImplementationPrompt renders the coding-agent prompt for the plan.
func buildPlanImplementationPrompt(action postActionPlanAutoImplement, replyText string) string {
	var b strings.Builder
	b.WriteString("You are a coding agent implementing a GitHub issue plan produced by a planning agent.\n")
	b.WriteString("Your durable playbook is part of your agent definition (system_prompt); follow it.\n")
	b.WriteString("Implement the plan fully on a separate PR with ALL applicable CI labels, then\n")
	b.WriteString("post a follow-up comment in the issue thread summarizing what you did.\n\n")
	b.WriteString("Context:\n")
	if repo := strings.TrimSpace(action.Repo); repo != "" {
		fmt.Fprintf(&b, "- Repository: %s\n", repo)
	}
	if action.Number != 0 {
		fmt.Fprintf(&b, "- Issue/PR number: %d\n", action.Number)
	}
	if action.Issue != nil && strings.TrimSpace(*action.Issue) != "" {
		fmt.Fprintf(&b, "- Issue/PR URL: %s\n", strings.TrimSpace(*action.Issue))
	}
	b.WriteString("\nPlan (verbatim):\n```\n")
	b.WriteString(strings.TrimSpace(replyText))
	b.WriteString("\n```\n")
	return b.String()
}

// postInteractionBaseURL is the public base URL used for a dispatched
// interaction's share link.
func postInteractionBaseURL() string {
	if host := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"); host != "" {
		return host
	}
	return "http://127.0.0.1:1984"
}
