package integrationhost

import (
	"context"
	"time"

	"backend/api/msgmate"
	wsapi "backend/api/websocket"
	"backend/chatstate"
	"backend/database"
	"backend/webtools"
	"backend/workspacelease"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

// This file implements the Host capability surfaces added in HostVersion 3 and
// wires integration-declared chat backends into the core registries.

// installChatBackends drains the integrationinterface chat-backend registry and
// registers each declared piece with the core chat-backend and chat-state
// registries. It runs after the Host is installed.
func installChatBackends() {
	for _, spec := range integrationinterface.ChatBackends() {
		if spec.Backend != nil {
			backend := spec.Backend
			msgmate.RegisterChatBackend(spec.Name, func(ctx context.Context, req msgmate.ChatBackendRequest) error {
				return backend(ctx, adaptChatBackendRequest(req))
			})
		}
		if spec.StateProvider != nil {
			provider := spec.StateProvider
			chatstate.RegisterBackendStateProvider(spec.Name, func(chatUUID string) (chatstate.BackendState, bool) {
				state, ok := provider(chatUUID)
				return chatstate.BackendState(state), ok
			})
		}
		if spec.InterruptHandler != nil {
			handler := spec.InterruptHandler
			chatstate.RegisterBackendInterruptHandler(spec.Name, func(chatUUID string) bool {
				return handler(chatUUID)
			})
		}
	}
}

// BotUser adapts a core user to the stable BotUser surface so a core-side
// caller can hand it to an integration helper (eg opencode's exported
// generation entry point).
func BotUser(u database.User) integrationinterface.BotUser { return botUserAdapter{u: u} }

// MessageWriter adapts a core websocket handler to the stable MessageWriter
// surface.
func MessageWriter(h *wsapi.WebSocketHandler) integrationinterface.MessageWriter {
	if h == nil {
		return nil
	}
	return messageWriterAdapter{h: h}
}

func adaptChatBackendRequest(req msgmate.ChatBackendRequest) integrationinterface.ChatBackendRequest {
	out := integrationinterface.ChatBackendRequest{
		DB:     req.DB,
		Config: req.Config,
		Message: integrationinterface.ChatMessage{
			ChatUUID:   req.Message.Content.ChatUUID,
			SenderUUID: req.Message.Content.SenderUUID,
			Text:       req.Message.Content.Text,
			Reasoning:  req.Message.Content.Reasoning,
			MetaData:   req.Message.Content.MetaData,
		},
	}
	if req.BotContext != nil {
		out.APIHost = req.BotContext.Config.Host
		if out.APIHost == "" && req.BotContext.Client != nil {
			out.APIHost = req.BotContext.Client.GetHost()
		}
		out.BotUser = botUserAdapter{u: req.BotContext.BotUser}
		if req.BotContext.WSHandler != nil {
			out.Messages = messageWriterAdapter{h: req.BotContext.WSHandler}
		}
	}
	return out
}

type botUserAdapter struct{ u database.User }

func (b botUserAdapter) ID() uint     { return b.u.ID }
func (b botUserAdapter) UUID() string { return b.u.UUID }

type messageWriterAdapter struct{ h *wsapi.WebSocketHandler }

func (w messageWriterAdapter) SendMessage(receiverUUID string, frame []byte) {
	if w.h == nil || w.h.MessageHandler == nil {
		return
	}
	w.h.MessageHandler.SendMessage(w.h, receiverUUID, frame)
}

func (w messageWriterAdapter) StartPartialMessage(chatUUID, senderUUID, sessionID string) []byte {
	if w.h == nil || w.h.MessageHandler == nil {
		return nil
	}
	return w.h.MessageHandler.StartPartialMessage(chatUUID, senderUUID, sessionID)
}

func (w messageWriterAdapter) EndPartialMessage(chatUUID, senderUUID, sessionID string) []byte {
	if w.h == nil || w.h.MessageHandler == nil {
		return nil
	}
	return w.h.MessageHandler.EndPartialMessage(chatUUID, senderUUID, sessionID)
}

func (w messageWriterAdapter) NewPartialMessage(chatUUID, senderUUID, sessionID string, seq int64, text string, reasoning []string, metaData *map[string]interface{}) []byte {
	if w.h == nil || w.h.MessageHandler == nil {
		return nil
	}
	return w.h.MessageHandler.NewPartialMessage(chatUUID, senderUUID, sessionID, seq, text, reasoning, metaData, nil, nil)
}

// chatAdapter exposes the core's native generation lifecycle to integrations.
type chatAdapter struct{}

func (chatAdapter) HasActiveGeneration(chatUUID string) bool {
	return msgmate.HasActiveChatGeneration(chatUUID)
}

func (chatAdapter) CancelActiveGeneration(chatUUID string) bool {
	return msgmate.CancelActiveChatGeneration(chatUUID)
}

// workspaceAdapter exposes the shared workspace-lease registry.
type workspaceAdapter struct{}

func (workspaceAdapter) CanonicalKey(path string) string { return workspacelease.CanonicalKey(path) }

func (workspaceAdapter) Acquire(db *gorm.DB, req integrationinterface.WorkspaceLeaseRequest) error {
	_, _, err := workspacelease.New(db).Acquire(workspacelease.AcquireRequest{
		Key:           req.Key,
		OwnerChatUUID: req.OwnerChatUUID,
		OwnerUserID:   req.OwnerUserID,
		OwnerLabel:    req.OwnerLabel,
		Kind:          req.Kind,
		Branch:        req.Branch,
		Path:          req.Path,
	})
	return err
}

func (workspaceAdapter) Status(db *gorm.DB, key, excludeChatUUID string) map[string]interface{} {
	return workspacelease.New(db).Status(key, excludeChatUUID)
}

// webAdapter exposes the shared agent-facing web helpers.
type webAdapter struct{}

func (webAdapter) Fetch(ctx context.Context, rawURL string, maxBytes int) (integrationinterface.WebFetchResult, error) {
	result, err := webtools.Fetch(ctx, rawURL, maxBytes)
	if err != nil {
		return integrationinterface.WebFetchResult{}, err
	}
	return integrationinterface.WebFetchResult{
		URL:         result.URL,
		Status:      result.Status,
		ContentType: result.ContentType,
		Title:       result.Title,
		Content:     result.Content,
		Truncated:   result.Truncated,
	}, nil
}

func (webAdapter) Search(ctx context.Context, query string, limit int, site string) ([]integrationinterface.WebSearchResult, error) {
	results, err := webtools.Search(ctx, query, limit, site)
	if err != nil {
		return nil, err
	}
	out := make([]integrationinterface.WebSearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, integrationinterface.WebSearchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Snippet,
		})
	}
	return out, nil
}

