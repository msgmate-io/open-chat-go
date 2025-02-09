package database

import (
	"encoding/json"
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
