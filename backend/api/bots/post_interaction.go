package bots

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"backend/api/chats"
	"backend/api/msgmate"
	"backend/chatstate"
	"backend/database"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
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

// runPostInteractionActions is the registered executor. It is best-effort: every
// error is logged and never aborts the interaction lifecycle.
func runPostInteractionActions(db *gorm.DB, _ *asynq.Client, _ *asynq.Inspector, chat database.Chat, replyText string) {
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
			comment, ok := decodePostActionGithubComment(action)
			if !ok {
				continue
			}
			if comment.Header == nil || strings.TrimSpace(*comment.Header) == "" {
				header := postActionPlanImplementHeader
				comment.Header = &header
			}
			if err := postGithubInteractionComment(comment, text); err != nil {
				log.Printf("post-interaction: plan comment failed for chat %s: %v", loaded.UUID, err)
			}
		default:
			comment, ok := decodePostActionGithubComment(action)
			if !ok {
				log.Printf("post-interaction: skipping unsupported post action for chat %s", loaded.UUID)
				continue
			}
			if err := postGithubInteractionComment(comment, text); err != nil {
				log.Printf("post-interaction: github_issue_comment failed for chat %s: %v", loaded.UUID, err)
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
		case "", postActionKindGithubComment, postActionKindPlanAutoImplement:
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
