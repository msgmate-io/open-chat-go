package chats

import (
	"encoding/json"
	"net/url"
	"strings"

	"backend/chatstate"
	"backend/database"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// followUpConfigKey is the shared-config key that records a follow-up
// interaction started by an interaction chat. Integrations/backends that
// dispatch a follow-up (e.g. a plan chat handing off to an implementation
// agent) write it, and the interaction badge / public interaction view read it.
//
// Shape:
//
//	"follow_up": {
//	  "chat_uuid": "...",              // required unless interaction_url is set
//	  "chat_share_uuid": "...",        // optional, resolved from the chat when absent
//	  "interaction_url": "/interaction/<share>", // optional, derived when absent
//	  "agent": "Implementation agent"  // optional human label, resolved from the bot user when absent
//	}
const followUpConfigKey = "follow_up"

// followUpReference is the normalized description of a follow-up interaction
// independently of where it was recorded.
type followUpReference struct {
	ChatUUID       string
	ChatShareUUID  string
	InteractionURL string
	Agent          string
}

// followUpInteraction is a follow-up reference enriched with the loaded
// follow-up chat and its resolved interaction status.
type followUpInteraction struct {
	followUpReference
	Chat   database.Chat
	Status InteractionStatusResponse
}

// PublicFollowUp is the public (share-scoped) view of a follow-up interaction.
// It is embedded in the shared interaction response so clients can link to -
// and redirect to - the follow-up the interaction actually handed off to.
type PublicFollowUp struct {
	ChatUUID       string `json:"chat_uuid,omitempty"`
	ChatShareUUID  string `json:"chat_share_uuid,omitempty"`
	InteractionURL string `json:"interaction_url,omitempty"`
	Agent          string `json:"agent,omitempty"`
	State          string `json:"state,omitempty"`
}

// resolveFollowUpReference returns the follow-up reference recorded for a chat,
// preferring an explicit shared-config `follow_up` entry (written by
// integrations) and falling back to a resolved runtime-selector message meta
// (the agent-chooser flow, which records the started interaction on the
// selector message).
func resolveFollowUpReference(DB *gorm.DB, chat database.Chat) (followUpReference, bool) {
	if ref, ok := followUpReferenceFromConfig(DB, chat); ok {
		return ref, true
	}
	return followUpReferenceFromRuntimeSelector(DB, chat)
}

// followUpReferenceFromConfig reads the explicit `follow_up` entry from the
// chat's shared config.
func followUpReferenceFromConfig(DB *gorm.DB, chat database.Chat) (followUpReference, bool) {
	config := sharedConfigMapForChat(DB, chat)
	if config == nil {
		return followUpReference{}, false
	}
	raw, ok := config[followUpConfigKey].(map[string]interface{})
	if !ok {
		return followUpReference{}, false
	}
	ref := followUpReference{
		ChatUUID:       strings.TrimSpace(actionTaskStringField(raw, "chat_uuid")),
		ChatShareUUID:  strings.TrimSpace(actionTaskStringField(raw, "chat_share_uuid")),
		InteractionURL: strings.TrimSpace(actionTaskFirstNonEmpty(actionTaskStringField(raw, "interaction_url"), actionTaskStringField(raw, "url"))),
		Agent:          strings.TrimSpace(actionTaskStringField(raw, "agent")),
	}
	if ref.ChatUUID == "" && ref.ChatShareUUID == "" && ref.InteractionURL == "" {
		return followUpReference{}, false
	}
	return ref, true
}

// followUpReferenceFromRuntimeSelector scans the most recent messages for a
// confirmed runtime selector that recorded the interaction it started. It is
// how the agent-chooser bot (git_propose_interaction_runtime) exposes the agent
// that was actually picked without any extra integration hook.
func followUpReferenceFromRuntimeSelector(DB *gorm.DB, chat database.Chat) (followUpReference, bool) {
	var messages []database.Message
	if err := DB.Where("chat_id = ?", chat.ID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Limit(confirmationScanLimit).
		Find(&messages).Error; err != nil {
		return followUpReference{}, false
	}
	for _, message := range messages {
		if len(message.MetaData) == 0 {
			continue
		}
		meta := map[string]interface{}{}
		if json.Unmarshal(message.MetaData, &meta) != nil {
			continue
		}
		for _, selector := range parseRuntimeSelectorsFromMeta(meta) {
			status, _ := selector["status"].(string)
			if status != "" && status != RuntimeSelectorConfirmed {
				continue
			}
			startedUUID := strings.TrimSpace(actionTaskStringField(selector, "started_chat_uuid"))
			startedURL := strings.TrimSpace(actionTaskStringField(selector, "started_interaction_url"))
			if startedUUID == "" && startedURL == "" {
				continue
			}
			return followUpReference{
				ChatUUID:       startedUUID,
				InteractionURL: startedURL,
			}, true
		}
	}
	return followUpReference{}, false
}

// resolveFollowUpInteraction resolves a chat's follow-up reference into a
// loaded follow-up chat (when available) plus the derived agent label, share
// uuid, public URL and live interaction status.
func resolveFollowUpInteraction(DB *gorm.DB, inspector *asynq.Inspector, chat database.Chat) (followUpInteraction, bool) {
	ref, ok := resolveFollowUpReference(DB, chat)
	if !ok {
		return followUpInteraction{}, false
	}
	result := followUpInteraction{followUpReference: ref}

	if ref.ChatUUID != "" {
		var followUpChat database.Chat
		if err := DB.Preload("User1").Preload("User2").Preload("SharedConfig").
			Where("uuid = ? AND deleted_at IS NULL", ref.ChatUUID).First(&followUpChat).Error; err == nil {
			result.Chat = followUpChat
			if result.Agent == "" {
				result.Agent = automatedParticipantName(followUpChat)
			}
			if result.ChatShareUUID == "" {
				if shareUUID, shareErr := sharedChatUUIDForChat(DB, followUpChat.ID); shareErr == nil {
					result.ChatShareUUID = shareUUID
				}
			}
			if status, statusErr := resolveInteractionStatus(DB, inspector, followUpChat); statusErr == nil {
				result.Status = status
			}
		}
	}

	result.InteractionURL = strings.TrimSpace(actionTaskFirstNonEmpty(
		result.InteractionURL,
		interactionURLForFollowUp(result.ChatShareUUID, result.ChatUUID),
	))
	if result.InteractionURL == "" && result.ChatUUID == "" && result.ChatShareUUID == "" {
		return followUpInteraction{}, false
	}
	return result, true
}

// interactionURLForFollowUp derives the human-facing interaction URL from a
// share uuid (preferred) or a raw chat uuid.
func interactionURLForFollowUp(shareUUID, chatUUID string) string {
	if shareUUID = strings.TrimSpace(shareUUID); shareUUID != "" {
		return "/interaction/" + url.PathEscape(shareUUID)
	}
	if chatUUID = strings.TrimSpace(chatUUID); chatUUID != "" {
		return "/chat/" + url.PathEscape(chatUUID)
	}
	return ""
}

// publicFollowUpForInteraction renders the share-scoped public follow-up view.
func publicFollowUpForInteraction(DB *gorm.DB, chat database.Chat) *PublicFollowUp {
	followUp, ok := resolveFollowUpInteraction(DB, nil, chat)
	if !ok {
		return nil
	}
	return &PublicFollowUp{
		ChatUUID:       followUp.ChatUUID,
		ChatShareUUID:  followUp.ChatShareUUID,
		InteractionURL: followUp.InteractionURL,
		Agent:          followUp.Agent,
		State:          followUp.Status.State,
	}
}

// badgeFollowUpForInteraction renders the follow-up as a nested badge row.
func badgeFollowUpForInteraction(DB *gorm.DB, inspector *asynq.Inspector, chat database.Chat) *badgeFollowUp {
	followUp, ok := resolveFollowUpInteraction(DB, inspector, chat)
	if !ok {
		return nil
	}
	state := badgeState{Color: "#8b8b8b"}
	if mapped, ok := badgeStates[chatstate.State(followUp.Status.State)]; ok {
		state = mapped
	}
	rendered := &badgeFollowUp{
		Agent: followUp.Agent,
		State: state.Label,
		Color: state.Color,
		URL:   followUp.InteractionURL,
	}
	if followUp.Chat.ID != 0 {
		rendered.Runtime = resolveInteractionRuntime(DB, followUp.Chat, followUp.Status)
	}
	return rendered
}

// automatedParticipantName returns the display name of the automated (bot)
// participant of a chat, used as the picked-agent label on the badge.
func automatedParticipantName(chat database.Chat) string {
	if chat.User1.IsAutomated {
		if name := strings.TrimSpace(chat.User1.Name); name != "" {
			return name
		}
	}
	if chat.User2.IsAutomated {
		if name := strings.TrimSpace(chat.User2.Name); name != "" {
			return name
		}
	}
	return ""
}

// sharedChatUUIDForChat returns the most recent share uuid for a chat.
func sharedChatUUIDForChat(DB *gorm.DB, chatID uint) (string, error) {
	var share database.SharedChatInstance
	err := DB.Where("chat_id = ?", chatID).Order("created_at DESC").First(&share).Error
	if err != nil {
		return "", err
	}
	return share.ChatShareUUID, nil
}

// sharedConfigMapForChat decodes the chat's persisted shared config. A chat
// without a shared config yields nil.
func sharedConfigMapForChat(DB *gorm.DB, chat database.Chat) map[string]interface{} {
	var configData []byte
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		configData = chat.SharedConfig.ConfigData
	} else if chat.SharedConfigId != nil && *chat.SharedConfigId != 0 {
		var shared database.SharedChatConfig
		if err := DB.First(&shared, "id = ?", *chat.SharedConfigId).Error; err != nil || len(shared.ConfigData) == 0 {
			return nil
		}
		configData = shared.ConfigData
	}
	if len(configData) == 0 {
		return nil
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil
	}
	return config
}
