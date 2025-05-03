package database

import "time"

// Integration represents a third-party service integration
type Integration struct {
	Model
	IntegrationName string     `json:"integration_name" gorm:"index"`
	IntegrationType string     `json:"integration_type" gorm:"index"`
	Active          bool       `json:"active"`
	Config          []byte     `json:"config"` // Encrypted configuration data
	LastUsed        *time.Time `json:"last_used,omitempty"`
	UserID          uint       `json:"user_id" gorm:"index"`
	User            User       `json:"-" gorm:"foreignKey:UserID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}