func (webAdapter) DefaultSearchLimit() int { return webtools.DefaultSearchLimit }

// tokensAdapter exposes the core's access-token minting.
type tokensAdapter struct{}

func (tokensAdapter) GenerateRaw() (raw, prefix, hash string, err error) {
	return database.GenerateRawAccessToken()
}

func (tokensAdapter) MintScoped(db *gorm.DB, userID uint, name, audience string, scopes []string, ttl time.Duration, parentTokenID uint) (string, error) {
	raw, _, err := database.MintScopedAccessToken(db, userID, name, audience, database.JoinTokenScopes(scopes), ttl, parentTokenID)
	return raw, err
}

func (tokensAdapter) PruneMismatched(db *gorm.DB, userID uint, name, audience string) error {
	return db.Where("user_id = ? AND name = ? AND (audience <> ? OR (expires_at IS NOT NULL AND expires_at < ?))", userID, name, audience, time.Now()).
		Delete(&database.AccessToken{}).Error
}

// toolsAdapter exposes the core tool-loop helpers.
type toolsAdapter struct{}

func (toolsAdapter) DiscoverMCPTools(config map[string]interface{}, auth map[string]interface{}) ([]map[string]interface{}, error) {
	return msgmate.DiscoverMCPTools(config, auth)
}

func (toolsAdapter) GetToolInitData(db *gorm.DB, chatID uint, toolName string) (map[string]interface{}, error) {
	return database.NewToolInitDataManager(db).GetToolInitData(chatID, toolName)
}
