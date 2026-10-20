package database

import (
	"encoding/json"
	"strings"
	"time"
)

// NormalizeChatTags trims, lowercases and de-duplicates a set of chat tags,
// preserving order. It is used whenever tags are written so filtering stays
// predictable across the backend and integrations.
func NormalizeChatTags(tags []string) StringSliceJSON {
	seen := map[string]struct{}{}
	out := StringSliceJSON{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

type Message struct {
	Model
	ReadAt     *time.Time         `json:"read_at" gorm:"default:null"`
	SenderId   uint               `json:"-" gorm:"index"`
	Sender     User               `json:"-" gorm:"foreignKey:SenderId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ReceiverId uint               `json:"-" gorm:"index"`
	Receiver   User               `json:"-" gorm:"foreignKey:ReceiverId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	DataType   string             `json:"data_type" gorm:"default:'text'"`
	ChatId     uint               `json:"-" gorm:"index"`
	Chat       Chat               `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Content    *[]byte            `json:"-"`
	Text       *string            `json:"text"`
	Reasoning  *[]string          `json:"reasoning,omitempty" gorm:"type:jsonb;serializer:json"`
	ToolCalls  *[]json.RawMessage `json:"tool_calls,omitempty" gorm:"type:jsonb;serializer:json"`
	MetaData   JSONRaw            `json:"meta_data" gorm:"type:jsonb"`
}

// SharedChatConfig stores the shared LLM/tool configuration for a chat.
// It is created from the `shared_config` payload when a chat is created and is
// loaded for both chat participants when listing/opening chats and when tools run.
type SharedChatConfig struct {
	Model
	ChatId     uint            `json:"-" gorm:"index"`
	Chat       Chat            `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ConfigData json.RawMessage `json:"config_data" gorm:"type:jsonb"`
}

type Chat struct {
	Model
	User1Id         uint              `json:"-" gorm:"index"`
	User2Id         uint              `json:"-" gorm:"index"`
	User1           User              `json:"user1" gorm:"foreignKey:User1Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	User2           User              `json:"user2" gorm:"foreignKey:User2Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	LatestMessageId *uint             `json:"-" gorm:"index"`
	LatestMessage   *Message          `json:"latest_message" gorm:"foreignKey:LatestMessageId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	SharedConfigId  *uint             `json:"-" gorm:"index"`
	SharedConfig    *SharedChatConfig `json:"config" gorm:"foreignKey:SharedConfigId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ChatType        string            `json:"chat_type" gorm:"default:'conversation'"`
	// Tags are free-form labels attached to a chat so users can group and
	// filter chats/interactions by category (e.g. "git", "opencode",
	// "automation"). Integrations set them at creation time; users can edit
	// them through the chat settings endpoint.
	Tags StringSliceJSON `json:"tags" gorm:"type:jsonb"`
}

// ChatSettings stores per-chat settings owned by the backend for server-side
// behavior/state, separate from shared user-visible chat config. It is scoped
// to a chat and currently not exposed directly through chat APIs.
type ChatSettings struct {
	Model
	ChatId     uint            `json:"ChatId" gorm:"index"`
	Chat       Chat            `json:"Chat" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ConfigData json.RawMessage `json:"config_data" gorm:"type:jsonb"`
}

// SharedChatInstance links a private chat to a share UUID owned by a user.
// It powers publish/unpublish chat sharing and read-only shared interaction
// views. A share is either fully public (AccessMode "public", the default) or
// scoped to a single target user (AccessMode "user", TargetUserId set), in
// which case only the owner and that user may open it.
type SharedChatInstance struct {
	// ContentMode controls how much of the shared chat is exposed to viewers.
	// Empty means censored (the safe default) for shares created before this
	// field existed.
	ContentMode string
	Model
	ChatId        uint   `json:"-" gorm:"index"`
	Chat          Chat   `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	OwningUserId  uint   `json:"-" gorm:"index"`
	OwningUser    User   `json:"-" gorm:"foreignKey:OwningUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	ChatShareUUID string `json:"chat_share_uuid" gorm:"uniqueIndex;not null"`
	// AccessMode is "public" (anyone with the link) or "user" (only the target
	// user and the owner). Empty is treated as "public" for legacy rows.
	AccessMode   string `json:"access_mode" gorm:"default:'public'"`
	TargetUserId *uint  `json:"-" gorm:"index"`
	TargetUser   *User  `json:"-" gorm:"foreignKey:TargetUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}

// ChatPin is a per-user pin flag: a user can pin any chat they participate in
// so it sorts to the top of their chat list. The (chat_id, user_id) pair is
// unique.
type ChatPin struct {
	Model
	ChatId uint `json:"-" gorm:"uniqueIndex:idx_chat_pin_chat_user"`
	Chat   Chat `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	UserId uint `json:"-" gorm:"uniqueIndex:idx_chat_pin_chat_user"`
	User   User `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

// IsPublic reports whether the share can be opened by anyone with the link.
func (s SharedChatInstance) IsPublic() bool {
	return s.AccessMode == "" || s.AccessMode == "public"
}

// AllowsUser reports whether the given user id may open a non-public share.
// The owner always keeps access so they can preview/revoke their own links.
func (s SharedChatInstance) AllowsUser(userID uint) bool {
	if s.IsPublic() {
		return true
	}
	if s.OwningUserId == userID {
		return true
	}
	return s.TargetUserId != nil && *s.TargetUserId == userID
}
