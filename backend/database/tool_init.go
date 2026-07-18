package database

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ToolInitData represents tool initialization data for a specific chat interaction
type ToolInitData struct {
	Model
	ChatId    uint            `json:"chat_id" gorm:"index"`
	Chat      Chat            `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	ToolName  string          `json:"tool_name" gorm:"index"`
	InitData  json.RawMessage `json:"init_data" gorm:"type:jsonb"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty" gorm:"index"` // Optional expiration for cleanup
}

// ToolInitDataManager provides methods to manage tool initialization data
type ToolInitDataManager struct {
	DB *gorm.DB
}

// NewToolInitDataManager creates a new manager instance
func NewToolInitDataManager(db *gorm.DB) *ToolInitDataManager {
	return &ToolInitDataManager{DB: db}
}

// StoreToolInitData stores tool initialization data for a chat
func (m *ToolInitDataManager) StoreToolInitData(chatId uint, toolName string, initData map[string]interface{}) error {
	// Convert initData to JSON
	initDataJSON, err := json.Marshal(initData)
	if err != nil {
		return err
	}

	// Check if data already exists for this chat and tool
	var existing ToolInitData
	result := m.DB.Where("chat_id = ? AND tool_name = ?", chatId, toolName).First(&existing)

	if result.Error == nil {
		// Update existing record
		existing.InitData = initDataJSON
		existing.UpdatedAt = time.Now()
		return m.DB.Save(&existing).Error
	} else if result.Error == gorm.ErrRecordNotFound {
		// Create new record
		toolInitData := ToolInitData{
			ChatId:    chatId,
			ToolName:  toolName,
			InitData:  initDataJSON,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		return m.DB.Create(&toolInitData).Error
	} else {
		return result.Error
	}
}

// StoreToolInitDataForChat stores tool initialization data and mirrors it into
// shared_chat_configs.config_data.tool_init to keep legacy readers aligned.
func (m *ToolInitDataManager) StoreToolInitDataForChat(chat *Chat, toolName string, initData map[string]interface{}) error {
	if chat == nil {
		return fmt.Errorf("chat is required")
	}

	return m.DB.Transaction(func(tx *gorm.DB) error {
		manager := NewToolInitDataManager(tx)
		if err := manager.StoreToolInitData(chat.ID, toolName, initData); err != nil {
			return err
		}

		var sharedConfig SharedChatConfig
		if chat.SharedConfigId != nil && *chat.SharedConfigId != 0 {
			if err := tx.First(&sharedConfig, "id = ?", *chat.SharedConfigId).Error; err != nil {
				return err
			}
		} else {
			sharedConfig = SharedChatConfig{ChatId: chat.ID, ConfigData: json.RawMessage("{}")}
			if err := tx.Create(&sharedConfig).Error; err != nil {
				return err
			}
			if err := tx.Model(&Chat{}).Where("id = ?", chat.ID).Update("shared_config_id", sharedConfig.ID).Error; err != nil {
				return err
			}
			chat.SharedConfigId = &sharedConfig.ID
		}

		configData := map[string]interface{}{}
		if len(sharedConfig.ConfigData) > 0 {
			_ = json.Unmarshal(sharedConfig.ConfigData, &configData)
		}
		toolInitRaw, _ := configData["tool_init"].(map[string]interface{})
		if toolInitRaw == nil {
			toolInitRaw = map[string]interface{}{}
		}
		toolInitRaw[toolName] = initData
		configData["tool_init"] = toolInitRaw

		encoded, err := json.Marshal(configData)
		if err != nil {
			return err
		}
		if err := tx.Model(&SharedChatConfig{}).Where("id = ?", sharedConfig.ID).Update("config_data", encoded).Error; err != nil {
			return err
		}
		sharedConfig.ConfigData = encoded
		chat.SharedConfig = &sharedConfig
		return nil
	})
}

// GetToolInitData retrieves tool initialization data for a chat and tool
func (m *ToolInitDataManager) GetToolInitData(chatId uint, toolName string) (map[string]interface{}, error) {
	var toolInitData ToolInitData
	result := m.DB.Where("chat_id = ? AND tool_name = ?", chatId, toolName).First(&toolInitData)

	if result.Error != nil {
		return nil, result.Error
	}

	// Check if data has expired
	if toolInitData.ExpiresAt != nil && toolInitData.ExpiresAt.Before(time.Now()) {
		// Data has expired, delete it and return error
		m.DB.Delete(&toolInitData)
		return nil, gorm.ErrRecordNotFound
	}

	// Parse JSON data
	var initData map[string]interface{}
	err := json.Unmarshal(toolInitData.InitData, &initData)
	if err != nil {
		return nil, err
	}

	return initData, nil
}

// ResolveToolInitData returns initialization data from tool_init_data first and
// falls back to chat shared_config.tool_init for backward compatibility.
func (m *ToolInitDataManager) ResolveToolInitData(chat Chat, toolName string) map[string]interface{} {
	if initData, err := m.GetToolInitData(chat.ID, toolName); err == nil && initData != nil {
		return initData
	}

	if chat.SharedConfig == nil || len(chat.SharedConfig.ConfigData) == 0 {
		return map[string]interface{}{}
	}

	configData := map[string]interface{}{}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err != nil {
		return map[string]interface{}{}
	}
	toolInitRaw, ok := configData["tool_init"].(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	initRaw, ok := toolInitRaw[toolName].(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return initRaw
}

// GetAllToolInitData retrieves all tool initialization data for a chat
func (m *ToolInitDataManager) GetAllToolInitData(chatId uint) (map[string]map[string]interface{}, error) {
	var toolInitDataList []ToolInitData
	result := m.DB.Where("chat_id = ?", chatId).Find(&toolInitDataList)

	if result.Error != nil {
		return nil, result.Error
	}

	toolInitDataMap := make(map[string]map[string]interface{})
	now := time.Now()

	for _, data := range toolInitDataList {
		// Skip expired data
		if data.ExpiresAt != nil && data.ExpiresAt.Before(now) {
			continue
		}

		var initData map[string]interface{}
		err := json.Unmarshal(data.InitData, &initData)
		if err != nil {
			continue // Skip malformed data
		}

		toolInitDataMap[data.ToolName] = initData
	}

	return toolInitDataMap, nil
}

// DeleteToolInitData removes tool initialization data for a specific chat and tool
func (m *ToolInitDataManager) DeleteToolInitData(chatId uint, toolName string) error {
	return m.DB.Where("chat_id = ? AND tool_name = ?", chatId, toolName).Delete(&ToolInitData{}).Error
}

// DeleteAllToolInitData removes all tool initialization data for a chat
func (m *ToolInitDataManager) DeleteAllToolInitData(chatId uint) error {
	return m.DB.Where("chat_id = ?", chatId).Delete(&ToolInitData{}).Error
}

// CleanupExpiredData removes all expired tool initialization data
func (m *ToolInitDataManager) CleanupExpiredData() error {
	now := time.Now()
	return m.DB.Where("expires_at IS NOT NULL AND expires_at < ?", now).Delete(&ToolInitData{}).Error
}

// SetExpiration sets an expiration time for tool initialization data
func (m *ToolInitDataManager) SetExpiration(chatId uint, toolName string, expiresAt time.Time) error {
	return m.DB.Model(&ToolInitData{}).
		Where("chat_id = ? AND tool_name = ?", chatId, toolName).
		Update("expires_at", expiresAt).Error
}

func redactToolInitValueShape(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		redacted := map[string]interface{}{}
		for key, nested := range typed {
			redacted[key] = redactToolInitValueShape(nested)
		}
		return redacted
	default:
		return nil
	}
}

func mergeToolInitValueShape(current interface{}, incoming interface{}) interface{} {
	if current == nil {
		return incoming
	}
	if incoming == nil {
		return current
	}

	currentMap, currentOK := current.(map[string]interface{})
	incomingMap, incomingOK := incoming.(map[string]interface{})
	if !currentOK || !incomingOK {
		if currentOK {
			return current
		}
		if incomingOK {
			return incoming
		}
		return nil
	}

	merged := map[string]interface{}{}
	for key, value := range currentMap {
		merged[key] = value
	}
	for key, value := range incomingMap {
		merged[key] = mergeToolInitValueShape(merged[key], value)
	}

	return merged
}

// DisposeToolInitDataForChat removes persisted tool init rows for a chat and
// keeps only the key shape in shared_config.tool_init with null leaf values.
func (m *ToolInitDataManager) DisposeToolInitDataForChat(chat *Chat) error {
	if chat == nil {
		return fmt.Errorf("chat is required")
	}

	redactedToolInit := map[string]interface{}{}
	if allToolInitData, err := m.GetAllToolInitData(chat.ID); err == nil {
		for toolName, initData := range allToolInitData {
			redactedToolInit[toolName] = redactToolInitValueShape(initData)
		}
	}

	var sharedConfig SharedChatConfig
	hasSharedConfig := false
	if chat.SharedConfig != nil {
		sharedConfig = *chat.SharedConfig
		hasSharedConfig = true
	} else if chat.SharedConfigId != nil && *chat.SharedConfigId != 0 {
		err := m.DB.First(&sharedConfig, "id = ?", *chat.SharedConfigId).Error
		if err != nil {
			if err != gorm.ErrRecordNotFound {
				return err
			}
		} else {
			hasSharedConfig = true
		}
	}

	if hasSharedConfig {
		configData := map[string]interface{}{}
		if len(sharedConfig.ConfigData) > 0 {
			_ = json.Unmarshal(sharedConfig.ConfigData, &configData)
		}
		if existingToolInitRaw, ok := configData["tool_init"].(map[string]interface{}); ok {
			for toolName, initPayload := range existingToolInitRaw {
				redacted := redactToolInitValueShape(initPayload)
				redactedToolInit[toolName] = mergeToolInitValueShape(redactedToolInit[toolName], redacted)
			}
		}
	}

	return m.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&ToolInitData{}).Error; err != nil {
			return err
		}

		if !hasSharedConfig {
			return nil
		}

		configData := map[string]interface{}{}
		if len(sharedConfig.ConfigData) > 0 {
			_ = json.Unmarshal(sharedConfig.ConfigData, &configData)
		}

		if len(redactedToolInit) == 0 {
			delete(configData, "tool_init")
		} else {
			configData["tool_init"] = redactedToolInit
		}

		encoded, err := json.Marshal(configData)
		if err != nil {
			return err
		}
		if err := tx.Model(&SharedChatConfig{}).Where("id = ?", sharedConfig.ID).Update("config_data", encoded).Error; err != nil {
			return err
		}

		sharedConfig.ConfigData = encoded
		chat.SharedConfig = &sharedConfig
		return nil
	})
}
